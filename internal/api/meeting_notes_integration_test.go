package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bdobrica/SecondContext/internal/knowledge"
	"github.com/bdobrica/SecondContext/internal/llm"
	memsvc "github.com/bdobrica/SecondContext/internal/memory"
)

const cl5Note = "Alex: I prefer small API review requests.\nAlex: I propose reviewing only the API section on Friday.\nAlex: We decided to keep the budget review separate."

type noteProvider struct {
	calls   int
	fail    bool
	subject string
}

func (p *noteProvider) Search(context.Context, string, string, knowledge.Filters, int) ([]knowledge.KnowledgeEvidence, error) {
	return nil, nil
}
func (p *noteProvider) IngestNote(_ context.Context, subject string, input knowledge.NoteInput, key string) (knowledge.NoteResult, error) {
	p.calls++
	p.subject = subject
	if p.fail {
		return knowledge.NoteResult{}, errors.New("fixture parser outage")
	}
	if input.Purpose == "reference" {
		return knowledge.NoteResult{Purpose: "reference", SourceID: historyTurnID(505), Status: "pending"}, nil
	}
	source := input.Text
	if input.Filename != "" {
		source = string(input.File)
	}
	return knowledge.NoteResult{Purpose: "personal", Text: source, Format: "text"}, nil
}

type noteClient struct {
	*cl4Client
	fail bool
	hook func(context.Context) error
}

func (c *noteClient) Generate(ctx context.Context, r llm.GenerateRequest) (llm.GenerateResponse, error) {
	if r.Messages[0].Content == memsvc.ObservationRelationPrompt {
		var input struct {
			Observation memsvc.Observation            `json:"observation"`
			Candidates  []memsvc.ObservationCandidate `json:"candidates"`
		}
		_ = json.Unmarshal([]byte(r.Messages[1].Content), &input)
		if input.Observation.Summary == cl4Correction {
			relations := []memsvc.ObservationRelation{}
			for _, candidate := range input.Candidates {
				kind := "unrelated"
				if strings.Contains(candidate.Summary, "I prefer small API review requests.") || candidate.Summary == cl4Preference {
					kind = "correction"
				}
				relations = append(relations, memsvc.ObservationRelation{ID: candidate.ID, Kind: kind, PriorQuote: candidate.Summary, NewQuote: input.Observation.Quote, Reason: "Explicit correction of Alex's review preference."})
			}
			raw, _ := json.Marshal(map[string]any{"relations": relations})
			return llm.GenerateResponse{OutputText: string(raw)}, nil
		}
	}
	if r.Messages[0].Content != memsvc.MeetingNotePrompt {
		return c.cl4Client.Generate(ctx, r)
	}
	if c.hook != nil {
		if err := c.hook(ctx); err != nil {
			return llm.GenerateResponse{}, err
		}
	}
	if c.fail {
		return llm.GenerateResponse{}, errors.New("fixture extraction outage")
	}
	var input map[string]string
	_ = json.Unmarshal([]byte(r.Messages[1].Content), &input)
	observations := []memsvc.Observation{}
	if input["note"] == cl5Note {
		for i, line := range strings.Split(cl5Note, "\n") {
			observations = append(observations, memsvc.Observation{Summary: line, Quote: line, Type: []string{"person_preference", "proposal", "decision"}[i], Evidence: "meeting_note", Speaker: "Alex", People: []string{"Alex"}, Topics: []string{"review"}, TimeScope: input["meeting_date"]})
		}
	}
	raw, _ := json.Marshal(map[string]any{"observations": observations})
	return llm.GenerateResponse{OutputText: string(raw)}, nil
}

func noteHarness(t *testing.T) (*learningHarness, *noteClient, *noteProvider) {
	h, base := cl4Harness(t)
	c := &noteClient{cl4Client: base}
	p := &noteProvider{}
	h.server = NewServerWithClient(h.cfg, testChatLogger(), h.pool, c)
	h.server.knowledge = p
	return h, c, p
}

func importNote(h *learningHarness, purpose, source, date string, uploaded bool) *httptest.ResponseRecorder {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("purpose", purpose)
	_ = w.WriteField("name", "API planning")
	_ = w.WriteField("meeting_date", date)
	if uploaded {
		part, _ := w.CreateFormFile("file", "notes.txt")
		_, _ = part.Write([]byte(source))
	} else {
		_ = w.WriteField("text", source)
	}
	_ = w.Close()
	r := httptest.NewRequest("POST", "/memory/notes", &body)
	r.Header.Set("Content-Type", w.FormDataContentType())
	r.Header.Set("Authorization", "Bearer chat-service-test")
	r.Header.Set("X-SecondContext-Subject", h.user)
	result := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(result, r)
	return result
}

func TestCL5NotesToFreshContextAndCorrection(t *testing.T) {
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses"} {
		t.Run(endpoint, func(t *testing.T) {
			h, _, p := noteHarness(t)
			historyStatus(t, importNote(h, "personal", cl5Note, "2026-10-10", false), 202, "")
			if len(h.memories()) != 0 {
				t.Fatal("queued notes exposed before extraction")
			}
			h.drain()
			var erased bool
			if err := h.pool.QueryRow(context.Background(), `SELECT source_text='' AND source_metadata='{}'::jsonb FROM learning_captures WHERE id=$1`, h.captureIDs()[0]).Scan(&erased); err != nil || !erased {
				t.Fatal("completed note retained queued content", err)
			}
			observations := cl4Observations(t, h)
			if len(observations) != 3 || p.subject != h.user {
				t.Fatal("missing isolated observations", observations)
			}
			var preference memoryResponse
			for _, m := range observations {
				meta := m.Metadata
				if m.Source != "meeting_note" || meta["speaker"] != "Alex" || meta["meeting_date"] != "2026-10-10" || meta["evidence"] != "meeting_note" {
					t.Fatal("lost note provenance", m)
				}
				if strings.Contains(m.Summary, "prefer") {
					preference = m
				}
			}
			answer := naturalAsk(h, endpoint, []llm.Message{{Role: "user", Content: "Help me ask Alex to review the API proposal."}}, nil)
			historyStatus(t, answer, 200, "")
			for _, text := range []string{"Meeting notes report:", "API planning", "2026-10-10", "proposal", "speaker=Alex"} {
				if !strings.Contains(answer.Body.String(), text) {
					t.Fatalf("missing note context %q: %s", text, answer.Body.String())
				}
			}
			historyStatus(t, h.request("PATCH", "/memory/"+preference.ID, map[string]string{"summary": "Alex prefers detailed API review briefs."}), 200, "")
			answer = naturalAsk(h, endpoint, []llm.Message{{Role: "user", Content: "Help me ask Alex for a review."}}, nil)
			if !strings.Contains(answer.Body.String(), "Alex prefers detailed API review briefs.") || strings.Contains(answer.Body.String(), "I prefer small API review requests.") {
				t.Fatal("correction did not change fresh context")
			}
			for _, m := range h.memories() {
				historyStatus(t, h.request("DELETE", "/memory/"+m.ID, nil), 200, "")
			}
			historyStatus(t, importNote(h, "personal", cl5Note, "2026-10-10", true), 202, "")
			h.drain()
			if len(h.memories()) != 0 {
				t.Fatal("file reimport recreated forgotten observations")
			}
		})
	}
}

func TestCL5ReferencePauseRetryAndIsolation(t *testing.T) {
	h, c, p := noteHarness(t)
	h.enable(false)
	historyStatus(t, importNote(h, "personal", cl5Note, "", false), 409, "learning_paused")
	if p.calls != 0 {
		t.Fatal("paused import sent to parser")
	}
	historyStatus(t, importNote(h, "reference", cl5Note, "", true), 202, "")
	if len(h.captureIDs()) != 0 {
		t.Fatal("reference became personal capture")
	}
	h.enable(true)
	p.fail = true
	historyStatus(t, importNote(h, "personal", cl5Note, "", false), 502, "")
	if len(h.captureIDs()) != 0 {
		t.Fatal("failed parse created capture")
	}
	p.fail = false
	c.fail = true
	historyStatus(t, importNote(h, "personal", cl5Note, "", true), 202, "")
	id := h.captureIDs()[0]
	if err := h.server.processLearningCapture(context.Background(), id); err == nil {
		t.Fatal("expected retryable extraction failure")
	}
	if len(h.memories()) != 0 {
		t.Fatal("failed extraction exposed memory")
	}
	c.fail = false
	historyStatus(t, h.request("POST", "/memory/learning/retry", nil), 200, "")
	h.drain()
	if len(h.memories()) != 3 {
		t.Fatal("retry failed")
	}
	owned := h.memories()[0].ID
	h.user = "oria:" + historyTurnID(5055)
	historyStatus(t, h.request("DELETE", "/memory/"+owned, nil), 404, "")
	if len(h.memories()) != 0 {
		t.Fatal("cross-subject note memory")
	}
}

func TestCL5PauseDuringExtractionAndZeroObservations(t *testing.T) {
	h, c, _ := noteHarness(t)
	historyStatus(t, importNote(h, "personal", "Thanks everyone.", "", false), 202, "")
	h.drain()
	if len(h.memories()) != 0 {
		t.Fatal("small talk learned")
	}
	historyStatus(t, importNote(h, "personal", cl5Note, "", false), 202, "")
	c.hook = func(context.Context) error { h.enable(false); return nil }
	h.drain()
	if len(h.memories()) != 0 {
		t.Fatal("pause during extraction published notes")
	}
	h.enable(true)
	c.hook = nil
	historyStatus(t, importNote(h, "personal", cl5Note, "", false), 202, "")
	h.drain()
	if len(h.memories()) != 0 {
		t.Fatal("suppressed note replay revived capture")
	}
}

func TestCL5HistoricalNotesAndNaturalCorrection(t *testing.T) {
	h, c, _ := noteHarness(t)
	naturalLearn(t, h, cl4Preference)
	comparisons := 0
	c.relationHook = func(context.Context) error { comparisons++; return nil }
	historyStatus(t, importNote(h, "personal", cl5Note, "2020-01-01", false), 202, "")
	h.drain()
	if comparisons != 0 {
		t.Fatal("backfilled notes compared against newer claims")
	}
	// A later ordinary correction can update the dated note's working claim.
	naturalLearn(t, h, cl4Correction)
	observations := cl4Observations(t, h)
	old := observations["Meeting notes report: Alex: I prefer small API review requests."]
	if old.Metadata["working_state"] != "superseded" {
		t.Fatal("natural correction did not update note observation", old)
	}
	if old.RawText != "Alex: I prefer small API review requests." {
		t.Fatal("lost historical source")
	}
	if len(observations) != 5 {
		t.Fatal("correction erased history", len(observations))
	}
}

func TestCL5InvalidImportsStopBeforeParser(t *testing.T) {
	h, _, p := noteHarness(t)
	for _, test := range []struct{ purpose, text, date string }{
		{"", cl5Note, ""}, {"both", cl5Note, ""}, {"personal", cl5Note, "yesterday"}, {"personal", "", ""}, {"personal", strings.Repeat("x", 65537), ""},
	} {
		historyStatus(t, importNote(h, test.purpose, test.text, test.date, false), 400, "")
	}
	if p.calls != 0 || len(h.captureIDs()) != 0 {
		t.Fatal("invalid import reached parser or storage")
	}
}

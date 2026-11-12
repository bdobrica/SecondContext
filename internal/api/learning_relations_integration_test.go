package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	memsvc "github.com/bdobrica/SecondContext/internal/memory"
	"github.com/bdobrica/SecondContext/internal/retrieval"
)

const (
	cl4Preference  = "Alex prefers short review requests."
	cl4Correction  = "I had that backwards: Alex prefers a detailed review brief, not a terse request."
	cl4Unavailable = "Alex is unavailable for reviews this week."
	cl4Available   = "Alex is now available for reviews after finishing the incident."
	cl4Action      = "I sent Alex a request to review the API section of the launch proposal."
	cl4OtherAction = "I sent Alex another request to review the budget section of the launch proposal."
	cl4Outcome     = "Alex agreed, but only to review the API section of the launch proposal."
	cl4Ambiguous   = "Alex agreed to review the launch proposal."
	cl4Conflict    = "Alex dislikes detailed review briefs."
)

// Deliberate semantic decisions, not an evaluation of a live model. The fixture
// generator echoes the prompt to expose exactly what subsequent advice receives.
type cl4Client struct {
	*naturalContextClient
	failRelations bool
	relationHook  func(context.Context) error
}

func (c *cl4Client) Generate(ctx context.Context, r llm.GenerateRequest) (llm.GenerateResponse, error) {
	switch r.Messages[0].Content {
	case memsvc.ObservationPrompt:
		_, source, _ := strings.Cut(r.Messages[1].Content, "User message:\n")
		observations := []memsvc.Observation{}
		o := memsvc.Observation{Summary: source, Quote: source, Type: "person_preference", Evidence: "explicit_report", People: []string{"Alex"}, Topics: []string{"review"}}
		switch source {
		case cl4Preference, cl4Correction, cl4Conflict:
		case cl4Unavailable:
			o.Type = "constraint"
			o.TimeScope = "this week"
		case cl4Available:
			o.Type = "constraint"
			o.TimeScope = "now, after the incident"
		case cl4Action, cl4OtherAction:
			o.Type = "reported_action"
		case cl4Outcome, cl4Ambiguous:
			o.Type = "outcome"
		case "Alex Morgan prefers short review requests.":
			o.People = []string{"Alex Morgan"}
		case "Alex prefers short novels.":
			o.Topics = []string{"books"}
		default:
			return llm.GenerateResponse{OutputText: `{"observations":[]}`}, nil
		}
		observations = append(observations, o)
		raw, _ := json.Marshal(map[string]any{"observations": observations})
		return llm.GenerateResponse{OutputText: string(raw)}, nil
	case memsvc.ObservationRelationPrompt:
		if c.relationHook != nil {
			if err := c.relationHook(ctx); err != nil {
				return llm.GenerateResponse{}, err
			}
		}
		if c.failRelations {
			return llm.GenerateResponse{}, errors.New("synthetic relation outage")
		}
		var input struct {
			Observation memsvc.Observation            `json:"observation"`
			Candidates  []memsvc.ObservationCandidate `json:"candidates"`
		}
		_ = json.Unmarshal([]byte(r.Messages[1].Content), &input)
		relations := []memsvc.ObservationRelation{}
		for _, p := range input.Candidates {
			kind := "unrelated"
			switch {
			case input.Observation.Summary == cl4Correction && p.Summary == cl4Preference:
				kind = "correction"
			case input.Observation.Summary == cl4Preference && p.Summary == cl4Preference:
				kind = "agreement"
			case input.Observation.Summary == cl4Conflict && p.Summary == cl4Correction:
				kind = "contradiction"
			case input.Observation.Summary == cl4Available && p.Summary == cl4Unavailable:
				kind = "changed_circumstances"
			case input.Observation.Summary == cl4Outcome && p.Summary == cl4Action:
				kind = "outcome_of"
			}
			relations = append(relations, memsvc.ObservationRelation{ID: p.ID, Kind: kind, PriorQuote: p.Summary, NewQuote: input.Observation.Quote, Reason: "Fixture compares the same person, claim and stated scope."})
		}
		raw, _ := json.Marshal(map[string]any{"relations": relations})
		return llm.GenerateResponse{OutputText: string(raw)}, nil
	}
	return c.naturalContextClient.Generate(ctx, r)
}

func cl4Harness(t *testing.T) (*learningHarness, *cl4Client) {
	h, n := naturalHarness(t)
	c := &cl4Client{naturalContextClient: n}
	h.server = NewServerWithClient(h.cfg, testChatLogger(), h.pool, c)
	h.enable(true)
	return h, c
}

func cl4Observations(t *testing.T, h *learningHarness) map[string]memoryResponse {
	t.Helper()
	w := h.request("GET", "/memory/observations", nil)
	historyStatus(t, w, 200, "")
	var response memoryListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	out := map[string]memoryResponse{}
	for _, m := range response.Data {
		out[m.Summary] = m
	}
	return out
}

func TestCL4CorrectionAndChangingAvailability(t *testing.T) {
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses"} {
		t.Run(endpoint, func(t *testing.T) {
			h, _ := cl4Harness(t)
			report := func(text string) {
				t.Helper()
				historyStatus(t, naturalAsk(h, endpoint, []llm.Message{{Role: "user", Content: text}}, nil), 200, "")
				h.drain()
			}
			report(cl4Preference)
			before := naturalAsk(h, endpoint, []llm.Message{{Role: "user", Content: "Help me ask Alex for review."}}, nil)
			if !strings.Contains(before.Body.String(), cl4Preference) {
				t.Fatal("missing original preference")
			}
			report(cl4Correction)
			after := naturalAsk(h, endpoint, []llm.Message{{Role: "user", Content: "Help me ask Alex for review."}}, nil)
			if !strings.Contains(after.Body.String(), cl4Correction) || strings.Contains(after.Body.String(), cl4Preference) || !strings.Contains(after.Body.String(), "correction:") {
				t.Fatal("correction did not change later advice context", after.Body)
			}
			observations := cl4Observations(t, h)
			if len(observations) != 2 || observations[cl4Preference].Metadata["working_state"] != "superseded" || observations[cl4Preference].RawText != cl4Preference {
				t.Fatal("history not retained", observations)
			}
			// A vector hit on only the obsolete wording must still find its replacement.
			old := observations[cl4Preference]
			repo := db.NewMemoryRepository(h.pool)
			ids, err := repo.ExpandObservationIDs(context.Background(), old.UserID, []string{old.ID})
			if err != nil || len(ids) != 2 {
				t.Fatal("replacement expansion", ids, err)
			}
			report(cl4Unavailable)
			report(cl4Available)
			observations = cl4Observations(t, h)
			if observations[cl4Unavailable].Metadata["working_state"] != "superseded" || observations[cl4Available].Metadata["working_state"] != "current" {
				t.Fatal("temporal change treated as contradiction", observations)
			}
			after = naturalAsk(h, endpoint, []llm.Message{{Role: "user", Content: "Help me ask Alex for review."}}, nil)
			if strings.Contains(after.Body.String(), cl4Unavailable) || !strings.Contains(after.Body.String(), cl4Available) || !strings.Contains(after.Body.String(), "changed_circumstances:") {
				t.Fatal("availability did not change advice context", after.Body)
			}
			// Replaying the source does not add observations or reinforce confidence.
			report(cl4Correction)
			if len(cl4Observations(t, h)) != 4 {
				t.Fatal("source replay duplicated evidence")
			}
		})
	}
}

func TestCL4NaturalOutcomesAndAmbiguousActions(t *testing.T) {
	h, _ := cl4Harness(t)
	for _, source := range []string{cl4Action, cl4OtherAction, cl4Ambiguous, cl4Outcome} {
		naturalLearn(t, h, source)
	}
	items := cl4Observations(t, h)
	if items[cl4Ambiguous].Metadata["relations"] != nil {
		t.Fatal("ambiguous outcome linked")
	}
	links, ok := items[cl4Outcome].Metadata["relations"].([]any)
	if !ok || len(links) != 1 || links[0].(map[string]any)["prior_id"] != items[cl4Action].ID || links[0].(map[string]any)["kind"] != "outcome_of" {
		t.Fatal("specific outcome link missing", items)
	}
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses"} {
		w := naturalAsk(h, endpoint, []llm.Message{{Role: "user", Content: "Help me ask Alex for review."}}, nil)
		if !strings.Contains(w.Body.String(), cl4Outcome) || !strings.Contains(w.Body.String(), "outcome_of:") {
			t.Fatal("natural outcome absent from later advice", w.Body)
		}
	}
	var count int
	if err := h.pool.QueryRow(context.Background(), `SELECT count(*) FROM interaction_outcomes o JOIN users u ON u.id=o.user_id WHERE u.external_id=$1`, h.user).Scan(&count); err != nil || count != 0 {
		t.Fatal("natural report inferred strategy scores", count, err)
	}
	// A draft is not a reported action, even if a later favorable event occurs.
	other, _ := cl4Harness(t)
	historyStatus(t, naturalAsk(other, "/v1/chat/completions", []llm.Message{{Role: "user", Content: "Draft an API review request to Alex."}, {Role: "assistant", Content: cl4Action}, {Role: "user", Content: cl4Outcome}}, nil), 200, "")
	other.drain()
	separate := cl4Observations(t, other)
	if len(separate) != 1 || separate[cl4Outcome].Metadata["relations"] != nil {
		t.Fatal("draft treated as sent action", separate)
	}
}

func TestCL4AgreementConflictAndUnrelatedClaims(t *testing.T) {
	h, _ := cl4Harness(t)
	naturalLearn(t, h, cl4Preference)
	historyStatus(t, h.chat([]llm.Message{{Role: "user", Content: "Another day"}, {Role: "assistant", Content: "Go ahead"}, {Role: "user", Content: cl4Preference}}, nil), 200, "")
	h.drain()
	items := cl4Observations(t, h)
	if items[cl4Preference].Metadata["relations"] == nil || items[cl4Preference].Confidence != 1 {
		t.Fatal("agreement missing or confidence changed")
	}
	naturalLearn(t, h, "Alex Morgan prefers short review requests.")
	naturalLearn(t, h, "Alex prefers short novels.")
	naturalLearn(t, h, cl4Correction)
	naturalLearn(t, h, cl4Conflict)
	items = cl4Observations(t, h)
	for _, text := range []string{"Alex Morgan prefers short review requests.", "Alex prefers short novels."} {
		if items[text].Metadata["working_state"] != "current" || items[text].Metadata["relations"] != nil {
			t.Fatal("unrelated similar claim merged", items[text])
		}
	}
	if items[cl4Correction].Metadata["working_state"] != "disputed" || items[cl4Conflict].Metadata["working_state"] != "disputed" {
		t.Fatal("contradictory reports silently resolved", items)
	}
	results, err := retrieval.NewService(h.cfg, h.pool, h.server.llm).Search(context.Background(), retrieval.SearchParams{UserExternalID: h.user, Query: "Alex review", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	disputed := 0
	for _, r := range results {
		if retrieval.MetadataMap(r.Memory.Metadata)["working_state"] == "disputed" {
			disputed++
		}
		if r.Memory.Summary == cl4Preference {
			t.Fatal("superseded agreement still current")
		}
	}
	if disputed != 2 {
		t.Fatal("conflict evidence missing", disputed)
	}
}

func TestCL4FailureRetryAndInvalidation(t *testing.T) {
	h, c := cl4Harness(t)
	naturalLearn(t, h, cl4Preference)
	c.failRelations = true
	historyStatus(t, h.chat([]llm.Message{{Role: "user", Content: cl4Correction}}, nil), 200, "")
	ids, err := h.server.pendingLearning(context.Background())
	if err != nil || len(ids) != 1 {
		t.Fatal(ids, err)
	}
	if err = h.server.processLearningCapture(context.Background(), ids[0]); err == nil {
		t.Fatal("expected classification failure")
	}
	if len(cl4Observations(t, h)) != 1 {
		t.Fatal("failed comparison published")
	}
	c.failRelations = false
	historyStatus(t, h.request("POST", "/memory/learning/retry", nil), 200, "")
	h.drain()
	items := cl4Observations(t, h)
	old, newer := items[cl4Preference], items[cl4Correction]
	// Editing either side invalidates the inferred relation, retaining the sources.
	historyStatus(t, h.request("PATCH", "/memory/"+newer.ID, map[string]string{"summary": "Alex prefers a medium review brief."}), 200, "")
	if cl4Observations(t, h)[cl4Preference].Metadata["working_state"] != "current" {
		t.Fatal("edit left stale relation")
	}
	historyStatus(t, h.request("DELETE", "/memory/"+old.ID, nil), 200, "")
	naturalLearn(t, h, cl4Preference)
	if len(cl4Observations(t, h)) != 1 {
		t.Fatal("forgotten prefix relearned")
	}
}

func TestCL4CandidateEditedWhileComparing(t *testing.T) {
	for _, action := range []string{"edit", "forget", "pause", "purge"} {
		t.Run(action, func(t *testing.T) {
			h, c := cl4Harness(t)
			naturalLearn(t, h, cl4Preference)
			old := cl4Observations(t, h)[cl4Preference]
			entered, release := make(chan struct{}), make(chan struct{})
			c.relationHook = func(ctx context.Context) error {
				close(entered)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			historyStatus(t, h.chat([]llm.Message{{Role: "user", Content: cl4Correction}}, nil), 200, "")
			ids, err := h.server.pendingLearning(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				done <- h.server.processLearningCapture(ctx, ids[0])
			}()
			select {
			case <-entered:
			case <-time.After(4 * time.Second):
				t.Fatal("comparison did not start")
			}
			switch action {
			case "edit":
				historyStatus(t, h.request("PATCH", "/memory/"+old.ID, map[string]string{"summary": "Alex prefers medium review briefs."}), 200, "")
			case "forget":
				historyStatus(t, h.request("DELETE", "/memory/"+old.ID, nil), 200, "")
			case "pause":
				h.enable(false)
			case "purge":
				historyStatus(t, h.call("/v1/subjects/purge", map[string]string{"user": h.user}, nil), 200, "")
			}
			close(release)
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			if action == "purge" {
				var count int
				if err = h.pool.QueryRow(context.Background(), `SELECT count(*) FROM observation_relations r JOIN users u ON u.id=r.user_id WHERE u.external_id=$1`, h.user).Scan(&count); err != nil || count != 0 {
					t.Fatal("purge retained relations", count, err)
				}
				return
			}
			items := cl4Observations(t, h)
			for _, m := range items {
				if m.Metadata["relations"] != nil {
					t.Fatal("stale decision published", m)
				}
			}
			if action == "pause" && len(items) != 1 {
				t.Fatal("pause published new report")
			}
		})
	}
}

func TestCL4RelationIsolationDeletionAndDerivatives(t *testing.T) {
	h, _ := cl4Harness(t)
	naturalLearn(t, h, cl4Preference)
	old := cl4Observations(t, h)[cl4Preference]
	_, err := h.pool.Exec(context.Background(), `INSERT INTO beliefs(user_id,claim,normalized_claim,stance,confidence,evidence_memory_ids)
 VALUES($1,'outdated preference','outdated preference','supports',0.9,ARRAY[$2::uuid])`, old.UserID, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	naturalLearn(t, h, cl4Correction)
	items := cl4Observations(t, h)
	newer := items[cl4Correction]
	var count int
	if err = h.pool.QueryRow(context.Background(), `SELECT count(*) FROM beliefs WHERE user_id=$1`, old.UserID).Scan(&count); err != nil || count != 0 {
		t.Fatal("superseded derivative retained", count, err)
	}
	other, _ := cl4Harness(t)
	naturalLearn(t, other, cl4Preference)
	foreign := cl4Observations(t, other)[cl4Preference]
	_, err = h.pool.Exec(context.Background(), `INSERT INTO observation_relations(user_id,prior_id,newer_id,kind,prior_quote,new_quote,reason)
 VALUES($1,$2,$3,'correction','x','x','x')`, old.UserID, foreign.ID, newer.ID)
	if err == nil {
		t.Fatal("foreign evidence relation accepted")
	}
	w := other.request("GET", "/memory/evidence?ids="+newer.ID, nil)
	historyStatus(t, w, 200, "")
	if strings.Contains(w.Body.String(), cl4Correction) || strings.Contains(w.Body.String(), old.ID) {
		t.Fatal("foreign relation leaked")
	}
	// Completed evidence links disappear with the forgotten source. Its retained
	// predecessor becomes current; the source occurrence still cannot replay.
	historyStatus(t, h.request("DELETE", "/memory/"+newer.ID, nil), 200, "")
	naturalLearn(t, h, cl4Correction)
	items = cl4Observations(t, h)
	if len(items) != 1 || items[cl4Preference].Metadata["working_state"] != "current" || items[cl4Preference].Metadata["relations"] != nil {
		t.Fatal("forgetting left inferred evidence", items)
	}
}

func TestCL4OpenAISDK(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	python := os.Getenv("SECOND_CONTEXT_CHAT_SDK_PYTHON")
	if python == "" {
		t.Log("unchanged SDK check not requested; set SECOND_CONTEXT_CHAT_SDK_PYTHON")
		return
	}
	h, c := cl4Harness(t)
	h.cfg.Auth.Tokens = append(h.cfg.Auth.Tokens, config.AuthTokenConfig{Subject: h.user, Token: "cl4-sdk-test"})
	h.server = NewServerWithClient(h.cfg, testChatLogger(), h.pool, c)
	gateway := httptest.NewServer(h.server.Handler())
	defer gateway.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); h.server.RunLearning(ctx) }()
	defer func() { cancel(); <-done }()
	output, err := exec.CommandContext(ctx, python, "../../scripts/test-conversational-updates-sdk.py", gateway.URL+"/v1").CombinedOutput()
	if err != nil {
		t.Fatalf("unchanged client: %v\n%s", err, output)
	}
	t.Log(string(output))
}

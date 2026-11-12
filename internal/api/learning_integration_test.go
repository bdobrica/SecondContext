package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	memsvc "github.com/bdobrica/SecondContext/internal/memory"
)

const learningAlex = "Alex prefers small, tightly scoped review requests."
const learningDeadline = "The launch deadline is Friday."

type learningTestClient struct {
	mu             sync.Mutex
	extractions    int
	hook           func(context.Context) error
	failChat       bool
	failExtraction bool
}

func TestLearningPartialIndexRetryAndForget(t *testing.T) {
	for _, forget := range []bool{false, true} {
		t.Run(map[bool]string{false: "retry", true: "forget"}[forget], func(t *testing.T) {
			h := newLearningHarness(t)
			h.enable(true)
			target, err := url.Parse(h.cfg.Qdrant.URL)
			if err != nil {
				t.Fatal(err)
			}
			proxy := httputil.NewSingleHostReverseProxy(target)
			var writes atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/points") && writes.Add(1) == 2 {
					w.WriteHeader(503)
					return
				}
				proxy.ServeHTTP(w, r)
			}))
			defer upstream.Close()
			h.cfg.Qdrant.URL = upstream.URL
			h.server = NewServerWithClient(h.cfg, testChatLogger(), h.pool, h.model)
			transcript := []llm.Message{{Role: "user", Content: learningAlex + " " + learningDeadline}}
			historyStatus(t, h.chat(transcript, nil), 200, "")
			id := h.captureIDs()[0]
			if err = h.server.processLearningCapture(context.Background(), id); err == nil {
				t.Fatal("expected partial index failure")
			}
			memories := h.memories()
			if len(memories) != 2 {
				t.Fatal("partial indexing fixture missing")
			}
			ids := []string{memories[0].ID, memories[1].ID}
			ready, err := db.NewMemoryRepository(h.pool).ListByIDs(context.Background(), ids)
			if err != nil || len(ready) != 0 {
				t.Fatal("partial capture exposed as ready", ready, err)
			}
			if forget {
				historyStatus(t, h.request("DELETE", "/memory/"+memories[0].ID, nil), 200, "")
			}
			historyStatus(t, h.request("POST", "/memory/learning/retry", nil), 200, "")
			h.drain()
			historyStatus(t, h.chat(transcript, nil), 200, "")
			h.drain()
			if forget {
				if len(h.memories()) != 1 {
					t.Fatal("partial retry resurrected forgotten observation")
				}
			} else {
				ready, err = db.NewMemoryRepository(h.pool).ListByIDs(context.Background(), ids)
				if err != nil || len(ready) != 2 {
					t.Fatal("partial retry did not converge", ready, err)
				}
				if h.model.extractions != 1 {
					t.Fatal("retry re-extracted immutable source")
				}
			}
		})
	}
}

func TestLearningLinkedSourceDeletion(t *testing.T) {
	h := newLearningHarness(t)
	h.enable(true)
	ref := projectionRef{Kind: "event", ID: historyTurnID(800), Revision: 1}
	h.projection(ref, "upsert", 801)
	historyStatus(t, h.finalize(1, "accept_local", map[string]any{"user_text": learningAlex, "assistant_text": "draft", "knowledge_refs": []projectionRef{ref}}), 200, "")
	h.drain()
	memories := h.memories()
	if len(memories) != 1 {
		t.Fatal("linked memory missing")
	}
	// A durable source fence must suppress recall even if cleanup has not finished.
	_, err := h.pool.Exec(context.Background(), `INSERT INTO context_session_fences(user_id,session_external_id) SELECT id,$2 FROM users WHERE external_id=$1`, h.user, h.session)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := db.NewMemoryRepository(h.pool).ListByIDs(context.Background(), []string{memories[0].ID})
	if err != nil || len(ready) != 0 {
		t.Fatal("fenced memory remained retrievable", ready, err)
	}
	ref.Revision = 2
	h.projection(ref, "delete", 802)
	if len(h.memories()) != 0 || len(h.captureIDs()) != 0 {
		t.Fatal("source erasure retained derived observation")
	}
	response, err := http.Get(h.cfg.Qdrant.URL + "/collections/" + h.cfg.Qdrant.Collection + "/points/" + memories[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatalf("vector survived erasure: %d", response.StatusCode)
	}
}

func TestLearningWorkerLifecycle(t *testing.T) {
	h := newLearningHarness(t)
	h.enable(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); h.server.RunLearning(ctx) }()
	historyStatus(t, h.chat([]llm.Message{{Role: "user", Content: learningAlex}}, nil), 200, "")
	deadline := time.After(8 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("background capture did not finish")
		case <-ticker.C:
		}
		var count int
		if err := h.pool.QueryRow(context.Background(), `SELECT count(*) FROM learning_captures c JOIN users u ON u.id=c.user_id WHERE u.external_id=$1 AND c.status='completed'`, h.user).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			break
		}
	}
	if len(h.memories()) != 1 {
		t.Fatal("worker did not publish")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestLearningManagedHandoffRecovery(t *testing.T) {
	h := newLearningHarness(t)
	h.enable(true)
	first := h.generate(1, "recent", "generated", "/v1/responses", learningAlex)
	historyStatus(t, first, 200, "")
	// Simulate a lost queue handoff with an intact canonical generation receipt.
	if _, err := h.pool.Exec(context.Background(), "DELETE FROM learning_captures WHERE id=$1", h.captureIDs()[0]); err != nil {
		t.Fatal(err)
	}
	retry := h.generate(1, "recent", "generated", "/v1/responses", learningAlex)
	historyStatus(t, retry, 200, "")
	if retry.Body.String() != first.Body.String() {
		t.Fatal("learning recovery changed canonical response")
	}
	var dated bool
	if err := h.pool.QueryRow(context.Background(), `SELECT c.observed_at=t.created_at FROM learning_captures c JOIN conversation_turns t ON t.user_id=c.user_id AND t.session_id=c.session_id AND t.turn_id=c.turn_id WHERE c.id=$1`, h.captureIDs()[0]).Scan(&dated); err != nil || !dated {
		t.Fatal("managed observation date refreshed", err)
	}
	h.drain()
	if len(h.memories()) != 1 {
		t.Fatal("cached generation could not recover handoff")
	}
	first = h.local(2, learningAlex, "local draft")
	historyStatus(t, first, 200, "")
	if _, err := h.pool.Exec(context.Background(), `DELETE FROM learning_captures WHERE user_id=(SELECT id FROM users WHERE external_id=$1) AND turn_id=$2`, h.user, historyTurnID(2)); err != nil {
		t.Fatal(err)
	}
	retry = h.local(2, learningAlex, "local draft")
	historyStatus(t, retry, 200, "")
	if !historySameJSON(retry.Body.Bytes(), first.Body.Bytes()) {
		t.Fatal("local recovery changed receipt")
	}
	h.drain()
	if len(h.memories()) != 2 {
		t.Fatal("cached local acceptance could not recover handoff")
	}
}

func TestLearningTemporaryBeforeFirstEnable(t *testing.T) {
	h := newLearningHarness(t)
	transcript := []llm.Message{{Role: "user", Content: learningAlex}}
	historyStatus(t, h.chat(transcript, map[string]any{"temporary_chat": true}), 200, "")
	h.enable(true)
	historyStatus(t, h.chat(transcript, nil), 200, "")
	h.drain()
	if len(h.memories()) != 0 {
		t.Fatal("temporary input relearned after first enable")
	}
}

func (c *learningTestClient) Generate(ctx context.Context, r llm.GenerateRequest) (llm.GenerateResponse, error) {
	if len(r.Messages) > 0 && r.Messages[0].Content == memsvc.ObservationRelationPrompt {
		var input struct {
			Candidates []memsvc.ObservationCandidate `json:"candidates"`
		}
		_ = json.Unmarshal([]byte(r.Messages[1].Content), &input)
		relations := []memsvc.ObservationRelation{}
		for _, c := range input.Candidates {
			relations = append(relations, memsvc.ObservationRelation{ID: c.ID, Kind: "unrelated", Reason: "Independent fixture report"})
		}
		raw, _ := json.Marshal(map[string]any{"relations": relations})
		return llm.GenerateResponse{OutputText: string(raw)}, nil
	}
	if len(r.Messages) > 0 && r.Messages[0].Content == memsvc.ObservationPrompt {
		c.mu.Lock()
		c.extractions++
		hook, fail := c.hook, c.failExtraction
		c.mu.Unlock()
		if hook != nil {
			if err := hook(ctx); err != nil {
				return llm.GenerateResponse{}, err
			}
		}
		if fail {
			return llm.GenerateResponse{}, errors.New("synthetic extraction failure")
		}
		_, source, _ := strings.Cut(r.Messages[1].Content, "User message:\n")
		observations := []memsvc.Observation{}
		if source == learningAlex || source == learningAlex+" "+learningDeadline {
			observations = append(observations, memsvc.Observation{Summary: learningAlex, Type: "person_preference", Quote: learningAlex, Evidence: "explicit_report", People: []string{"Alex"}, Topics: []string{}})
		}
		if source == learningAlex+" "+learningDeadline {
			expires := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
			observations = append(observations, memsvc.Observation{Summary: learningDeadline, Type: "constraint", Quote: learningDeadline, Evidence: "explicit_report", TimeScope: "Friday", ExpiresAt: &expires, People: []string{}, Topics: []string{"launch"}})
		}
		raw, _ := json.Marshal(map[string]any{"observations": observations})
		return llm.GenerateResponse{OutputText: string(raw)}, nil
	}
	return llm.GenerateResponse{ID: "answer", OutputText: r.Messages[0].Content}, nil
}
func (c *learningTestClient) Embed(context.Context, llm.EmbedRequest) (llm.EmbedResponse, error) {
	return llm.EmbedResponse{Vector: []float64{1, 0, 0}}, nil
}
func (c *learningTestClient) Chat(ctx context.Context, r llm.ChatRequest, emit func(json.RawMessage) error) (llm.ChatResult, error) {
	var messages []llm.Message
	_ = json.Unmarshal(r.Body["messages"], &messages)
	if r.Stream {
		if err := emit(json.RawMessage(`{"id":"test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":null}]}`)); err != nil {
			return llm.ChatResult{}, err
		}
	}
	c.mu.Lock()
	fail := c.failChat
	c.mu.Unlock()
	if fail {
		return llm.ChatResult{}, errors.New("synthetic interrupted answer")
	}
	raw, _ := json.Marshal(map[string]any{"id": "test", "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": messages[0].Content}, "finish_reason": "stop"}}})
	return llm.ChatResult{Body: raw}, nil
}

type learningHarness struct {
	*historyHarness
	model *learningTestClient
}

func newLearningHarness(t *testing.T) *learningHarness {
	h := newHistoryHarness(t)
	h.cfg.Qdrant.Collection = "learning_" + strings.TrimPrefix(h.user, "oria:")
	h.cfg.Qdrant.VectorSize = 3
	h.cfg.Qdrant.DenseVector = "dense"
	h.cfg.Qdrant.SparseVector = "sparse"
	c := &learningTestClient{}
	h.server = NewServerWithClient(h.cfg, testChatLogger(), h.pool, c)
	l := &learningHarness{h, c}
	collectionURL := h.cfg.Qdrant.URL + "/collections/" + h.cfg.Qdrant.Collection
	t.Cleanup(func() {
		request, _ := http.NewRequest("DELETE", collectionURL, nil)
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			response.Body.Close()
		}
	})
	return l
}
func (h *learningHarness) request(method, path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer chat-service-test")
	r.Header.Set("X-SecondContext-Subject", h.user)
	w := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(w, r)
	return w
}
func (h *learningHarness) enable(enabled bool) {
	h.t.Helper()
	historyStatus(h.t, h.request("PUT", "/memory/learning", map[string]bool{"enabled": enabled}), 200, "")
}
func (h *learningHarness) chat(messages []llm.Message, extra map[string]any) *httptest.ResponseRecorder {
	body := map[string]any{"model": "context-agent-1", "messages": messages, "disable_memory": true, "disable_knowledge": true}
	for k, v := range extra {
		body[k] = v
	}
	return h.request("POST", "/v1/chat/completions", body)
}
func (h *learningHarness) drain() {
	h.t.Helper()
	ids, err := h.server.pendingLearning(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	for _, id := range ids {
		if err = h.server.processLearningCapture(context.Background(), id); err != nil {
			h.t.Fatal(err)
		}
	}
}
func (h *learningHarness) memories() []memoryResponse {
	h.t.Helper()
	w := h.request("GET", "/memory", nil)
	historyStatus(h.t, w, 200, "")
	var list memoryListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		h.t.Fatal(err)
	}
	return list.Data
}
func (h *learningHarness) captureIDs() []string {
	h.t.Helper()
	rows, err := h.pool.Query(context.Background(), `SELECT c.id::text FROM learning_captures c JOIN users u ON u.id=c.user_id WHERE u.external_id=$1 ORDER BY c.observed_at`, h.user)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			h.t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		h.t.Fatal(err)
	}
	return ids
}

func TestLearningOrdinaryCaptureCorrectionAndForgetting(t *testing.T) {
	h := newLearningHarness(t)
	h.enable(true)
	transcript := []llm.Message{{Role: "user", Content: learningAlex + " " + learningDeadline}}
	w := h.chat(transcript, nil)
	historyStatus(t, w, 200, "")
	if h.model.extractions != 0 {
		t.Fatal("extraction held up answer")
	}
	if !strings.Contains(h.request("GET", "/memory/learning", nil).Body.String(), `"status":"pending"`) {
		t.Fatal("pending status missing")
	}
	// Simulate restart before capture; durable queue survives the server instance.
	h.server = NewServerWithClient(h.cfg, testChatLogger(), h.pool, h.model)
	h.drain()
	memories := h.memories()
	if len(memories) != 2 {
		t.Fatalf("memories: %#v", memories)
	}
	var alex memoryResponse
	for _, m := range memories {
		if m.Summary == learningAlex {
			alex = m
		}
		if m.Metadata["speaker"] != "user" || m.Metadata["evidence"] != "explicit_report" || m.Metadata["observed_at"] == nil || m.Metadata["source_quote"] == nil {
			t.Fatalf("provenance: %#v", m)
		}
		if m.RawText != m.Metadata["source_quote"] {
			t.Fatal("unrelated claims copied into observation source")
		}
	}
	historyStatus(t, h.chat(transcript, nil), 200, "")
	h.drain()
	if len(h.memories()) != 2 || h.model.extractions != 1 {
		t.Fatal("retry duplicated evidence")
	}
	if h.memories()[0].CreatedAt != memories[0].CreatedAt {
		t.Fatal("evidence date refreshed")
	}
	// Fixture generator echoes its assembled prompt: this proves context changes,
	// not the usefulness of a real provider's advice (the M1 evaluation is separate).
	question := []llm.Message{{Role: "user", Content: "Help me ask Alex for a review."}}
	w = h.chat(question, map[string]any{"disable_memory": false})
	historyStatus(t, w, 200, "")
	if !strings.Contains(w.Body.String(), learningAlex) {
		t.Fatal("fresh chat did not receive observation")
	}
	corrected := "Alex now prefers comprehensive review requests."
	historyStatus(t, h.request("PATCH", "/memory/"+alex.ID, map[string]string{"summary": corrected}), 200, "")
	w = h.chat(question, map[string]any{"disable_memory": false})
	if !strings.Contains(w.Body.String(), corrected) || strings.Contains(w.Body.String(), learningAlex) {
		t.Fatalf("correction not reflected: %s", w.Body)
	}
	historyStatus(t, h.request("DELETE", "/memory/"+alex.ID, nil), 200, "")
	historyStatus(t, h.chat(transcript, nil), 200, "")
	h.drain()
	w = h.chat(question, map[string]any{"disable_memory": false})
	if strings.Contains(w.Body.String(), corrected) || strings.Contains(w.Body.String(), learningAlex) {
		t.Fatal("forgotten observation returned")
	}
	if len(h.memories()) != 1 {
		t.Fatal("forgetting removed unrelated observation or relearned old one")
	}
	// A new occurrence in an extended transcript is a new report, even if equal.
	expanded := append(transcript, llm.Message{Role: "assistant", Content: "Acknowledged"}, llm.Message{Role: "user", Content: learningAlex})
	historyStatus(t, h.chat(expanded, nil), 200, "")
	h.drain()
	if len(h.memories()) != 2 {
		t.Fatal("distinct report collapsed")
	}
}

func TestLearningControlsNoMemoryAndIsolation(t *testing.T) {
	h := newLearningHarness(t)
	historyStatus(t, h.chat([]llm.Message{{Role: "user", Content: learningAlex}}, nil), 200, "")
	if len(h.captureIDs()) != 0 {
		t.Fatal("learning enabled by default")
	}
	h.enable(true)
	for _, text := range []string{"Hello!", "Imagine Alex agreed to a review.", "Draft a message saying Alex agreed."} {
		historyStatus(t, h.chat([]llm.Message{{Role: "user", Content: text}}, nil), 200, "")
	}
	h.drain()
	if len(h.memories()) != 0 {
		t.Fatal("no-memory fixture created observations")
	}
	h.enable(false)
	transcript := []llm.Message{{Role: "user", Content: learningAlex}}
	historyStatus(t, h.chat(transcript, nil), 200, "")
	h.enable(true)
	historyStatus(t, h.chat(transcript, nil), 200, "")
	h.drain()
	if len(h.memories()) != 0 {
		t.Fatal("paused history learned on resend")
	}
	temporary := []llm.Message{{Role: "user", Content: learningAlex + " " + learningDeadline}}
	historyStatus(t, h.chat(temporary, map[string]any{"temporary_chat": true}), 200, "")
	historyStatus(t, h.chat(temporary, nil), 200, "")
	h.drain()
	if len(h.memories()) != 0 {
		t.Fatal("temporary history learned on resend")
	}
	// An independent user can learn the same source; receipts and data are scoped.
	other := newLearningHarness(t)
	other.enable(true)
	historyStatus(t, other.chat(transcript, nil), 200, "")
	other.drain()
	foreign := other.memories()
	if len(foreign) != 1 || len(h.memories()) != 0 {
		t.Fatal("subject isolation failed")
	}
	historyStatus(t, h.request("DELETE", "/memory/"+foreign[0].ID, nil), 404, "")
	historyStatus(t, h.request("PATCH", "/memory/"+foreign[0].ID, map[string]string{"summary": "wrong"}), 404, "")
}

func TestLearningManagedAcceptance(t *testing.T) {
	h := newLearningHarness(t)
	h.enable(true)
	w := h.generate(1, "recent", "explicit", "/v1/chat/completions", learningAlex)
	historyStatus(t, w, 200, "")
	h.drain()
	if h.model.extractions != 0 {
		t.Fatal("pending draft learned")
	}
	status := h.request("GET", "/memory/learning", nil)
	if !strings.Contains(status.Body.String(), "waiting_acceptance") {
		t.Fatal("acceptance status missing")
	}
	historyStatus(t, h.finalize(1, "abandon", nil), 200, "")
	h.drain()
	if h.model.extractions != 0 {
		t.Fatal("abandoned draft learned")
	}
	w = h.generate(2, "recent", "explicit", "/v1/responses", learningAlex)
	historyStatus(t, w, 200, "")
	historyStatus(t, h.finalize(2, "accept_generated", map[string]any{"response_id": historyResponseID(t, w), "assistant_text": "Assistant invented a sent review."}), 200, "")
	h.drain()
	if len(h.memories()) != 1 {
		t.Fatal("accepted user input not learned")
	}
	historyStatus(t, h.generate(3, "recent", "generated", "/v1/chat/completions", learningAlex), 200, "")
	h.drain()
	if len(h.memories()) != 2 {
		t.Fatal("generated acceptance not learned")
	}
	historyStatus(t, h.local(4, learningAlex, "Assistant draft, not evidence"), 200, "")
	h.drain()
	if len(h.memories()) != 3 {
		t.Fatal("local accepted user input not learned")
	}
	for _, m := range h.memories() {
		if m.RawText != learningAlex {
			t.Fatal("assistant draft stored as evidence")
		}
	}
}

func TestLearningFailureRecoveryAndStreams(t *testing.T) {
	h := newLearningHarness(t)
	h.enable(true)
	transcript := []llm.Message{{Role: "user", Content: learningAlex}}
	h.model.failChat = true
	historyStatus(t, h.chat(transcript, nil), 502, "")
	w := h.chat(transcript, map[string]any{"stream": true})
	if !strings.Contains(w.Body.String(), "incomplete_stream") {
		t.Fatal("incomplete stream missing")
	}
	if len(h.captureIDs()) != 0 {
		t.Fatal("failed answer captured")
	}
	h.model.failChat = false
	h.model.failExtraction = true
	w = h.chat(transcript, map[string]any{"stream": true})
	historyStatus(t, w, 200, "")
	if !strings.Contains(w.Body.String(), "[DONE]") || h.model.extractions != 0 {
		t.Fatal("capture interfered with stream")
	}
	ids := h.captureIDs()
	if len(ids) != 1 {
		t.Fatal("successful stream not queued")
	}
	if err := h.server.processLearningCapture(context.Background(), ids[0]); err == nil {
		t.Fatal("expected extraction failure")
	}
	if !strings.Contains(h.request("GET", "/memory/learning", nil).Body.String(), `"status":"failed"`) {
		t.Fatal("failure not inspectable")
	}
	h.model.failExtraction = false
	historyStatus(t, h.request("POST", "/memory/learning/retry", nil), 200, "")
	h.drain()
	if len(h.memories()) != 1 {
		t.Fatal("retry failed")
	}
}

func TestLearningPauseAndPurgeDuringExtraction(t *testing.T) {
	for _, action := range []string{"pause", "purge"} {
		t.Run(action, func(t *testing.T) {
			h := newLearningHarness(t)
			h.enable(true)
			historyStatus(t, h.request("POST", "/v1/responses", map[string]any{"input": learningAlex, "disable_memory": true, "disable_knowledge": true}), 200, "")
			entered, release := make(chan struct{}), make(chan struct{})
			h.model.hook = func(ctx context.Context) error {
				close(entered)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			done := make(chan error, 1)
			id := h.captureIDs()[0]
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				done <- h.server.processLearningCapture(ctx, id)
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("extractor did not start")
			}
			if action == "pause" {
				h.enable(false)
			} else {
				historyStatus(t, h.call("/v1/subjects/purge", map[string]string{"user": h.user}, nil), 200, "")
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			var count int
			if err := h.pool.QueryRow(context.Background(), `SELECT count(*) FROM memory_items m JOIN users u ON u.id=m.user_id WHERE u.external_id=$1`, h.user).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatal("worker recreated forgotten/paused data")
			}
		})
	}
}

func TestLearningExpiredObservationExcluded(t *testing.T) {
	h := newLearningHarness(t)
	h.enable(true)
	historyStatus(t, h.chat([]llm.Message{{Role: "user", Content: learningAlex}}, nil), 200, "")
	h.drain()
	m := h.memories()[0]
	if _, err := h.pool.Exec(context.Background(), "UPDATE memory_items SET expires_at=now()-interval '1 day' WHERE id=$1", m.ID); err != nil {
		t.Fatal(err)
	}
	w := h.chat([]llm.Message{{Role: "user", Content: "Help me ask Alex for review."}}, map[string]any{"disable_memory": false})
	if strings.Contains(w.Body.String(), learningAlex) {
		t.Fatal("expired observation recalled")
	}
	var models int
	if err := h.pool.QueryRow(context.Background(), `SELECT count(*) FROM person_topic_models p JOIN users u ON u.id=p.user_id WHERE u.external_id=$1`, h.user).Scan(&models); err != nil {
		t.Fatal(err)
	}
	if models != 0 {
		t.Fatal("numeric modeling ran for simple observations")
	}
}

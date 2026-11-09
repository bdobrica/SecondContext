package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/llm"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type historyTestClient struct {
	mu    sync.Mutex
	calls [][]llm.Message
	hook  func(context.Context) error
}

func (c *historyTestClient) Generate(context.Context, llm.GenerateRequest) (llm.GenerateResponse, error) {
	return llm.GenerateResponse{ID: "legacy", OutputText: "legacy unaccepted draft"}, nil
}
func (c *historyTestClient) Embed(context.Context, llm.EmbedRequest) (llm.EmbedResponse, error) {
	return llm.EmbedResponse{}, fmt.Errorf("unexpected embedding")
}
func (c *historyTestClient) Chat(ctx context.Context, r llm.ChatRequest, _ func(json.RawMessage) error) (llm.ChatResult, error) {
	var messages []llm.Message
	if err := json.Unmarshal(r.Body["messages"], &messages); err != nil {
		return llm.ChatResult{}, err
	}
	c.mu.Lock()
	c.calls = append(c.calls, messages)
	n := len(c.calls)
	hook := c.hook
	c.mu.Unlock()
	if hook != nil {
		if err := hook(ctx); err != nil {
			return llm.ChatResult{}, err
		}
	}
	text := fmt.Sprintf("generated draft %d", n)
	raw, _ := json.Marshal(map[string]any{"id": "reused-provider-id", "object": "chat.completion", "created": 1780000000, "model": "synthetic-model", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 12, "completion_tokens": 4, "total_tokens": 16}})
	return llm.ChatResult{Body: raw, Usage: llm.Usage{InputTokens: 12, OutputTokens: 4, TotalTokens: 16}}, nil
}
func (c *historyTestClient) count() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.calls) }
func (c *historyTestClient) last() []llm.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]llm.Message{}, c.calls[len(c.calls)-1]...)
}
func (c *historyTestClient) setHook(h func(context.Context) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hook = h
}

type historyHarness struct {
	t             *testing.T
	pool          *pgxpool.Pool
	cfg           config.Config
	server        *Server
	client        *historyTestClient
	user, session string
}

func newHistoryHarness(t *testing.T) *historyHarness {
	t.Helper()
	if testing.Short() {
		t.Skip("integration")
	}
	pool, cleanup := openIsolationTestDB(t)
	t.Cleanup(cleanup)
	var id string
	if err := pool.QueryRow(context.Background(), "SELECT gen_random_uuid()::text").Scan(&id); err != nil {
		t.Fatal(err)
	}
	cfg := testChatConfig()
	cfg.History = config.HistoryConfig{Enabled: true, RecentExchanges: 8, PromptInputTokens: 32768, ReservedOutputTokens: 4096}
	cfg.Qdrant.URL = os.Getenv("QDRANT_URL")
	cfg.Qdrant.Collection = "history_test"
	h := &historyHarness{t: t, pool: pool, cfg: cfg, client: &historyTestClient{}, user: "oria:" + id, session: "history-" + id}
	h.restart()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE external_id=$1", h.user)
		_, _ = pool.Exec(context.Background(), "DELETE FROM subject_purges WHERE external_id=$1", h.user)
	})
	return h
}
func (h *historyHarness) restart() {
	h.server = NewServerWithClient(h.cfg, testChatLogger(), h.pool, h.client)
}
func historyTurnID(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
func (h *historyHarness) call(path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	h.t.Helper()
	var raw []byte
	if b, ok := body.([]byte); ok {
		raw = b
	} else {
		raw, _ = json.Marshal(body)
	}
	r := httptest.NewRequest("POST", path, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer chat-service-test")
	r.Header.Set("X-SecondContext-Subject", h.user)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(w, r)
	return w
}
func (h *historyHarness) generate(n int, mode, acceptance, endpoint, text string) *httptest.ResponseRecorder {
	body := map[string]any{"model": "context-agent-1", "user": h.user, "disable_memory": true, "disable_knowledge": true}
	if endpoint == "/v1/responses" {
		body["input"] = text
	} else {
		body["messages"] = []llm.Message{{Role: "user", Content: text}}
	}
	return h.call(endpoint, body, map[string]string{"X-SecondContext-Session": h.session, "X-SecondContext-Turn": historyTurnID(n), "X-SecondContext-History": mode, "X-SecondContext-Acceptance": acceptance})
}
func (h *historyHarness) finalize(n int, action string, fields map[string]any) *httptest.ResponseRecorder {
	body := map[string]any{"contract_version": 1, "user": h.user, "session_id": h.session, "turn_id": historyTurnID(n), "action": action}
	for k, v := range fields {
		body[k] = v
	}
	return h.call("/v1/context/turns/finalize", body, nil)
}
func (h *historyHarness) local(n int, user, text string) *httptest.ResponseRecorder {
	return h.finalize(n, "accept_local", map[string]any{"user_text": user, "assistant_text": text, "knowledge_refs": []projectionRef{}})
}
func historyStatus(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d want %d: %s", w.Code, status, w.Body)
	}
	if code != "" {
		assertAPIErrorCode(t, w.Body.Bytes(), code)
	}
}
func historyResponseID(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	historyStatus(t, w, 200, "")
	var b struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(w.Body.Bytes(), &b) != nil || b.ID == "" {
		t.Fatal(w.Body)
	}
	return b.ID
}
func historyDialogue(messages []llm.Message) string {
	var result []string
	for _, m := range messages {
		if m.Role == "user" || m.Role == "assistant" {
			result = append(result, m.Role+":"+m.Content)
		}
	}
	return strings.Join(result, "|")
}

func TestManagedHistoryAcceptanceAndRetryIntegration(t *testing.T) {
	h := newHistoryHarness(t)
	cap := h.call("/v1/context/capabilities", map[string]any{"user": h.user}, nil)
	historyStatus(t, cap, 200, "")
	var capability map[string]json.RawMessage
	_ = json.Unmarshal(cap.Body.Bytes(), &capability)
	if len(capability) != 11 || string(capability["history_modes"]) != `["off","recent"]` {
		t.Fatal(cap.Body)
	}
	out := h.generate(1, "recent", "explicit", "/v1/responses", "first question")
	id := historyResponseID(t, out)
	if out.Header().Get("X-SecondContext-Snapshot") != "0" || out.Header().Get("X-SecondContext-Contract") != "conversation-history-1" {
		t.Fatal(out.Header())
	}
	historyStatus(t, h.generate(2, "recent", "explicit", "/v1/responses", "follow up"), 409, "turn_pending")
	// An entire server restart retains result, snapshot, immutable request binding.
	h.restart()
	retry := h.generate(1, "recent", "explicit", "/v1/responses", "first question")
	historyStatus(t, retry, 200, "")
	if retry.Body.String() != out.Body.String() || h.client.count() != 1 {
		t.Fatal("retry changed result or called provider")
	}
	historyStatus(t, h.generate(1, "recent", "explicit", "/v1/responses", "changed question"), 409, "turn_conflict")
	historyStatus(t, h.finalize(1, "accept_generated", map[string]any{"response_id": "foreign-response", "assistant_text": "replacement"}), 404, "turn_not_found")
	receipt := h.finalize(1, "accept_generated", map[string]any{"response_id": id, "assistant_text": "accepted replacement"})
	historyStatus(t, receipt, 200, "")
	same := h.finalize(1, "accept_generated", map[string]any{"response_id": id, "assistant_text": "accepted replacement"})
	historyStatus(t, same, 200, "")
	if !historySameJSON(same.Body.Bytes(), receipt.Body.Bytes()) {
		t.Fatal("receipt changed")
	}
	historyStatus(t, h.finalize(1, "accept_generated", map[string]any{"response_id": id, "assistant_text": "different"}), 409, "turn_conflict")
	historyStatus(t, h.finalize(1, "abandon", nil), 409, "turn_conflict")
	out2 := h.generate(2, "recent", "generated", "/v1/chat/completions", "follow up")
	id2 := historyResponseID(t, out2)
	if id2 == id || out2.Header().Get("X-SecondContext-Snapshot") != "1" {
		t.Fatal("wrong binding", out2.Header())
	}
	want := "user:first question|assistant:accepted replacement|user:follow up"
	if got := historyDialogue(h.client.last()); got != want {
		t.Fatalf("history %q want %q", got, want)
	}
	// Retained original draft is only a generation retry result, never replay.
	retry = h.generate(1, "recent", "explicit", "/v1/responses", "first question")
	historyStatus(t, retry, 200, "")
	if retry.Body.String() != out.Body.String() {
		t.Fatal("lost original result")
	}
	historyStatus(t, h.generate(3, "off", "generated", "/v1/responses", "separate"), 200, "")
	if historyDialogue(h.client.last()) != "user:separate" {
		t.Fatal("off replayed history")
	}
	historyStatus(t, h.generate(4, "recent", "generated", "/v1/responses", "remember off"), 200, "")
	if !strings.Contains(historyDialogue(h.client.last()), "user:separate|assistant:generated draft 3") {
		t.Fatal("off failed to capture")
	}
	var legacy int
	if err := h.pool.QueryRow(context.Background(), `SELECT count(*) FROM messages m JOIN users u ON u.id=m.user_id WHERE u.external_id=$1`, h.user).Scan(&legacy); err != nil || legacy != 0 {
		t.Fatal("replayed transcript duplicated legacy rows", legacy, err)
	}
}
func TestManagedHistoryLocalWindowAndLegacyIntegration(t *testing.T) {
	h := newHistoryHarness(t)
	legacy := h.call("/v1/responses", map[string]any{"input": "legacy input", "disable_memory": true, "disable_knowledge": true, "metadata": map[string]any{"session_id": h.session}}, nil)
	historyStatus(t, legacy, 200, "")
	for n := 1; n <= 10; n++ {
		historyStatus(t, h.local(n, "legitimate repetition", fmt.Sprintf("local %d", n)), 200, "")
	}
	same := h.local(10, "legitimate repetition", "local 10")
	historyStatus(t, same, 200, "")
	historyStatus(t, h.local(10, "legitimate repetition", "changed"), 409, "turn_conflict")
	historyStatus(t, h.generate(11, "recent", "generated", "/v1/responses", "next"), 200, "")
	messages := h.client.last()
	dialogue := historyDialogue(messages)
	if len(messages) != 18 || strings.Contains(dialogue, "legacy") || strings.Contains(dialogue, "assistant:local 1|") || strings.Contains(dialogue, "assistant:local 2|") || !strings.Contains(dialogue, "assistant:local 3|") {
		t.Fatal("recent window/backfill", dialogue, len(messages))
	}
	if strings.Count(dialogue, "user:legitimate repetition") != 8 {
		t.Fatal("text deduplicated", dialogue)
	}
	// Another chat belonging to the same subject has no inferred transcript.
	h.session += "-other"
	historyStatus(t, h.generate(1, "recent", "generated", "/v1/responses", "fresh"), 200, "")
	if historyDialogue(h.client.last()) != "user:fresh" {
		t.Fatal("cross-session replay")
	}
	// Ordinary supplied conversation cannot create managed history.
	h.call("/v1/chat/completions", map[string]any{"model": "context-agent-1", "messages": []llm.Message{{Role: "user", Content: "supplied"}}, "disable_memory": true, "disable_knowledge": true, "metadata": map[string]any{"session_id": h.session}}, nil)
	historyStatus(t, h.generate(2, "recent", "generated", "/v1/responses", "again"), 200, "")
	if strings.Contains(historyDialogue(h.client.last()), "supplied") {
		t.Fatal("ordinary chat promoted")
	}
}
func TestManagedHistoryAbandonExpiryAndDisableIntegration(t *testing.T) {
	h := newHistoryHarness(t)
	id := historyResponseID(t, h.generate(1, "off", "explicit", "/v1/responses", "pending"))
	h.cfg.History.Enabled = false
	h.restart()
	historyStatus(t, h.call("/v1/context/capabilities", map[string]any{"user": h.user}, nil), 503, "history_unavailable")
	// Already prepared finalize work remains operable while capture is disabled.
	historyStatus(t, h.finalize(1, "accept_generated", map[string]any{"response_id": id, "assistant_text": "accepted while disabled"}), 200, "")
	historyStatus(t, h.local(2, "new", "local"), 503, "history_unavailable")
	h.cfg.History.Enabled = true
	h.restart()
	historyStatus(t, h.generate(2, "recent", "explicit", "/v1/responses", "abandon me"), 200, "")
	abandoned := h.finalize(2, "abandon", nil)
	historyStatus(t, abandoned, 200, "")
	same := h.finalize(2, "abandon", nil)
	historyStatus(t, same, 200, "")
	if !historySameJSON(abandoned.Body.Bytes(), same.Body.Bytes()) {
		t.Fatal("abandon receipt changed")
	}
	historyStatus(t, h.generate(2, "recent", "explicit", "/v1/responses", "abandon me"), 409, "turn_abandoned")
	historyStatus(t, h.finalize(999, "abandon", nil), 404, "turn_not_found")
	id = historyResponseID(t, h.generate(3, "recent", "explicit", "/v1/responses", "expire me"))
	_, err := h.pool.Exec(context.Background(), `UPDATE conversation_turns SET expires_at=now()-interval '1 second' WHERE turn_id=$1 AND user_id=(SELECT id FROM users WHERE external_id=$2)`, historyTurnID(3), h.user)
	if err != nil {
		t.Fatal(err)
	}
	historyStatus(t, h.finalize(3, "accept_generated", map[string]any{"response_id": id, "assistant_text": "too late"}), 410, "turn_expired")
	historyStatus(t, h.generate(3, "recent", "explicit", "/v1/responses", "expire me"), 410, "turn_expired")
	historyStatus(t, h.finalize(3, "abandon", nil), 200, "")
	historyStatus(t, h.generate(4, "recent", "generated", "/v1/responses", "continue"), 200, "")
	if got := historyDialogue(h.client.last()); strings.Contains(got, "abandon me") || strings.Contains(got, "expire me") {
		t.Fatal("terminal content replayed", got)
	}
	var residual int
	err = h.pool.QueryRow(context.Background(), `SELECT count(*) FROM conversation_turns WHERE user_id=(SELECT id FROM users WHERE external_id=$1) AND state='abandoned' AND (user_text IS NOT NULL OR assistant_text IS NOT NULL OR result IS NOT NULL OR request_hash IS NOT NULL OR finalize_hash IS NOT NULL OR config_hash IS NOT NULL OR selected_turns IS NOT NULL OR response_id IS NOT NULL)`, h.user).Scan(&residual)
	if err != nil || residual != 0 {
		t.Fatal("terminal payload survived", residual, err)
	}
}
func TestManagedHistoryConcurrencyAndRecoveryIntegration(t *testing.T) {
	h := newHistoryHarness(t)
	entered, release := make(chan struct{}), make(chan struct{})
	h.client.setHook(func(context.Context) error { close(entered); <-release; return nil })
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- h.generate(1, "recent", "explicit", "/v1/responses", "in flight") }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider not called")
	}
	historyStatus(t, h.generate(1, "recent", "explicit", "/v1/responses", "in flight"), 409, "turn_in_progress")
	historyStatus(t, h.generate(2, "recent", "explicit", "/v1/responses", "other"), 409, "turn_pending")
	close(release)
	historyStatus(t, <-done, 200, "")
	h.client.setHook(nil)
	historyStatus(t, h.finalize(1, "abandon", nil), 200, "")
	h.client.setHook(func(context.Context) error { return fmt.Errorf("synthetic upstream failure") })
	historyStatus(t, h.generate(2, "recent", "generated", "/v1/responses", "retry failure"), 502, "")
	h.cfg.History.RecentExchanges = 7
	h.restart()
	historyStatus(t, h.generate(2, "recent", "generated", "/v1/responses", "retry failure"), 503, "history_unavailable")
	h.cfg.History.RecentExchanges = 8
	h.restart()
	h.client.setHook(nil)
	historyStatus(t, h.generate(2, "recent", "generated", "/v1/responses", "retry failure"), 200, "")
	// Destroy the lock connection while the provider is running. The former worker
	// cannot commit on a fresh pooled connection; a subsequent worker can recover.
	h.client.setHook(func(ctx context.Context) error {
		conn := ctx.Value(subjectConnectionKey{}).(*pgx.Conn)
		_, err := h.pool.Exec(context.Background(), "SELECT pg_terminate_backend($1)", conn.PgConn().PID())
		return err
	})
	historyStatus(t, h.generate(3, "recent", "generated", "/v1/responses", "lost worker"), 503, "history_unavailable")
	h.client.setHook(nil)
	h.restart()
	historyStatus(t, h.generate(3, "recent", "generated", "/v1/responses", "lost worker"), 200, "")
	// Expiry is rechecked at result commit, even if generation started in time.
	h.client.setHook(func(context.Context) error {
		_, err := h.pool.Exec(context.Background(), `UPDATE conversation_turns SET expires_at=now()-interval '1 second' WHERE turn_id=$1 AND user_id=(SELECT id FROM users WHERE external_id=$2)`, historyTurnID(4), h.user)
		return err
	})
	historyStatus(t, h.generate(4, "recent", "generated", "/v1/responses", "late completion"), 410, "turn_expired")
	h.client.setHook(nil)
	historyStatus(t, h.finalize(4, "abandon", nil), 200, "")
}
func TestManagedHistoryOwnershipAndValidationIntegration(t *testing.T) {
	h := newHistoryHarness(t)
	other := newHistoryHarness(t)
	id := historyResponseID(t, h.generate(1, "recent", "explicit", "/v1/responses", "private"))
	other.session = h.session
	historyStatus(t, other.generate(1, "recent", "generated", "/v1/responses", "foreign"), 404, "session_not_found")
	historyStatus(t, other.finalize(1, "accept_generated", map[string]any{"response_id": id, "assistant_text": "foreign"}), 404, "session_not_found")
	historyStatus(t, h.call("/v1/context/capabilities", map[string]any{"user": other.user}, nil), 400, "identity_conflict")
	for _, tc := range []struct {
		raw    string
		status int
		code   string
	}{
		{`{"user":"` + h.user + `","user":"` + h.user + `"}`, 400, "invalid_history_request"},
		{`{"user":"` + h.user + `","unknown":true}`, 400, "invalid_history_request"},
		{`{"user":"\ud800"}`, 400, "invalid_history_request"},
		{`{"user":"` + strings.Repeat("a", 5000) + `"}`, 413, "request_too_large"},
	} {
		historyStatus(t, h.call("/v1/context/capabilities", []byte(tc.raw), nil), tc.status, tc.code)
	}
	historyStatus(t, h.call("/memory/extract", map[string]any{"raw_text": "x"}, nil), 403, "service_scope_denied")
	historyStatus(t, h.finalize(1, "abandon", nil), 200, "")
	// A valid Unicode NUL remains lossless in accepted dialogue (Postgres text
	// cannot contain it, so the managed transcript uses bytea).
	historyStatus(t, h.local(2, "a\x00b", "c\x00d"), 200, "")
	historyStatus(t, h.generate(3, "recent", "generated", "/v1/responses", "unicode"), 200, "")
	if !strings.Contains(historyDialogue(h.client.last()), "a\x00b|assistant:c\x00d") {
		t.Fatal("Unicode changed")
	}
}

func historySameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func TestManagedHistoryProviderBoundsAndIncompleteIntegration(t *testing.T) {
	h := newHistoryHarness(t)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Error("unexpected provider path", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		var body map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid provider request")
		}
		if string(body["max_completion_tokens"]) != "4096" {
			t.Error("missing provider output ceiling")
		}
		var messages []llm.Message
		_ = json.Unmarshal(body["messages"], &messages)
		if !strings.Contains(messages[0].Content, "linked synthetic significance") {
			t.Error("current linked context missing")
		}
		result := testChatCompletion
		if calls.Add(1) == 1 {
			result = strings.Replace(result, `"finish_reason":"stop"`, `"finish_reason":"length"`, 1)
		}
		_, _ = w.Write([]byte(result))
	}))
	defer provider.Close()
	h.cfg.OpenAI.BaseURL = provider.URL
	h.server = NewServer(h.cfg, testChatLogger(), h.pool)
	ref := projectionRef{Kind: "event", ID: historyTurnID(701), Revision: 1}
	h.projection(ref, "upsert", 702)
	body := map[string]any{"model": "context-agent-1", "messages": []llm.Message{{Role: "user", Content: "current linked question"}}, "knowledge_refs": []projectionRef{ref}, "disable_memory": true, "disable_knowledge": true}
	headers := map[string]string{"X-SecondContext-Session": h.session, "X-SecondContext-Turn": historyTurnID(1), "X-SecondContext-History": "recent"}
	historyStatus(t, h.call("/v1/chat/completions", body, headers), 502, "incomplete_generation")
	var accepted int
	if err := h.pool.QueryRow(context.Background(), `SELECT count(*) FROM conversation_turns WHERE user_id=(SELECT id FROM users WHERE external_id=$1) AND state='accepted'`, h.user).Scan(&accepted); err != nil || accepted != 0 {
		t.Fatal("incomplete result accepted", accepted, err)
	}
	out := h.call("/v1/chat/completions", body, headers)
	id := historyResponseID(t, out)
	if id == "chatcmpl_fixture" {
		t.Fatal("provider ID was not bound uniquely")
	}
	cached := h.call("/v1/chat/completions", body, headers)
	historyStatus(t, cached, 200, "")
	if calls.Load() != 2 || out.Body.String() != cached.Body.String() {
		t.Fatal("cached result not durable")
	}
	historyStatus(t, h.finalize(1, "accept_generated", map[string]any{"response_id": id, "assistant_text": " answer "}), 200, "")
	ref.Revision = 2
	h.projection(ref, "delete", 703)
	historyStatus(t, h.call("/v1/chat/completions", body, headers), 410, "session_deleted")
	if calls.Load() != 2 {
		t.Fatal("erased result retry reached provider")
	}
}

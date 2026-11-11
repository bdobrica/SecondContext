package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/decisions"
)

func enableTestDecisions(h *historyHarness, handler http.HandlerFunc) {
	server := httptest.NewServer(handler)
	h.t.Cleanup(server.Close)
	h.cfg.Decisions = config.DecisionsConfig{Enabled: true, BaseURL: server.URL, APIKey: "test-key", Model: "gpt-6-luna", Recipe: decisions.Recipe, CacheSecret: strings.Repeat("s", 32), Timeout: time.Second, MaxInputBytes: 65536, SegmentExchanges: 4}
	h.restart()
}
func TestHistoryObservationKeepsRecentAndExactRetriesIntegration(t *testing.T) {
	h := newHistoryHarness(t)
	var mu sync.Mutex
	requests := []decisions.Request{}
	enableTestDecisions(h, func(w http.ResponseWriter, r *http.Request) {
		var v decisions.Request
		_ = json.NewDecoder(r.Body).Decode(&v)
		mu.Lock()
		requests = append(requests, v)
		mu.Unlock()
		_, _ = fmt.Fprint(w, `{"model":"gpt-6-luna","answers":[{"name":"recent_dependence","type":"predicate","probability":0.01},{"name":"older_reference","type":"predicate","probability":0.01}]}`)
	})
	historyStatus(t, h.local(1, "horoscope tomorrow", "synthetic horoscope interpretation"), 200, "")
	historyStatus(t, h.local(2, "prepared compatibility reading", "synthetic compatibility interpretation"), 200, "")
	historyStatus(t, h.generate(3, "recent", "generated", "/v1/responses", "Regarding the compatibility reading: What does it mean in plain English?"), 200, "")
	if last := h.client.last(); len(last) != 6 || last[2].Content != "synthetic horoscope interpretation" || last[4].Content != "synthetic compatibility interpretation" {
		t.Fatal("standalone decision changed recent generation", last)
	}
	historyStatus(t, h.generate(3, "recent", "generated", "/v1/responses", "Regarding the compatibility reading: What does it mean in plain English?"), 200, "")
	historyStatus(t, h.generate(4, "off", "generated", "/v1/responses", "off mode"), 200, "")
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatal("retry/off triggered observation", len(requests))
	}
	b, _ := json.Marshal(requests[0])
	if !strings.Contains(string(b), "synthetic compatibility interpretation") || !strings.Contains(string(b), "Regarding the compatibility reading") || strings.Contains(string(b), h.user) || strings.Contains(string(b), h.session) {
		t.Fatal("wrong evidence or identity exposure")
	}
	if metrics := h.server.obs.renderPrometheus(); !strings.Contains(metrics, `suggestion="standalone"`) || strings.Contains(metrics, h.user) || strings.Contains(metrics, requests[0].CacheKey) {
		t.Fatal("unsafe/missing metrics")
	}
}
func TestHistoryObservationFallbackIntegration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{{"refusal", 200, `{"model":"gpt-6-luna","answers":[{"name":"recent_dependence","type":"refusal"},{"name":"older_reference","type":"refusal"}]}`}, {"invalid", 200, `{"invalid":true}`}, {"denied", 403, "private upstream error"}, {"outage", 503, "private upstream error"}} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHistoryHarness(t)
			enableTestDecisions(h, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); _, _ = fmt.Fprint(w, tc.body) })
			historyStatus(t, h.local(1, "choose", "red or blue"), 200, "")
			historyStatus(t, h.generate(2, "recent", "generated", "/v1/chat/completions", "the second one"), 200, "")
			if got := h.client.last(); len(got) != 4 || got[2].Content != "red or blue" {
				t.Fatal("fallback lost context")
			}
		})
	}
}
func TestHistoryObservationFencesAndRevalidationIntegration(t *testing.T) {
	for _, mode := range []string{"recent", "adaptive"} {
		t.Run(mode, func(t *testing.T) {
			h := newHistoryHarness(t)
			var calls atomic.Int64
			enableTestDecisions(h, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = fmt.Fprint(w, `{"model":"gpt-6-luna","answers":[{"name":"recent_dependence","type":"predicate","probability":0.9},{"name":"older_reference","type":"predicate","probability":0.1}]}`)
			})
			h.cfg.Decisions.AdaptiveEnabled = mode == "adaptive"
			h.restart()
			ref := projectionRef{Kind: "event", ID: historyTurnID(601), Revision: 1}
			h.projection(ref, "upsert", 602)
			historyStatus(t, h.finalize(1, "accept_local", map[string]any{"user_text": "prepared", "assistant_text": "linked reading", "knowledge_refs": []projectionRef{ref}}), 200, "")
			historyStatus(t, h.generate(2, mode, "generated", "/v1/responses", "explain it"), 200, "")
			if calls.Load() != 1 {
				t.Fatal(calls.Load())
			}
			_, err := h.pool.Exec(context.Background(), `UPDATE context_projections SET revision=2 WHERE user_id=(SELECT id FROM users WHERE external_id=$1) AND record_id=$2`, h.user, ref.ID)
			if err != nil {
				t.Fatal(err)
			}
			historyStatus(t, h.generate(3, mode, "generated", "/v1/responses", "no new refs"), 409, "history_source_conflict")
			if calls.Load() != 1 {
				t.Fatal("stale input reached classifier")
			}
			_, err = h.pool.Exec(context.Background(), `UPDATE context_projections SET revision=1 WHERE user_id=(SELECT id FROM users WHERE external_id=$1) AND record_id=$2`, h.user, ref.ID)
			if err != nil {
				t.Fatal(err)
			}
			ref.Revision = 2
			h.projection(ref, "upsert", 603)
			historyStatus(t, h.generate(2, mode, "generated", "/v1/responses", "explain it"), 410, "session_deleted")
			historyStatus(t, h.generate(3, mode, "generated", "/v1/responses", "no new refs"), 410, "session_deleted")
			if calls.Load() != 1 {
				t.Fatal("deleted input reached classifier")
			}
			historyStatus(t, h.call("/v1/subjects/purge", map[string]any{"user": h.user}, nil), 200, "")
			historyStatus(t, h.generate(3, mode, "generated", "/v1/responses", "no new refs"), 410, "subject_deleted")
			if calls.Load() != 1 {
				t.Fatal("purged input reached classifier")
			}
		})
	}
}
func TestHistoryObservationCanonicalChangeDuringCallIntegration(t *testing.T) {
	h := newHistoryHarness(t)
	enableTestDecisions(h, func(w http.ResponseWriter, r *http.Request) {
		// Fault injection bypasses the production subject lock to prove the second
		// fence check. Production correction/erasure cannot interleave this way.
		_, err := h.pool.Exec(context.Background(), `INSERT INTO context_session_fences(user_id,session_external_id) SELECT id,$2 FROM users WHERE external_id=$1`, h.user, h.session)
		if err != nil {
			t.Error(err)
		}
		w.WriteHeader(503)
	})
	historyStatus(t, h.local(1, "choose", "red or blue"), 200, "")
	historyStatus(t, h.generate(2, "recent", "generated", "/v1/responses", "the second one"), 410, "session_deleted")
	if h.client.count() != 0 {
		t.Fatal("fallback bypassed fence")
	}
}

func TestHistoryObservationSegmentRotationAndPendingIntegration(t *testing.T) {
	h := newHistoryHarness(t)
	h.cfg.History.RecentExchanges = 2
	var mu sync.Mutex
	var requests []decisions.Request
	enableTestDecisions(h, func(w http.ResponseWriter, r *http.Request) {
		var v decisions.Request
		_ = json.NewDecoder(r.Body).Decode(&v)
		mu.Lock()
		requests = append(requests, v)
		mu.Unlock()
		_, _ = fmt.Fprint(w, `{"model":"gpt-6-luna","answers":[{"name":"recent_dependence","type":"predicate","probability":0.9},{"name":"older_reference","type":"predicate","probability":0.1}]}`)
	})
	for i := 1; i <= 3; i++ {
		historyStatus(t, h.local(i, fmt.Sprintf("input %d", i), fmt.Sprintf("reply %d", i)), 200, "")
	}
	historyStatus(t, h.generate(4, "recent", "generated", "/v1/responses", "follow up"), 200, "")
	historyStatus(t, h.generate(5, "recent", "generated", "/v1/responses", "continue"), 200, "")
	historyStatus(t, h.generate(6, "recent", "explicit", "/v1/responses", "draft question"), 200, "")
	historyStatus(t, h.generate(7, "recent", "generated", "/v1/responses", "after draft"), 409, "turn_pending")
	historyStatus(t, h.finalize(6, "abandon", nil), 200, "")
	historyStatus(t, h.generate(7, "recent", "generated", "/v1/responses", "after draft"), 200, "")
	historyStatus(t, h.generate(8, "recent", "generated", "/v1/responses", "rotation"), 200, "")
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 5 {
		t.Fatal(len(requests))
	}
	if requests[0].CacheKey != requests[1].CacheKey || requests[0].Input[0].Content[1].Text != requests[1].Input[0].Content[1].Text {
		t.Fatal("sliding prefix")
	}
	if requests[4].CacheKey == requests[0].CacheKey {
		t.Fatal("segment never rotated")
	}
	for _, r := range requests[3:] {
		b, _ := json.Marshal(r)
		if strings.Contains(string(b), "draft question") {
			t.Fatal("abandoned input reached observer")
		}
	}
	if len(h.client.last()) != 6 {
		t.Fatal("cache segment expanded generator history")
	}
}

func TestHistoryObservationTimeoutAndSizePreserveGenerationIntegration(t *testing.T) {
	for _, mode := range []string{"timeout", "input_limit"} {
		t.Run(mode, func(t *testing.T) {
			h := newHistoryHarness(t)
			var calls atomic.Int64
			enableTestDecisions(h, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); time.Sleep(60 * time.Millisecond) })
			if mode == "timeout" {
				h.cfg.Decisions.Timeout = 20 * time.Millisecond
			} else {
				h.cfg.Decisions.MaxInputBytes = 1024
			}
			h.restart()
			historyStatus(t, h.local(1, "pick a color", "red or blue"), 200, "")
			historyStatus(t, h.generate(2, "recent", "generated", "/v1/responses", "the second one"), 200, "")
			if len(h.client.last()) != 4 {
				t.Fatal("fallback changed recent prompt")
			}
			if mode == "input_limit" && calls.Load() != 0 {
				t.Fatal("oversized classifier request sent")
			}
			if !strings.Contains(h.server.obs.renderPrometheus(), `outcome="`+mode+`"`) {
				t.Fatal("missing fallback metric")
			}
		})
	}
}

func TestHistoryObservationLabelsBudgetFittedRecentIntegration(t *testing.T) {
	h := newHistoryHarness(t)
	h.cfg.History.PromptInputTokens = 4096
	var received decisions.Request
	var mu sync.Mutex
	enableTestDecisions(h, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		_ = json.NewDecoder(r.Body).Decode(&received)
		mu.Unlock()
		w.WriteHeader(503)
	})
	historyStatus(t, h.local(1, "large historical reply", strings.Repeat("x", 6000)), 200, "")
	historyStatus(t, h.generate(2, "recent", "generated", "/v1/responses", "a standalone request"), 200, "")
	if len(h.client.last()) != 2 {
		t.Fatal("expected omitted oversized exchange")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received.Questions) != 2 || !strings.Contains(received.Questions[0].Instructions, "sequence numbers []") {
		t.Fatal("observer called omitted history recent")
	}
}

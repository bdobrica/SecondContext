package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const standaloneDecision = `{"model":"gpt-6-luna","answers":[{"name":"recent_dependence","type":"predicate","probability":0.01},{"name":"older_reference","type":"predicate","probability":0.01}]}`

func enableTestAdaptive(h *historyHarness, handler http.HandlerFunc) {
	enableTestDecisions(h, handler)
	h.cfg.Decisions.Enabled = false // independent of observation
	h.cfg.Decisions.AdaptiveEnabled = true
	h.restart()
}
func TestHistoryAdaptiveSelectionAndRecoveryIntegration(t *testing.T) {
	for _, omit := range []bool{true, false} {
		t.Run(fmt.Sprint(omit), func(t *testing.T) {
			h := newHistoryHarness(t)
			var calls atomic.Int64
			enableTestAdaptive(h, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if omit {
					fmt.Fprint(w, standaloneDecision)
				} else {
					w.WriteHeader(503)
				}
			})
			historyStatus(t, h.local(1, "compare two choices", "first walking, second cycling"), 200, "")
			h.client.setHook(func(context.Context) error { return fmt.Errorf("synthetic generator outage") })
			historyStatus(t, h.generate(2, "adaptive", "explicit", "/v1/responses", "Why do rainbows form?"), 502, "")
			want := 4
			if omit {
				want = 2
			}
			if len(h.client.last()) != want {
				t.Fatal("incorrect selection", h.client.last())
			}
			// SQL verifies NULL versus an empty array survives round-tripping/restart.
			var pendingSelection bool
			err := h.pool.QueryRow(context.Background(), `SELECT selected_turns IS NULL FROM conversation_turns WHERE turn_id=$1 AND user_id=(SELECT id FROM users WHERE external_id=$2)`, historyTurnID(2), h.user).Scan(&pendingSelection)
			if err != nil || pendingSelection {
				t.Fatal("selection not durable", err)
			}
			h.restart()
			h.client.setHook(nil)
			first := h.generate(2, "adaptive", "explicit", "/v1/responses", "Why do rainbows form?")
			historyStatus(t, first, 200, "")
			if !strings.Contains(h.server.obs.renderPrometheus(), `outcome="selection_reused"`) {
				t.Fatal("unmeasured generation retry")
			}
			if calls.Load() != 1 || len(h.client.last()) != want {
				t.Fatal("retry reclassified or changed prompt")
			}
			again := h.generate(2, "adaptive", "explicit", "/v1/responses", "Why do rainbows form?")
			if again.Body.String() != first.Body.String() || calls.Load() != 1 || h.client.count() != 2 {
				t.Fatal("saved retry changed")
			}
			// Adaptive disablement preserves finalize, abandonment and recent replay.
			h.cfg.Decisions.AdaptiveEnabled = false
			h.restart()
			historyStatus(t, h.generate(3, "adaptive", "generated", "/v1/responses", "new"), 400, "unsupported_feature")
			historyStatus(t, h.finalize(2, "accept_generated", map[string]any{"response_id": historyResponseID(t, first), "assistant_text": "accepted rainbow explanation"}), 200, "")
			historyStatus(t, h.generate(3, "recent", "generated", "/v1/responses", "the second one"), 200, "")
			if len(h.client.last()) != 6 || calls.Load() != 1 {
				t.Fatal("rollback lost accepted history")
			}
		})
	}
}
func TestHistoryAdaptiveFallbacksIntegration(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		delay      bool
	}{
		{"uncertain", strings.Replace(standaloneDecision, "0.01", "0.4", 1), 200, false},
		{"threshold", strings.Replace(standaloneDecision, "0.01", "0.06", 1), 200, false},
		{"older", strings.ReplaceAll(standaloneDecision, "0.01", "0.9"), 200, false},
		{"refusal", `{"model":"gpt-6-luna","answers":[{"name":"recent_dependence","type":"refusal"},{"name":"older_reference","type":"refusal"}]}`, 200, false},
		{"invalid", `{}`, 200, false}, {"denied", "", 403, false}, {"timeout", "", 200, true}, {"input_limit", "", 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHistoryHarness(t)
			var calls atomic.Int64
			enableTestAdaptive(h, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if tc.delay {
					time.Sleep(60 * time.Millisecond)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			if tc.delay {
				h.cfg.Decisions.Timeout = 20 * time.Millisecond
			}
			if tc.name == "input_limit" {
				h.cfg.Decisions.MaxInputBytes = 1024
			}
			h.restart()
			historyStatus(t, h.local(1, "two choices", "first red, second blue"), 200, "")
			historyStatus(t, h.generate(2, "adaptive", "generated", "/v1/chat/completions", "the second one"), 200, "")
			if len(h.client.last()) != 4 {
				t.Fatal("fallback lost context")
			}
			if tc.name == "input_limit" && calls.Load() != 0 {
				t.Fatal("oversize was sent")
			}
			if !strings.Contains(h.server.obs.renderPrometheus(), `selection="recent"`) {
				t.Fatal("missing applied selection")
			}
		})
	}
}
func TestHistoryAdaptiveCapabilityEmptyAndConfigIntegration(t *testing.T) {
	h := newHistoryHarness(t)
	var calls atomic.Int64
	caps := func() string {
		w := h.call("/v1/context/capabilities", map[string]any{"user": h.user}, nil)
		historyStatus(t, w, 200, "")
		return w.Body.String()
	}
	if strings.Contains(caps(), "adaptive") {
		t.Fatal("default advertised")
	}
	enableTestAdaptive(h, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, standaloneDecision) })
	if !strings.Contains(caps(), "adaptive") {
		t.Fatal("enabled not advertised")
	}
	historyStatus(t, h.generate(1, "adaptive", "generated", "/v1/chat/completions", "hello"), 200, "")
	if calls.Load() != 0 || len(h.client.last()) != 2 {
		t.Fatal("empty replay invoked classifier")
	}
	h.client.setHook(func(context.Context) error { return fmt.Errorf("outage") })
	historyStatus(t, h.generate(2, "adaptive", "generated", "/v1/chat/completions", "new topic"), 502, "")
	h.cfg.Decisions.SegmentExchanges++
	h.restart()
	historyStatus(t, h.generate(2, "adaptive", "generated", "/v1/chat/completions", "new topic"), 503, "history_unavailable")
	historyStatus(t, h.finalize(2, "abandon", nil), 200, "")
}
func TestHistoryAdaptiveFencesIntegration(t *testing.T) {
	h := newHistoryHarness(t)
	var calls atomic.Int64
	enableTestAdaptive(h, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, err := h.pool.Exec(context.Background(), `INSERT INTO context_session_fences(user_id,session_external_id) SELECT id,$2 FROM users WHERE external_id=$1`, h.user, h.session)
		if err != nil {
			t.Error(err)
		}
		fmt.Fprint(w, standaloneDecision)
	})
	historyStatus(t, h.local(1, "prepared", "accepted reading"), 200, "")
	historyStatus(t, h.generate(2, "adaptive", "generated", "/v1/responses", "standalone"), 410, "session_deleted")
	historyStatus(t, h.generate(2, "adaptive", "generated", "/v1/responses", "standalone"), 410, "session_deleted")
	if h.client.count() != 0 || calls.Load() != 1 {
		t.Fatal("fence bypassed")
	}
	historyStatus(t, h.call("/v1/subjects/purge", map[string]any{"user": h.user}, nil), 200, "")
	historyStatus(t, h.generate(3, "adaptive", "generated", "/v1/responses", "standalone"), 410, "subject_deleted")
}

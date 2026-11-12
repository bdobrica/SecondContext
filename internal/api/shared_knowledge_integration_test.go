package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/knowledge"
	"github.com/bdobrica/SecondContext/internal/llm"
)

// Real Postgres/Qdrant personal memories, HTTP reference fixture and deterministic
// inference. This checks the actual prompt boundary, not live model answer quality.
func TestCL6SharedReferencesWithPrivateMemoryAndLifecycle(t *testing.T) {
	h, c := cl4Harness(t)
	alice := h.user
	var bob string
	if err := h.pool.QueryRow(context.Background(), "SELECT 'oria:' || gen_random_uuid()::text").Scan(&bob); err != nil {
		t.Fatal(err)
	}
	defer func() { h.user = alice }()
	t.Cleanup(func() { _, _ = h.pool.Exec(context.Background(), "DELETE FROM users WHERE external_id=$1", bob) })
	var version atomic.Int32
	version.Store(1)
	var reads atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/search" {
			t.Error("conversation attempted to write shared knowledge")
			w.WriteHeader(500)
			return
		}
		reads.Add(1)
		items := []knowledge.KnowledgeEvidence{}
		e := knowledgeFixture()
		switch r.Header.Get("Authorization") {
		case "Bearer shared-handbook":
			switch version.Load() {
			case 1:
				items = append(items, e)
			case 2:
				e.Text = "Production deployment requires exactly three approvers."
				e.ChunkID = historyTurnID(6602)
				items = append(items, e)
			}
		case "Bearer alice-references":
			e.Text = "Alice's private reference: cobalt review checklist."
			e.ChunkID = historyTurnID(6610)
			e.DocumentID = historyTurnID(6611)
			e.SourceID = historyTurnID(6612)
			items = append(items, e)
		case "Bearer bob-references":
			e.Text = "Bob's private reference: amber review checklist."
			e.ChunkID = historyTurnID(6620)
			e.DocumentID = historyTurnID(6621)
			e.SourceID = historyTurnID(6622)
			items = append(items, e)
		default:
			t.Error("unknown credential")
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": items})
	}))
	defer upstream.Close()
	h.cfg.Knowledge = config.KnowledgeConfig{Enabled: true, URL: upstream.URL, Limit: 4, SharedToken: "shared-handbook", Tokens: []config.AuthTokenConfig{{Subject: alice, Token: "alice-references"}, {Subject: bob, Token: "bob-references"}}}
	h.server = NewServerWithClient(h.cfg, testChatLogger(), h.pool, c)
	naturalLearn(t, h, cl4Preference)
	h.user = bob
	h.enable(true)
	naturalLearn(t, h, cl4Correction)
	for _, v := range []int32{1, 2, 0} {
		version.Store(v)
		for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses"} {
			for _, user := range []string{alice, bob} {
				h.user = user
				// No history, memory metadata, knowledge filters, or conversation ID.
				w := naturalAsk(h, endpoint, []llm.Message{{Role: "user", Content: "Help me ask Alex for a production review. How many approvers are needed?"}}, nil)
				historyStatus(t, w, 200, "")
				c.mu.Lock()
				raw, _ := json.Marshal(c.generated)
				c.mu.Unlock()
				prompt := string(raw)
				own, foreign, private, otherPrivate := cl4Preference, cl4Correction, "cobalt", "amber"
				if user == bob {
					own, foreign, private, otherPrivate = cl4Correction, cl4Preference, "amber", "cobalt"
				}
				if !strings.Contains(prompt, own) || strings.Contains(prompt, foreign) || !strings.Contains(prompt, private) || strings.Contains(prompt, otherPrivate) {
					t.Fatalf("private context isolation failed: %s", prompt)
				}
				if strings.Contains(prompt, "exactly two approvers") != (v == 1) || strings.Contains(prompt, "exactly three approvers") != (v == 2) {
					t.Fatalf("stale or missing shared evidence, version %d: %s", v, prompt)
				}
				if v != 0 && (!strings.Contains(prompt, knowledgeFixture().URI) || !strings.Contains(prompt, `\"scope\":\"shared\"`) || !strings.Contains(prompt, knowledgeFixture().SourceID)) {
					t.Fatal("shared provenance missing", prompt)
				}
				if !strings.Contains(prompt, `\"scope\":\"private\"`) {
					t.Fatal("private provenance missing", prompt)
				}
				h.drain()
				if len(h.memories()) != 1 {
					t.Fatal("reference or assistant content became personal memory")
				}
			}
		}
	}
	if reads.Load() != 24 {
		t.Fatalf("expected fresh shared and private reads on each question: %d", reads.Load())
	}
}

func TestCL6SharedOnlyControlsAndPartialContext(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer private-down" {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []knowledge.KnowledgeEvidence{knowledgeFixture()}})
	}))
	defer upstream.Close()
	cfg := knowledgeTestConfig(upstream.URL)
	cfg.Knowledge.SharedToken = "shared"
	cfg.Knowledge.Tokens = []config.AuthTokenConfig{{Subject: "alice", Token: "private-down"}}
	cfg.Auth = config.AuthConfig{Enabled: true, Tokens: []config.AuthTokenConfig{{Subject: "alice", Token: "alice-token"}, {Subject: "bob", Token: "bob-token"}}}
	client := &fakeLLMClient{response: llm.GenerateResponse{OutputText: "answer"}}
	s := NewServerWithClient(cfg, testChatLogger(), nil, client)
	for _, test := range []struct{ token, body, status string }{
		{"bob-token", `{"input":"approvers?","disable_memory":true}`, "ready"},
		{"alice-token", `{"input":"approvers?"}`, "partial"},
		{"alice-token", `{"input":"approvers?","disable_knowledge":true}`, "disabled"},
	} {
		r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(test.body))
		r.Header.Set("Authorization", "Bearer "+test.token)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		historyStatus(t, w, 200, "")
		var result createResponseResult
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		packet := result.Metadata["context_packet"].(map[string]any)
		if packet["knowledge_status"] != test.status {
			t.Fatal(packet)
		}
		prompt := client.request.Messages[0].Content
		if strings.Contains(prompt, "exactly two approvers") != (test.status != "disabled") {
			t.Fatal("knowledge control failed", prompt)
		}
		if test.status == "partial" && !strings.Contains(prompt, "partial retrieval") {
			t.Fatal("partial retrieval not disclosed to model")
		}
	}
	// A shared collection does not relax gateway authentication or subject checks.
	for _, test := range []struct {
		token, body string
		status      int
	}{
		{"", `{"input":"approvers?"}`, 401},
		{"bob-token", `{"input":"approvers?","user":"alice"}`, 400},
	} {
		r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(test.body))
		r.Header.Set("Authorization", "Bearer "+test.token)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		historyStatus(t, w, test.status, "")
	}
}

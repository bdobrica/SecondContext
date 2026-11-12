package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bdobrica/SecondContext/internal/llm"
)

func TestMemoryCommandsM1CorrectionAndForgetting(t *testing.T) {
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses"} {
		t.Run(endpoint, func(t *testing.T) {
			h, _ := naturalHarness(t)
			ask := func(text string, extra map[string]any) string {
				t.Helper()
				w := naturalAsk(h, endpoint, []llm.Message{{Role: "user", Content: text}}, extra)
				historyStatus(t, w, 200, "")
				if parseMemoryCommand([]llm.Message{{Role: "user", Content: text}}).Action != "" && w.Header().Get("X-SecondContext-Capture") == "pending" {
					t.Fatal("command falsely reported pending capture")
				}
				return w.Body.String()
			}
			if !strings.Contains(ask("Enable learning", nil), "Learning is enabled") {
				t.Fatal("enable failed")
			}
			naturalLearn(t, h, learningAlex)
			if !strings.Contains(ask("Help me ask Alex for review.", nil), learningAlex) {
				t.Fatal("no fresh recall")
			}
			inspect := ask("What do you remember about Alex?", nil)
			id := h.memories()[0].ID
			if !strings.Contains(inspect, id) || !strings.Contains(inspect, "explicit_report") || !strings.Contains(inspect, "capture:") {
				t.Fatal("inspection missing evidence", inspect)
			}
			corrected := "Alex now prefers comprehensive review requests."
			reply := ask("Correct memory Alex to "+corrected, nil)
			if !strings.Contains(reply, "Corrected memory "+id) {
				t.Fatal("correction failed", reply)
			}
			fresh := ask("Help me ask Alex for review.", nil)
			if !strings.Contains(fresh, corrected) || strings.Contains(fresh, learningAlex) || !strings.Contains(fresh, "user_corrected=") {
				t.Fatal("fresh advice has wrong context", fresh)
			}
			// The correction command is never re-extracted as a new observation.
			h.drain()
			if len(h.memories()) != 1 {
				t.Fatal("command captured as evidence")
			}
			if !strings.Contains(ask("Forget memory "+id, nil), "Forgot memory") {
				t.Fatal("forget failed")
			}
			// A replayed old source remains suppressed; a new report remains possible.
			naturalLearn(t, h, learningAlex)
			fresh = ask("Help me ask Alex for review.", nil)
			if strings.Contains(fresh, corrected) || strings.Contains(fresh, learningAlex) || len(h.memories()) != 0 {
				t.Fatal("forgotten memory revived", fresh)
			}
			naturalAsk(h, endpoint, []llm.Message{{Role: "user", Content: "New report today"}, {Role: "assistant", Content: "Go ahead"}, {Role: "user", Content: learningAlex}}, nil)
			h.drain()
			if len(h.memories()) != 1 {
				t.Fatal("new report not learned")
			}
			ask("Pause learning", nil)
			if !strings.Contains(ask("Help me ask Alex for review.", nil), learningAlex) {
				t.Fatal("pause disabled recall")
			}
			if strings.Contains(ask("Help me ask Alex for review.", map[string]any{"temporary_chat": true}), learningAlex) {
				t.Fatal("temporary leaked personal evidence")
			}
			before := len(h.memories())
			ask(learningAlex+" "+learningDeadline, nil)
			h.drain()
			if len(h.memories()) != before {
				t.Fatal("paused capture published")
			}
			ask("Resume learning", nil)
			ask(learningAlex+" "+learningDeadline, nil)
			h.drain()
			if len(h.memories()) != before {
				t.Fatal("suppressed prefix revived")
			}
		})
	}
}

func TestMemoryCommandsAmbiguityControlsAndManagedReceipts(t *testing.T) {
	h, _ := naturalHarness(t)
	h.enable(true)
	naturalLearn(t, h, "Alex Chen prefers small review requests.")
	naturalLearn(t, h, "Alex Morgan prefers broad review requests.")
	for _, command := range []string{"Forget memory Alex", "Correct memory Alex to Alex prefers broad reviews."} {
		w := naturalAsk(h, "/v1/chat/completions", []llm.Message{{Role: "user", Content: command}}, nil)
		historyStatus(t, w, 200, "")
		if !strings.Contains(w.Body.String(), "Several observations match") || len(h.memories()) != 2 {
			t.Fatal("ambiguous mutation", w.Body)
		}
	}
	id := h.memories()[0].ID
	for _, controls := range []map[string]any{{"temporary_chat": true}, {"disable_memory": true}} {
		w := naturalAsk(h, "/v1/responses", []llm.Message{{Role: "user", Content: "Forget memory " + id}}, controls)
		historyStatus(t, w, 200, "")
		if len(h.memories()) != 2 || !strings.Contains(w.Body.String(), "disabled") {
			t.Fatal("controls ignored")
		}
	}
	// A foreign account cannot discover or mutate a target by its known ID.
	original := h.user
	h.user = "oria:" + historyTurnID(900)
	w := naturalAsk(h, "/v1/responses", []llm.Message{{Role: "user", Content: "Forget memory " + id}}, nil)
	historyStatus(t, w, 200, "")
	if !strings.Contains(w.Body.String(), "No matching") {
		t.Fatal("foreign observation exposed", w.Body)
	}
	h.user = original
	if len(h.memories()) != 2 {
		t.Fatal("foreign mutation")
	}
	w = naturalAsk(h, "/v1/chat/completions", []llm.Message{{Role: "user", Content: "Forget memory " + id}}, map[string]any{"response_format": map[string]string{"type": "json_object"}})
	historyStatus(t, w, 400, "memory_command_requires_text")
	if len(h.memories()) != 2 {
		t.Fatal("unsupported format mutated")
	}
	managed := func(n int, acceptance, endpoint, command string) *httptest.ResponseRecorder {
		body := map[string]any{"model": "context-agent-1", "disable_knowledge": true}
		if endpoint == "/v1/responses" {
			body["input"] = command
		} else {
			body["messages"] = []llm.Message{{Role: "user", Content: command}}
		}
		return h.call(endpoint, body, map[string]string{"X-SecondContext-Session": h.session, "X-SecondContext-Turn": historyTurnID(n), "X-SecondContext-History": "recent", "X-SecondContext-Acceptance": acceptance})
	}
	// Explicit acceptance does not authorize side effects of an assistant draft.
	w = managed(1, "explicit", "/v1/responses", "Forget memory "+id)
	historyStatus(t, w, 200, "")
	if !strings.Contains(w.Body.String(), "explicit draft acceptance") || len(h.memories()) != 2 {
		t.Fatal("draft mutated memory", w.Body)
	}
	var response createResponseResult
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	historyStatus(t, h.finalize(1, "accept_generated", map[string]any{"response_id": response.ID, "assistant_text": response.OutputText}), 200, "")
	h.drain()
	if len(h.memories()) != 2 {
		t.Fatal("acceptance performed operation")
	}
	w = managed(2, "generated", "/v1/chat/completions", "Forget memory "+id)
	historyStatus(t, w, 200, "")
	if len(h.memories()) != 1 {
		t.Fatal("managed forget failed", w.Body)
	}
	retry := managed(2, "generated", "/v1/chat/completions", "Forget memory "+id)
	historyStatus(t, retry, 200, "")
	if retry.Body.String() != w.Body.String() {
		t.Fatal("managed receipt changed")
	}
	// SSE local receipt remains consumable without a generator call.
	w = naturalAsk(h, "/v1/chat/completions", []llm.Message{{Role: "user", Content: "Pause learning"}}, map[string]any{"stream": true, "stream_options": map[string]bool{"include_usage": true}})
	historyStatus(t, w, 200, "")
	if !strings.Contains(w.Body.String(), "Learning is paused") || !strings.HasSuffix(w.Body.String(), "data: [DONE]\n\n") {
		t.Fatal("invalid command stream", w.Body)
	}
}

func TestObservationSearchPaginationAndExpiry(t *testing.T) {
	h, _ := naturalHarness(t)
	h.enable(true)
	naturalLearn(t, h, learningAlex)
	id := h.memories()[0].ID
	_, err := h.pool.Exec(context.Background(), `INSERT INTO memory_items(user_id,capture_id,memory_type,source,raw_text,summary,people,topics)
 SELECT m.user_id,m.capture_id,m.memory_type,m.source,m.raw_text,'Search fixture '||n,ARRAY[]::text[],ARRAY[]::text[] FROM memory_items m,generate_series(1,55) n WHERE m.id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	read := func(path string) ([]memoryResponse, bool) {
		t.Helper()
		w := h.request("GET", path, nil)
		historyStatus(t, w, 200, "")
		var v struct {
			Data []memoryResponse `json:"data"`
			More bool             `json:"has_more"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v.Data, v.More
	}
	page, more := read("/memory/observations")
	if len(page) != 50 || !more {
		t.Fatal("missing first page", len(page), more)
	}
	next, more := read("/memory/observations?offset=50")
	if len(next) != 6 || more {
		t.Fatal("missing second page", len(next), more)
	}
	seen := map[string]bool{}
	for _, m := range page {
		seen[m.ID] = true
	}
	for _, m := range next {
		if seen[m.ID] {
			t.Fatal("duplicate pages")
		}
	}
	page, _ = read("/memory/observations?q=" + url.QueryEscape("tightly scoped"))
	if len(page) != 1 || page[0].ID != id || page[0].RecallStatus != "ready" {
		t.Fatal("wrong search", page)
	}
	_, err = h.pool.Exec(context.Background(), "UPDATE memory_items SET expires_at=now()-interval '1 second' WHERE id=$1", id)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = read("/memory/observations?q=" + id)
	if len(page) != 1 || page[0].RecallStatus != "expired" {
		t.Fatal("expiry hidden", page)
	}
	evidence := h.request("GET", "/memory/evidence?ids="+id, nil)
	if strings.Contains(evidence.Body.String(), learningAlex) {
		t.Fatal("expired evidence returned")
	}
	page, _ = read("/memory/observations?q=" + url.QueryEscape("%"))
	if len(page) != 0 {
		t.Fatal("wildcard unexpectedly expanded")
	}
	historyStatus(t, h.request("GET", "/memory/observations?offset=-1", nil), 400, "invalid_observation_query")
}

func TestMemoryDerivativesInvalidatedOnCorrectionAndDelete(t *testing.T) {
	for _, operation := range []string{"correct", "delete"} {
		t.Run(operation, func(t *testing.T) {
			h, _ := naturalHarness(t)
			h.enable(true)
			naturalLearn(t, h, learningAlex)
			id := h.memories()[0].ID
			var user, person, topic string
			ctx := context.Background()
			if err := h.pool.QueryRow(ctx, "SELECT user_id::text FROM memory_items WHERE id=$1", id).Scan(&user); err != nil {
				t.Fatal(err)
			}
			if err := h.pool.QueryRow(ctx, `INSERT INTO people(user_id,name,normalized_name) VALUES($1,'Alex','alex') RETURNING id::text`, user).Scan(&person); err != nil {
				t.Fatal(err)
			}
			if err := h.pool.QueryRow(ctx, `INSERT INTO topics(user_id,name,normalized_name) VALUES($1,'review','review') RETURNING id::text`, user).Scan(&topic); err != nil {
				t.Fatal(err)
			}
			for _, statement := range []string{
				`INSERT INTO person_topic_models(user_id,person_id,topic_id,evidence_count,metadata) VALUES($1,$2,$3,1,jsonb_build_object('evidence_memory_ids',jsonb_build_array($4::text),'evidence_summary','outdated derived claim'))`,
				`INSERT INTO beliefs(user_id,topic_id,claim,normalized_claim,stance,evidence_memory_ids,metadata) VALUES($1,$3,'outdated derived claim','outdated derived claim','supports',ARRAY[$4::uuid],jsonb_build_object('person',$2::text))`,
				`INSERT INTO graph_edges(user_id,source_kind,source_name,target_kind,target_name,relationship,evidence_memory_ids,metadata) VALUES($1,'person','Alex','topic','review','prefers',ARRAY[$4::uuid],jsonb_build_object('person',$2::text,'topic',$3::text))`,
			} {
				if _, err := h.pool.Exec(ctx, statement, user, person, topic, id); err != nil {
					t.Fatal(err)
				}
			}
			// An independent belief must survive invalidation of this source.
			if _, err := h.pool.Exec(ctx, `INSERT INTO beliefs(user_id,claim,normalized_claim,stance) VALUES($1,'unrelated claim','unrelated claim','supports')`, user); err != nil {
				t.Fatal(err)
			}
			if operation == "correct" {
				historyStatus(t, h.request("PATCH", "/memory/"+id, map[string]string{"summary": "Alex prefers comprehensive review requests."}), 200, "")
			} else {
				historyStatus(t, h.request("DELETE", "/memory/"+id, nil), 200, "")
			}
			for _, table := range []string{"person_topic_models", "beliefs", "graph_edges"} {
				var count int
				if err := h.pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE user_id=$1", user).Scan(&count); err != nil {
					t.Fatal(err)
				}
				expected := 0
				if table == "beliefs" {
					expected = 1
				}
				if count != expected {
					t.Fatalf("%s retained dependent aggregate: %d", table, count)
				}
			}
			w := naturalAsk(h, "/v1/responses", []llm.Message{{Role: "user", Content: "Help me ask Alex for review."}}, map[string]any{"metadata": map[string]any{"people": []string{"Alex"}, "topics": []string{"review"}}})
			historyStatus(t, w, 200, "")
			if strings.Contains(w.Body.String(), "outdated derived claim") {
				t.Fatal("old derivative informed answer", w.Body)
			}
		})
	}
}

func TestMemoryCorrectionIndexFailureDoesNotClaimSuccess(t *testing.T) {
	h, c := naturalHarness(t)
	h.enable(true)
	naturalLearn(t, h, learningAlex)
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer unavailable.Close()
	h.cfg.Qdrant.URL = unavailable.URL
	h.server = NewServerWithClient(h.cfg, testChatLogger(), h.pool, c)
	w := naturalAsk(h, "/v1/responses", []llm.Message{{Role: "user", Content: "Correct memory Alex to Alex prefers broad review requests."}}, nil)
	historyStatus(t, w, 502, "observation_index_failed")
	if strings.Contains(w.Body.String(), "Corrected memory") || h.memories()[0].Summary != learningAlex {
		t.Fatal("failed correction claimed success", w.Body)
	}
}

// Service consumers validate the scope echo on every response path, including
// local command receipts and their managed-history retries.
func TestMemoryCommandResponseSubjectEcho(t *testing.T) {
	for _, mode := range []string{"legacy", "explicit", "generated"} {
		for _, command := range []string{"Pause learning", "Can you show my memories?"} {
			t.Run(mode+"/"+command, func(t *testing.T) {
				h, _ := naturalHarness(t)
				body := map[string]any{"model": "context-agent-1", "input": command, "metadata": map[string]any{"session_id": h.session}}
				headers := map[string]string{}
				if mode != "legacy" {
					headers = map[string]string{"X-SecondContext-Session": h.session, "X-SecondContext-Turn": historyTurnID(1), "X-SecondContext-History": "recent", "X-SecondContext-Acceptance": mode}
				}
				var first string
				for attempt := 0; attempt < 2; attempt++ {
					w := h.call("/v1/responses", body, headers)
					historyStatus(t, w, 200, "")
					var response createResponseResult
					if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
						t.Fatal(err)
					}
					packet, ok := response.Metadata["context_packet"].(map[string]any)
					if !ok || packet["user_external_id"] != h.user || response.Metadata["session_id"] != h.session {
						t.Fatal("command response lost authenticated subject/session echo")
					}
					if mode != "legacy" {
						if response.Metadata["user_external_id"] != h.user {
							t.Fatal("managed response subject echoes disagree")
						}
						if attempt > 0 && w.Body.String() != first {
							t.Fatal("managed retry changed its response")
						}
					}
					first = w.Body.String()
				}
			})
		}
	}
}

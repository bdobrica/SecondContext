package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	memsvc "github.com/bdobrica/SecondContext/internal/memory"
	"github.com/bdobrica/SecondContext/internal/prompts"
)

// A protocol fixture, not a model-quality evaluator. Deliberately identical dense
// vectors force the evidence selector to reject irrelevant semantic candidates.
type naturalContextClient struct {
	base                   *learningTestClient
	mu                     sync.Mutex
	dialogues              [][]llm.Message
	generated              []llm.Message
	chatBody               map[string]json.RawMessage
	embeddings             []string
	failPlan, failEvidence bool
	invalidEvidence        bool
}

func (c *naturalContextClient) Generate(ctx context.Context, r llm.GenerateRequest) (llm.GenerateResponse, error) {
	switch r.Messages[0].Content {
	case contextInterpretationPrompt:
		c.mu.Lock()
		defer c.mu.Unlock()
		var dialogue []llm.Message
		_ = json.Unmarshal([]byte(r.Messages[1].Content), &dialogue)
		c.dialogues = append(c.dialogues, dialogue)
		if c.failPlan {
			return llm.GenerateResponse{}, errors.New("fixture interpretation unavailable")
		}
		latest := dialogue[len(dialogue)-1].Content
		p := contextInterpretation{Query: latest, Goal: latest, People: []string{}, Topics: []string{"review"}, Intent: "answer", ContextMessages: []int{}, ReferenceStatus: "clear"}
		if strings.HasPrefix(latest, "Help me ask") {
			p.Intent = "draft"
		}
		if latest == "What are two other approaches?" || latest == "Make it warmer." {
			p.Query = "Alex review request"
			p.Goal = "request review"
			p.People = []string{"Alex"}
			p.ContextMessages = []int{len(dialogue) - 3, len(dialogue) - 2}
			p.Intent = "alternatives"
			if latest == "Make it warmer." {
				p.Intent = "draft"
			}
		} else {
			for _, name := range []string{"Alex Chen", "Alex Morgan", "Alex Rivera", "Lex", "Jordan", "Alex"} {
				if containsPhrase(latest, name) {
					p.People = []string{name}
					break
				}
			}
		}
		if strings.Contains(latest, "sourdough") {
			p.Topics = []string{"baking"}
		}
		if latest == "Help me ask him." {
			p.ReferenceStatus = "ambiguous"
		}
		raw, _ := json.Marshal(p)
		return llm.GenerateResponse{OutputText: string(raw)}, nil
	case contextEvidencePrompt:
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.failEvidence {
			return llm.GenerateResponse{}, errors.New("fixture selection unavailable")
		}
		if c.invalidEvidence {
			return llm.GenerateResponse{OutputText: `{"evidence":[{"id":"invented","reason":"injected"}]}`}, nil
		}
		var input struct {
			Query    string                  `json:"query"`
			Memories []prompts.ContextMemory `json:"memories"`
		}
		_ = json.Unmarshal([]byte(r.Messages[1].Content), &input)
		selected := []map[string]string{}
		if !strings.Contains(input.Query, "sourdough") {
			for _, m := range input.Memories {
				if containsPhrase(m.Summary, "Alex") {
					selected = append(selected, map[string]string{"id": m.ID, "reason": "The reported review preference helps scope this request."})
				}
			}
		}
		raw, _ := json.Marshal(map[string]any{"evidence": selected})
		return llm.GenerateResponse{OutputText: string(raw)}, nil
	case memsvc.ObservationPrompt:
		_, source, _ := strings.Cut(r.Messages[1].Content, "User message:\n")
		for _, name := range []string{"Alex Chen", "Alex Morgan"} {
			if strings.HasPrefix(source, name) {
				raw, _ := json.Marshal(map[string]any{"observations": []memsvc.Observation{{Summary: source, Quote: source, Type: "person_preference", Evidence: "explicit_report", People: []string{name}, Topics: []string{"review"}}}})
				return llm.GenerateResponse{OutputText: string(raw)}, nil
			}
		}
	default:
		c.mu.Lock()
		c.generated = append([]llm.Message(nil), r.Messages...)
		c.mu.Unlock()
	}
	return c.base.Generate(ctx, r)
}
func (c *naturalContextClient) Embed(ctx context.Context, r llm.EmbedRequest) (llm.EmbedResponse, error) {
	c.mu.Lock()
	c.embeddings = append(c.embeddings, r.Input)
	c.mu.Unlock()
	return c.base.Embed(ctx, r)
}
func (c *naturalContextClient) Chat(ctx context.Context, r llm.ChatRequest, emit func(json.RawMessage) error) (llm.ChatResult, error) {
	c.mu.Lock()
	_ = json.Unmarshal(r.Body["messages"], &c.generated)
	c.chatBody = r.Body
	c.mu.Unlock()
	return c.base.Chat(ctx, r, emit)
}
func naturalHarness(t *testing.T) (*learningHarness, *naturalContextClient) {
	h := newLearningHarness(t)
	c := &naturalContextClient{base: h.model}
	h.server = NewServerWithClient(h.cfg, testChatLogger(), h.pool, c)
	return h, c
}
func naturalAsk(h *learningHarness, endpoint string, messages []llm.Message, extra map[string]any) *httptest.ResponseRecorder {
	body := map[string]any{"model": "context-agent-1"}
	if endpoint == "/v1/responses" {
		body["input"] = messages
	} else {
		body["messages"] = messages
	}
	for k, v := range extra {
		body[k] = v
	}
	return h.request("POST", endpoint, body)
}
func naturalLearn(t *testing.T, h *learningHarness, text string) {
	t.Helper()
	historyStatus(t, h.chat([]llm.Message{{Role: "user", Content: text}}, nil), 200, "")
	h.drain()
}

func TestNaturalContextFreshChatFollowupsAndTopicSwitch(t *testing.T) {
	h, c := naturalHarness(t)
	h.enable(true)
	naturalLearn(t, h, learningAlex+" "+learningDeadline)
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses"} {
		t.Run(endpoint, func(t *testing.T) {
			question := []llm.Message{{Role: "user", Content: "Help me ask Alex for review."}}
			w := naturalAsk(h, endpoint, question, nil)
			historyStatus(t, w, 200, "")
			ids := w.Header().Get("X-SecondContext-Memory-Ids")
			if ids == "" || strings.Contains(ids, ",") || w.Header().Get("X-SecondContext-Interpretation") != "inferred" || w.Header().Get("X-SecondContext-Evidence") != "selected" {
				t.Fatal("missing exact evidence", w.Header(), w.Body)
			}
			if !strings.Contains(c.generated[0].Content, learningAlex) || strings.Contains(c.generated[0].Content, learningDeadline) || !strings.Contains(c.generated[0].Content, "Draft the requested message") {
				t.Fatal("wrong fresh-chat context", c.generated)
			}
			historyStatus(t, h.request("GET", "/memory/evidence?ids="+ids, nil), 200, "")
			for _, followup := range []string{"What are two other approaches?", "Make it warmer.", "How do I bake sourdough?"} {
				dialogue := append(append([]llm.Message{}, question...), llm.Message{Role: "assistant", Content: "Draft: Alex, could you review the API section?"}, llm.Message{Role: "user", Content: followup})
				w = naturalAsk(h, endpoint, dialogue, nil)
				historyStatus(t, w, 200, "")
				prompt := c.generated[0].Content
				if strings.Contains(followup, "sourdough") {
					if w.Header().Get("X-SecondContext-Memory-Ids") != "" || strings.Contains(prompt, learningAlex) || strings.Contains(c.embeddings[len(c.embeddings)-1], "Alex") {
						t.Fatal("old topic contaminated retrieval", prompt)
					}
				} else {
					if w.Header().Get("X-SecondContext-Memory-Ids") != ids || !strings.Contains(prompt, learningAlex) {
						t.Fatal("followup lost context", prompt)
					}
					if followup == "What are two other approaches?" && !strings.Contains(prompt, "Offer a few useful alternative approaches") {
						t.Fatal("natural alternatives unavailable", prompt)
					}
				}
			}
			if endpoint == "/v1/responses" {
				w = naturalAsk(h, endpoint, question, nil)
				var body createResponseResult
				_ = json.Unmarshal(w.Body.Bytes(), &body)
				raw, _ := json.Marshal(body.Metadata["context_packet"])
				if !strings.Contains(string(raw), "evidence_reasons") || !strings.Contains(string(raw), "observed_at") {
					t.Fatal("evidence provenance/reason missing", string(raw))
				}
			} else {
				w = naturalAsk(h, endpoint, question, map[string]any{"stream": true})
				historyStatus(t, w, 200, "")
				if !strings.Contains(w.Body.String(), "data: [DONE]") || w.Header().Get("X-SecondContext-Memory-Ids") != ids {
					t.Fatal("stream lost evidence or format", w.Header(), w.Body)
				}
			}
		})
	}
}

func TestNaturalContextIdentityAliasesAndCorrections(t *testing.T) {
	h, c := naturalHarness(t)
	h.enable(true)
	naturalLearn(t, h, "Alex Chen prefers API-only review requests.")
	naturalLearn(t, h, "Alex Morgan prefers comprehensive review requests.")
	user, err := db.NewUserRepository(h.pool).GetByExternalID(context.Background(), h.user)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.NewPersonRepository(h.pool).Upsert(context.Background(), db.UpsertPersonParams{UserID: user.ID, Name: "Alex Chen", Aliases: []string{"Lex"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Alex", "Alex Chen", "Lex", "Alex Rivera", "Jordan", "him"} {
		w := naturalAsk(h, "/v1/chat/completions", []llm.Message{{Role: "user", Content: "Help me ask " + name + "."}}, nil)
		historyStatus(t, w, 200, "")
		prompt := c.generated[0].Content
		if name == "Alex Chen" || name == "Lex" {
			if !strings.Contains(prompt, "Alex Chen prefers API-only") || strings.Contains(prompt, "Alex Morgan prefers") {
				t.Fatal("alias/full name resolved incorrectly", prompt)
			}
			if name == "Lex" && !strings.Contains(c.embeddings[len(c.embeddings)-1], "Alex Chen") {
				t.Fatal("canonical alias missing from query")
			}
		} else {
			if w.Header().Get("X-SecondContext-Memory-Ids") != "" || strings.Contains(prompt, "prefers API-only") || strings.Contains(prompt, "prefers comprehensive") {
				t.Fatal("ambiguous/unknown identity acquired observations", name, prompt)
			}
			if (name == "Alex" || name == "him") && !strings.Contains(prompt, "Ask for clarification") {
				t.Fatal("missing clarification", prompt)
			}
		}
	}
	var chen memoryResponse
	for _, m := range h.memories() {
		if containsFold(m.People, "Alex Chen") {
			chen = m
		}
	}
	// Explicit metadata overrides ambiguous inferred names and goal/topic hints.
	w := naturalAsk(h, "/v1/responses", []llm.Message{{Role: "user", Content: "Help me ask Alex."}}, map[string]any{"metadata": map[string]any{"people": []string{"Alex Chen"}, "topics": []string{}, "goal": "explicit goal", "memory_mode": "normal_answer"}})
	historyStatus(t, w, 200, "")
	var response createResponseResult
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	raw, _ := json.Marshal(response.Metadata["context_packet"])
	var packet prompts.ContextPacket
	_ = json.Unmarshal(raw, &packet)
	if packet.Goal != "explicit goal" || packet.IdentityUncertain || packet.Intent != "" || len(packet.Topics) != 0 || len(packet.People) != 1 || packet.People[0] != "Alex Chen" {
		t.Fatal("metadata did not override", packet)
	}
	historyStatus(t, h.request("PATCH", "/memory/"+chen.ID, map[string]string{"summary": "Alex Chen now prefers written design questions."}), 200, "")
	w = naturalAsk(h, "/v1/chat/completions", []llm.Message{{Role: "user", Content: "Help me ask Lex."}}, nil)
	if !strings.Contains(c.generated[0].Content, "now prefers written") || strings.Contains(c.generated[0].Content, "prefers API-only") {
		t.Fatal("correction failed", w.Body)
	}
	historyStatus(t, h.request("DELETE", "/memory/"+chen.ID, nil), 200, "")
	evidence := h.request("GET", "/memory/evidence?ids="+chen.ID, nil)
	historyStatus(t, evidence, 200, "")
	if strings.Contains(evidence.Body.String(), chen.ID) {
		t.Fatal("forgotten inspection retained evidence")
	}
	w = naturalAsk(h, "/v1/chat/completions", []llm.Message{{Role: "user", Content: "Help me ask Lex."}}, nil)
	if w.Header().Get("X-SecondContext-Memory-Ids") != "" {
		t.Fatal("stale alias resurrected evidence", w.Body)
	}
}

func TestNaturalContextFallbackIsolationAndControls(t *testing.T) {
	h, c := naturalHarness(t)
	h.enable(true)
	naturalLearn(t, h, learningAlex)
	id := h.memories()[0].ID
	for _, failure := range []string{"plan", "evidence", "invalid evidence"} {
		c.failPlan = failure == "plan"
		c.failEvidence = failure == "evidence"
		c.invalidEvidence = failure == "invalid evidence"
		w := naturalAsk(h, "/v1/chat/completions", []llm.Message{{Role: "user", Content: "Unrelated old travel plans"}, {Role: "assistant", Content: "Okay"}, {Role: "user", Content: "Help me ask Alex for review."}}, nil)
		historyStatus(t, w, 200, "")
		if w.Header().Get("X-SecondContext-Memory-Ids") != id {
			t.Fatal("ordinary retrieval fallback lost evidence", failure, w.Header())
		}
		if c.failPlan && strings.Contains(c.embeddings[len(c.embeddings)-1], "travel") {
			t.Fatal("fallback concatenated old messages")
		}
	}
	c.failPlan = false
	c.failEvidence = false
	c.invalidEvidence = false
	for _, control := range []string{"disable_memory", "temporary_chat"} {
		n := len(c.dialogues)
		w := naturalAsk(h, "/v1/chat/completions", []llm.Message{{Role: "user", Content: "Help me ask Alex."}}, map[string]any{control: true})
		historyStatus(t, w, 200, "")
		if len(c.dialogues) != n || w.Header().Get("X-SecondContext-Memory-Ids") != "" {
			t.Fatal("retrieval disabled but interpreter ran")
		}
	}
	other, _ := naturalHarness(t)
	w := naturalAsk(other, "/v1/chat/completions", []llm.Message{{Role: "user", Content: "Help me ask Alex."}}, nil)
	historyStatus(t, w, 200, "")
	if w.Header().Get("X-SecondContext-Memory-Ids") != "" || strings.Contains(w.Body.String(), learningAlex) {
		t.Fatal("cross-subject recall")
	}
	w = other.request("GET", "/memory/evidence?ids="+id, nil)
	historyStatus(t, w, 200, "")
	if strings.Contains(w.Body.String(), id) {
		t.Fatal("cross-subject evidence inspection")
	}
	historyStatus(t, h.request("GET", "/memory/evidence?ids=not-an-id", nil), 400, "invalid_ids")
}

func TestNaturalContextManagedAcceptedFollowup(t *testing.T) {
	h, c := naturalHarness(t)
	h.enable(true)
	naturalLearn(t, h, learningAlex)
	historyStatus(t, h.local(1, "Help me ask Alex for review.", "Draft: Alex, please review the API."), 200, "")
	body := map[string]any{"model": "context-agent-1", "input": "Make it warmer."}
	w := h.call("/v1/responses", body, map[string]string{"X-SecondContext-Session": h.session, "X-SecondContext-Turn": historyTurnID(2), "X-SecondContext-History": "recent", "X-SecondContext-Acceptance": "explicit"})
	historyStatus(t, w, 200, "")
	if w.Header().Get("X-SecondContext-Memory-Ids") == "" || !strings.Contains(c.generated[0].Content, learningAlex) {
		t.Fatal("managed followup did not receive observation", w.Body)
	}
	d := c.dialogues[len(c.dialogues)-1]
	if len(d) != 3 || d[0].Content != "Help me ask Alex for review." {
		t.Fatal("wrong accepted dialogue", d)
	}
	// The pending draft is never eligible interpretation context for another turn.
	historyStatus(t, h.finalize(2, "abandon", nil), 200, "")
	body["input"] = "What are two other approaches?"
	w = h.call("/v1/responses", body, map[string]string{"X-SecondContext-Session": h.session, "X-SecondContext-Turn": historyTurnID(3), "X-SecondContext-History": "recent", "X-SecondContext-Acceptance": "generated"})
	historyStatus(t, w, 200, "")
	d = c.dialogues[len(c.dialogues)-1]
	if len(d) != 3 || d[2].Content != "What are two other approaches?" {
		t.Fatal("abandoned dialogue replayed", d)
	}
}

func TestNaturalContextPreservesToolAndStructuredOutputInput(t *testing.T) {
	h, c := naturalHarness(t)
	h.enable(true)
	naturalLearn(t, h, learningAlex)
	messages := []map[string]any{
		{"role": "user", "content": "Help me ask Alex for review."},
		{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{"id": "call_review", "type": "function", "function": map[string]string{"name": "lookup", "arguments": `{"person":"Invented Tool Identity"}`}}}},
		{"role": "tool", "tool_call_id": "call_review", "content": "Tool output mentioning someone else"},
	}
	format := map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "draft", "strict": true, "schema": map[string]any{"type": "object", "properties": map[string]any{"draft": map[string]string{"type": "string"}}, "required": []string{"draft"}, "additionalProperties": false}}}
	w := h.request("POST", "/v1/chat/completions", map[string]any{"model": "context-agent-1", "messages": messages, "response_format": format, "tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}}}}})
	historyStatus(t, w, 200, "")
	var forwarded []json.RawMessage
	_ = json.Unmarshal(c.chatBody["messages"], &forwarded)
	if len(forwarded) != len(messages)+1 {
		t.Fatal("tool exchange changed")
	}
	for i, want := range messages {
		expected, _ := json.Marshal(want)
		var a, b any
		_ = json.Unmarshal(expected, &a)
		_ = json.Unmarshal(forwarded[i+1], &b)
		aj, _ := json.Marshal(a)
		bj, _ := json.Marshal(b)
		if string(aj) != string(bj) {
			t.Fatal("tool exchange not preserved", string(bj))
		}
	}
	encoded, _ := json.Marshal(format)
	if string(c.chatBody["response_format"]) != string(encoded) {
		t.Fatal("structured output contract changed")
	}
	dialogue := c.dialogues[len(c.dialogues)-1]
	if len(dialogue) != 1 || strings.Contains(dialogue[0].Content, "Invented Tool Identity") {
		t.Fatal("tools became identity evidence", dialogue)
	}
	if w.Header().Get("X-SecondContext-Memory-Ids") == "" {
		t.Fatal("tool continuation lost selected evidence")
	}
}

func TestNaturalContextOpenAISDK(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	python := os.Getenv("SECOND_CONTEXT_CHAT_SDK_PYTHON")
	if python == "" {
		t.Log("unchanged SDK check not requested; set SECOND_CONTEXT_CHAT_SDK_PYTHON")
		return
	}
	h, c := naturalHarness(t)
	h.enable(true)
	h.cfg.Auth.Tokens = append(h.cfg.Auth.Tokens, config.AuthTokenConfig{Subject: h.user, Token: "natural-sdk-test"})
	h.server = NewServerWithClient(h.cfg, testChatLogger(), h.pool, c)
	gateway := httptest.NewServer(h.server.Handler())
	defer gateway.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); h.server.RunLearning(ctx) }()
	defer func() { cancel(); <-done }()
	output, err := exec.Command(python, "../../scripts/test-conversational-recall-sdk.py", gateway.URL+"/v1").CombinedOutput()
	if err != nil {
		t.Fatalf("unchanged client: %v\n%s", err, output)
	}
	t.Log(string(output))
}

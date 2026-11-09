package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/knowledge"
	"github.com/bdobrica/SecondContext/internal/llm"
	memsvc "github.com/bdobrica/SecondContext/internal/memory"
)

func testChatConfig() config.Config {
	return config.Config{App: config.AppConfig{Env: "test"}, Auth: config.AuthConfig{Enabled: true, Tokens: []config.AuthTokenConfig{{Subject: "chat-subject", Token: "chat-test"}}, ServiceTokens: []config.AuthTokenConfig{{Subject: "oria:", Token: "chat-service-test"}}}, OpenAI: config.OpenAIConfig{ChatModel: "synthetic-model", APIKey: "synthetic-provider-token", RequestTimeout: time.Second * 5}}
}
func testChatLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

const testChatCompletion = `{"id":"chatcmpl_fixture","object":"chat.completion","created":1780000000,"model":"synthetic-model","choices":[{"index":0,"message":{"role":"assistant","content":" answer "},"finish_reason":"stop","logprobs":null}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`

func testChatChunk(delta, finish string) string {
	return fmt.Sprintf(`{"id":"chatcmpl_fixture","object":"chat.completion.chunk","created":1780000000,"model":"synthetic-model","choices":[{"index":0,"delta":%s,"finish_reason":%s}],"usage":null}`, delta, finish)
}
func testChatSSE(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range events {
		_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
		w.(http.Flusher).Flush()
	}
}
func testChatInvoke(handler http.Handler, body, token, subject string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if subject != "" {
		r.Header.Set("X-SecondContext-Subject", subject)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}
func TestChatScopeAndUnavailableStorage(t *testing.T) {
	server := NewServerWithClient(testChatConfig(), testChatLogger(), nil, &fakeLLMClient{})
	body := `{"model":"context-agent-1","messages":[{"role":"user","content":"hello"}]}`
	subject := "oria:11111111-1111-4111-8111-111111111111"
	for _, tc := range []struct {
		token, subject, body string
		status               int
	}{
		{"", "", body, 401}, {"chat-test", "", body, 503}, {"chat-service-test", subject, body, 503},
		{"chat-service-test", "", body, 403}, {"chat-service-test", "foreign:11111111-1111-4111-8111-111111111111", body, 403},
		{"chat-test", subject, body, 403}, {"chat-test", "", strings.Replace(body, `"model":`, `"user":"foreign","model":`, 1), 400},
		{"chat-service-test", subject, strings.Replace(body, `"model":`, `"metadata":{"user_external_id":"foreign"},"model":`, 1), 400},
	} {
		w := testChatInvoke(server.Handler(), tc.body, tc.token, tc.subject)
		if w.Code != tc.status {
			t.Fatalf("got %d want %d: %s", w.Code, tc.status, w.Body)
		}
	}
	for _, path := range []string{"/v1/models", "/v1/chat/completions/saved-id", "/v1/context/capabilities", "/v1/context/turns/finalize"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer chat-service-test")
		r.Header.Set("X-SecondContext-Subject", subject)
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("service access expanded: %s %d", path, w.Code)
		}
	}
}
func TestChatSuppliedHistoryIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool, cleanup := openIsolationTestDB(t)
	defer cleanup()
	captures := make(chan map[string]json.RawMessage, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&b)
		captures <- b
		_, _ = io.WriteString(w, testChatCompletion)
	}))
	defer upstream.Close()
	cfg := testChatConfig()
	cfg.OpenAI.BaseURL = upstream.URL
	server := NewServer(cfg, testChatLogger(), pool)
	handler := server.Handler()
	ctx := context.Background()
	defer pool.Exec(ctx, "DELETE FROM users WHERE external_id='chat-subject'") //nolint:errcheck
	for _, transcript := range []string{
		`[{"role":"user","content":"My project is Iris."}]`,
		`[{"role":"system","content":" Be concise "},{"role":"developer","content":" Use text "},{"role":"user","content":"My project is Iris."},{"role":"assistant","content":"Noted."},{"role":"user","content":"What is its name?"}]`,
		`[{"role":"user","content":"Separate chat about birch trees."}]`,
		`[{"role":"user","content":"lookup"},{"role":"assistant","content":null,"tool_calls":[{"id":"call_x","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_x","content":" found "}]`,
	} {
		body := `{"model":"context-agent-1","messages":` + transcript + `,"disable_memory":true,"disable_knowledge":true,"temperature":0,"max_completion_tokens":32}`
		w := testChatInvoke(handler, body, "chat-test", "")
		if w.Code != 200 || w.Body.String() != testChatCompletion {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		capture := <-captures
		if string(capture["model"]) != `"synthetic-model"` || string(capture["temperature"]) != "0" || string(capture["max_completion_tokens"]) != "32" {
			t.Fatal(capture)
		}
		for _, key := range []string{"disable_memory", "disable_knowledge", "user", "metadata"} {
			if _, ok := capture[key]; ok {
				t.Fatal("local extension leaked upstream", key)
			}
		}
		var expected, sent []json.RawMessage
		_ = json.Unmarshal([]byte(transcript), &expected)
		_ = json.Unmarshal(capture["messages"], &sent)
		if len(sent) != len(expected)+1 {
			t.Fatal("history supplemented or lost")
		}
		for i := range expected {
			var a, b any
			_ = json.Unmarshal(expected[i], &a)
			_ = json.Unmarshal(sent[i+1], &b)
			aj, _ := json.Marshal(a)
			bj, _ := json.Marshal(b)
			if !bytes.Equal(aj, bj) {
				t.Fatalf("message changed: %s %s", aj, bj)
			}
		}
	}
	// Supplied transcripts are not archived or promoted into replayable history.
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM messages m JOIN users u ON u.id=m.user_id WHERE u.external_id='chat-subject'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("archived supplied history: %d %v", count, err)
	}
	// A foreign stored session is rejected before retrieval or generation.
	owner, err := db.NewUserRepository(pool).Ensure(ctx, db.EnsureUserParams{ExternalID: "chat-foreign"})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, "DELETE FROM users WHERE id=$1", owner.ID) //nolint:errcheck
	session := "chat-foreign-" + fmt.Sprint(time.Now().UnixNano())
	if _, err := db.NewSessionRepository(pool).Create(ctx, db.CreateSessionParams{UserID: owner.ID, ExternalID: session, Title: "foreign"}); err != nil {
		t.Fatal(err)
	}
	body := `{"model":"context-agent-1","messages":[{"role":"user","content":"hello"}],"metadata":{"session_id":"` + session + `"},"disable_memory":true}`
	w := testChatInvoke(handler, body, "chat-test", "")
	if w.Code != 404 {
		t.Fatalf("foreign session %d %s", w.Code, w.Body)
	}
	if len(captures) != 0 {
		t.Fatal("foreign session generated")
	}
	// New endpoint observes the same durable subject fence as Responses.
	if _, err := pool.Exec(ctx, "INSERT INTO subject_purges(external_id) VALUES('chat-subject') ON CONFLICT DO NOTHING"); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, "DELETE FROM subject_purges WHERE external_id='chat-subject'") //nolint:errcheck
	w = testChatInvoke(handler, body, "chat-test", "")
	if w.Code != 410 || len(captures) != 0 {
		t.Fatalf("purge fence bypass: %d", w.Code)
	}
}
func TestChatStreamingIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool, cleanup := openIsolationTestDB(t)
	defer cleanup()
	for _, tc := range []struct {
		name   string
		events []string
		status int
		done   bool
	}{
		{"complete", []string{testChatChunk(`{"role":"assistant","content":" hello "}`, `null`), testChatChunk(`{}`, `"stop"`), "[DONE]"}, 200, true},
		{"incomplete", []string{testChatChunk(`{"content":"partial"}`, `null`)}, 200, false},
		{"malformed first", []string{`{`}, 502, false},
		{"malformed later", []string{testChatChunk(`{"content":"partial"}`, `null`), `{`}, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { testChatSSE(w, tc.events...) }))
			defer upstream.Close()
			cfg := testChatConfig()
			cfg.OpenAI.BaseURL = upstream.URL
			server := NewServer(cfg, testChatLogger(), pool)
			w := testChatInvoke(server.Handler(), `{"model":"context-agent-1","messages":[{"role":"user","content":"hello"}],"stream":true,"disable_memory":true}`, "chat-test", "")
			if w.Code != tc.status || strings.Contains(w.Body.String(), "[DONE]") != tc.done {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if !tc.done && tc.status == 200 && !strings.Contains(w.Body.String(), `"code":"incomplete_stream"`) {
				t.Fatal("missing error event", w.Body)
			}
			if tc.done && w.Header().Get("Content-Type") != "text/event-stream" {
				t.Fatal("not SSE")
			}
		})
	}
	// The first chunk must arrive while the provider is still running; disconnect
	// must cancel that provider and release the subject lock, not wait for timeout.
	canceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testChatSSE(w, testChatChunk(`{"content":"first"}`, `null`))
		<-r.Context().Done()
		close(canceled)
	}))
	defer upstream.Close()
	cfg := testChatConfig()
	cfg.OpenAI.BaseURL = upstream.URL
	gateway := httptest.NewServer(NewServer(cfg, testChatLogger(), pool).Handler())
	defer gateway.Close()
	req, _ := http.NewRequest("POST", gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"context-agent-1","messages":[{"role":"user","content":"hello"}],"stream":true,"disable_memory":true}`))
	req.Header.Set("Authorization", "Bearer chat-test")
	client := &http.Client{Timeout: time.Second * 3}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 16)
	if _, err := resp.Body.Read(b); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not cancel provider")
	}
}
func TestChatKnowledgeControlsIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool, cleanup := openIsolationTestDB(t)
	defer cleanup()
	var knowledgeCalls atomic.Int32
	reference := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		knowledgeCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []knowledge.KnowledgeEvidence{knowledgeFixture()}})
	}))
	defer reference.Close()
	captures := make(chan string, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		captures <- string(data)
		_, _ = io.WriteString(w, testChatCompletion)
	}))
	defer upstream.Close()
	cfg := testChatConfig()
	cfg.OpenAI.BaseURL = upstream.URL
	cfg.Knowledge = config.KnowledgeConfig{Enabled: true, URL: reference.URL, Limit: 4, Tokens: []config.AuthTokenConfig{{Subject: "chat-subject", Token: "synthetic-knowledge"}}}
	server := NewServer(cfg, testChatLogger(), pool)
	for _, disabled := range []bool{false, true} {
		w := testChatInvoke(server.Handler(), fmt.Sprintf(`{"model":"context-agent-1","messages":[{"role":"user","content":"How many approvers?"}],"disable_memory":true,"disable_knowledge":%t}`, disabled), "chat-test", "")
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		sent := <-captures
		if strings.Contains(sent, "Engineering handbook") == disabled {
			t.Fatal("knowledge control failed", sent)
		}
	}
	if knowledgeCalls.Load() != 1 {
		t.Fatal("unexpected reference calls")
	}
}

func TestChatOpenAISDK(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	python := os.Getenv("SECOND_CONTEXT_CHAT_SDK_PYTHON")
	if python == "" {
		t.Log("OpenAI SDK check not requested; set SECOND_CONTEXT_CHAT_SDK_PYTHON to the prepared interpreter (see docs/chat-completions.md)")
		return
	}
	pool, cleanup := openIsolationTestDB(t)
	defer cleanup()
	// Synthetic provider asserts the actual SDK-generated wire request at the
	// gateway's outbound boundary. No monkeypatch, custom transport or paid model.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path == "/embeddings" {
			_, _ = io.WriteString(w, `{"data":[{"embedding":[0.1,0.2,0.3]}],"usage":{"prompt_tokens":1,"total_tokens":1}}`)
			return
		}
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer synthetic-provider-token" {
			t.Error("wrong provider request")
		}
		var messages []map[string]any
		_ = json.Unmarshal(body["messages"], &messages)
		last := messages[len(messages)-1]
		content, _ := last["content"].(string)
		var stream bool
		_ = json.Unmarshal(body["stream"], &stream)
		if stream {
			events := []string{testChatChunk(`{"role":"assistant","content":""}`, `null`)}
			if len(body["tools"]) > 0 {
				events = append(events, testChatChunk(`{"tool_calls":[{"index":0,"id":"call_sdk","type":"function","function":{"name":"lookup","arguments":"{\"city\":"}}]}`, `null`), testChatChunk(`{"tool_calls":[{"index":0,"function":{"arguments":"\"Berlin\"}"}}]}`, `null`), testChatChunk(`{}`, `"tool_calls"`))
			} else {
				events = append(events, testChatChunk(`{"content":" streamed "}`, `null`), testChatChunk(`{}`, `"length"`))
			}
			if strings.Contains(string(body["stream_options"]), `true`) {
				events = append(events, `{"id":"chatcmpl_fixture","object":"chat.completion.chunk","created":1780000000,"model":"synthetic-model","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)
			}
			events = append(events, "[DONE]")
			testChatSSE(w, events...)
			return
		}
		if len(body["tools"]) > 0 && last["role"] != "tool" {
			_, _ = io.WriteString(w, strings.Replace(strings.Replace(testChatCompletion, `"content":" answer "`, `"content":null,"tool_calls":[{"id":"call_sdk","type":"function","function":{"name":"lookup","arguments":"{\"city\":\"Berlin\"}"}}]`, 1), `"finish_reason":"stop"`, `"finish_reason":"tool_calls"`, 1))
			return
		}
		answer := " answer "
		if content == "What is its name?" {
			if len(messages) != 4 || messages[1]["content"] != "My project is Iris." {
				t.Error("missing supplied SDK history")
			}
			answer = "Iris"
		}
		if content == "Separate chat" {
			if len(messages) != 2 {
				t.Error("cross-chat history merged")
			}
			answer = "separate"
		}
		if last["role"] == "tool" {
			if last["tool_call_id"] != "call_sdk" {
				t.Error("lost tool id")
			}
			answer = "Berlin result"
		}
		if len(body["response_format"]) > 0 {
			answer = `{"answer":"structured"}`
		}
		encoded, _ := json.Marshal(answer)
		_, _ = io.WriteString(w, strings.Replace(testChatCompletion, `" answer "`, string(encoded), 1))
	}))
	defer upstream.Close()
	cfg := testChatConfig()
	cfg.OpenAI.BaseURL = upstream.URL
	cfg.Qdrant = isolationTestConfig(t, "chat_sdk").Qdrant
	cfg.Qdrant.URL = os.Getenv("QDRANT_URL")
	gateway := httptest.NewServer(NewServer(cfg, testChatLogger(), pool).Handler())
	defer gateway.Close()
	defer pool.Exec(context.Background(), "DELETE FROM users WHERE external_id='chat-subject'") //nolint:errcheck
	command := exec.Command(python, "../../scripts/test-chat-sdk.py", gateway.URL+"/v1")
	result, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("SDK: %v\n%s", err, result)
	}
	t.Log(string(result))
}

func TestChatMemoryScopeIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool, cleanup := openIsolationTestDB(t)
	defer cleanup()
	captures := make(chan string, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/embeddings" {
			_, _ = io.WriteString(w, `{"data":[{"embedding":[0.1,0.2,0.3]}]}`)
			return
		}
		data, _ := io.ReadAll(r.Body)
		captures <- string(data)
		_, _ = io.WriteString(w, testChatCompletion)
	}))
	defer upstream.Close()
	cfg := testChatConfig()
	cfg.OpenAI.BaseURL = upstream.URL
	base := isolationTestConfig(t, "chat_memory_"+fmt.Sprint(time.Now().UnixNano()))
	cfg.Qdrant = base.Qdrant
	cfg.Qdrant.URL = os.Getenv("QDRANT_URL")
	cfg.Scoring = base.Scoring
	cfg.Auth.Tokens = append(cfg.Auth.Tokens, config.AuthTokenConfig{Subject: "chat-other", Token: "chat-other-token"})
	fake := &fakeLLMClient{response: llm.GenerateResponse{OutputText: `{"pairs":[],"beliefs":[]}`}}
	memory := memsvc.NewService(cfg, pool, fake)
	record, err := memory.Ingest(context.Background(), memsvc.IngestParams{RawText: "Synthetic private preference: concise answers about Iris.", Summary: "Synthetic private preference: concise answers about Iris.", MemoryType: "preference", RequestUser: "chat-subject", Confidence: floatPointer(1)})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(context.Background(), "DELETE FROM users WHERE external_id IN ('chat-subject','chat-other')") //nolint:errcheck
	defer func() {
		if err := memory.Delete(context.Background(), record.ID); err != nil {
			t.Error(err)
		}
	}()
	server := NewServer(cfg, testChatLogger(), pool)
	for _, tc := range []struct {
		token          string
		disabled, want bool
	}{{"chat-test", false, true}, {"chat-test", true, false}, {"chat-other-token", false, false}} {
		body := fmt.Sprintf(`{"model":"context-agent-1","messages":[{"role":"user","content":"Tell me about Iris"}],"disable_memory":%t}`, tc.disabled)
		w := testChatInvoke(server.Handler(), body, tc.token, "")
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		sent := <-captures
		if strings.Contains(sent, "Synthetic private preference") != tc.want {
			t.Fatal("memory isolation/control failed")
		}
	}
	subject := "oria:11111111-1111-4111-8111-111111111111"
	w := testChatInvoke(server.Handler(), `{"model":"context-agent-1","messages":[{"role":"user","content":"hello"}],"disable_memory":true}`, "chat-service-test", subject)
	if w.Code != 200 {
		t.Fatalf("service chat failed: %d %s", w.Code, w.Body)
	}
	<-captures
}

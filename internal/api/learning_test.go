package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bdobrica/SecondContext/internal/llm"
)

func TestCaptureOccurrenceIdentity(t *testing.T) {
	first := []llm.Message{{Role: "user", Content: "Alex agreed."}}
	expanded := append(append([]llm.Message{}, first...), llm.Message{Role: "assistant", Content: "Thanks"}, llm.Message{Role: "user", Content: "Alex agreed."})
	a := suppliedCaptureSources(first, "")
	b := suppliedCaptureSources(expanded, "")
	if len(b) != 2 || a[0].Key != b[0].Key || b[0].Key == b[1].Key {
		t.Fatalf("occurrence identity: %#v %#v", a, b)
	}
	if suppliedCaptureSources(first, "another-session")[0].Key == a[0].Key {
		t.Fatal("sessions collapsed")
	}
	if len(suppliedCaptureSources([]llm.Message{{Role: "assistant", Content: "Alex agreed."}, {Role: "tool", Content: "Alex agreed."}}, "")) != 0 {
		t.Fatal("non-user evidence")
	}
}

func TestLearningInspectorIsPublicButMemoryRequiresAuthentication(t *testing.T) {
	server := NewServerWithClient(testChatConfig(), testChatLogger(), nil, &fakeLLMClient{})
	for _, path := range []string{"/memory/inspector", "/memory/inspector.js"} {
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "connect-src 'self'") {
			t.Fatalf("public shell: %d", w.Code)
		}
	}
	for _, path := range []string{"/memory/learning", "/memory/evidence?ids=11111111-1111-4111-8111-111111111111"} {
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 401 {
			t.Fatalf("private API exposed: %s: %d", path, w.Code)
		}
	}
}

func TestTemporaryChatWireAndRetrievalControls(t *testing.T) {
	for _, raw := range []string{
		`{"model":"context-agent-1","messages":[{"role":"user","content":"hello"}],"temporary_chat":true}`,
		`{"model":"context-agent-1","messages":[{"role":"user","content":"hello"}],"metadata":{"temporary_chat":true}}`,
	} {
		var body map[string]json.RawMessage
		_ = json.Unmarshal([]byte(raw), &body)
		parsed, err := parseChatRequest(body)
		if err != nil {
			t.Fatal(err)
		}
		if !requestDisablesMemory(parsed.context) || requestDisablesKnowledge(parsed.context) {
			t.Fatal("temporary chat controls leaked into knowledge")
		}
		if _, ok := parsed.upstream.Body["temporary_chat"]; ok {
			t.Fatal("gateway control forwarded")
		}
	}
}

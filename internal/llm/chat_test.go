package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
)

const completionFixture = `{"id":"chatcmpl_test","object":"chat.completion","created":1780000000,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":" answer "},"finish_reason":"length","logprobs":null}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_tokens_details":{"cached_tokens":4}},"system_fingerprint":"fp_test"}`

func chunk(delta, finish string) string {
	return fmt.Sprintf(`{"id":"chatcmpl_test","object":"chat.completion.chunk","created":1780000000,"model":"test-model","choices":[{"index":0,"delta":%s,"finish_reason":%s}],"usage":null}`, delta, finish)
}
func sse(events ...string) string { return "data: " + strings.Join(events, "\n\ndata: ") + "\n\n" }

const usageChunk = `{"id":"chatcmpl_test","object":"chat.completion.chunk","created":1780000000,"model":"test-model","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`

func TestChatPreservesCompletionAndUpstreamOptions(t *testing.T) {
	var sent map[string]json.RawMessage
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer synthetic-provider-token" {
			t.Error("wrong upstream route or credential")
		}
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, completionFixture)
	}))
	defer upstream.Close()
	body := map[string]json.RawMessage{"model": json.RawMessage(`"test-model"`), "messages": json.RawMessage(`[{"role":"user","content":"hello"}]`), "temperature": json.RawMessage(`0`), "response_format": json.RawMessage(`{"type":"json_object"}`)}
	client := NewOpenAIClient(config.OpenAIConfig{BaseURL: upstream.URL + "/v1", APIKey: "synthetic-provider-token", RequestTimeout: time.Second})
	got, err := client.Chat(context.Background(), ChatRequest{Body: body}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Body) != completionFixture || got.Usage.TotalTokens != 12 || !reflect.DeepEqual(sent, body) {
		t.Fatalf("lost wire data: %s %v", got.Body, sent)
	}
}
func TestChatStreamTerminalSemantics(t *testing.T) {
	first := chunk(`{"role":"assistant","content":""}`, `null`)
	text := chunk(`{"content":"hello"}`, `null`)
	end := chunk(`{}`, `"stop"`)
	for _, tc := range []struct {
		name, wire string
		usage      bool
		want       string
	}{
		{"complete", sse(first, text, end, "[DONE]"), false, ""},
		{"usage", sse(first, text, end, usageChunk, "[DONE]"), true, ""},
		{"tools", sse(first, chunk(`{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{"}}]}`, `null`), chunk(`{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]}`, `null`), chunk(`{}`, `"tool_calls"`), "[DONE]"), false, ""},
		{"eof", sse(first, text), false, "incomplete_stream"},
		{"no done", sse(first, text, end), false, "incomplete_stream"},
		{"early done", sse(first, "[DONE]"), false, "incomplete_stream"},
		{"missing usage", sse(first, end, "[DONE]"), true, "incomplete_stream"},
		{"bad json", sse(first, `{`), false, "invalid_stream"},
		{"error event", sse(first, `{"error":{"message":"private provider body"}}`), false, "invalid_stream"},
		{"changed id", sse(first, strings.ReplaceAll(text, "chatcmpl_test", "different")), false, "invalid_stream"},
		{"after finish", sse(first, end, text, "[DONE]"), false, "invalid_stream"},
		{"bad delta", sse(strings.ReplaceAll(first, `{"role":"assistant","content":""}`, `null`)), false, "invalid_stream"},
		{"unknown finish", sse(first, chunk(`{}`, `"fake"`), "[DONE]"), false, "invalid_stream"},
		{"oversize event", sse(strings.Repeat("x", 1<<20)), false, "invalid_stream"},
		{"comments", ": heartbeat\n\n" + sse(first, end, "[DONE]"), false, ""},
		{"crlf", strings.ReplaceAll(sse(first, end, "[DONE]"), "\n", "\r\n"), false, ""},
		{"multi data lines", strings.Replace(sse(first, end, "[DONE]"), `,"object"`, ",\ndata: \"object\"", 1), false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var received []string
			result, err := readChatStream(strings.NewReader(tc.wire), tc.usage, func(p json.RawMessage) error { received = append(received, string(p)); return nil })
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if len(received) < 2 {
					t.Fatal("missing events")
				}
				if tc.usage && result.Usage.TotalTokens != 12 {
					t.Fatal(result)
				}
			} else {
				var e *Error
				if !errors.As(err, &e) || e.Code != tc.want {
					t.Fatalf("got %v want %s", err, tc.want)
				}
			}
		})
	}
	failure := errors.New("downstream closed")
	_, err := readChatStream(strings.NewReader(sse(first, end, "[DONE]")), false, func(json.RawMessage) error { return failure })
	if !errors.Is(err, failure) {
		t.Fatal("swallowed downstream failure")
	}
}
func TestChatHTTPFailuresAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name              string
		status            int
		contentType, body string
		stream            bool
		want              int
	}{
		{"400", 400, "application/json", `{"error":{"message":"private"}}`, false, 400},
		{"429", 429, "application/json", `private`, false, 429},
		{"500", 500, "application/json", `private`, false, 502},
		{"authentication", 401, "application/json", `private`, false, 502},
		{"empty", 200, "application/json", `{}`, false, 502},
		{"bad finish", 200, "application/json", strings.ReplaceAll(completionFixture, `"length"`, `null`), false, 502},
		{"size", 200, "application/json", strings.Repeat("x", MaxOpenAIResponseBodyBytes+1), false, 502},
		{"not sse", 200, "application/json", completionFixture, true, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			client := NewOpenAIClient(config.OpenAIConfig{BaseURL: upstream.URL, APIKey: "synthetic", RequestTimeout: time.Second})
			_, err := client.Chat(context.Background(), ChatRequest{Stream: tc.stream}, func(json.RawMessage) error { return nil })
			var e *Error
			if !errors.As(err, &e) || e.StatusCode != tc.want || strings.Contains(err.Error(), "private") {
				t.Fatalf("%v", err)
			}
		})
	}
}
func TestChatCancellationAndRedirect(t *testing.T) {
	canceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse(chunk(`{"content":"start"}`, `null`)))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(canceled)
	}))
	defer upstream.Close()
	client := NewOpenAIClient(config.OpenAIConfig{BaseURL: upstream.URL, APIKey: "synthetic", RequestTimeout: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := client.Chat(ctx, ChatRequest{Stream: true}, func(json.RawMessage) error { cancel(); return nil })
	if err == nil {
		t.Fatal("canceled stream succeeded")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("provider request not canceled")
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed credential-bearing redirect") }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client = NewOpenAIClient(config.OpenAIConfig{BaseURL: redirect.URL, APIKey: "synthetic", RequestTimeout: time.Second})
	if _, err := client.Chat(context.Background(), ChatRequest{}, nil); err == nil {
		t.Fatal("redirect succeeded")
	}
}

package api

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func chatBody(t *testing.T, extra string) map[string]json.RawMessage {
	t.Helper()
	var b map[string]json.RawMessage
	if err := json.Unmarshal([]byte(`{"model":"context-agent-1","messages":[{"role":"user","content":" hello "}]`+extra+`}`), &b); err != nil {
		t.Fatal(err)
	}
	return b
}
func TestChatParameterMatrix(t *testing.T) {
	cases := []struct{ extra, code string }{
		{``, ""}, {`,"n":1`, ""}, {`,"n":2`, "unsupported_feature"},
		{`,"temperature":0,"top_p":0.5,"max_tokens":17,"presence_penalty":-1,"frequency_penalty":2,"stop":["end"],"seed":42,"logit_bias":{"1":-100},"logprobs":true,"top_logprobs":3`, ""},
		{`,"max_completion_tokens":24,"temperature":null,"stop":null`, ""},
		{`,"stream":true,"stream_options":{"include_usage":true,"include_obfuscation":false}`, ""},
		{`,"stream_options":{"include_usage":true}`, "invalid_parameter"},
		{`,"stream":true,"stream_options":{"unknown":true}`, "unsupported_feature"},
		{`,"temperature":3`, "invalid_parameter"}, {`,"top_p":false`, "invalid_parameter"},
		{`,"max_tokens":0`, "invalid_parameter"}, {`,"max_tokens":1.5`, "invalid_parameter"},
		{`,"max_tokens":3,"max_completion_tokens":3`, "invalid_parameter"},
		{`,"stop":[1]`, "invalid_parameter"}, {`,"stop":[]`, "invalid_parameter"},
		{`,"logit_bias":{"not-token":2}`, "invalid_parameter"}, {`,"logprobs":"yes"`, "invalid_parameter"},
		{`,"response_format":{"type":"json_object"}`, ""},
		{`,"response_format":{"type":"text"}`, ""},
		{`,"response_format":{"type":"json_schema","json_schema":{"name":"answer","strict":true,"schema":{"type":"object","properties":{"result":{"type":"string"}},"required":["result"],"additionalProperties":false}}}`, ""},
		{`,"response_format":{"type":"json_schema","json_schema":{"name":"answer","schema":[]}}`, "invalid_parameter"},
		{`,"response_format":{"type":"xml"}`, "unsupported_feature"},
		{`,"response_format":{"type":"text","unknown":true}`, "unsupported_feature"},
		{`,"tools":[{"type":"function","function":{"name":"lookup","description":"Look up","parameters":{"type":"object"},"strict":true}}],"tool_choice":{"type":"function","function":{"name":"lookup"}},"parallel_tool_calls":true`, ""},
		{`,"tool_choice":"auto"`, ""}, {`,"tool_choice":"none"`, ""}, {`,"tool_choice":"required"`, ""},
		{`,"tool_choice":{"type":"custom","custom":{"name":"run"}}`, "unsupported_feature"},
		{`,"tools":[{"type":"function","function":{"name":"lookup","other":1}}]`, "unsupported_feature"},
		{`,"tools":[{"type":"web_search"}]`, "unsupported_feature"},
		{`,"modalities":["audio"]`, "unsupported_feature"}, {`,"audio":{}`, "unsupported_feature"},
		{`,"reasoning_effort":"low"`, "unsupported_feature"}, {`,"functions":[]`, "unsupported_feature"},
		{`,"store":true`, "unsupported_feature"}, {`,"store":false`, ""},
		{`,"metadata":{"session_id":"legacy session","goal":"help","people":["Alex"],"topics":[],"disable_memory":true},"disable_knowledge":true,"knowledge_filters":{"formats":["pdf"]}`, ""},
		{`,"metadata":{"memory_mode":"scenario_generation"}`, "unsupported_feature"},
		{`,"metadata":{"arbitrary_provider_metadata":"x"}`, "unsupported_feature"},
		{`,"knowledge_refs":[]`, "unsupported_feature"}, {`,"knowledge_filters":{"tags":["x"]}`, "unsupported_feature"},
	}
	for _, tc := range cases {
		t.Run(tc.extra, func(t *testing.T) {
			body := chatBody(t, tc.extra)
			got, err := parseChatRequest(body)
			if tc.code != "" {
				if err == nil || err.code != tc.code {
					t.Fatalf("got %v want %s", err, tc.code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for key, value := range got.upstream.Body {
				if !reflect.DeepEqual(value, body[key]) {
					t.Fatalf("changed %s", key)
				}
			}
		})
	}
}
func TestChatSuppliedToolHistory(t *testing.T) {
	body := chatBody(t, "")
	transcript := `[{"role":"system","content":"system"},{"role":"developer","content":[{"type":"text","text":"developer"}]},{"role":"user","name":"alex","content":" question "},{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"a\": 1}"}},{"id":"call_2","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_2","content":[{"type":"text","text":"second"}]},{"role":"tool","tool_call_id":"call_1","content":" first "},{"role":"assistant","content":" answer "},{"role":"user","content":[{"type":"text","text":" follow"},{"type":"text","text":" up "}]}]`
	body["messages"] = json.RawMessage(transcript)
	got, err := parseChatRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.upstream.Body["messages"]) != transcript || got.text[2].Content != " question " || got.text[7].Content != " follow up " {
		t.Fatal("supplied history changed")
	}
	for _, bad := range []string{
		`[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/a"}}]}]`,
		`[{"role":"function","content":"x"}]`,
		`[{"role":"tool","tool_call_id":"foreign","content":"x"}]`,
		`[{"role":"user","content":"x","hidden":true}]`,
		`[{"role":"user","content":null}]`,
		`[{"role":"assistant","content":null}]`,
		`[{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"x","arguments":"{}"}}]}]`,
		`[{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"x","arguments":"{}"}}]},{"role":"user","content":"interrupt"}]`,
	} {
		body["messages"] = json.RawMessage(bad)
		if _, err := parseChatRequest(body); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
func TestChatHistoryHeaders(t *testing.T) {
	for _, tc := range []struct{ key, value, code string }{
		{"Session", "session-1", ""}, {"Session", "bad/id", "invalid_history_request"},
		{"History", " off ", ""}, {"History", "recent", "unsupported_feature"}, {"History", "adaptive", "unsupported_feature"},
		{"History", "OFF", "invalid_history_request"}, {"History", "off,recent", "invalid_history_request"}, {"History", "", "invalid_history_request"},
		{"Acceptance", "generated", ""}, {"Acceptance", "explicit", "unsupported_feature"},
		{"Turn", "00000000-0000-4000-8000-000000000001", "unsupported_feature"}, {"Turn", "NOT-A-UUID", "invalid_history_request"},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			r.Header.Set("X-SecondContext-"+tc.key, tc.value)
			_, err := chatHistoryHeaders(r, nil)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.code != tc.code {
				t.Fatalf("%v want %s", err, tc.code)
			}
		})
	}
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Add("X-SecondContext-History", "off")
	r.Header.Add("X-SecondContext-History", "off")
	if _, e := chatHistoryHeaders(r, nil); e == nil {
		t.Fatal("duplicate accepted")
	}
	r.Header.Del("X-SecondContext-History")
	r.Header.Set("X-SecondContext-Session", "first")
	if _, e := chatHistoryHeaders(r, map[string]any{"session_id": "second"}); e == nil || e.code != "session_conflict" {
		t.Fatal(e)
	}
}

func TestChatRejectsInvalidRequestBeforeUpstream(t *testing.T) {
	s := NewServerWithClient(testChatConfig(), testChatLogger(), nil, &fakeLLMClient{})
	for _, body := range []string{`null`, `{}`, `{"model":"x","messages":[]}`, `{"model":"x","messages":null}`, `{"model":"x","messages":[{"role":"user","content":"x"}],"prediction":{}}`, strings.Repeat(" ", maxJSONRequestBodyBytes) + `{}`} {
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer chat-test")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 400 && w.Code != 413 {
			t.Fatalf("status %d", w.Code)
		}
	}
}

func TestChatRejectsAmbiguousJSON(t *testing.T) {
	for _, raw := range []string{`{"model":"a","model":"b"}`, `{"messages":[{"role":"user","role":"assistant"}]}`, `{"tools":[{"function":{"parameters":{"x":1,"\u0078":2}}}]}`, string([]byte{'"', 0xff, '"'})} {
		if validateChatJSON([]byte(raw)) == nil {
			t.Fatalf("accepted ambiguous JSON: %q", raw)
		}
	}
	if err := validateChatJSON([]byte(`{"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hello"}]}`)); err != nil {
		t.Fatal(err)
	}
}

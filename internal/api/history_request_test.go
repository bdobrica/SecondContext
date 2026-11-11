package api

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
)

func TestManagedSelectors(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/responses", nil)
	r.Header.Set("X-SecondContext-Turn", "00000000-0000-4000-8000-000000000001")
	r.Header.Set("X-SecondContext-Session", "session")
	r.Header.Set("X-SecondContext-History", "recent")
	h, e := parseHistoryHeaders(r, nil)
	if e != nil || h.Acceptance != "generated" || h.Session != "session" {
		t.Fatal(h, e)
	}
	r.Header.Set("X-SecondContext-History", "adaptive")
	if _, e = parseHistoryHeaders(r, nil); e != nil {
		t.Fatal(e)
	}
	r.Header.Set("X-SecondContext-History", "off")
	r.Header.Del("X-SecondContext-Session")
	if _, e = parseHistoryHeaders(r, map[string]any{"session_id": " session "}); e == nil {
		t.Fatal("trimmed managed selector")
	}
}
func TestManagedInputProfile(t *testing.T) {
	for _, tc := range []struct {
		path, raw string
		valid     bool
	}{
		{"/v1/responses", `{"input":" hello "}`, true},
		{"/v1/responses", `{"input":[{"role":"user","content":"hi"}]}`, false},
		{"/v1/responses", `{"input":"hi","tools":[]}`, false},
		{"/v1/responses", `{"input":"hi","stream":true}`, false},
		{"/v1/responses", `{"input":"hi","metadata":{"memory_mode":"scenario_generation"}}`, false},
		{"/v1/chat/completions", `{"model":"context-agent-1","messages":[{"role":"developer","content":"current facts"},{"role":"user","content":"hello"}]}`, true},
		{"/v1/chat/completions", `{"model":"context-agent-1","messages":[{"role":"user","content":"prior"},{"role":"user","content":"hello"}]}`, false},
		{"/v1/chat/completions", `{"model":"context-agent-1","messages":[{"role":"user","content":"hello"}],"tools":[]}`, false},
		{"/v1/chat/completions", `{"model":"context-agent-1","messages":[{"role":"user","content":"hello"}],"n":2}`, false},
	} {
		var body map[string]json.RawMessage
		_ = json.Unmarshal([]byte(tc.raw), &body)
		_, _, _, ok := parseManagedInput(tc.path, body)
		if ok != tc.valid {
			t.Errorf("%s: %v", tc.raw, ok)
		}
	}
	if historyText(strings.Repeat("界", 4097), 4096) || !historyText(strings.Repeat("界", 4096), 4096) {
		t.Fatal("Unicode character bound")
	}
}
func TestHistoryBindingsAndBudget(t *testing.T) {
	h := historySelectors{Session: "s", Turn: "t", History: "off", Acceptance: "generated"}
	a, _ := generationHash([]byte(`{"input":"hi","x":1.0}`), "owner", "/v1/responses", h)
	b, _ := generationHash([]byte(`{ "x":1e0, "input":"hi" }`), "owner", "/v1/responses", h)
	if string(a) != string(b) {
		t.Fatal("noncanonical binding")
	}
	c, _ := generationHash([]byte(`{"input":"hi","x":1,"stream":false}`), "owner", "/v1/responses", h)
	if string(a) == string(c) {
		t.Fatal("defaults collapsed")
	}
	current := []llm.Message{{Role: "system", Content: "mandatory"}, {Role: "user", Content: "now"}}
	history := []db.HistoryTurn{{UserText: strings.Repeat("old", 100), AssistantText: "old reply"}, {UserText: "new", AssistantText: "new reply"}}
	body := map[string]json.RawMessage{}
	got, e := fitHistory(body, current, history, 500)
	if e != nil || len(got) != 4 || got[1].Content != "new" || got[2].Content != "new reply" || got[3].Content != "now" {
		t.Fatal(got, e)
	}
	if _, e = fitHistory(body, current, history, 1); e != db.HistoryError("context_budget_exceeded") {
		t.Fatal(e)
	}
	if managedOutputLimit(map[string]json.RawMessage{"max_tokens": []byte("4097")}, 4096) {
		t.Fatal("output reservation exceeded")
	}
}
func TestHistoryFinalizePublishedExamples(t *testing.T) {
	raw, err := os.ReadFile("../../docs/contracts/conversation-history-v1.examples.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name       string          `json:"name"`
		Definition string          `json:"definition"`
		Valid      bool            `json:"valid"`
		Value      json.RawMessage `json:"value"`
	}
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		if f.Definition == "FinalizeRequest" {
			_, ok := parseHistoryFinalize(f.Value)
			if ok != f.Valid {
				t.Errorf("fixture %s: %v want %v", f.Name, ok, f.Valid)
			}
		}
	}
	base := `"contract_version":1,"user":"owner","session_id":"session","turn_id":"00000000-0000-4000-8000-000000000001"`
	for _, tc := range []struct {
		fields string
		valid  bool
	}{
		{`,"action":"abandon"`, true}, {`,"action":"abandon","response_id":"x"`, false},
		{`,"action":"accept_generated","response_id":"response","assistant_text":"accepted"`, true},
		{`,"action":"accept_generated","response_id":"response","assistant_text":" "`, false},
		{`,"action":"accept_local","user_text":"current","assistant_text":"local","knowledge_refs":[]`, true},
		{`,"action":"accept_local","user_text":"current","assistant_text":"local","knowledge_refs":null`, false},
		{`,"action":"accept_local","user_text":"current","assistant_text":"local"`, false},
	} {
		_, ok := parseHistoryFinalize([]byte("{" + base + tc.fields + "}"))
		if ok != tc.valid {
			t.Error(tc.fields, ok)
		}
	}
}

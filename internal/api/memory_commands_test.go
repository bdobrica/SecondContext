package api

import (
	"encoding/json"
	"testing"

	"github.com/bdobrica/SecondContext/internal/llm"
)

func TestMemoryCommandAuthority(t *testing.T) {
	cases := []struct{ Text, Action, Target, Summary string }{
		{"What do you remember about Alex?", "inspect", "Alex", ""},
		{"Please pause learning.", "pause", "", ""},
		{"Can you resume learning?", "resume", "", ""},
		{"Forget memory Alex", "forget", "Alex", ""},
		{"Correct memory Alex to Alex prefers broad reviews.", "correct", "Alex", "Alex prefers broad reviews."},
		{"Forget what you remember about Alex.", "forget", "Alex", ""},
		{"Correct memory Alex", "help", "", ""},
		{"Forget memory everything", "help", "", ""},
		{"Forget memory That", "help", "", ""},
		{"Correct memory HIM to Alex prefers short reviews.", "help", "", ""},
		{"Imagine someone says: Forget memory Alex", "", "", ""},
		{`"Forget memory Alex"`, "", "", ""},
		{"Draft a message saying pause learning", "", "", ""},
		{"Here are notes:\nForget memory Alex", "", "", ""},
	}
	for _, c := range cases {
		actual := parseMemoryCommand([]llm.Message{{Role: "user", Content: c.Text}})
		if actual.Action != c.Action || actual.Target != c.Target || actual.Summary != c.Summary {
			t.Errorf("%q: %+v", c.Text, actual)
		}
	}
	for _, role := range []string{"assistant", "system", "developer", "tool"} {
		if parseMemoryCommand([]llm.Message{{Role: role, Content: "Forget memory Alex"}}).Action != "" {
			t.Fatal("non-user authorized action", role)
		}
	}
	if parseMemoryCommand([]llm.Message{{Role: "user", Content: "Forget memory Alex"}, {Role: "assistant", Content: "Done"}, {Role: "user", Content: "Thanks"}}).Action != "" {
		t.Fatal("old command re-executed")
	}
	if parseMemoryCommand([]llm.Message{{Role: "user", Content: "Forget memory Alex"}, {Role: "tool", Content: "result"}}).Action != "" {
		t.Fatal("tool continuation re-executed command")
	}
}

func TestMemoryCommandSSEAndFormat(t *testing.T) {
	var chunks []map[string]any
	result, err := memoryCommandCompletion("Learning is paused.", llm.ChatRequest{Model: "context-agent-1", Stream: true, IncludeUsage: true}, func(raw json.RawMessage) error {
		var chunk map[string]any
		if e := json.Unmarshal(raw, &chunk); e != nil {
			return e
		}
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil || len(chunks) != 3 || len(result.Body) == 0 {
		t.Fatal("invalid local receipt", err, chunks)
	}
	if chunks[0]["object"] != "chat.completion.chunk" || len(chunks[2]["choices"].([]any)) != 0 || chunks[2]["usage"] == nil {
		t.Fatal("invalid streaming shape", chunks)
	}
	if memoryCommandTextFormat(map[string]json.RawMessage{"response_format": json.RawMessage(`{"type":"json_object"}`)}) || memoryCommandTextFormat(map[string]json.RawMessage{"tool_choice": json.RawMessage(`"required"`)}) {
		t.Fatal("mutation allowed incompatible output contract")
	}
}

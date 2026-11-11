package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bdobrica/SecondContext/internal/llm"
	"github.com/bdobrica/SecondContext/internal/prompts"
)

func TestContextInterpretationBoundsAndLatestQuery(t *testing.T) {
	messages := []llm.Message{{Role: "system", Content: "Secret system"}, {Role: "user", Content: "Obsolete travel plans"}}
	for i := 0; i < 15; i++ {
		messages = append(messages, llm.Message{Role: "assistant", Content: strings.Repeat("é", 1900)}, llm.Message{Role: "user", Content: strings.Repeat("界", 1400)})
	}
	messages = append(messages, llm.Message{Role: "tool", Content: "tool is not identity evidence"}, llm.Message{Role: "user", Content: "Help me ask Alex for review."}, llm.Message{Role: "assistant", Content: "not a new request"})
	dialogue := interpretationDialogue(messages)
	total := 0
	for _, m := range dialogue {
		total += len(m.Content)
		if !utf8.ValidString(m.Content) || len(m.Content) > 3000 || m.Role == "tool" || m.Role == "system" {
			t.Fatal("invalid bounded dialogue", m)
		}
	}
	if len(dialogue) > 7 || total > 10000 || dialogue[len(dialogue)-1].Content != "Help me ask Alex for review." {
		t.Fatal("unbounded/wrong latest dialogue")
	}
	if got := collectUserQuery(messages); got != "Help me ask Alex for review." {
		t.Fatal("old query polluted current request", got)
	}
}

func TestContextInterpretationValidation(t *testing.T) {
	dialogue := []llm.Message{{Role: "user", Content: "Ask Alex for a review."}, {Role: "assistant", Content: "Draft"}, {Role: "user", Content: "Make it warmer."}}
	valid := `{"query":"Warm review request to Alex","goal":"request review","people":["Alex"],"topics":["review"],"intent":"draft","context_messages":[0,1],"reference_status":"clear"}`
	p, evidence, err := parseContextInterpretation(valid, dialogue)
	if err != nil || p.Intent != "draft" || !strings.Contains(evidence, "Alex") {
		t.Fatal(p, evidence, err)
	}
	for _, raw := range []string{
		strings.Replace(valid, `[0,1]`, `[]`, 1), // name unsupported by selected dialogue
		strings.Replace(valid, `[0,1]`, `[3]`, 1),
		strings.Replace(valid, `[0,1]`, `[0,0]`, 1),
		strings.Replace(valid, `"people":["Alex"]`, `"people":null`, 1),
		strings.Replace(valid, `"draft"`, `"send_message"`, 1),
		strings.Replace(valid, `"people":["Alex"]`, `"people":["Morgan"]`, 1),
		strings.Replace(valid, `"goal":`, `"goal":"duplicate","goal":`, 1),
		strings.Replace(valid, `"reference_status":"clear"`, `"extra":true`, 1),
		valid + ` {}`,
	} {
		if _, _, err := parseContextInterpretation(raw, dialogue); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestResolveContextPeopleConservative(t *testing.T) {
	known := []contextPerson{{Name: "Alex Chen", Aliases: []string{"Lex"}}, {Name: "Alex Morgan", Aliases: []string{"AM"}}, {Name: "Alex"}, {Name: "Zoë Smith"}}
	cases := []struct {
		input                   string
		refs, people, ambiguous []string
	}{
		{input: "Ask Alex for review", ambiguous: []string{"Alex"}},
		{input: "Ask Alex Chen for review", people: []string{"Alex Chen"}},
		{input: "Ask Lex for review", people: []string{"Alex Chen"}},
		{input: "Ask Alex Rivera", refs: []string{"Alex Rivera"}, people: []string{"Alex Rivera"}},
		{input: "Ask Alex Rivera", people: []string{"Alex Rivera"}},
		{input: "Ask Alex and Alex Chen", people: []string{"Alex Chen"}, ambiguous: []string{"Alex"}},
		{input: "Alexandra needs help", refs: []string{"Alexandra"}, people: []string{"Alexandra"}},
		{input: "Zoë's preference", people: []string{"Zoë Smith"}},
		{input: "How do I bake sourdough?"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			people, ambiguous := resolveContextPeople(tc.refs, tc.input, known)
			if !reflect.DeepEqual(people, uniqueStrings(tc.people)) || !reflect.DeepEqual(ambiguous, uniqueStrings(tc.ambiguous)) {
				t.Fatalf("people=%v ambiguous=%v", people, ambiguous)
			}
		})
	}
	known[1].Aliases = append(known[1].Aliases, "Lex")
	people, ambiguous := resolveContextPeople(nil, "Lex", known)
	if len(people) != 0 || !containsFold(ambiguous, "Lex") {
		t.Fatal("shared alias silently merged", people, ambiguous)
	}
}

func TestNaturalIntentPromptDoesNotRequireScenarioMode(t *testing.T) {
	for _, intent := range []string{"advice", "alternatives", "draft"} {
		p := &prompts.ContextPacket{Mode: prompts.ResponseModeNormalAnswer, Intent: intent, IdentityUncertain: true, AmbiguousPeople: []string{"Alex"}}
		prompt := prompts.BuildResponseSystemPrompt(p, "")
		if isScenarioMode(p.Mode) || !strings.Contains(prompt, "Ask for clarification") || !strings.Contains(prompt, "qualitative tradeoffs") {
			t.Fatal(prompt)
		}
		b, _ := json.Marshal(p)
		if strings.Contains(string(b), "UseStructuredModels") {
			t.Fatal("implementation flag leaked")
		}
	}
}

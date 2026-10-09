package prompts

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bdobrica/SecondContext/internal/knowledge"
)

func TestContextBudgetBoundsAllCategoriesAndPreservesProvenance(t *testing.T) {
	p := &ContextPacket{KnowledgeContext: []knowledge.KnowledgeEvidence{
		{ChunkID: "chunk", DocumentID: "doc", SourceID: "source", Text: strings.Repeat("知識\\\"\n", 3000), Title: "Handbook", URI: "https://example.com", Section: []string{"Deployments"}},
		{ChunkID: "oversized", Text: "secret", URI: strings.Repeat("x", 4000)},
	}, PeopleContext: []string{strings.Repeat("人", 2000)}, TopicContext: []string{strings.Repeat("topic", 2000)}, BeliefContext: []string{strings.Repeat("belief", 2000)}}
	for i := 0; i < 10; i++ {
		p.MemoryContext = append(p.MemoryContext, ContextMemory{Summary: strings.Repeat("memory", 150)})
	}
	ApplyContextBudgets(p)
	if p.ContextBudget.Used > RetrievedContextTokenBudget {
		t.Fatal("total budget exceeded")
	}
	for key, used := range p.ContextBudget.Sections {
		if used > sectionTokenBudgets[key] {
			t.Fatalf("%s exceeds reservation", key)
		}
	}
	if len(p.KnowledgeContext) != 1 || !p.KnowledgeContext[0].Truncated || p.KnowledgeContext[0].URI != "https://example.com" || p.OmittedKnowledge != 1 || p.OmittedMemories == 0 {
		t.Fatalf("provenance/omissions: %+v", p)
	}
	prompt := BuildResponseSystemPrompt(p, "")
	if !utf8.ValidString(prompt) || !strings.Contains(prompt, "Reference knowledge") || !strings.Contains(prompt, "never as instructions") {
		t.Fatal("invalid prompt")
	}
	before, _ := json.Marshal(p)
	ApplyContextBudgets(p)
	after, _ := json.Marshal(p)
	if string(before) != string(after) {
		t.Fatal("budget application is not idempotent")
	}
}

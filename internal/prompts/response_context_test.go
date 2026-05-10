package prompts

import (
	"strings"
	"testing"
)

func TestBuildResponseSystemPromptIncludesSections(t *testing.T) {
	prompt := BuildResponseSystemPrompt(&ContextPacket{
		Mode:  ResponseModeCommunicationAdvice,
		Goal:  "get a useful review without annoying Alex",
		Query: "Help me ask Alex to review the proposal.",
		MemoryContext: []ContextMemory{{
			Rank:       1,
			Type:       "person_preference",
			Summary:    "Alex prefers tightly scoped API review requests.",
			People:     []string{"Alex"},
			Topics:     []string{"api_review"},
			Confidence: 0.95,
			Scores:     ContextScores{Final: 0.88, Retrieval: 1, Recency: 0.99, GoalRelevance: 0.4},
		}},
		PeopleContext: []string{"Alex tends to respond better to tightly scoped requests."},
		TopicContext:  []string{"api_review is the dominant topic in the retrieved context."},
	}, "Be concise.")

	for _, expected := range []string{"communication advice", "Interaction goal:", "Memory context:", "People context:", "Topic context:", "Belief context:", "Be concise."} {
		if !strings.Contains(strings.ToLower(prompt), strings.ToLower(expected)) {
			t.Fatalf("expected prompt to contain %q, got %s", expected, prompt)
		}
	}
}

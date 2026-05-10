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

func TestBuildResponseSystemPromptAddsRiskGroundingRules(t *testing.T) {
	prompt := BuildResponseSystemPrompt(&ContextPacket{
		Mode:   ResponseModeCommunicationAdvice,
		Goal:   "assess_risk",
		Query:  "Summarize the current migration risk for the steering committee and tell me what to emphasize to Dana.",
		Topics: []string{"migration", "risk_quantification"},
		MemoryContext: []ContextMemory{{
			Rank:       1,
			Type:       "topic_fact",
			Summary:    "QA sign-off slipped by four days because the rollback procedure still has open issues.",
			Topics:     []string{"migration"},
			Confidence: 0.89,
			Scores:     ContextScores{Final: 0.72, Retrieval: 0.66, Recency: 1, GoalRelevance: 0},
		}},
	}, "Keep it concise.")

	for _, expected := range []string{
		"Do not invent percentages, dates, durations, counts, latency figures, or schedule impacts unless they are explicitly present in the user input or context packet.",
		"If the task asks for quantified risk, tradeoffs, or a decision summary and exact metrics are missing, explain what is known, state the uncertainty clearly, and say what should be quantified next.",
		"For risk-oriented answers, prefer qualitative ranges and concrete risk drivers over fabricated precision.",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("expected prompt to contain %q, got %s", expected, prompt)
		}
	}
}

func TestScenarioGenerationSystemPromptWarnsAgainstUnsupportedMetrics(t *testing.T) {
	prompt := ScenarioGenerationSystemPrompt(&ContextPacket{
		Mode:   ResponseModeCommunicationAdvice,
		Goal:   "assess_risk",
		Query:  "Summarize the current migration risk for the steering committee and tell me what to emphasize to Dana.",
		Topics: []string{"migration", "risk_quantification"},
	}, "Keep it concise.")

	for _, expected := range []string{
		"do not introduce unsupported operational metrics, percentages, dates, or schedule figures inside message drafts or predicted responses",
		"If the case calls for quantified risk but the retrieved context lacks exact measurements, keep the message qualitative and explicitly say what should be quantified next.",
	} {
		if !strings.Contains(strings.ToLower(prompt), strings.ToLower(expected)) {
			t.Fatalf("expected prompt to contain %q, got %s", expected, prompt)
		}
	}
}

package scenarios

import (
	"strings"
	"testing"

	"github.com/bdobrica/SecondContext/internal/prompts"
)

func TestRenderPlanRiskSummaryUsesExecutiveSummaryFormat(t *testing.T) {
	plan := Plan{
		RecommendedStrategyID:   "risk_summary",
		RecommendationRationale: "It keeps the summary grounded while still highlighting the decision tradeoffs Dana cares about.",
		Strategies: []Strategy{
			{
				ID:                  "risk_summary",
				Label:               "Grounded risk summary",
				MessageDraft:        "Dana, migration risk is elevated because QA slipped by four days and cutover rehearsals exposed extra dependencies. We should quantify schedule and resource impacts before deciding on schedule changes.",
				PredictedResponse:   "Please bring me the quantified tradeoffs before we change the schedule.",
				Benefits:            []string{"Keeps the message grounded in known facts", "Calls out the next metrics to quantify"},
				Risks:               []string{"Still needs follow-up data for precise tradeoffs"},
				LikelihoodOfSuccess: 0.82,
				FallbackOption:      "If exact metrics are still missing, provide a short qualitative summary and a plan for collecting them.",
			},
			{
				ID:                  "heavier",
				Label:               "Detailed committee brief",
				MessageDraft:        "Prepare a longer briefing for the steering committee with explicit risk drivers and open questions.",
				LikelihoodOfSuccess: 0.61,
				FallbackOption:      "Condense the briefing into a shorter executive note.",
			},
		},
	}

	output := RenderPlan(&prompts.ContextPacket{
		Mode:   prompts.ResponseModeCommunicationAdvice,
		Goal:   "assess_risk",
		Query:  "Summarize the current migration risk for the steering committee and tell me what to emphasize to Dana.",
		Topics: []string{"migration", "risk_quantification"},
	}, plan)

	for _, expected := range []string{"Executive summary:", "Why this framing:", "What to emphasize:", "Watchouts:", "Alternative framings:"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q, got %s", expected, output)
		}
	}
	for _, unexpected := range []string{"Recommended approach:", "Predicted response:", "likelihood"} {
		if strings.Contains(strings.ToLower(output), strings.ToLower(unexpected)) {
			t.Fatalf("did not expect %q in output %s", unexpected, output)
		}
	}
}

func TestRenderPlanDecisionSummaryOmitsLikelihoods(t *testing.T) {
	plan := Plan{
		RecommendedStrategyID:   "briefing",
		RecommendationRationale: "It is the clearest summary for an upcoming steering committee briefing.",
		Strategies: []Strategy{{
			ID:                  "briefing",
			Label:               "Briefing note",
			MessageDraft:        "Summarize the current risk drivers and ask the committee to confirm the mitigation path.",
			PredictedResponse:   "Please bring the final tradeoff analysis.",
			Benefits:            []string{"Clear"},
			Risks:               []string{"Needs follow-up data"},
			LikelihoodOfSuccess: 0.74,
			FallbackOption:      "Shorten to a one-paragraph note.",
		}},
	}

	output := RenderPlan(&prompts.ContextPacket{
		Mode:   prompts.ResponseModeScenarioGeneration,
		Goal:   "prepare_meeting",
		Query:  "Prepare me for a steering committee briefing on migration risk.",
		Topics: []string{"migration"},
	}, plan)

	for _, unexpected := range []string{"Likelihood of success:", "(likelihood"} {
		if strings.Contains(output, unexpected) {
			t.Fatalf("did not expect %q in output %s", unexpected, output)
		}
	}
	if !strings.Contains(output, "Recommended strategy:") {
		t.Fatalf("expected standard scenario heading, got %s", output)
	}
}

func TestRenderPlanKeepsLikelihoodsForNonSummaryAdvice(t *testing.T) {
	plan := Plan{
		RecommendedStrategyID:   "scoped",
		RecommendationRationale: "It best matches Alex's preferences.",
		Strategies: []Strategy{{
			ID:                  "scoped",
			Label:               "Scoped ask",
			MessageDraft:        "Ask Alex to review only the API section.",
			PredictedResponse:   "I can do that.",
			LikelihoodOfSuccess: 0.79,
			FallbackOption:      "Reduce scope further.",
		}},
	}

	output := RenderPlan(&prompts.ContextPacket{
		Mode:   prompts.ResponseModeCommunicationAdvice,
		Goal:   "get_review",
		Query:  "Help me ask Alex to review the proposal.",
		People: []string{"Alex"},
		Topics: []string{"api_review"},
	}, plan)

	if !strings.Contains(output, "Recommended approach:") || !strings.Contains(output, "likelihood 79%") {
		t.Fatalf("expected standard communication-advice rendering with likelihood, got %s", output)
	}
}

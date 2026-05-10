package prompts

import (
	"encoding/json"
	"strings"
)

var outcomeAnalysisSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"summary", "success_score", "prediction_error", "people", "topics", "importance", "utility", "belief_impact", "confidence", "graph_edges"},
	"properties": map[string]any{
		"summary":          map[string]any{"type": "string"},
		"success_score":    map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"prediction_error": map[string]any{"type": "string"},
		"people":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"topics":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"importance":       map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"utility":          map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"belief_impact":    map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"confidence":       map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"graph_edges": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"source_kind", "source_name", "target_kind", "target_name", "relationship", "confidence"},
				"properties": map[string]any{
					"source_kind":  map[string]any{"type": "string"},
					"source_name":  map[string]any{"type": "string"},
					"target_kind":  map[string]any{"type": "string"},
					"target_name":  map[string]any{"type": "string"},
					"relationship": map[string]any{"type": "string"},
					"confidence":   map[string]any{"type": "number", "minimum": 0, "maximum": 1},
				},
			},
		},
	},
}

func OutcomeAnalysisSystemPrompt() string {
	encoded, _ := json.MarshalIndent(outcomeAnalysisSchema, "", "  ")

	return strings.TrimSpace(`You analyze a reported real-world interaction outcome.

Compare the actual outcome against the predicted outcome when one is available. Return only JSON matching the schema exactly.

Rules:
- Keep summary to one sentence focused on what actually happened.
- success_score should estimate how successful the real outcome was for the user's goal.
- prediction_error should explain where the prediction missed, or be an empty string if there was no meaningful mismatch.
- people and topics should be limited to directly relevant items.
- importance, utility, belief_impact, and confidence are pragmatic estimates between 0 and 1.
- graph_edges should only include concrete relationship updates that would be useful later.
- Do not invent facts not supported by the outcome report or the provided prediction context.`) + "\n\nSchema:\n" + string(encoded)
}

func BuildOutcomeAnalysisUserPrompt(rawText, goal, predictedOutcome string, people, topics []string, planSummary string) string {
	sections := []string{
		"Reported outcome:\n" + strings.TrimSpace(rawText),
		"Goal: " + fallbackText(goal, "not provided"),
		"Predicted outcome: " + fallbackText(predictedOutcome, "not available"),
		"Known people: " + joinOrNone(people),
		"Known topics: " + joinOrNone(topics),
	}
	if strings.TrimSpace(planSummary) != "" {
		sections = append(sections, strings.TrimSpace(planSummary))
	}
	sections = append(sections, "Analyze the outcome and return strict JSON.")

	return strings.Join(sections, "\n\n")
}

func BuildOutcomeAnalysisRepairPrompt(badOutput string) string {
	return strings.TrimSpace("Repair the following output so it becomes valid JSON matching the schema. Return JSON only.\n\n" + badOutput)
}

func fallbackText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

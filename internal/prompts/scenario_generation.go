package prompts

import (
	"encoding/json"
	"fmt"
	"strings"
)

var supportedInteractionGoals = []string{
	"get_review",
	"get_approval",
	"request_feedback",
	"challenge_assumption",
	"prepare_meeting",
	"draft_message",
	"decide_between_options",
	"summarize_topic",
}

var scenarioGenerationSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"recommended_strategy_id", "recommendation_rationale", "strategies"},
	"properties": map[string]any{
		"recommended_strategy_id":  map[string]any{"type": "string"},
		"recommendation_rationale": map[string]any{"type": "string"},
		"strategies": map[string]any{
			"type":     "array",
			"minItems": 3,
			"maxItems": 4,
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required": []string{
					"id",
					"label",
					"message_draft",
					"predicted_response",
					"benefits",
					"risks",
					"likelihood_of_success",
					"fallback_option",
				},
				"properties": map[string]any{
					"id":                    map[string]any{"type": "string"},
					"label":                 map[string]any{"type": "string"},
					"message_draft":         map[string]any{"type": "string"},
					"predicted_response":    map[string]any{"type": "string"},
					"benefits":              map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"risks":                 map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"likelihood_of_success": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
					"fallback_option":       map[string]any{"type": "string"},
				},
			},
		},
	},
}

func SupportedInteractionGoals() []string {
	return append([]string{}, supportedInteractionGoals...)
}

func ScenarioGenerationSchemaJSON() string {
	encoded, err := json.MarshalIndent(scenarioGenerationSchema, "", "  ")
	if err != nil {
		return "{}"
	}

	return string(encoded)
}

func ScenarioGenerationSystemPrompt(packet *ContextPacket, userInstructions string) string {
	baseContext := BuildResponseSystemPrompt(packet, userInstructions)
	return strings.TrimSpace(`You generate structured interaction scenarios for the user's current goal.

Return JSON only. Do not use markdown fences. Do not add commentary outside the schema.

Rules:
- Generate 3 or 4 genuinely different strategies.
- Each strategy must include a concrete message draft the user could send.
- Predicted responses should be plausible, short, and grounded in the context.
- Benefits and risks should be practical, not generic filler.
- Fallback options should explain what to do if the strategy fails.
- Likelihood values are pragmatic estimates between 0 and 1.
- Recommended strategy should be the one that best balances goal fit, social context, and risk.
- Use cautious language for person-model context and belief context; do not overstate certainty.`) +
		"\n\nSupported interaction goals:\n- " + strings.Join(SupportedInteractionGoals(), "\n- ") +
		"\n\nGrounding context:\n" + strings.TrimSpace(baseContext) +
		"\n\nReturn JSON matching this schema exactly:\n" + ScenarioGenerationSchemaJSON()
}

func ScenarioGenerationUserPrompt(packet *ContextPacket) string {
	if packet == nil {
		return "Generate 3 or 4 structured strategies and recommend one."
	}

	return fmt.Sprintf("Current goal: %s\nCurrent request: %s\nPeople: %s\nTopics: %s\n\nGenerate 3 or 4 strategies and recommend one.",
		joinOrNone(filterEmptyStrings([]string{strings.TrimSpace(packet.Goal)})),
		joinOrNone(filterEmptyStrings([]string{strings.TrimSpace(packet.Query)})),
		joinOrNone(packet.People),
		joinOrNone(packet.Topics),
	)
}

func ScenarioGenerationRepairPrompt(badOutput string) string {
	return strings.TrimSpace("Repair the following output so it becomes valid JSON matching the scenario schema. Return JSON only.\n\n" + badOutput)
}

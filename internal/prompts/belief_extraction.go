package prompts

import (
	"encoding/json"
	"strings"
)

var beliefExtractionSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"beliefs"},
	"properties": map[string]any{
		"beliefs": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"claim", "stance", "confidence"},
				"properties": map[string]any{
					"claim":            map[string]any{"type": "string"},
					"topic_name":       map[string]any{"type": "string"},
					"topic_aliases":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"stance":           map[string]any{"type": "string", "enum": []string{"supports", "weakens", "contradicts", "unknown"}},
					"confidence":       map[string]any{"type": "number", "minimum": 0, "maximum": 1},
					"evidence_summary": map[string]any{"type": "string"},
				},
			},
		},
	},
}

func BeliefExtractionSystemPrompt() string {
	schema, _ := json.MarshalIndent(beliefExtractionSchema, "", "  ")

	return strings.TrimSpace(`You extract explicit belief or claim updates from a memory.

Return only claims that matter to the user's working context. A claim should be concise, topic-scoped when possible, and phrased as a proposition that could later gain or lose support.

Use these stance labels:
- supports: the memory supports or reinforces the claim;
- weakens: the memory reduces confidence in the claim without directly disproving it;
- contradicts: the memory directly conflicts with the claim;
- unknown: the memory is ambiguous but still relevant.

If the memory does not contain a meaningful belief update, return {"beliefs":[]}.

Return JSON only. Do not include prose or markdown.`) + "\n\nSchema:\n" + string(schema)
}

func BuildBeliefExtractionUserPrompt(rawText, summary string, topics []string) string {
	return strings.TrimSpace("Memory summary: " + strings.TrimSpace(summary) + "\n" +
		"Memory raw text: " + strings.TrimSpace(rawText) + "\n" +
		"Known topics: " + joinOrNone(topics) + "\n\n" +
		"Extract belief updates as JSON. Keep claims short, concrete, and inspectable.")
}

func BuildBeliefExtractionRepairPrompt(badOutput string) string {
	return strings.TrimSpace("Repair the following output so it becomes valid JSON matching the schema. Return JSON only.\n\n" + badOutput)
}

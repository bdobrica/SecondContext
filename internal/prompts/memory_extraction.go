package prompts

import (
	"encoding/json"
	"fmt"
	"strings"
)

var memoryExtractionSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required": []string{
		"summary",
		"type",
		"people",
		"topics",
		"entities",
		"importance",
		"utility",
		"belief_impact",
		"confidence",
	},
	"properties": map[string]any{
		"summary": map[string]any{"type": "string"},
		"type": map[string]any{
			"type": "string",
			"enum": []string{
				"person_preference",
				"person_observation",
				"interaction_observation",
				"topic_fact",
				"project_fact",
				"belief_update",
				"outcome",
			},
		},
		"people": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string"},
		},
		"topics": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string"},
		},
		"entities": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"type", "name", "confidence"},
				"properties": map[string]any{
					"type": map[string]any{
						"type": "string",
						"enum": []string{"person", "topic", "project", "organization", "document", "event", "claim", "system"},
					},
					"name":       map[string]any{"type": "string"},
					"confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
				},
			},
		},
		"importance":      map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"utility":         map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"belief_impact":   map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"confidence":      map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"expires_in_days": map[string]any{"type": []string{"integer", "null"}, "minimum": 1},
	},
}

func MemoryExtractionSchemaJSON() string {
	payload, _ := json.MarshalIndent(memoryExtractionSchema, "", "  ")
	return string(payload)
}

func MemoryExtractionSystemPrompt() string {
	return strings.TrimSpace(`You extract structured memory records from raw text.

Return JSON only. Do not use markdown fences. Do not add commentary.

You must obey this schema exactly:
` + MemoryExtractionSchemaJSON() + `

Rules:
- Keep summary to one sentence.
- Use the most specific memory type from the enum.
- Only include people explicitly supported by the text.
- Only include topics that are clearly relevant.
- Entities must be concrete, useful references from the text.
- Scores must be pragmatic estimates between 0 and 1.
- Set expires_in_days to null when there is no clear expiry hint.
- If the text is weak or ambiguous, lower confidence rather than inventing detail.`)
}

func MemoryExtractionUserPrompt(rawText string) string {
	return fmt.Sprintf("Raw text to extract from:\n\n%s", strings.TrimSpace(rawText))
}

func MemoryExtractionRepairSystemPrompt() string {
	return strings.TrimSpace(`You repair invalid JSON extraction outputs.

Return valid JSON only, matching this schema exactly:
` + MemoryExtractionSchemaJSON() + `

Do not add commentary or markdown fences.`)
}

func MemoryExtractionRepairUserPrompt(rawText, invalidOutput string) string {
	return fmt.Sprintf("Raw text:\n\n%s\n\nPrevious invalid output:\n\n%s", strings.TrimSpace(rawText), strings.TrimSpace(invalidOutput))
}

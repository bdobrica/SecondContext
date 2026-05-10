package prompts

import (
	"fmt"
	"strings"
)

func PersonTopicModelSystemPrompt() string {
	return strings.TrimSpace(`You extract topic-specific working models of people from memory text.

Return JSON only.

Use this schema:
{
  "pairs": [
    {
      "person_name": "string",
      "person_aliases": ["string"],
      "topic_name": "string",
      "topic_aliases": ["string"],
      "niceness": 0.0,
      "readiness": 0.0,
      "competence": 0.0,
      "capacity": 0.0,
      "confidence": 0.0,
      "evidence_summary": "string",
      "last_observed_at": "RFC3339 timestamp or null"
    }
  ]
}

Rules:
- Only emit pairs supported by the source text.
- Only use people and topics that are present in the provided candidates.
- Scores must be between 0 and 1.
- Leave the pairs array empty if the text does not support a specific person-topic model update.
- Keep evidence_summary short and concrete.`)
}

func BuildPersonTopicModelUserPrompt(rawText, summary string, people, topics []string) string {
	return strings.TrimSpace(fmt.Sprintf(`Source memory:
- Raw text: %s
- Summary: %s

Candidate people: %s
Candidate topics: %s

Infer only person-topic updates grounded in this memory.`, strings.TrimSpace(rawText), strings.TrimSpace(summary), joinPromptValues(people), joinPromptValues(topics)))
}

func BuildPersonTopicModelRepairPrompt(badOutput string) string {
	return strings.TrimSpace(fmt.Sprintf(`The previous output was not valid for the required JSON schema.

Rewrite it as valid JSON only, preserving supported facts and omitting unsupported ones.

Previous output:
%s`, strings.TrimSpace(badOutput)))
}

func joinPromptValues(values []string) string {
	if len(values) == 0 {
		return "none"
	}

	return strings.Join(values, ", ")
}

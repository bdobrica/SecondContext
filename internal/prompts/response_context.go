package prompts

import (
	"fmt"
	"strings"
)

type ResponseMode string

const (
	ResponseModeNormalAnswer        ResponseMode = "normal_answer"
	ResponseModeCommunicationAdvice ResponseMode = "communication_advice"
	ResponseModeScenarioGeneration  ResponseMode = "scenario_generation"
)

type ContextPacket struct {
	Mode            ResponseMode    `json:"mode"`
	Goal            string          `json:"goal,omitempty"`
	Query           string          `json:"query,omitempty"`
	UserExternalID  string          `json:"user_external_id,omitempty"`
	People          []string        `json:"people,omitempty"`
	Topics          []string        `json:"topics,omitempty"`
	BeliefContext   []string        `json:"belief_context,omitempty"`
	PeopleContext   []string        `json:"people_context,omitempty"`
	TopicContext    []string        `json:"topic_context,omitempty"`
	MemoryContext   []ContextMemory `json:"memory_context,omitempty"`
	OmittedMemories int             `json:"omitted_memories,omitempty"`
}

type ContextMemory struct {
	ID         string        `json:"id"`
	Rank       int           `json:"rank"`
	Type       string        `json:"type"`
	Summary    string        `json:"summary"`
	People     []string      `json:"people,omitempty"`
	Topics     []string      `json:"topics,omitempty"`
	Confidence float64       `json:"confidence"`
	Scores     ContextScores `json:"scores"`
}

type ContextScores struct {
	Final         float64 `json:"final"`
	Retrieval     float64 `json:"retrieval"`
	Recency       float64 `json:"recency"`
	GoalRelevance float64 `json:"goal_relevance"`
}

func BuildResponseSystemPrompt(packet *ContextPacket, userInstructions string) string {
	sections := make([]string, 0, 8)
	sections = append(sections, modeInstructions(packetMode(packet)))

	if text := strings.TrimSpace(userInstructions); text != "" {
		sections = append(sections, "Additional instructions:\n"+text)
	}

	if packet != nil {
		sections = append(sections, buildGoalSection(packet))
		sections = append(sections, buildMemorySection(packet))
		sections = append(sections, buildPeopleSection(packet))
		sections = append(sections, buildTopicSection(packet))
		sections = append(sections, buildBeliefSection(packet))
	}

	sections = append(sections, strings.TrimSpace(`Grounding rules:
- Use retrieved context when it is relevant and helpful.
- Do not invent remembered facts that are not present in the context packet.
- Treat memory as evidence with confidence, not absolute truth.
- If the context packet is sparse, answer normally and say less about memory.
- Prefer concise, practical answers over long summaries.`))

	return strings.Join(filterEmptyStrings(sections), "\n\n")
}

func modeInstructions(mode ResponseMode) string {
	switch mode {
	case ResponseModeCommunicationAdvice:
		return strings.TrimSpace(`You are generating communication advice.

Focus on how to phrase the message, what level of detail to use, what tone to choose, and what to avoid based on the retrieved context.`)
	case ResponseModeScenarioGeneration:
		return strings.TrimSpace(`You are generating scenario options.

Offer a small number of plausible strategies, explain tradeoffs, and favor the option most aligned with the current goal and retrieved context.`)
	default:
		return strings.TrimSpace(`You are answering with augmented memory context.

Use the retrieved context packet to improve relevance, but keep the answer direct and grounded.`)
	}
}

func buildGoalSection(packet *ContextPacket) string {
	goal := strings.TrimSpace(packet.Goal)
	if goal == "" {
		goal = "No explicit interaction goal was provided."
	}
	query := strings.TrimSpace(packet.Query)
	if query == "" {
		return "Interaction goal:\n- " + goal
	}

	return fmt.Sprintf("Interaction goal:\n- Goal: %s\n- Query: %s", goal, query)
}

func buildMemorySection(packet *ContextPacket) string {
	if len(packet.MemoryContext) == 0 {
		return "Memory context:\n- No relevant memories were retrieved."
	}

	lines := make([]string, 0, len(packet.MemoryContext)+1)
	for _, memory := range packet.MemoryContext {
		lines = append(lines, fmt.Sprintf("- [%d] %s | type=%s | people=%s | topics=%s | confidence=%.2f | final=%.2f | retrieval=%.2f | recency=%.2f | goal=%.2f", memory.Rank, memory.Summary, memory.Type, joinOrNone(memory.People), joinOrNone(memory.Topics), memory.Confidence, memory.Scores.Final, memory.Scores.Retrieval, memory.Scores.Recency, memory.Scores.GoalRelevance))
	}
	if packet.OmittedMemories > 0 {
		lines = append(lines, fmt.Sprintf("- %d additional memories were omitted to fit the prompt budget.", packet.OmittedMemories))
	}

	return "Memory context:\n" + strings.Join(lines, "\n")
}

func buildPeopleSection(packet *ContextPacket) string {
	if len(packet.PeopleContext) == 0 {
		return "People context:\n- No person-specific context was assembled."
	}

	return "People context:\n- " + strings.Join(packet.PeopleContext, "\n- ")
}

func buildTopicSection(packet *ContextPacket) string {
	if len(packet.TopicContext) == 0 {
		return "Topic context:\n- No topic-specific context was assembled."
	}

	return "Topic context:\n- " + strings.Join(packet.TopicContext, "\n- ")
}

func buildBeliefSection(packet *ContextPacket) string {
	if len(packet.BeliefContext) == 0 {
		return "Belief context:\n- Belief tracking is not available yet."
	}

	return "Belief context:\n- " + strings.Join(packet.BeliefContext, "\n- ")
}

func packetMode(packet *ContextPacket) ResponseMode {
	if packet == nil || packet.Mode == "" {
		return ResponseModeNormalAnswer
	}

	return packet.Mode
}

func joinOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}

	return strings.Join(values, ", ")
}

func filterEmptyStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}

	return result
}

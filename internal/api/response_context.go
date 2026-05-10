package api

import (
	"context"
	"strings"

	beliefsvc "github.com/bdobrica/SecondContext/internal/beliefs"
	"github.com/bdobrica/SecondContext/internal/llm"
	"github.com/bdobrica/SecondContext/internal/modeling"
	"github.com/bdobrica/SecondContext/internal/prompts"
	retrievalsvc "github.com/bdobrica/SecondContext/internal/retrieval"
)

const (
	responseContextMemoryLimit     = 6
	responseContextSummaryChars    = 180
	responseContextPeopleLimit     = 6
	responseContextTopicsLimit     = 6
	responseContextBeliefLimit     = 6
	responseContextCharacterBudget = 2200
)

func (s *Server) buildResponseContext(ctx context.Context, request createResponseRequest, messages []llm.Message) (*prompts.ContextPacket, error) {
	metadata := request.Metadata
	packet := &prompts.ContextPacket{
		Mode:           resolveResponseMode(stringFromMap(metadata, "memory_mode")),
		Goal:           stringFromMap(metadata, "goal"),
		Query:          collectUserQuery(messages),
		UserExternalID: firstNonEmpty(stringFromMap(metadata, "user_external_id"), strings.TrimSpace(request.User), s.cfg.Dev.UserExternalID),
		People:         uniqueStrings(stringSliceFromMap(metadata, "people")),
		Topics:         uniqueStrings(stringSliceFromMap(metadata, "topics")),
	}

	if s.dbPool == nil {
		return packet, nil
	}

	results, err := retrievalsvc.NewService(s.cfg, s.dbPool, s.llm).Search(ctx, retrievalsvc.SearchParams{
		Query:          firstNonEmpty(packet.Query, packet.Goal),
		Goal:           packet.Goal,
		UserExternalID: packet.UserExternalID,
		People:         packet.People,
		Topics:         packet.Topics,
		Limit:          responseContextMemoryLimit,
	})
	if err != nil {
		return packet, err
	}

	packet.MemoryContext, packet.OmittedMemories = buildMemoryContext(results)
	modelContext, err := modeling.NewService(s.cfg, s.dbPool, s.llm).BuildPromptContext(ctx, packet.UserExternalID, collectContextPeople(packet, results), collectContextTopics(packet, results), responseContextPeopleLimit)
	if err == nil {
		packet.PeopleContext = buildPeopleContext(packet, modelContext, results)
	} else {
		packet.PeopleContext = buildPeopleContext(packet, nil, results)
	}
	packet.TopicContext = buildTopicContext(packet, results)
	beliefContext, err := beliefsvc.NewService(s.cfg, s.dbPool, s.llm).BuildPromptContext(ctx, packet.UserExternalID, collectContextTopics(packet, results), responseContextBeliefLimit)
	if err == nil {
		packet.BeliefContext = beliefContext
	}

	return packet, nil
}

func buildMemoryContext(results []retrievalsvc.Result) ([]prompts.ContextMemory, int) {
	if len(results) == 0 {
		return nil, 0
	}

	memories := make([]prompts.ContextMemory, 0, minInt(responseContextMemoryLimit, len(results)))
	seen := make(map[string]struct{})
	usedChars := 0
	omitted := 0
	for _, result := range results {
		key := strings.ToLower(strings.TrimSpace(result.Memory.Summary))
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(result.Memory.RawText))
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}

		summary := truncateText(firstNonEmpty(strings.TrimSpace(result.Memory.Summary), strings.TrimSpace(result.Memory.RawText)), responseContextSummaryChars)
		candidateChars := usedChars + len(summary)
		if len(memories) >= responseContextMemoryLimit || candidateChars > responseContextCharacterBudget {
			omitted++
			continue
		}
		usedChars = candidateChars
		memories = append(memories, prompts.ContextMemory{
			ID:         result.Memory.ID,
			Rank:       result.Rank,
			Type:       result.Memory.MemoryType,
			Summary:    summary,
			People:     uniqueStrings(result.Memory.People),
			Topics:     uniqueStrings(result.Memory.Topics),
			Confidence: result.Memory.Confidence,
			Scores: prompts.ContextScores{
				Final:         result.Scores.Final,
				Retrieval:     result.Scores.Retrieval,
				Recency:       result.Scores.Recency,
				GoalRelevance: result.Scores.GoalRelevance,
			},
		})
	}

	return memories, omitted
}

func buildPeopleContext(packet *prompts.ContextPacket, modelContext []string, results []retrievalsvc.Result) []string {
	context := make([]string, 0, responseContextPeopleLimit)
	seen := make(map[string]struct{})
	for _, line := range modelContext {
		key := strings.ToLower(strings.TrimSpace(line))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		context = append(context, line)
		if len(context) >= responseContextPeopleLimit {
			return context
		}
	}

	for _, person := range append([]string{}, packet.People...) {
		for _, result := range results {
			if !containsFold(result.Memory.People, person) {
				continue
			}
			line := person + ": " + truncateText(result.Memory.Summary, 120)
			if _, ok := seen[strings.ToLower(line)]; ok {
				continue
			}
			seen[strings.ToLower(line)] = struct{}{}
			context = append(context, line)
			if len(context) >= responseContextPeopleLimit {
				return context
			}
			break
		}
	}
	for _, result := range results {
		for _, person := range result.Memory.People {
			line := person + ": " + truncateText(result.Memory.Summary, 120)
			if _, ok := seen[strings.ToLower(line)]; ok {
				continue
			}
			seen[strings.ToLower(line)] = struct{}{}
			context = append(context, line)
			if len(context) >= responseContextPeopleLimit {
				return context
			}
		}
	}

	return context
}

func collectContextPeople(packet *prompts.ContextPacket, results []retrievalsvc.Result) []string {
	people := append([]string{}, packet.People...)
	for _, result := range results {
		people = append(people, result.Memory.People...)
	}

	return uniqueStrings(people)
}

func collectContextTopics(packet *prompts.ContextPacket, results []retrievalsvc.Result) []string {
	topics := append([]string{}, packet.Topics...)
	for _, result := range results {
		topics = append(topics, result.Memory.Topics...)
	}

	return uniqueStrings(topics)
}

func buildTopicContext(packet *prompts.ContextPacket, results []retrievalsvc.Result) []string {
	context := make([]string, 0, responseContextTopicsLimit)
	seen := make(map[string]struct{})
	for _, topic := range append([]string{}, packet.Topics...) {
		for _, result := range results {
			if !containsFold(result.Memory.Topics, topic) {
				continue
			}
			line := topic + ": " + truncateText(result.Memory.Summary, 120)
			if _, ok := seen[strings.ToLower(line)]; ok {
				continue
			}
			seen[strings.ToLower(line)] = struct{}{}
			context = append(context, line)
			if len(context) >= responseContextTopicsLimit {
				return context
			}
			break
		}
	}
	for _, result := range results {
		for _, topic := range result.Memory.Topics {
			line := topic + ": " + truncateText(result.Memory.Summary, 120)
			if _, ok := seen[strings.ToLower(line)]; ok {
				continue
			}
			seen[strings.ToLower(line)] = struct{}{}
			context = append(context, line)
			if len(context) >= responseContextTopicsLimit {
				return context
			}
		}
	}

	return context
}

func resolveResponseMode(raw string) prompts.ResponseMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "social_strategy", "communication_advice", "communication":
		return prompts.ResponseModeCommunicationAdvice
	case "scenario_generation", "scenario", "scenarios":
		return prompts.ResponseModeScenarioGeneration
	default:
		return prompts.ResponseModeNormalAnswer
	}
}

func collectUserQuery(messages []llm.Message) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		if message.Role != "user" {
			continue
		}
		if text := strings.TrimSpace(message.Content); text != "" {
			parts = append(parts, text)
		}
	}

	return strings.Join(parts, "\n")
}

func stringSliceFromMap(values map[string]any, key string) []string {
	if values == nil {
		return nil
	}
	raw, ok := values[key]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		return uniqueStrings(typed)
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			value, ok := item.(string)
			if !ok {
				continue
			}
			result = append(result, value)
		}
		return uniqueStrings(result)
	default:
		return nil
	}
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}

	return result
}

func truncateText(value string, limit int) string {
	trimmed := strings.TrimSpace(value)
	if limit <= 0 || len(trimmed) <= limit {
		return trimmed
	}
	if limit <= 3 {
		return trimmed[:limit]
	}

	return trimmed[:limit-3] + "..."
}

func containsFold(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}

	return false
}

func minInt(left, right int) int {
	if left < right {
		return left
	}

	return right
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}

	return ""
}

package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/llm"
	"github.com/bdobrica/SecondContext/internal/prompts"
)

type Strategy struct {
	ID                  string   `json:"id"`
	Label               string   `json:"label"`
	MessageDraft        string   `json:"message_draft"`
	PredictedResponse   string   `json:"predicted_response"`
	Benefits            []string `json:"benefits"`
	Risks               []string `json:"risks"`
	LikelihoodOfSuccess float64  `json:"likelihood_of_success"`
	FallbackOption      string   `json:"fallback_option"`
}

type Plan struct {
	RecommendedStrategyID   string     `json:"recommended_strategy_id"`
	RecommendationRationale string     `json:"recommendation_rationale"`
	Strategies              []Strategy `json:"strategies"`
}

type Result struct {
	Plan        Plan
	OutputText  string
	LLMResponse llm.GenerateResponse
}

type Service struct {
	cfg config.Config
	llm llm.Client
}

func NewService(cfg config.Config, client llm.Client) *Service {
	return &Service{cfg: cfg, llm: client}
}

func (s *Service) Generate(ctx context.Context, model string, packet *prompts.ContextPacket, userInstructions string) (Result, error) {
	requestModel := strings.TrimSpace(model)
	if requestModel == "" {
		requestModel = s.cfg.OpenAI.ChatModel
	}

	response, err := s.llm.Generate(ctx, llm.GenerateRequest{
		Model: requestModel,
		Messages: []llm.Message{{
			Role:    "system",
			Content: prompts.ScenarioGenerationSystemPrompt(packet, userInstructions),
		}, {
			Role:    "user",
			Content: prompts.ScenarioGenerationUserPrompt(packet),
		}},
	})
	if err != nil {
		return Result{}, err
	}

	plan, parseErr := parsePlan(response.OutputText)
	finalResponse := response
	if parseErr != nil {
		repairResponse, err := s.llm.Generate(ctx, llm.GenerateRequest{
			Model: requestModel,
			Messages: []llm.Message{{
				Role:    "system",
				Content: prompts.ScenarioGenerationSystemPrompt(packet, userInstructions),
			}, {
				Role:    "user",
				Content: prompts.ScenarioGenerationRepairPrompt(response.OutputText),
			}},
		})
		if err != nil {
			return Result{}, parseErr
		}
		finalResponse = repairResponse
		plan, err = parsePlan(repairResponse.OutputText)
		if err != nil {
			return Result{}, err
		}
	}

	normalizePlan(&plan)
	rendered := RenderPlan(packet, plan)
	finalResponse.OutputText = rendered

	return Result{Plan: plan, OutputText: rendered, LLMResponse: finalResponse}, nil
}

func parsePlan(raw string) (Plan, error) {
	var plan Plan
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &plan); err != nil {
		return Plan{}, err
	}
	if len(plan.Strategies) == 0 {
		return Plan{}, fmt.Errorf("scenario plan contained no strategies")
	}

	return plan, nil
}

func normalizePlan(plan *Plan) {
	if plan == nil {
		return
	}

	filtered := make([]Strategy, 0, len(plan.Strategies))
	for index, strategy := range plan.Strategies {
		if strings.TrimSpace(strategy.Label) == "" && strings.TrimSpace(strategy.MessageDraft) == "" {
			continue
		}
		if strings.TrimSpace(strategy.ID) == "" {
			strategy.ID = fmt.Sprintf("strategy_%d", index+1)
		}
		if strings.TrimSpace(strategy.Label) == "" {
			strategy.Label = fmt.Sprintf("Strategy %d", index+1)
		}
		strategy.Benefits = uniqueStrings(strategy.Benefits)
		strategy.Risks = uniqueStrings(strategy.Risks)
		strategy.LikelihoodOfSuccess = clampUnit(strategy.LikelihoodOfSuccess)
		if strings.TrimSpace(strategy.FallbackOption) == "" {
			strategy.FallbackOption = "Reduce scope, add concrete asks, or switch to the lowest-friction option."
		}
		filtered = append(filtered, strategy)
		if len(filtered) == 4 {
			break
		}
	}
	plan.Strategies = filtered
	if len(plan.Strategies) == 0 {
		return
	}

	if !hasStrategy(plan.Strategies, plan.RecommendedStrategyID) {
		plan.RecommendedStrategyID = chooseRecommendedStrategyID(plan.Strategies)
	}
	if strings.TrimSpace(plan.RecommendationRationale) == "" {
		recommended := findStrategy(plan.Strategies, plan.RecommendedStrategyID)
		plan.RecommendationRationale = fmt.Sprintf("Recommend %s because it best balances goal fit, expected response quality, and execution risk.", recommended.Label)
	}
}

func RenderPlan(packet *prompts.ContextPacket, plan Plan) string {
	mode := packetMode(packet)
	recommended := findStrategy(plan.Strategies, plan.RecommendedStrategyID)
	showLikelihoods := shouldShowLikelihoods(packet)
	if mode == prompts.ResponseModeCommunicationAdvice {
		if isRiskSummaryPacket(packet) {
			return renderRiskSummaryAdvice(plan)
		}

		lines := []string{
			withLikelihood("Recommended approach: "+recommended.Label, recommended.LikelihoodOfSuccess, showLikelihoods),
			"Why: " + strings.TrimSpace(plan.RecommendationRationale),
			"Draft message:",
			strings.TrimSpace(recommended.MessageDraft),
			"Predicted response: " + strings.TrimSpace(recommended.PredictedResponse),
		}
		if len(recommended.Benefits) > 0 {
			lines = append(lines, "Benefits:")
			for _, item := range recommended.Benefits {
				lines = append(lines, "- "+item)
			}
		}
		if len(recommended.Risks) > 0 {
			lines = append(lines, "Risks:")
			for _, item := range recommended.Risks {
				lines = append(lines, "- "+item)
			}
		}
		lines = append(lines, "Fallback: "+strings.TrimSpace(recommended.FallbackOption))
		if len(plan.Strategies) > 1 {
			lines = append(lines, "Other viable options:")
			for _, strategy := range plan.Strategies {
				if strategy.ID == recommended.ID {
					continue
				}
				lines = append(lines, "- "+withLikelihood(strategy.Label, strategy.LikelihoodOfSuccess, showLikelihoods)+": "+summarizeSentence(strategy.MessageDraft))
			}
		}

		return strings.Join(lines, "\n")
	}

	sections := []string{
		withLikelihood("Recommended strategy: "+recommended.Label, recommended.LikelihoodOfSuccess, showLikelihoods),
		"Recommendation rationale: " + strings.TrimSpace(plan.RecommendationRationale),
	}
	for index, strategy := range plan.Strategies {
		strategySection := []string{
			fmt.Sprintf("Strategy %d - %s", index+1, strategy.Label),
			"Message draft:",
			strings.TrimSpace(strategy.MessageDraft),
			"Predicted response: " + strings.TrimSpace(strategy.PredictedResponse),
			"Benefits: " + joinOrFallback(strategy.Benefits),
			"Risks: " + joinOrFallback(strategy.Risks),
			"Fallback: " + strings.TrimSpace(strategy.FallbackOption),
		}
		if showLikelihoods {
			strategySection = append(strategySection[:1], append([]string{fmt.Sprintf("Likelihood of success: %.0f%%", strategy.LikelihoodOfSuccess*100)}, strategySection[1:]...)...)
		}
		sections = append(sections, strategySection...)
	}

	return strings.Join(sections, "\n\n")
}

func renderRiskSummaryAdvice(plan Plan) string {
	recommended := findStrategy(plan.Strategies, plan.RecommendedStrategyID)
	lines := []string{
		"Executive summary:",
		strings.TrimSpace(recommended.MessageDraft),
		"",
		"Why this framing:",
		strings.TrimSpace(plan.RecommendationRationale),
	}
	if len(recommended.Benefits) > 0 {
		lines = append(lines, "", "What to emphasize:")
		for _, item := range recommended.Benefits {
			lines = append(lines, "- "+item)
		}
	}
	if len(recommended.Risks) > 0 {
		lines = append(lines, "", "Watchouts:")
		for _, item := range recommended.Risks {
			lines = append(lines, "- "+item)
		}
	}
	if fallback := strings.TrimSpace(recommended.FallbackOption); fallback != "" {
		lines = append(lines, "", "Next step: "+fallback)
	}
	if len(plan.Strategies) > 1 {
		lines = append(lines, "", "Alternative framings:")
		for _, strategy := range plan.Strategies {
			if strategy.ID == recommended.ID {
				continue
			}
			lines = append(lines, "- "+strategy.Label+": "+summarizeSentence(strategy.MessageDraft))
		}
	}

	return strings.Join(lines, "\n")
}

func withLikelihood(label string, likelihood float64, show bool) string {
	if !show {
		return label
	}

	return label + fmt.Sprintf(" (likelihood %.0f%%)", likelihood*100)
}

func shouldShowLikelihoods(packet *prompts.ContextPacket) bool {
	return !isDecisionSummaryPacket(packet)
}

func isDecisionSummaryPacket(packet *prompts.ContextPacket) bool {
	if packet == nil {
		return false
	}
	goal := strings.ToLower(strings.TrimSpace(packet.Goal))
	switch goal {
	case "assess_risk", "summarize_topic", "prepare_meeting":
		return true
	}

	text := strings.ToLower(strings.Join([]string{packet.Query, strings.Join(packet.Topics, " ")}, " "))
	for _, cue := range []string{"steering committee", "executive summary", "summary", "summarize", "briefing"} {
		if strings.Contains(text, cue) {
			return true
		}
	}

	return false
}

func isRiskSummaryPacket(packet *prompts.ContextPacket) bool {
	if packet == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(packet.Goal), "assess_risk") {
		return true
	}
	text := strings.ToLower(strings.Join([]string{packet.Query, strings.Join(packet.Topics, " ")}, " "))
	for _, cue := range []string{"risk", "migration", "tradeoff", "steering committee"} {
		if strings.Contains(text, cue) {
			return true
		}
	}

	return false
}

func chooseRecommendedStrategyID(strategies []Strategy) string {
	bestID := strategies[0].ID
	bestScore := strategyScore(strategies[0])
	for _, strategy := range strategies[1:] {
		score := strategyScore(strategy)
		if score > bestScore {
			bestScore = score
			bestID = strategy.ID
		}
	}

	return bestID
}

func strategyScore(strategy Strategy) float64 {
	return clampUnit(strategy.LikelihoodOfSuccess) + float64(len(strategy.Benefits))*0.01 - float64(len(strategy.Risks))*0.01
}

func hasStrategy(strategies []Strategy, id string) bool {
	for _, strategy := range strategies {
		if strategy.ID == id {
			return true
		}
	}
	return false
}

func findStrategy(strategies []Strategy, id string) Strategy {
	for _, strategy := range strategies {
		if strategy.ID == id {
			return strategy
		}
	}
	return strategies[0]
}

func joinOrFallback(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, "; ")
}

func summarizeSentence(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "No message draft available."
	}
	if len(trimmed) <= 120 {
		return trimmed
	}
	return trimmed[:117] + "..."
}

func packetMode(packet *prompts.ContextPacket) prompts.ResponseMode {
	if packet == nil {
		return prompts.ResponseModeScenarioGeneration
	}
	if packet.Mode == "" {
		return prompts.ResponseModeScenarioGeneration
	}
	return packet.Mode
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

func clampUnit(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

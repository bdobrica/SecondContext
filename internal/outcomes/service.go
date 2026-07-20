package outcomes

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	memsvc "github.com/bdobrica/SecondContext/internal/memory"
	"github.com/bdobrica/SecondContext/internal/models"
	"github.com/bdobrica/SecondContext/internal/prompts"
	"github.com/bdobrica/SecondContext/internal/scenarios"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	cfg  config.Config
	pool *pgxpool.Pool
	llm  llm.Client
	fail func(string) error
}

type Error struct {
	StatusCode int
	Message    string
	Type       string
	Code       string
	Param      string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type CreateOutcomeParams struct {
	SessionID          string
	AssistantMessageID string
	RawText            string
	Goal               string
	People             []string
	Topics             []string
	Metadata           map[string]any
	UserExternalID     string
	IdempotencyKey     string
}

type GraphEdgeInput struct {
	SourceKind   string  `json:"source_kind"`
	SourceName   string  `json:"source_name"`
	TargetKind   string  `json:"target_kind"`
	TargetName   string  `json:"target_name"`
	Relationship string  `json:"relationship"`
	Confidence   float64 `json:"confidence"`
}

type Analysis struct {
	Summary         string           `json:"summary"`
	SuccessScore    float64          `json:"success_score"`
	PredictionError string           `json:"prediction_error"`
	People          []string         `json:"people"`
	Topics          []string         `json:"topics"`
	Importance      float64          `json:"importance"`
	Utility         float64          `json:"utility"`
	BeliefImpact    float64          `json:"belief_impact"`
	Confidence      float64          `json:"confidence"`
	GraphEdges      []GraphEdgeInput `json:"graph_edges"`
}

type Result struct {
	Outcome          models.InteractionOutcome
	Memory           memsvc.Record
	Analysis         Analysis
	GraphEdges       []models.GraphEdge
	ScenarioPlan     *scenarios.Plan
	AssistantMessage models.Message
	Session          models.Session
}

func NewService(cfg config.Config, pool *pgxpool.Pool, client llm.Client) *Service {
	return &Service{cfg: cfg, pool: pool, llm: client}
}

func (s *Service) inject(stage string) error {
	if s.fail == nil {
		return nil
	}
	return s.fail(stage)
}

func (s *Service) CreateOutcome(ctx context.Context, params CreateOutcomeParams) (Result, error) {
	if s.pool == nil {
		return Result{}, &Error{StatusCode: http.StatusInternalServerError, Message: "postgres is not configured", Type: "server_error", Code: "postgres_disabled"}
	}
	if strings.TrimSpace(params.RawText) == "" {
		return Result{}, &Error{StatusCode: http.StatusBadRequest, Message: "raw_text is required", Type: "invalid_request_error", Code: "missing_raw_text", Param: "raw_text"}
	}

	user, session, assistantMessage, scenarioPlan, contextGoal, contextPeople, contextTopics, err := s.resolveContext(ctx, params)
	if err != nil {
		return Result{}, err
	}

	goal := firstNonEmpty(params.Goal, contextGoal)
	people := mergeStrings(params.People, contextPeople)
	topics := mergeStrings(params.Topics, contextTopics)
	predictedOutcome := predictedOutcomeFromPlan(scenarioPlan)
	idempotencyKey, requestHash := outcomeIdentity(user.ID, params)
	if strings.TrimSpace(params.IdempotencyKey) != "" {
		idempotencyKey = strings.TrimSpace(params.IdempotencyKey)
	}
	outcomes := db.NewInteractionOutcomeRepository(s.pool)
	outcome, existingErr := outcomes.GetByIdempotencyKey(ctx, user.ID, idempotencyKey)
	if existingErr == nil && outcome.RequestHash != requestHash {
		return Result{}, &Error{StatusCode: http.StatusConflict, Message: "idempotency key was already used for a different outcome", Type: "invalid_request_error", Code: "idempotency_conflict", Param: "idempotency_key"}
	}
	if existingErr != nil && !errors.Is(existingErr, pgx.ErrNoRows) {
		return Result{}, existingErr
	}

	var analysis Analysis
	if existingErr == nil {
		analysis = analysisFromMetadata(outcome.Metadata)
		if analysis.Summary == "" {
			return Result{}, fmt.Errorf("stored outcome %s has no recoverable analysis", outcome.ID)
		}
	} else {
		analysis, err = s.analyze(ctx, params.RawText, goal, predictedOutcome, people, topics, scenarioPlan)
		if err != nil {
			return Result{}, err
		}
		if err := s.inject("analysis_completed"); err != nil {
			return Result{}, err
		}
	}
	people = mergeStrings(people, analysis.People)
	topics = mergeStrings(topics, analysis.Topics)

	if existingErr != nil {
		personID := resolveSinglePersonID(ctx, s.pool, user.ID, people)
		topicID := resolveSingleTopicID(ctx, s.pool, user.ID, topics)
		metadata := mergeMetadata(params.Metadata, map[string]any{
			"assistant_message_id": assistantMessage.ID,
			"analysis":             analysis,
		})
		if scenarioPlan != nil {
			metadata["scenario_plan"] = scenarioPlan
		}
		tx, txErr := s.pool.Begin(ctx)
		if txErr != nil {
			return Result{}, txErr
		}
		outcome, err = db.NewInteractionOutcomeRepositoryWithDB(tx).Create(ctx, db.CreateInteractionOutcomeParams{
			UserID: user.ID, SessionID: session.ID, MessageID: assistantMessage.ID, PersonID: personID, TopicID: topicID,
			Goal: goal, PredictedOutcome: predictedOutcome, ActualOutcome: analysis.Summary, SuccessScore: analysis.SuccessScore,
			PredictionError: analysis.PredictionError, Metadata: encodeMetadata(metadata), IdempotencyKey: idempotencyKey, RequestHash: requestHash,
		})
		if err != nil {
			_ = tx.Rollback(ctx)
			return Result{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return Result{}, err
		}
		if err = s.inject("outcome_inserted"); err != nil {
			_ = outcomes.UpdateProcessing(ctx, outcome.ID, "failed", "outcome_inserted", err.Error(), "")
			return Result{}, err
		}
		if outcome.RequestHash != requestHash {
			return Result{}, &Error{StatusCode: http.StatusConflict, Message: "idempotency key was already used for a different outcome", Type: "invalid_request_error", Code: "idempotency_conflict", Param: "idempotency_key"}
		}
	}

	memoryMetadata := mergeMetadata(params.Metadata, map[string]any{
		"assistant_message_id": assistantMessage.ID,
		"prediction_error":     analysis.PredictionError,
		"predicted_outcome":    predictedOutcome,
	})
	if scenarioPlan != nil {
		memoryMetadata["scenario_plan"] = scenarioPlan
	}
	memory, err := memsvc.NewServiceWithFailureInjector(s.cfg, s.pool, s.llm, s.fail).Ingest(ctx, memsvc.IngestParams{
		RawText:         params.RawText,
		Summary:         analysis.Summary,
		MemoryType:      "outcome",
		Source:          "interaction.outcome",
		SourceMessageID: assistantMessage.ID,
		People:          people,
		Topics:          topics,
		Importance:      floatPointer(analysis.Importance),
		Utility:         floatPointer(analysis.Utility),
		BeliefImpact:    floatPointer(analysis.BeliefImpact),
		Confidence:      floatPointer(analysis.Confidence),
		Metadata:        memoryMetadata,
		IdempotencyKey:  "outcome:" + outcome.ID,
		RequestUser:     user.ExternalID,
		Meta: memsvc.RequestMetadata{
			SessionID:      session.ExternalID,
			UserExternalID: user.ExternalID,
			UserName:       user.DisplayName,
			UserEmail:      user.Email,
			SessionTitle:   session.Title,
		},
	})
	if err != nil {
		_ = outcomes.UpdateProcessing(ctx, outcome.ID, "failed", "memory", err.Error(), "")
		return Result{}, err
	}

	if err := outcomes.UpdateProcessing(ctx, outcome.ID, "pending", "", "", memory.ID); err != nil {
		return Result{}, err
	}
	graphEdges, err := s.createGraphEdges(ctx, user.ID, analysis.GraphEdges, memory.ID, outcome.ID)
	if err != nil {
		_ = outcomes.UpdateProcessing(ctx, outcome.ID, "failed", "graph_edges", err.Error(), memory.ID)
		return Result{}, err
	}

	personID := resolveSinglePersonID(ctx, s.pool, user.ID, people)
	topicID := resolveSingleTopicID(ctx, s.pool, user.ID, topics)
	if err := outcomes.Complete(ctx, outcome.ID, memory.ID, personID, topicID, len(graphEdges)); err != nil {
		return Result{}, err
	}
	outcome, err = outcomes.GetByID(ctx, outcome.ID)
	if err != nil {
		return Result{}, err
	}

	return Result{
		Outcome:          outcome,
		Memory:           memory,
		Analysis:         analysis,
		GraphEdges:       graphEdges,
		ScenarioPlan:     scenarioPlan,
		AssistantMessage: assistantMessage,
		Session:          session,
	}, nil
}

func (s *Service) analyze(ctx context.Context, rawText, goal, predictedOutcome string, people, topics []string, plan *scenarios.Plan) (Analysis, error) {
	response, err := s.llm.Generate(ctx, llm.GenerateRequest{
		Model: s.cfg.OpenAI.ChatModel,
		Messages: []llm.Message{{
			Role:    "system",
			Content: prompts.OutcomeAnalysisSystemPrompt(),
		}, {
			Role:    "user",
			Content: prompts.BuildOutcomeAnalysisUserPrompt(rawText, goal, predictedOutcome, people, topics, summarizePlan(plan)),
		}},
	})
	if err != nil {
		return Analysis{}, err
	}

	analysis, parseErr := parseAnalysis(response.OutputText)
	if parseErr == nil {
		return analysis, nil
	}

	repairResponse, err := s.llm.Generate(ctx, llm.GenerateRequest{
		Model: s.cfg.OpenAI.ChatModel,
		Messages: []llm.Message{{
			Role:    "system",
			Content: prompts.OutcomeAnalysisSystemPrompt(),
		}, {
			Role:    "user",
			Content: prompts.BuildOutcomeAnalysisRepairPrompt(response.OutputText),
		}},
	})
	if err != nil {
		return Analysis{}, parseErr
	}

	return parseAnalysis(repairResponse.OutputText)
}

func parseAnalysis(raw string) (Analysis, error) {
	var analysis Analysis
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &analysis); err != nil {
		return Analysis{}, err
	}
	analysis.Summary = strings.TrimSpace(analysis.Summary)
	analysis.PredictionError = strings.TrimSpace(analysis.PredictionError)
	analysis.People = uniqueStrings(analysis.People)
	analysis.Topics = uniqueStrings(analysis.Topics)
	analysis.SuccessScore = clampUnit(analysis.SuccessScore)
	analysis.Importance = clampUnit(analysis.Importance)
	analysis.Utility = clampUnit(analysis.Utility)
	analysis.BeliefImpact = clampUnit(analysis.BeliefImpact)
	analysis.Confidence = clampUnit(analysis.Confidence)
	filteredEdges := make([]GraphEdgeInput, 0, len(analysis.GraphEdges))
	for _, edge := range analysis.GraphEdges {
		edge.SourceKind = strings.TrimSpace(edge.SourceKind)
		edge.SourceName = strings.TrimSpace(edge.SourceName)
		edge.TargetKind = strings.TrimSpace(edge.TargetKind)
		edge.TargetName = strings.TrimSpace(edge.TargetName)
		edge.Relationship = strings.TrimSpace(edge.Relationship)
		edge.Confidence = clampUnit(edge.Confidence)
		if edge.SourceKind == "" || edge.SourceName == "" || edge.TargetKind == "" || edge.TargetName == "" || edge.Relationship == "" {
			continue
		}
		filteredEdges = append(filteredEdges, edge)
	}
	analysis.GraphEdges = filteredEdges
	if analysis.Summary == "" {
		return Analysis{}, &Error{StatusCode: http.StatusBadGateway, Message: "outcome analysis did not produce a summary", Type: "server_error", Code: "outcome_analysis_invalid"}
	}

	return analysis, nil
}

func outcomeIdentity(userID string, params CreateOutcomeParams) (string, string) {
	payload := struct {
		UserID             string         `json:"user_id"`
		SessionID          string         `json:"session_id"`
		AssistantMessageID string         `json:"assistant_message_id"`
		RawText            string         `json:"raw_text"`
		Goal               string         `json:"goal"`
		People             []string       `json:"people"`
		Topics             []string       `json:"topics"`
		Metadata           map[string]any `json:"metadata,omitempty"`
	}{
		UserID: userID, SessionID: strings.TrimSpace(params.SessionID), AssistantMessageID: strings.TrimSpace(params.AssistantMessageID),
		RawText: strings.TrimSpace(params.RawText), Goal: strings.TrimSpace(params.Goal),
		People: uniqueStrings(params.People), Topics: uniqueStrings(params.Topics), Metadata: params.Metadata,
	}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	hash := fmt.Sprintf("%x", sum[:])
	return "derived:" + hash, hash
}

func analysisFromMetadata(raw json.RawMessage) Analysis {
	var payload struct {
		Analysis Analysis `json:"analysis"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Analysis{}
	}
	return payload.Analysis
}

func (s *Service) resolveContext(ctx context.Context, params CreateOutcomeParams) (models.User, models.Session, models.Message, *scenarios.Plan, string, []string, []string, error) {
	users := db.NewUserRepository(s.pool)
	sessions := db.NewSessionRepository(s.pool)
	messages := db.NewMessageRepository(s.pool)

	var expectedUser models.User
	var err error
	if strings.TrimSpace(params.UserExternalID) != "" {
		expectedUser, err = s.resolveUser(ctx, params.UserExternalID)
		if err != nil {
			return models.User{}, models.Session{}, models.Message{}, nil, "", nil, nil, err
		}
	}

	var assistantMessage models.Message
	var session models.Session
	var user models.User

	if strings.TrimSpace(params.AssistantMessageID) != "" {
		assistantMessage, err = messages.GetByID(ctx, params.AssistantMessageID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return models.User{}, models.Session{}, models.Message{}, nil, "", nil, nil, &Error{StatusCode: http.StatusNotFound, Message: "assistant message not found", Type: "invalid_request_error", Code: "assistant_message_not_found", Param: "assistant_message_id"}
			}
			return models.User{}, models.Session{}, models.Message{}, nil, "", nil, nil, err
		}
		user, err = users.GetByID(ctx, assistantMessage.UserID)
		if err != nil {
			return models.User{}, models.Session{}, models.Message{}, nil, "", nil, nil, err
		}
		if strings.TrimSpace(expectedUser.ID) != "" && user.ID != expectedUser.ID {
			return models.User{}, models.Session{}, models.Message{}, nil, "", nil, nil, &Error{StatusCode: http.StatusNotFound, Message: "assistant message not found", Type: "invalid_request_error", Code: "assistant_message_not_found", Param: "assistant_message_id"}
		}
		if strings.TrimSpace(assistantMessage.SessionID) != "" {
			session, err = sessions.GetByID(ctx, assistantMessage.SessionID)
			if err != nil {
				return models.User{}, models.Session{}, models.Message{}, nil, "", nil, nil, err
			}
		}
	}

	if strings.TrimSpace(session.ID) == "" && strings.TrimSpace(params.SessionID) != "" {
		session, err = sessions.GetByExternalID(ctx, strings.TrimSpace(params.SessionID))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return models.User{}, models.Session{}, models.Message{}, nil, "", nil, nil, &Error{StatusCode: http.StatusNotFound, Message: "session not found", Type: "invalid_request_error", Code: "session_not_found", Param: "session_id"}
			}
			return models.User{}, models.Session{}, models.Message{}, nil, "", nil, nil, err
		}
		user, err = users.GetByID(ctx, session.UserID)
		if err != nil {
			return models.User{}, models.Session{}, models.Message{}, nil, "", nil, nil, err
		}
		if strings.TrimSpace(expectedUser.ID) != "" && user.ID != expectedUser.ID {
			return models.User{}, models.Session{}, models.Message{}, nil, "", nil, nil, &Error{StatusCode: http.StatusNotFound, Message: "session not found", Type: "invalid_request_error", Code: "session_not_found", Param: "session_id"}
		}
	}

	if strings.TrimSpace(user.ID) == "" {
		if strings.TrimSpace(expectedUser.ID) != "" {
			user = expectedUser
		} else {
			user, err = s.resolveUser(ctx, params.UserExternalID)
			if err != nil {
				return models.User{}, models.Session{}, models.Message{}, nil, "", nil, nil, err
			}
		}
	}

	if strings.TrimSpace(assistantMessage.ID) == "" && strings.TrimSpace(session.ID) != "" {
		listed, err := messages.ListBySession(ctx, session.ID, 100)
		if err != nil {
			return models.User{}, models.Session{}, models.Message{}, nil, "", nil, nil, err
		}
		assistantMessage = latestAssistantWithScenarioPlan(listed)
	}

	plan, goal, people, topics := parseMessageContext(assistantMessage)
	return user, session, assistantMessage, plan, goal, people, topics, nil
}

func (s *Service) createGraphEdges(ctx context.Context, userID string, inputs []GraphEdgeInput, memoryID, outcomeID string) ([]models.GraphEdge, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	repo := db.NewGraphEdgeRepository(s.pool)
	edges := make([]models.GraphEdge, 0, len(inputs))
	for _, input := range inputs {
		edge, err := repo.Create(ctx, db.CreateGraphEdgeParams{
			UserID:            userID,
			SourceKind:        input.SourceKind,
			SourceName:        input.SourceName,
			TargetKind:        input.TargetKind,
			TargetName:        input.TargetName,
			Relationship:      input.Relationship,
			Confidence:        input.Confidence,
			EvidenceMemoryIDs: []string{memoryID},
			Metadata:          encodeMetadata(map[string]any{"source": "interaction.outcome", "outcome_id": outcomeID}),
		})
		if err != nil {
			return nil, err
		}
		edges = append(edges, edge)
		if err := s.inject(fmt.Sprintf("graph_edge_%d_created", len(edges))); err != nil {
			return nil, err
		}
	}

	return edges, nil
}

func parseMessageContext(message models.Message) (*scenarios.Plan, string, []string, []string) {
	if len(strings.TrimSpace(string(message.Metadata))) == 0 {
		return nil, "", nil, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(message.Metadata, &payload); err != nil {
		return nil, "", nil, nil
	}
	var plan *scenarios.Plan
	if rawPlan, ok := payload["scenario_plan"]; ok {
		encoded, err := json.Marshal(rawPlan)
		if err == nil {
			var parsed scenarios.Plan
			if err := json.Unmarshal(encoded, &parsed); err == nil {
				plan = &parsed
			}
		}
	}
	var goal string
	var people []string
	var topics []string
	if rawPacket, ok := payload["context_packet"].(map[string]any); ok {
		goal = stringFromMap(rawPacket, "goal")
		people = stringSliceFromMap(rawPacket, "people")
		topics = stringSliceFromMap(rawPacket, "topics")
	}

	return plan, goal, people, topics
}

func latestAssistantWithScenarioPlan(messages []models.Message) models.Message {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Role != "assistant" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(message.Metadata, &payload); err != nil {
			continue
		}
		if _, ok := payload["scenario_plan"]; ok {
			return message
		}
	}
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "assistant" {
			return messages[index]
		}
	}
	return models.Message{}
}

func predictedOutcomeFromPlan(plan *scenarios.Plan) string {
	if plan == nil {
		return ""
	}
	for _, strategy := range plan.Strategies {
		if strategy.ID == plan.RecommendedStrategyID {
			return strings.TrimSpace(strategy.PredictedResponse)
		}
	}
	if len(plan.Strategies) == 0 {
		return ""
	}
	return strings.TrimSpace(plan.Strategies[0].PredictedResponse)
}

func summarizePlan(plan *scenarios.Plan) string {
	if plan == nil {
		return ""
	}
	lines := []string{"Scenario plan:"}
	for _, strategy := range plan.Strategies {
		lines = append(lines, fmt.Sprintf("- %s: predicted_response=%s | likelihood=%.2f", strategy.Label, strings.TrimSpace(strategy.PredictedResponse), strategy.LikelihoodOfSuccess))
	}

	return strings.Join(lines, "\n")
}

func resolveSinglePersonID(ctx context.Context, pool *pgxpool.Pool, userID string, people []string) string {
	if len(people) != 1 {
		return ""
	}
	person, err := db.NewPersonRepository(pool).GetByName(ctx, userID, people[0])
	if err != nil {
		return ""
	}
	return person.ID
}

func resolveSingleTopicID(ctx context.Context, pool *pgxpool.Pool, userID string, topics []string) string {
	if len(topics) != 1 {
		return ""
	}
	topic, err := db.NewTopicRepository(pool).GetByName(ctx, userID, topics[0])
	if err != nil {
		return ""
	}
	return topic.ID
}

func mergeMetadata(base map[string]any, updates map[string]any) map[string]any {
	merged := make(map[string]any)
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range updates {
		if value == nil {
			continue
		}
		merged[key] = value
	}
	return merged
}

func encodeMetadata(values map[string]any) json.RawMessage {
	encoded, err := json.Marshal(values)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func (s *Service) resolveUser(ctx context.Context, externalID string) (models.User, error) {
	resolvedExternalID := strings.TrimSpace(externalID)
	if resolvedExternalID == "" {
		resolvedExternalID = s.cfg.Dev.UserExternalID
	}
	resolvedName := s.cfg.Dev.UserName
	resolvedEmail := s.cfg.Dev.UserEmail
	if resolvedExternalID != s.cfg.Dev.UserExternalID {
		resolvedName = resolvedExternalID
		resolvedEmail = ""
	}

	return db.NewUserRepository(s.pool).Ensure(ctx, db.EnsureUserParams{ExternalID: resolvedExternalID, Email: resolvedEmail, DisplayName: resolvedName})
}

func stringFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	raw, ok := values[key]
	if !ok {
		return ""
	}
	text, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func stringSliceFromMap(values map[string]any, key string) []string {
	if values == nil {
		return nil
	}
	raw, ok := values[key]
	if !ok {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			continue
		}
		result = append(result, text)
	}
	return uniqueStrings(result)
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

func mergeStrings(left, right []string) []string {
	merged := append(append([]string{}, left...), right...)
	return uniqueStrings(merged)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func floatPointer(value float64) *float64 {
	return &value
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

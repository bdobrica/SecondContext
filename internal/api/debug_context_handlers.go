package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	beliefsvc "github.com/bdobrica/SecondContext/internal/beliefs"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	modelsvc "github.com/bdobrica/SecondContext/internal/modeling"
	"github.com/bdobrica/SecondContext/internal/models"
	"github.com/bdobrica/SecondContext/internal/prompts"
	"github.com/bdobrica/SecondContext/internal/scenarios"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
)

type debugContextQuery struct {
	SessionID          string
	AssistantMessageID string
	Input              string
	Goal               string
	Instructions       string
	MemoryMode         string
	UserExternalID     string
	People             []string
	Topics             []string
	DisableMemory      bool
	CompareAnswers     bool
	Format             string
}

func (s *Server) handleGetDebugContext(w http.ResponseWriter, r *http.Request) {
	if s.dbPool == nil {
		s.writeAPIError(w, r, http.StatusInternalServerError, "postgres is not configured", "server_error", "postgres_disabled", "")
		return
	}

	query := parseDebugContextQuery(r)
	response, err := s.buildDebugContextResponse(r.Context(), query)
	if err != nil {
		s.writeDebugContextError(w, r, err)
		return
	}

	if wantsDebugContextHTML(r, query) {
		writeDebugContextHTML(w, response)
		return
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *Server) buildDebugContextResponse(ctx context.Context, query debugContextQuery) (debugContextResponse, error) {
	users := db.NewUserRepository(s.dbPool)
	sessions := db.NewSessionRepository(s.dbPool)
	messagesRepo := db.NewMessageRepository(s.dbPool)

	var session models.Session
	var user models.User
	var listedMessages []models.Message
	var assistantMessage models.Message
	var err error

	if strings.TrimSpace(query.AssistantMessageID) != "" {
		assistantMessage, err = messagesRepo.GetByID(ctx, query.AssistantMessageID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return debugContextResponse{}, newDebugContextError(http.StatusNotFound, "assistant message not found", "assistant_message_not_found", "assistant_message_id")
			}
			return debugContextResponse{}, err
		}
		user, err = users.GetByID(ctx, assistantMessage.UserID)
		if err != nil {
			return debugContextResponse{}, err
		}
		if strings.TrimSpace(assistantMessage.SessionID) != "" {
			session, err = sessions.GetByID(ctx, assistantMessage.SessionID)
			if err != nil {
				return debugContextResponse{}, err
			}
		}
	}

	if strings.TrimSpace(session.ID) == "" && strings.TrimSpace(query.SessionID) != "" {
		session, err = sessions.GetByExternalID(ctx, query.SessionID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return debugContextResponse{}, newDebugContextError(http.StatusNotFound, "session not found", "session_not_found", "session_id")
			}
			return debugContextResponse{}, err
		}
		user, err = users.GetByID(ctx, session.UserID)
		if err != nil {
			return debugContextResponse{}, err
		}
	}

	if strings.TrimSpace(session.ID) != "" {
		listedMessages, err = messagesRepo.ListBySession(ctx, session.ID, 100)
		if err != nil {
			return debugContextResponse{}, err
		}
	}
	if strings.TrimSpace(assistantMessage.ID) == "" {
		assistantMessage = latestAssistantMessage(listedMessages)
	}

	if strings.TrimSpace(user.ID) == "" {
		resolvedExternalID := s.defaultUserExternalID(ctx, query.UserExternalID)
		user, err = users.Ensure(ctx, db.EnsureUserParams{ExternalID: resolvedExternalID, Email: s.cfg.Dev.UserEmail, DisplayName: firstNonEmpty(resolvedExternalID, s.cfg.Dev.UserName)})
		if err != nil {
			return debugContextResponse{}, err
		}
	}

	storedPacket, storedPlan := parseStoredContextMetadata(assistantMessage)
	latestUserInput := firstNonEmpty(latestUserMessageText(listedMessages), query.Input)
	if latestUserInput == "" && storedPacket != nil {
		latestUserInput = strings.TrimSpace(storedPacket.Query)
	}
	input := firstNonEmpty(query.Input, latestUserInput)
	goal := firstNonEmpty(query.Goal, storedPacketGoal(storedPacket))
	people := uniqueStrings(append([]string{}, query.People...))
	if len(people) == 0 && storedPacket != nil {
		people = uniqueStrings(storedPacket.People)
	}
	topics := uniqueStrings(append([]string{}, query.Topics...))
	if len(topics) == 0 && storedPacket != nil {
		topics = uniqueStrings(storedPacket.Topics)
	}
	memoryMode := firstNonEmpty(query.MemoryMode, storedPacketMode(storedPacket))

	if strings.TrimSpace(input) == "" && strings.TrimSpace(goal) == "" && strings.TrimSpace(session.ID) == "" && strings.TrimSpace(assistantMessage.ID) == "" {
		return debugContextResponse{}, newDebugContextError(http.StatusBadRequest, "input, session_id, or assistant_message_id is required", "missing_context_seed", "input")
	}

	request, promptMessages, err := buildDebugCreateResponseRequest(query, user.ExternalID, session.ExternalID, input, goal, people, topics, memoryMode)
	if err != nil {
		return debugContextResponse{}, newDebugContextError(http.StatusBadRequest, err.Error(), "invalid_input", "input")
	}

	memoryDisabledPacket := buildBaseContextPacket(request, promptMessages, s.defaultUserExternalID(ctx))
	augmentedPacket := buildBaseContextPacket(request, promptMessages, s.defaultUserExternalID(ctx))
	if err := s.populateResponseContext(ctx, augmentedPacket); err != nil {
		return debugContextResponse{}, err
	}

	currentPacket := augmentedPacket
	if query.DisableMemory {
		currentPacket = memoryDisabledPacket
	}

	referencePacket := currentPacket
	if storedPacket != nil {
		referencePacket = storedPacket
	}
	peopleModels, err := s.loadPeopleModels(ctx, user.ID, extractContextPeople(referencePacket), extractContextTopics(referencePacket))
	if err != nil {
		return debugContextResponse{}, err
	}
	beliefsList, err := s.loadBeliefDebugViews(ctx, user.ExternalID, extractContextTopics(referencePacket))
	if err != nil {
		return debugContextResponse{}, err
	}
	latestTurnUpdates, err := s.loadLatestTurnUpdates(ctx, user.ID, session.ID, assistantMessage.ID)
	if err != nil {
		return debugContextResponse{}, err
	}

	response := debugContextResponse{
		Session: debugContextSessionResponse{
			ID:                 session.ID,
			ExternalID:         session.ExternalID,
			Title:              session.Title,
			UserID:             user.ID,
			AssistantMessageID: assistantMessage.ID,
			LatestUserInput:    latestUserInput,
		},
		Request: debugContextRequestResponse{
			Input:          input,
			Goal:           goal,
			Instructions:   request.Instructions,
			MemoryMode:     string(resolveResponseMode(memoryMode)),
			UserExternalID: user.ExternalID,
			People:         people,
			Topics:         topics,
			DisableMemory:  query.DisableMemory,
			CompareAnswers: query.CompareAnswers,
		},
		StoredContextPacket:  storedPacket,
		CurrentContextPacket: currentPacket,
		ScenarioPlan:         scenarioPlanMap(storedPlan),
		CurrentPromptPreview: buildDebugPromptPreview(currentPacket, request.Instructions),
		PeopleModels:         peopleModels,
		RelevantBeliefs:      beliefsList,
		LatestTurnUpdates:    latestTurnUpdates,
	}

	if query.CompareAnswers && len(promptMessages) > 0 {
		augmentedVariant, err := s.generateDebugVariant(ctx, request, promptMessages, augmentedPacket)
		if err != nil {
			return debugContextResponse{}, err
		}
		memoryDisabledVariant, err := s.generateDebugVariant(ctx, request, promptMessages, memoryDisabledPacket)
		if err != nil {
			return debugContextResponse{}, err
		}
		response.Comparison = &debugContextComparisonResponse{
			MemoryAugmented: augmentedVariant,
			MemoryDisabled:  memoryDisabledVariant,
		}
	}

	return response, nil
}

func (s *Server) generateDebugVariant(ctx context.Context, request createResponseRequest, messages []llm.Message, packet *prompts.ContextPacket) (debugContextVariantResponse, error) {
	promptPreview := buildDebugPromptPreview(packet, request.Instructions)
	upstreamModel := resolveUpstreamModel(s.cfg.OpenAI.ChatModel, request.Model)
	if isScenarioMode(packetModeOrDefault(packet)) {
		result, err := scenarios.NewService(s.cfg, s.llm).Generate(ctx, upstreamModel, packet, request.Instructions)
		if err != nil {
			return debugContextVariantResponse{}, err
		}
		return debugContextVariantResponse{ContextPacket: packet, PromptPreview: promptPreview, OutputText: result.OutputText, ScenarioPlan: scenarioPlanMap(&result.Plan)}, nil
	}

	llmMessages := messages
	if strings.TrimSpace(promptPreview) != "" {
		llmMessages = prependSystemMessage(messages, promptPreview)
	}
	response, err := s.llm.Generate(ctx, llm.GenerateRequest{Model: upstreamModel, Messages: llmMessages})
	if err != nil {
		return debugContextVariantResponse{}, err
	}

	return debugContextVariantResponse{ContextPacket: packet, PromptPreview: promptPreview, OutputText: response.OutputText}, nil
}

func (s *Server) loadPeopleModels(ctx context.Context, userID string, people, topics []string) ([]personDebugResponse, error) {
	if strings.TrimSpace(userID) == "" || len(people) == 0 {
		return nil, nil
	}

	peopleRepo := db.NewPersonRepository(s.dbPool)
	service := modelsvc.NewService(s.cfg, s.dbPool, s.llm)
	topicFilter := make(map[string]struct{}, len(topics))
	for _, topic := range uniqueStrings(topics) {
		topicFilter[strings.ToLower(strings.TrimSpace(topic))] = struct{}{}
	}

	responses := make([]personDebugResponse, 0, len(people))
	for _, personName := range uniqueStrings(people) {
		person, err := peopleRepo.GetByName(ctx, userID, personName)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return nil, err
		}
		profile, err := service.GetPersonProfile(ctx, person.ID, "", "")
		if err != nil {
			return nil, err
		}
		response := toPersonDebugResponse(profile)
		if len(topicFilter) > 0 {
			filtered := make([]personTopicModelResponse, 0, len(response.Models))
			for _, model := range response.Models {
				if _, ok := topicFilter[strings.ToLower(strings.TrimSpace(model.TopicName))]; !ok {
					continue
				}
				filtered = append(filtered, model)
			}
			response.Models = filtered
		}
		if len(response.Models) == 0 {
			continue
		}
		responses = append(responses, response)
	}

	return responses, nil
}

func (s *Server) loadBeliefDebugViews(ctx context.Context, userExternalID string, topics []string) ([]beliefDebugResponse, error) {
	if strings.TrimSpace(userExternalID) == "" {
		return nil, nil
	}

	service := beliefsvc.NewService(s.cfg, s.dbPool, s.llm)
	if len(topics) == 0 {
		views, err := service.ListBeliefs(ctx, beliefsvc.ListBeliefsParams{UserExternalID: userExternalID, Limit: 10})
		if err != nil {
			return nil, err
		}
		return mapBeliefViews(views), nil
	}

	collected := make([]beliefDebugResponse, 0)
	seen := make(map[string]struct{})
	for _, topic := range uniqueStrings(topics) {
		views, err := service.ListBeliefs(ctx, beliefsvc.ListBeliefsParams{UserExternalID: userExternalID, TopicName: topic, Limit: 10})
		if err != nil {
			return nil, err
		}
		for _, item := range mapBeliefViews(views) {
			if _, ok := seen[item.ID]; ok {
				continue
			}
			seen[item.ID] = struct{}{}
			collected = append(collected, item)
		}
	}

	return collected, nil
}

func (s *Server) loadLatestTurnUpdates(ctx context.Context, userID, sessionID, assistantMessageID string) (debugContextLatestTurnResponse, error) {
	response := debugContextLatestTurnResponse{AssistantMessageID: assistantMessageID}
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(sessionID) == "" {
		return response, nil
	}

	memoryItems, err := db.NewMemoryRepository(s.dbPool).ListBySession(ctx, sessionID, 50)
	if err != nil {
		return response, err
	}
	filteredMemories := make([]models.MemoryItem, 0)
	memoryIDs := make([]string, 0)
	for _, item := range memoryItems {
		if strings.TrimSpace(assistantMessageID) != "" && item.SourceMessageID != assistantMessageID {
			continue
		}
		filteredMemories = append(filteredMemories, item)
		memoryIDs = append(memoryIDs, item.ID)
		response.Memories = append(response.Memories, toMemoryResponse(item))
	}

	outcomes, err := db.NewInteractionOutcomeRepository(s.dbPool).ListBySession(ctx, sessionID, 20)
	if err != nil {
		return response, err
	}
	for _, outcome := range outcomes {
		if strings.TrimSpace(assistantMessageID) != "" && outcome.MessageID != assistantMessageID {
			continue
		}
		mapped := mapInteractionOutcomeResponse(outcome)
		response.Outcome = &mapped
		break
	}

	if len(memoryIDs) == 0 {
		return response, nil
	}
	edges, err := db.NewGraphEdgeRepository(s.dbPool).ListByUser(ctx, userID, 100)
	if err != nil {
		return response, err
	}
	for _, edge := range edges {
		if !hasStringIntersection(edge.EvidenceMemoryIDs, memoryIDs) {
			continue
		}
		response.GraphEdges = append(response.GraphEdges, mapGraphEdgeResponse(edge))
	}

	_ = filteredMemories
	return response, nil
}

func mapBeliefViews(views []beliefsvc.BeliefView) []beliefDebugResponse {
	response := make([]beliefDebugResponse, 0, len(views))
	for _, view := range views {
		response = append(response, beliefDebugResponse{
			ID:                view.Belief.ID,
			UserID:            view.Belief.UserID,
			TopicID:           view.Topic.ID,
			TopicName:         view.Topic.Name,
			TopicAliases:      view.Topic.Aliases,
			Claim:             view.Belief.Claim,
			Stance:            view.Belief.Stance,
			Confidence:        view.Belief.Confidence,
			EvidenceMemoryIDs: view.Belief.EvidenceMemoryIDs,
			HasContradiction:  view.HasContradiction,
			Summary:           view.Summary,
			Metadata:          decodeJSONMap(view.Belief.Metadata),
			LastUpdatedAt:     view.Belief.LastUpdatedAt.UTC().Format(time.RFC3339),
			CreatedAt:         view.Belief.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt:         view.Belief.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}

	return response
}

func mapInteractionOutcomeResponse(outcome models.InteractionOutcome) interactionOutcomeResponse {
	return interactionOutcomeResponse{
		ID:               outcome.ID,
		UserID:           outcome.UserID,
		SessionID:        outcome.SessionID,
		MessageID:        outcome.MessageID,
		PersonID:         outcome.PersonID,
		TopicID:          outcome.TopicID,
		Goal:             outcome.Goal,
		PredictedOutcome: outcome.PredictedOutcome,
		ActualOutcome:    outcome.ActualOutcome,
		SuccessScore:     outcome.SuccessScore,
		PredictionError:  outcome.PredictionError,
		Metadata:         decodeJSONMap(outcome.Metadata),
		CreatedAt:        outcome.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:        outcome.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func mapGraphEdgeResponse(edge models.GraphEdge) graphEdgeResponse {
	return graphEdgeResponse{
		ID:                edge.ID,
		UserID:            edge.UserID,
		SourceKind:        edge.SourceKind,
		SourceName:        edge.SourceName,
		TargetKind:        edge.TargetKind,
		TargetName:        edge.TargetName,
		Relationship:      edge.Relationship,
		Confidence:        edge.Confidence,
		EvidenceMemoryIDs: edge.EvidenceMemoryIDs,
		Metadata:          decodeJSONMap(edge.Metadata),
		CreatedAt:         edge.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:         edge.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func buildDebugCreateResponseRequest(query debugContextQuery, userExternalID, sessionExternalID, input, goal string, people, topics []string, memoryMode string) (createResponseRequest, []llm.Message, error) {
	inputValue := strings.TrimSpace(input)
	if inputValue == "" {
		inputValue = strings.TrimSpace(goal)
	}
	inputBytes, err := json.Marshal(inputValue)
	if err != nil {
		return createResponseRequest{}, nil, err
	}
	request := createResponseRequest{
		Model:        defaultPublicModel,
		Input:        inputBytes,
		Instructions: strings.TrimSpace(query.Instructions),
		Metadata: map[string]any{
			"goal":             strings.TrimSpace(goal),
			"people":           uniqueStrings(people),
			"topics":           uniqueStrings(topics),
			"memory_mode":      strings.TrimSpace(memoryMode),
			"user_external_id": strings.TrimSpace(userExternalID),
			"session_id":       strings.TrimSpace(sessionExternalID),
		},
		User: strings.TrimSpace(userExternalID),
	}
	messages, err := buildPromptMessages("", request.Input)
	if err != nil {
		return createResponseRequest{}, nil, err
	}

	return request, messages, nil
}

func parseDebugContextQuery(r *http.Request) debugContextQuery {
	values := r.URL.Query()
	return debugContextQuery{
		SessionID:          strings.TrimSpace(values.Get("session_id")),
		AssistantMessageID: strings.TrimSpace(values.Get("assistant_message_id")),
		Input:              strings.TrimSpace(values.Get("input")),
		Goal:               strings.TrimSpace(values.Get("goal")),
		Instructions:       strings.TrimSpace(values.Get("instructions")),
		MemoryMode:         strings.TrimSpace(values.Get("memory_mode")),
		UserExternalID:     strings.TrimSpace(values.Get("user_external_id")),
		People:             parseQueryCSV(values["people"]),
		Topics:             parseQueryCSV(values["topics"]),
		DisableMemory:      parseBoolQuery(values.Get("disable_memory")),
		CompareAnswers:     parseBoolQuery(values.Get("compare")) || parseBoolQuery(values.Get("compare_answers")),
		Format:             strings.TrimSpace(values.Get("format")),
	}
}

func parseStoredContextMetadata(message models.Message) (*prompts.ContextPacket, *scenarios.Plan) {
	if strings.TrimSpace(message.ID) == "" || len(strings.TrimSpace(string(message.Metadata))) == 0 {
		return nil, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(message.Metadata, &payload); err != nil {
		return nil, nil
	}

	var packet *prompts.ContextPacket
	if rawPacket, ok := payload["context_packet"]; ok {
		encoded, err := json.Marshal(rawPacket)
		if err == nil {
			var parsed prompts.ContextPacket
			if err := json.Unmarshal(encoded, &parsed); err == nil {
				packet = &parsed
			}
		}
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

	return packet, plan
}

func latestAssistantMessage(messages []models.Message) models.Message {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "assistant" {
			return messages[index]
		}
	}

	return models.Message{}
}

func latestUserMessageText(messages []models.Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "user" {
			return strings.TrimSpace(messages[index].Content)
		}
	}

	return ""
}

func extractContextPeople(packet *prompts.ContextPacket) []string {
	if packet == nil {
		return nil
	}
	people := append([]string{}, packet.People...)
	for _, memory := range packet.MemoryContext {
		people = append(people, memory.People...)
	}

	return uniqueStrings(people)
}

func extractContextTopics(packet *prompts.ContextPacket) []string {
	if packet == nil {
		return nil
	}
	topics := append([]string{}, packet.Topics...)
	for _, memory := range packet.MemoryContext {
		topics = append(topics, memory.Topics...)
	}

	return uniqueStrings(topics)
}

func scenarioPlanMap(plan *scenarios.Plan) map[string]any {
	if plan == nil {
		return nil
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return nil
	}
	result := make(map[string]any)
	_ = json.Unmarshal(encoded, &result)
	return result
}

func buildDebugPromptPreview(packet *prompts.ContextPacket, userInstructions string) string {
	if isScenarioMode(packetModeOrDefault(packet)) {
		return prompts.ScenarioGenerationSystemPrompt(packet, userInstructions)
	}
	return prompts.BuildResponseSystemPrompt(packet, userInstructions)
}

func packetModeOrDefault(packet *prompts.ContextPacket) prompts.ResponseMode {
	if packet == nil || packet.Mode == "" {
		return prompts.ResponseModeNormalAnswer
	}
	return packet.Mode
}

func storedPacketGoal(packet *prompts.ContextPacket) string {
	if packet == nil {
		return ""
	}
	return strings.TrimSpace(packet.Goal)
}

func storedPacketMode(packet *prompts.ContextPacket) string {
	if packet == nil {
		return ""
	}
	return string(packet.Mode)
}

func parseQueryCSV(values []string) []string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			items = append(items, item)
		}
	}
	return uniqueStrings(items)
}

func parseBoolQuery(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func hasStringIntersection(left, right []string) bool {
	lookup := make(map[string]struct{}, len(left))
	for _, value := range left {
		lookup[strings.TrimSpace(value)] = struct{}{}
	}
	for _, value := range right {
		if _, ok := lookup[strings.TrimSpace(value)]; ok {
			return true
		}
	}
	return false
}

type debugContextError struct {
	statusCode int
	message    string
	code       string
	param      string
}

func (e *debugContextError) Error() string { return e.message }

func newDebugContextError(statusCode int, message, code, param string) error {
	return &debugContextError{statusCode: statusCode, message: message, code: code, param: param}
}

func (s *Server) writeDebugContextError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Error("debug context request failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	var debugErr *debugContextError
	if errors.As(err, &debugErr) {
		s.writeAPIError(w, r, debugErr.statusCode, debugErr.message, "invalid_request_error", debugErr.code, debugErr.param)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		s.writeAPIError(w, r, http.StatusNotFound, "resource not found", "invalid_request_error", "not_found", "")
		return
	}
	s.writeAPIError(w, r, http.StatusInternalServerError, "request failed", "server_error", "request_failed", "")
}

func wantsDebugContextHTML(r *http.Request, query debugContextQuery) bool {
	if strings.EqualFold(query.Format, "html") {
		return true
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html")
}

func writeDebugContextHTML(w http.ResponseWriter, response debugContextResponse) {
	view := struct {
		SessionTitle       string
		RequestJSON        string
		StoredContextJSON  string
		CurrentContextJSON string
		ScenarioPlanJSON   string
		PeopleModelsJSON   string
		BeliefsJSON        string
		UpdatesJSON        string
		ComparisonJSON     string
		PromptPreview      string
		FullJSON           string
	}{
		SessionTitle:       firstNonEmpty(response.Session.Title, response.Session.ExternalID, "Debug Context"),
		RequestJSON:        prettyJSON(response.Request),
		StoredContextJSON:  prettyJSON(response.StoredContextPacket),
		CurrentContextJSON: prettyJSON(response.CurrentContextPacket),
		ScenarioPlanJSON:   prettyJSON(response.ScenarioPlan),
		PeopleModelsJSON:   prettyJSON(response.PeopleModels),
		BeliefsJSON:        prettyJSON(response.RelevantBeliefs),
		UpdatesJSON:        prettyJSON(response.LatestTurnUpdates),
		ComparisonJSON:     prettyJSON(response.Comparison),
		PromptPreview:      response.CurrentPromptPreview,
		FullJSON:           prettyJSON(response),
	}

	const page = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>{{.SessionTitle}}</title>
  <style>
    body { font-family: sans-serif; margin: 2rem; line-height: 1.4; }
    pre { background: #f5f5f5; padding: 1rem; overflow-x: auto; border-radius: 6px; }
    h1, h2 { margin-bottom: 0.4rem; }
  </style>
</head>
<body>
  <h1>{{.SessionTitle}}</h1>
  <h2>Request</h2><pre>{{.RequestJSON}}</pre>
  <h2>Current Prompt Preview</h2><pre>{{.PromptPreview}}</pre>
  <h2>Stored Context Packet</h2><pre>{{.StoredContextJSON}}</pre>
  <h2>Current Context Packet</h2><pre>{{.CurrentContextJSON}}</pre>
  <h2>Scenario Plan</h2><pre>{{.ScenarioPlanJSON}}</pre>
  <h2>People Models</h2><pre>{{.PeopleModelsJSON}}</pre>
  <h2>Relevant Beliefs</h2><pre>{{.BeliefsJSON}}</pre>
  <h2>Latest Turn Updates</h2><pre>{{.UpdatesJSON}}</pre>
  <h2>Comparison</h2><pre>{{.ComparisonJSON}}</pre>
  <h2>Full JSON</h2><pre>{{.FullJSON}}</pre>
</body>
</html>`

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = template.Must(template.New("debug-context").Parse(page)).Execute(w, view)
}

func prettyJSON(value any) string {
	if value == nil {
		return "null"
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
}

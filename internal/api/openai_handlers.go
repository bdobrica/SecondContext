package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	"github.com/bdobrica/SecondContext/internal/models"
	"github.com/bdobrica/SecondContext/internal/prompts"
	"github.com/bdobrica/SecondContext/internal/scenarios"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
)

func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	createdAt := time.Now().Unix()
	data := []modelInfo{{
		ID:      defaultPublicModel,
		Object:  "model",
		Created: createdAt,
		OwnedBy: s.cfg.App.Name,
	}}

	if s.cfg.OpenAI.ChatModel != "" && s.cfg.OpenAI.ChatModel != defaultPublicModel {
		data = append(data, modelInfo{
			ID:      s.cfg.OpenAI.ChatModel,
			Object:  "model",
			Created: createdAt,
			OwnedBy: "openai-upstream",
		})
	}

	writeJSON(w, http.StatusOK, listModelsResponse{Object: "list", Data: data})
}

func (s *Server) handleCreateResponse(w http.ResponseWriter, r *http.Request) {
	var request createResponseRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.writeAPIError(w, r, http.StatusBadRequest, "invalid request body", "invalid_request_error", "invalid_json", "")
		return
	}

	if request.Stream {
		s.writeAPIError(w, r, http.StatusBadRequest, "streaming is not implemented yet", "invalid_request_error", "unsupported_feature", "stream")
		return
	}

	metadata, err := s.resolveRequestMetadata(r.Context(), request.Metadata, request.User)
	if err != nil {
		s.writeRequestScopeError(w, r, err)
		return
	}

	messages, err := buildPromptMessages("", request.Input)
	if err != nil {
		s.writeAPIError(w, r, http.StatusBadRequest, err.Error(), "invalid_request_error", "invalid_input", "input")
		return
	}

	contextPacket, err := s.buildResponseContext(r.Context(), request, metadata, messages)
	if err != nil {
		s.logger.Warn("build response context", "error", err, "request_id", middleware.GetReqID(r.Context()))
	}
	scenarioMode := contextPacket != nil && isScenarioMode(contextPacket.Mode)
	if !scenarioMode {
		systemPrompt := prompts.BuildResponseSystemPrompt(contextPacket, request.Instructions)
		if strings.TrimSpace(systemPrompt) != "" {
			messages = prependSystemMessage(messages, systemPrompt)
		}
	}

	responseModel := request.Model
	if responseModel == "" {
		responseModel = defaultPublicModel
	}
	upstreamModel := resolveUpstreamModel(s.cfg.OpenAI.ChatModel, responseModel)

	var session models.Session
	if s.dbPool != nil {
		session, err = s.persistInboundMessages(r, metadata, messages, upstreamModel)
		if err != nil {
			if s.writeRequestScopeError(w, r, err) {
				return
			}
			s.logger.Error("persist inbound messages", "error", err, "request_id", middleware.GetReqID(r.Context()))
			s.writeAPIError(w, r, http.StatusInternalServerError, "failed to persist inbound messages", "server_error", "persistence_failed", "")
			return
		}
	}

	var upstreamResponse llm.GenerateResponse
	var scenarioPlan *scenarios.Plan
	if scenarioMode {
		scenarioResult, err := scenarios.NewService(s.cfg, s.llm).Generate(r.Context(), upstreamModel, contextPacket, request.Instructions)
		if err != nil {
			s.logger.Error("generate scenario plan", "error", err, "request_id", middleware.GetReqID(r.Context()))
			s.writeAPIError(w, r, http.StatusInternalServerError, "failed to generate scenario plan", "server_error", "scenario_generation_failed", "")
			return
		}
		upstreamResponse = scenarioResult.LLMResponse
		scenarioPlan = &scenarioResult.Plan
	} else {
		upstreamResponse, err = s.llm.Generate(r.Context(), llm.GenerateRequest{
			Model:    upstreamModel,
			Messages: messages,
		})
		if err != nil {
			s.writeLLMError(w, r, err)
			return
		}
	}

	if s.dbPool != nil {
		if err := s.persistAssistantMessage(r, session, upstreamResponse, contextPacket, scenarioPlan, requestDisablesMemory(request)); err != nil {
			s.logger.Error("persist assistant response", "error", err, "request_id", middleware.GetReqID(r.Context()))
			s.writeAPIError(w, r, http.StatusInternalServerError, "failed to persist assistant response", "server_error", "persistence_failed", "")
			return
		}
	}

	responseID := upstreamResponse.ID
	if responseID == "" {
		responseID = newIdentifier("resp")
	}

	response := createResponseResult{
		ID:         responseID,
		Object:     "response",
		CreatedAt:  time.Now().Unix(),
		Status:     "completed",
		Model:      responseModel,
		OutputText: upstreamResponse.OutputText,
		Output: []responseOutputItem{{
			ID:   newIdentifier("msg"),
			Type: "message",
			Role: "assistant",
			Content: []responseContentBlock{{
				Type: "output_text",
				Text: upstreamResponse.OutputText,
			}},
		}},
		Usage: &responseUsage{
			InputTokens:  upstreamResponse.Usage.InputTokens,
			OutputTokens: upstreamResponse.Usage.OutputTokens,
			TotalTokens:  upstreamResponse.Usage.TotalTokens,
		},
		Metadata: map[string]any{"request_id": middleware.GetReqID(r.Context())},
	}

	if session.ExternalID != "" {
		response.Metadata["session_id"] = session.ExternalID
	}
	if contextPacket != nil {
		response.Metadata["context_packet"] = contextPacket
	}
	if requestDisablesMemory(request) {
		response.Metadata["disable_memory"] = true
	}
	if scenarioPlan != nil {
		response.Metadata["scenario_plan"] = scenarioPlan
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *Server) persistInboundMessages(r *http.Request, metadata requestMetadata, messages []llm.Message, upstreamModel string) (models.Session, error) {
	ctx := r.Context()
	users := db.NewUserRepository(s.dbPool)
	sessions := db.NewSessionRepository(s.dbPool)
	messageRepo := db.NewMessageRepository(s.dbPool)

	user, err := users.Ensure(ctx, db.EnsureUserParams{
		ExternalID:  metadata.UserExternalID,
		Email:       metadata.UserEmail,
		DisplayName: metadata.UserName,
	})
	if err != nil {
		return models.Session{}, err
	}

	session, err := s.ensureSession(ctx, sessions, user.ID, metadata, messages)
	if err != nil {
		return models.Session{}, err
	}

	requestID := middleware.GetReqID(ctx)
	for _, message := range messages {
		if message.Role != "user" {
			continue
		}

		if _, err := messageRepo.Create(ctx, db.CreateMessageParams{
			SessionID: session.ID,
			UserID:    user.ID,
			Role:      message.Role,
			Content:   message.Content,
			Model:     upstreamModel,
			RequestID: requestID,
		}); err != nil {
			return models.Session{}, err
		}
	}

	return session, nil
}

func (s *Server) ensureSession(ctx context.Context, repo *db.SessionRepository, userID string, metadata requestMetadata, messages []llm.Message) (models.Session, error) {
	if metadata.SessionID != "" {
		session, err := repo.GetByExternalID(ctx, metadata.SessionID)
		if err == nil {
			if session.UserID != userID {
				return models.Session{}, &requestScopeError{StatusCode: http.StatusNotFound, Message: "session not found", Type: "invalid_request_error", Code: "session_not_found", Param: "session_id"}
			}
			return session, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return models.Session{}, err
		}
	}

	sessionID := metadata.SessionID
	if sessionID == "" {
		sessionID = newIdentifier("sess")
	}

	title := metadata.SessionTitle
	if title == "" {
		title = deriveSessionTitle(messages)
	}

	return repo.Create(ctx, db.CreateSessionParams{
		UserID:     userID,
		ExternalID: sessionID,
		Title:      title,
		Metadata:   json.RawMessage(`{"source":"v1.responses"}`),
	})
}

func (s *Server) persistAssistantMessage(r *http.Request, session models.Session, response llm.GenerateResponse, contextPacket *prompts.ContextPacket, scenarioPlan *scenarios.Plan, disableMemory bool) error {
	if session.ID == "" {
		return nil
	}

	messageRepo := db.NewMessageRepository(s.dbPool)
	metadata := map[string]any{}
	if contextPacket != nil {
		metadata["context_packet"] = contextPacket
	}
	if disableMemory {
		metadata["disable_memory"] = true
	}
	if scenarioPlan != nil {
		metadata["scenario_plan"] = scenarioPlan
	}
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = messageRepo.Create(r.Context(), db.CreateMessageParams{
		SessionID: session.ID,
		UserID:    session.UserID,
		Role:      "assistant",
		Content:   response.OutputText,
		Model:     response.Model,
		RequestID: middleware.GetReqID(r.Context()),
		Metadata:  metadataBytes,
	})

	return err
}

func isScenarioMode(mode prompts.ResponseMode) bool {
	switch mode {
	case prompts.ResponseModeCommunicationAdvice, prompts.ResponseModeScenarioGeneration:
		return true
	default:
		return false
	}
}

func prependSystemMessage(messages []llm.Message, content string) []llm.Message {
	if strings.TrimSpace(content) == "" {
		return messages
	}

	result := make([]llm.Message, 0, len(messages)+1)
	result = append(result, llm.Message{Role: "system", Content: content})
	result = append(result, messages...)

	return result
}

func buildPromptMessages(instructions string, rawInput json.RawMessage) ([]llm.Message, error) {
	messages := make([]llm.Message, 0, 4)
	if text := strings.TrimSpace(instructions); text != "" {
		messages = append(messages, llm.Message{Role: "system", Content: text})
	}

	trimmed := strings.TrimSpace(string(rawInput))
	if trimmed == "" || trimmed == "null" {
		return nil, errors.New("input is required")
	}

	switch trimmed[0] {
	case '"':
		var text string
		if err := json.Unmarshal(rawInput, &text); err != nil {
			return nil, fmt.Errorf("decode input string: %w", err)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, errors.New("input must not be empty")
		}
		messages = append(messages, llm.Message{Role: "user", Content: text})
	case '[':
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(rawInput, &items); err != nil {
			return nil, fmt.Errorf("decode input array: %w", err)
		}
		for _, item := range items {
			role := parseRawString(item["role"])
			if role == "" {
				role = "user"
			}
			content := extractContentText(item["content"])
			if strings.TrimSpace(content) == "" {
				continue
			}
			messages = append(messages, llm.Message{Role: role, Content: content})
		}
	case '{':
		var item map[string]json.RawMessage
		if err := json.Unmarshal(rawInput, &item); err != nil {
			return nil, fmt.Errorf("decode input object: %w", err)
		}
		role := parseRawString(item["role"])
		if role == "" {
			role = "user"
		}
		content := extractContentText(item["content"])
		if strings.TrimSpace(content) == "" {
			return nil, errors.New("input content must not be empty")
		}
		messages = append(messages, llm.Message{Role: role, Content: content})
	default:
		return nil, errors.New("unsupported input format")
	}

	if len(messages) == 0 {
		return nil, errors.New("input produced no messages")
	}

	return messages, nil
}

func extractContentText(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}

	if trimmed[0] == '"' {
		return strings.TrimSpace(parseRawString(raw))
	}

	if trimmed[0] == '[' {
		var blocks []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return ""
		}

		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			text := parseRawString(block["text"])
			if strings.TrimSpace(text) != "" {
				parts = append(parts, strings.TrimSpace(text))
			}
		}

		return strings.Join(parts, "\n")
	}

	return ""
}

func parseRawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}

	return strings.TrimSpace(value)
}

func deriveSessionTitle(messages []llm.Message) string {
	for _, message := range messages {
		if message.Role != "user" {
			continue
		}

		trimmed := strings.TrimSpace(message.Content)
		if trimmed == "" {
			continue
		}

		if len(trimmed) > 80 {
			return trimmed[:80]
		}

		return trimmed
	}

	return "Untitled Session"
}

func parseRequestMetadata(input map[string]any) requestMetadata {
	metadata := requestMetadata{}
	if input == nil {
		return metadata
	}

	metadata.SessionID = stringFromMap(input, "session_id")
	metadata.UserExternalID = stringFromMap(input, "user_external_id")
	metadata.UserName = stringFromMap(input, "user_name")
	metadata.UserEmail = stringFromMap(input, "user_email")
	metadata.SessionTitle = stringFromMap(input, "session_title")

	return metadata
}

func stringFromMap(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok {
		return ""
	}

	stringValue, ok := value.(string)
	if !ok {
		return ""
	}

	return strings.TrimSpace(stringValue)
}

func boolFromMap(values map[string]any, key string) bool {
	if values == nil {
		return false
	}
	value, ok := values[key]
	if !ok {
		return false
	}
	flag, ok := value.(bool)
	if !ok {
		return false
	}
	return flag
}

func requestDisablesMemory(request createResponseRequest) bool {
	return request.DisableMemory || boolFromMap(request.Metadata, "disable_memory")
}

func resolveUpstreamModel(defaultModel, requestedModel string) string {
	trimmed := strings.TrimSpace(requestedModel)
	if trimmed == "" || trimmed == defaultPublicModel {
		return defaultModel
	}

	return trimmed
}

func newIdentifier(prefix string) string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}

	return prefix + "_" + hex.EncodeToString(buffer)
}

func (s *Server) writeLLMError(w http.ResponseWriter, r *http.Request, err error) {
	var llmError *llm.Error
	if errors.As(err, &llmError) {
		s.writeAPIError(w, r, llmError.StatusCode, llmError.Message, llmError.Type, llmError.Code, "")
		return
	}

	s.logger.Error("upstream llm request failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	s.writeAPIError(w, r, http.StatusBadGateway, "upstream llm request failed", "server_error", "upstream_error", "")
}

func (s *Server) writeAPIError(w http.ResponseWriter, r *http.Request, statusCode int, message, errorType, code, param string) {
	if statusCode <= 0 {
		statusCode = http.StatusInternalServerError
	}

	writeJSON(w, statusCode, apiErrorEnvelope{Error: apiError{
		Message:   message,
		Type:      errorType,
		Code:      code,
		Param:     param,
		RequestID: middleware.GetReqID(r.Context()),
	}})
}

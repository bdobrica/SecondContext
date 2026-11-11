package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	"github.com/bdobrica/SecondContext/internal/prompts"
	"github.com/jackc/pgx/v5"
)

func (s *Server) handleChatCompletion(w http.ResponseWriter, r *http.Request) {
	if s.handleHistoryGeneration(w, r) {
		return
	}
	var raw json.RawMessage
	if !s.decodeJSONRequest(w, r, &raw, false) {
		return
	}
	if err := validateChatJSON(raw); err != nil {
		s.writeAPIError(w, r, 400, "invalid JSON or duplicate object member", "invalid_request_error", "invalid_json", "")
		return
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(raw, &body) != nil {
		s.writeChatValidation(w, r, invalidChat(""))
		return
	}
	request, validationErr := parseChatRequest(body)
	if validationErr != nil {
		s.writeChatValidation(w, r, validationErr)
		return
	}
	session, validationErr := chatHistoryHeaders(r, request.context.Metadata)
	if validationErr != nil {
		s.writeChatValidation(w, r, validationErr)
		return
	}
	metadata, err := s.resolveRequestMetadata(r.Context(), request.context.Metadata, request.context.User)
	if err != nil {
		s.writeRequestScopeError(w, r, err)
		return
	}
	metadata.SessionID = session
	if authenticatedSubject(r.Context()) == "" {
		s.writeAPIError(w, r, 401, "authentication required", "authentication_error", "authentication_required", "")
		return
	}
	if s.dbPool == nil {
		s.writeAPIError(w, r, 503, "subject storage unavailable", "server_error", "subject_unavailable", "")
		return
	}
	if err := s.checkLinkedSession(r.Context(), metadata.UserExternalID, session); err != nil {
		s.historyFailure(w, r, err)
		return
	}
	// A supplied session can scope the request, but cannot select another owner's
	// data. No transcript is read or archived on this supplied-history endpoint.
	if session != "" {
		stored, err := db.NewSessionRepository(s.dbPool).GetByExternalID(r.Context(), session)
		if err == nil {
			owner, err := db.NewUserRepository(s.dbPool).GetByExternalID(r.Context(), metadata.UserExternalID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				s.writeAPIError(w, r, 503, "session storage unavailable", "server_error", "session_unavailable", "")
				return
			}
			if err != nil || stored.UserID != owner.ID {
				s.writeAPIError(w, r, 404, "session not found", "invalid_request_error", "session_not_found", "")
				return
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			s.writeAPIError(w, r, 503, "session storage unavailable", "server_error", "session_unavailable", "")
			return
		}
	}
	client, ok := s.llm.(llm.ChatClient)
	if !ok {
		s.writeAPIError(w, r, 503, "chat completion provider unavailable", "server_error", "chat_unavailable", "")
		return
	}
	// Retrieval uses the supplied text view; the original messages (including tool
	// blocks, nulls and whitespace) remain intact and in order in the model input.
	packet, err := s.buildResponseContext(r.Context(), request.context, metadata, request.text)
	if err != nil {
		s.logger.Warn("chat memory retrieval degraded")
	}
	system := prompts.BuildResponseSystemPrompt(packet, "")
	messages := request.messages
	if strings.TrimSpace(system) != "" {
		instruction, _ := json.Marshal(llm.Message{Role: "system", Content: system})
		messages = append([]json.RawMessage{instruction}, messages...)
	}
	request.upstream.Body["messages"], _ = json.Marshal(messages)
	request.upstream.Model = resolveUpstreamModel(s.cfg.OpenAI.ChatModel, request.upstream.Model)
	request.upstream.Body["model"], _ = json.Marshal(request.upstream.Model)
	if deadline, ok := r.Context().Deadline(); ok {
		_ = http.NewResponseController(w).SetWriteDeadline(deadline)
	}
	started := false
	if request.upstream.Stream {
		w.Header().Add("Trailer", "X-SecondContext-Capture")
	}
	emit := func(payload json.RawMessage) error {
		if err := r.Context().Err(); err != nil {
			return err
		}
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(http.StatusOK)
			started = true
		}
		// Compact multiline upstream SSE JSON so every event remains one data line.
		var compact bytes.Buffer
		if err := json.Compact(&compact, payload); err != nil {
			return err
		}
		encoded := append([]byte("data: "), compact.Bytes()...)
		encoded = append(encoded, '\n', '\n')
		if _, err := w.Write(encoded); err != nil {
			return err
		}
		return http.NewResponseController(w).Flush()
	}
	result, err := client.Chat(r.Context(), request.upstream, emit)
	if err != nil {
		if started {
			if r.Context().Err() == nil {
				payload, _ := json.Marshal(apiErrorEnvelope{Error: apiError{Message: "chat completion stream interrupted", Type: "upstream_error", Code: "incomplete_stream"}})
				_ = emit(payload)
			}
		} else {
			s.writeLLMError(w, r, err)
		}
		return
	}
	if request.upstream.Stream {
		if r.Context().Err() != nil {
			return
		}
		if _, err := w.Write([]byte("data: [DONE]\n\n")); err == nil {
			if http.NewResponseController(w).Flush() == nil {
				w.Header().Set("X-SecondContext-Capture", s.enqueueLearning(r, request.context, chatCaptureMessages(request), session, ""))
			}
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-SecondContext-Capture", s.enqueueLearning(r, request.context, chatCaptureMessages(request), session, ""))
	_, _ = w.Write(result.Body)
}

func (s *Server) writeChatValidation(w http.ResponseWriter, r *http.Request, err *chatRequestError) {
	message := "invalid chat completion parameter"
	if err.code == "unsupported_feature" {
		message = "unsupported chat completion feature; see the supported API matrix"
	}
	s.writeAPIError(w, r, http.StatusBadRequest, message, "invalid_request_error", err.code, err.param)
}

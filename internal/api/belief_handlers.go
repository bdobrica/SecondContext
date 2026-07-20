package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	beliefsvc "github.com/bdobrica/SecondContext/internal/beliefs"
	"github.com/jackc/pgx/v5"
)

func (s *Server) handleListDebugBeliefs(w http.ResponseWriter, r *http.Request) {
	limit := 10
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 || parsed > maxListResults {
			s.writeAPIError(w, r, http.StatusBadRequest, "limit must be between 1 and 100", "invalid_request_error", "invalid_limit", "limit")
			return
		}
		limit = parsed
	}

	userExternalID, err := s.resolveUserExternalID(r.Context(), requestUserSelector{Param: "user_external_id", Value: r.URL.Query().Get("user_external_id")})
	if err != nil {
		s.writeRequestScopeError(w, r, err)
		return
	}
	service := beliefsvc.NewService(s.cfg, s.dbPool, s.llm)
	beliefsList, err := service.ListBeliefs(r.Context(), beliefsvc.ListBeliefsParams{
		UserExternalID: userExternalID,
		TopicID:        strings.TrimSpace(r.URL.Query().Get("topic_id")),
		TopicName:      strings.TrimSpace(r.URL.Query().Get("topic_name")),
		Limit:          limit,
	})
	if err != nil {
		s.writeBeliefError(w, r, err)
		return
	}

	response := beliefListResponse{Data: make([]beliefDebugResponse, 0, len(beliefsList))}
	for _, view := range beliefsList {
		response.Data = append(response.Data, beliefDebugResponse{
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

	writeJSON(w, http.StatusOK, response)
}

func (s *Server) writeBeliefError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Error("belief request failed", "error", err)

	var serviceError *beliefsvc.Error
	if errors.As(err, &serviceError) {
		s.writeAPIError(w, r, serviceError.StatusCode, serviceError.Message, serviceError.Type, serviceError.Code, serviceError.Param)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		s.writeAPIError(w, r, http.StatusNotFound, "resource not found", "invalid_request_error", "not_found", "")
		return
	}

	s.writeAPIError(w, r, http.StatusInternalServerError, "request failed", "server_error", "request_failed", "")
}

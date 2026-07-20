package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	retrievalsvc "github.com/bdobrica/SecondContext/internal/retrieval"
)

func (s *Server) handleMemorySearch(w http.ResponseWriter, r *http.Request) {
	var request memorySearchRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.writeAPIError(w, r, http.StatusBadRequest, "invalid request body", "invalid_request_error", "invalid_json", "")
		return
	}

	userExternalID, err := s.resolveUserExternalID(r.Context(), requestUserSelector{Param: "user_external_id", Value: request.UserExternalID})
	if err != nil {
		s.writeRequestScopeError(w, r, err)
		return
	}
	service := retrievalsvc.NewService(s.cfg, s.dbPool, s.llm)
	results, err := service.Search(r.Context(), retrievalsvc.SearchParams{
		Query:               request.Query,
		Goal:                request.Goal,
		UserExternalID:      userExternalID,
		MemoryType:          request.MemoryType,
		People:              request.People,
		Topics:              request.Topics,
		ConfidenceThreshold: request.ConfidenceThreshold,
		IncludeExpired:      request.IncludeExpired,
		Limit:               request.Limit,
	})
	if err != nil {
		s.writeRetrievalError(w, r, err)
		return
	}

	response := memorySearchResponse{Data: make([]memorySearchResultResponse, 0, len(results))}
	for _, result := range results {
		response.Data = append(response.Data, memorySearchResultResponse{
			Rank:   result.Rank,
			Memory: toMemoryResponse(result.Memory),
			Scores: memorySearchScoresResponse{
				Final:                       result.Scores.Final,
				Hybrid:                      result.Scores.Hybrid,
				Dense:                       result.Scores.Dense,
				Sparse:                      result.Scores.Sparse,
				Retrieval:                   result.Scores.Retrieval,
				Recency:                     result.Scores.Recency,
				Importance:                  result.Scores.Importance,
				Utility:                     result.Scores.Utility,
				GoalRelevance:               result.Scores.GoalRelevance,
				BeliefImpact:                result.Scores.BeliefImpact,
				Confidence:                  result.Scores.Confidence,
				MaxSimilarityToHigherRanked: result.Scores.MaxSimilarityToHigherRanked,
			},
		})
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *Server) writeRetrievalError(w http.ResponseWriter, r *http.Request, err error) {
	var serviceError *retrievalsvc.Error
	if errors.As(err, &serviceError) {
		s.writeAPIError(w, r, serviceError.StatusCode, serviceError.Message, serviceError.Type, serviceError.Code, serviceError.Param)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		s.writeAPIError(w, r, http.StatusNotFound, "resource not found", "invalid_request_error", "not_found", "")
		return
	}

	s.logger.Error("retrieval request failed", "error", err)
	s.writeAPIError(w, r, http.StatusInternalServerError, "request failed", "server_error", "request_failed", "")
}

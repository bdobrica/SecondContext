package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	outcomesvc "github.com/bdobrica/SecondContext/internal/outcomes"
)

func (s *Server) handleCreateInteractionOutcome(w http.ResponseWriter, r *http.Request) {
	var request createInteractionOutcomeRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.writeAPIError(w, r, http.StatusBadRequest, "invalid request body", "invalid_request_error", "invalid_json", "")
		return
	}

	metadata, err := s.resolveRequestMetadata(r.Context(), request.Metadata, request.User)
	if err != nil {
		s.writeRequestScopeError(w, r, err)
		return
	}
	service := outcomesvc.NewService(s.cfg, s.dbPool, s.llm)
	result, err := service.CreateOutcome(r.Context(), outcomesvc.CreateOutcomeParams{
		SessionID:          firstNonEmpty(request.SessionID, metadata.SessionID),
		AssistantMessageID: request.AssistantMessageID,
		RawText:            request.RawText,
		Goal:               request.Goal,
		People:             request.People,
		Topics:             request.Topics,
		Metadata:           request.Metadata,
		UserExternalID:     metadata.UserExternalID,
	})
	if err != nil {
		s.writeOutcomeError(w, r, err)
		return
	}

	graphEdges := make([]graphEdgeResponse, 0, len(result.GraphEdges))
	for _, edge := range result.GraphEdges {
		graphEdges = append(graphEdges, graphEdgeResponse{
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
		})
	}

	analysisEdges := make([]outcomeAnalysisGraphEdgeResponse, 0, len(result.Analysis.GraphEdges))
	for _, edge := range result.Analysis.GraphEdges {
		analysisEdges = append(analysisEdges, outcomeAnalysisGraphEdgeResponse(edge))
	}

	responseMetadata := map[string]any{}
	if result.ScenarioPlan != nil {
		responseMetadata["scenario_plan"] = result.ScenarioPlan
	}
	if result.AssistantMessage.ID != "" {
		responseMetadata["assistant_message_id"] = result.AssistantMessage.ID
	}
	if result.Session.ExternalID != "" {
		responseMetadata["session_id"] = result.Session.ExternalID
	}

	writeJSON(w, http.StatusCreated, createInteractionOutcomeResponse{
		Outcome: interactionOutcomeResponse{
			ID:               result.Outcome.ID,
			UserID:           result.Outcome.UserID,
			SessionID:        result.Outcome.SessionID,
			MessageID:        result.Outcome.MessageID,
			PersonID:         result.Outcome.PersonID,
			TopicID:          result.Outcome.TopicID,
			Goal:             result.Outcome.Goal,
			PredictedOutcome: result.Outcome.PredictedOutcome,
			ActualOutcome:    result.Outcome.ActualOutcome,
			SuccessScore:     result.Outcome.SuccessScore,
			PredictionError:  result.Outcome.PredictionError,
			Metadata:         decodeJSONMap(result.Outcome.Metadata),
			CreatedAt:        result.Outcome.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt:        result.Outcome.UpdatedAt.UTC().Format(time.RFC3339),
		},
		Memory: toMemoryResponse(result.Memory),
		Analysis: outcomeAnalysisResponse{
			Summary:         result.Analysis.Summary,
			SuccessScore:    result.Analysis.SuccessScore,
			PredictionError: result.Analysis.PredictionError,
			People:          result.Analysis.People,
			Topics:          result.Analysis.Topics,
			Importance:      result.Analysis.Importance,
			Utility:         result.Analysis.Utility,
			BeliefImpact:    result.Analysis.BeliefImpact,
			Confidence:      result.Analysis.Confidence,
			GraphEdges:      analysisEdges,
		},
		GraphEdges: graphEdges,
		Metadata:   responseMetadata,
	})
}

func (s *Server) writeOutcomeError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Error("outcome request failed", "error", err)

	var serviceError *outcomesvc.Error
	if errors.As(err, &serviceError) {
		s.writeAPIError(w, r, serviceError.StatusCode, serviceError.Message, serviceError.Type, serviceError.Code, serviceError.Param)
		return
	}

	s.writeAPIError(w, r, http.StatusInternalServerError, "request failed", "server_error", "request_failed", "")
}

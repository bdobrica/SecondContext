package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bdobrica/SecondContext/internal/db"
	memsvc "github.com/bdobrica/SecondContext/internal/memory"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

func (s *Server) handleMemoryIngest(w http.ResponseWriter, r *http.Request) {
	var request ingestMemoryRequest
	if !s.decodeJSONRequest(w, r, &request, true) {
		return
	}

	service := memsvc.NewService(s.cfg, s.dbPool, s.llm)
	metadata, err := s.resolveRequestMetadata(r.Context(), request.Metadata, request.User)
	if err != nil {
		s.writeRequestScopeError(w, r, err)
		return
	}
	record, err := service.Ingest(r.Context(), memsvc.IngestParams{
		RawText:       request.RawText,
		Summary:       request.Summary,
		MemoryType:    request.Type,
		Source:        request.Source,
		People:        request.People,
		Topics:        request.Topics,
		Importance:    request.Importance,
		Utility:       request.Utility,
		BeliefImpact:  request.BeliefImpact,
		Confidence:    request.Confidence,
		ExpiresInDays: request.ExpiresInDays,
		Metadata:      request.Metadata,
		RequestUser:   metadata.UserExternalID,
		Meta: memsvc.RequestMetadata{
			SessionID:      metadata.SessionID,
			UserExternalID: metadata.UserExternalID,
			UserName:       metadata.UserName,
			UserEmail:      metadata.UserEmail,
			SessionTitle:   metadata.SessionTitle,
		},
	})
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}

	writeJSON(w, http.StatusCreated, toMemoryResponse(record))
}

func (s *Server) handleMemoryExtract(w http.ResponseWriter, r *http.Request) {
	var request extractMemoryRequest
	if !s.decodeJSONRequest(w, r, &request, true) {
		return
	}

	metadata, err := s.resolveRequestMetadata(r.Context(), request.Metadata, request.User)
	if err != nil {
		s.writeRequestScopeError(w, r, err)
		return
	}
	service := memsvc.NewService(s.cfg, s.dbPool, s.llm)
	result, err := service.ExtractAndIngest(r.Context(), memsvc.ExtractParams{
		RawText:     request.RawText,
		Source:      request.Source,
		Metadata:    request.Metadata,
		RequestUser: metadata.UserExternalID,
		Meta: memsvc.RequestMetadata{
			SessionID:      metadata.SessionID,
			UserExternalID: metadata.UserExternalID,
			UserName:       metadata.UserName,
			UserEmail:      metadata.UserEmail,
			SessionTitle:   metadata.SessionTitle,
		},
	})
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}

	entities := make([]extractedEntityResponse, 0, len(result.Extraction.Entities))
	for _, entity := range result.Extraction.Entities {
		entities = append(entities, extractedEntityResponse{Type: entity.Type, Name: entity.Name, Confidence: entity.Confidence})
	}

	writeJSON(w, http.StatusCreated, extractMemoryResponse{
		Memory: toMemoryResponse(result.Memory),
		Extraction: extractionResponse{
			Summary:       result.Extraction.Summary,
			Type:          result.Extraction.Type,
			People:        result.Extraction.People,
			Topics:        result.Extraction.Topics,
			Entities:      entities,
			Importance:    result.Extraction.Importance,
			Utility:       result.Extraction.Utility,
			BeliefImpact:  result.Extraction.BeliefImpact,
			Confidence:    result.Extraction.Confidence,
			ExpiresInDays: result.Extraction.ExpiresInDays,
		},
	})
}

func (s *Server) handleListMemories(w http.ResponseWriter, r *http.Request) {
	limit := int32(50)
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 || parsed > maxListResults {
			s.writeAPIError(w, r, http.StatusBadRequest, "limit must be between 1 and 100", "invalid_request_error", "invalid_limit", "limit")
			return
		}
		limit = int32(parsed)
	}

	userExternalID, err := s.resolveUserExternalID(r.Context(), requestUserSelector{Param: "user_external_id", Value: r.URL.Query().Get("user_external_id")})
	if err != nil {
		s.writeRequestScopeError(w, r, err)
		return
	}
	service := memsvc.NewService(s.cfg, s.dbPool, s.llm)
	memories, err := service.List(r.Context(), memsvc.ListParams{
		UserExternalID: userExternalID,
		Limit:          limit,
	})
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}

	response := memoryListResponse{Data: make([]memoryResponse, 0, len(memories))}
	for _, item := range memories {
		response.Data = append(response.Data, toMemoryResponse(item))
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	memoryID := chi.URLParam(r, "memoryID")
	if strings.TrimSpace(memoryID) == "" {
		s.writeAPIError(w, r, http.StatusBadRequest, "memory id is required", "invalid_request_error", "missing_memory_id", "memoryID")
		return
	}
	if s.dbPool != nil {
		record, err := db.NewMemoryRepository(s.dbPool).GetByID(r.Context(), memoryID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				s.writeAPIError(w, r, http.StatusNotFound, "memory not found", "invalid_request_error", "memory_not_found", "memoryID")
				return
			}
			s.writeMemoryError(w, r, err)
			return
		}
		if err := s.ensureActorOwnsUserID(r.Context(), record.UserID, "memory not found", "memory_not_found", "memoryID"); err != nil {
			if s.writeRequestScopeError(w, r, err) {
				return
			}
			s.writeMemoryError(w, r, err)
			return
		}
	}

	service := memsvc.NewService(s.cfg, s.dbPool, s.llm)
	if err := service.Delete(r.Context(), memoryID); err != nil {
		s.writeMemoryError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, memoryDeleteResponse{ID: memoryID, Deleted: true})
}

func (s *Server) writeMemoryError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Error("memory request failed", "error", err)

	var serviceError *memsvc.Error
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

func toMemoryResponse(item memsvc.Record) memoryResponse {
	metadata := make(map[string]any)
	if len(item.Metadata) > 0 {
		_ = json.Unmarshal(item.Metadata, &metadata)
	}

	response := memoryResponse{
		ID:            item.ID,
		UserID:        item.UserID,
		MemoryType:    item.MemoryType,
		Source:        item.Source,
		RawText:       item.RawText,
		Summary:       item.Summary,
		People:        item.People,
		Topics:        item.Topics,
		Importance:    item.Importance,
		Utility:       item.Utility,
		BeliefImpact:  item.BeliefImpact,
		Confidence:    item.Confidence,
		QdrantPointID: item.QdrantPointID,
		Metadata:      metadata,
		CreatedAt:     item.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     item.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if item.ExpiresAt != nil {
		timestamp := item.ExpiresAt.UTC().Format(time.RFC3339)
		response.ExpiresAt = &timestamp
	}

	return response
}

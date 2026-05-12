package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bdobrica/SecondContext/internal/db"
	modelsvc "github.com/bdobrica/SecondContext/internal/modeling"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

func (s *Server) handleGetDebugPerson(w http.ResponseWriter, r *http.Request) {
	personID := strings.TrimSpace(chi.URLParam(r, "personID"))
	if personID == "" {
		s.writeAPIError(w, r, http.StatusBadRequest, "person id is required", "invalid_request_error", "missing_person_id", "personID")
		return
	}
	if s.dbPool != nil {
		person, err := db.NewPersonRepository(s.dbPool).GetByID(r.Context(), personID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				s.writeAPIError(w, r, http.StatusNotFound, "person not found", "invalid_request_error", "person_not_found", "personID")
				return
			}
			s.writeModelingError(w, r, err)
			return
		}
		if err := s.ensureActorOwnsUserID(r.Context(), person.UserID, "person not found", "person_not_found", "personID"); err != nil {
			if s.writeRequestScopeError(w, r, err) {
				return
			}
			s.writeModelingError(w, r, err)
			return
		}
	}

	service := modelsvc.NewService(s.cfg, s.dbPool, s.llm)
	profile, err := service.GetPersonProfile(
		r.Context(),
		personID,
		strings.TrimSpace(r.URL.Query().Get("topic_id")),
		strings.TrimSpace(r.URL.Query().Get("topic_name")),
	)
	if err != nil {
		s.writeModelingError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, toPersonDebugResponse(profile))
}

func (s *Server) handleUpdateDebugPerson(w http.ResponseWriter, r *http.Request) {
	personID := strings.TrimSpace(chi.URLParam(r, "personID"))
	if personID == "" {
		s.writeAPIError(w, r, http.StatusBadRequest, "person id is required", "invalid_request_error", "missing_person_id", "personID")
		return
	}
	if s.dbPool != nil {
		person, err := db.NewPersonRepository(s.dbPool).GetByID(r.Context(), personID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				s.writeAPIError(w, r, http.StatusNotFound, "person not found", "invalid_request_error", "person_not_found", "personID")
				return
			}
			s.writeModelingError(w, r, err)
			return
		}
		if err := s.ensureActorOwnsUserID(r.Context(), person.UserID, "person not found", "person_not_found", "personID"); err != nil {
			if s.writeRequestScopeError(w, r, err) {
				return
			}
			s.writeModelingError(w, r, err)
			return
		}
	}

	var request updatePersonModelRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.writeAPIError(w, r, http.StatusBadRequest, "invalid request body", "invalid_request_error", "invalid_json", "")
		return
	}

	service := modelsvc.NewService(s.cfg, s.dbPool, s.llm)
	profile, err := service.UpdatePersonModel(r.Context(), personID, modelsvc.UpdatePersonModelParams{
		TopicID:           strings.TrimSpace(request.TopicID),
		TopicName:         strings.TrimSpace(request.TopicName),
		TopicAliases:      request.TopicAliases,
		PersonAliases:     request.PersonAliases,
		Niceness:          request.Niceness,
		Readiness:         request.Readiness,
		Competence:        request.Competence,
		Capacity:          request.Capacity,
		Confidence:        request.Confidence,
		EvidenceCount:     request.EvidenceCount,
		EvidenceMemoryIDs: request.EvidenceMemoryIDs,
		LastObservedAt:    request.LastObservedAt,
	})
	if err != nil {
		s.writeModelingError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, toPersonDebugResponse(profile))
}

func (s *Server) writeModelingError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Error("person model request failed", "error", err)

	var serviceError *modelsvc.Error
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

func toPersonDebugResponse(profile modelsvc.PersonProfile) personDebugResponse {
	response := personDebugResponse{
		ID:        profile.Person.ID,
		UserID:    profile.Person.UserID,
		Name:      profile.Person.Name,
		Aliases:   profile.Person.Aliases,
		Metadata:  decodeJSONMap(profile.Person.Metadata),
		CreatedAt: profile.Person.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: profile.Person.UpdatedAt.UTC().Format(time.RFC3339),
		Models:    make([]personTopicModelResponse, 0, len(profile.Models)),
	}

	for _, modelView := range profile.Models {
		response.Models = append(response.Models, personTopicModelResponse{
			ID:                modelView.Model.ID,
			TopicID:           modelView.Topic.ID,
			TopicName:         modelView.Topic.Name,
			TopicAliases:      modelView.Topic.Aliases,
			Niceness:          modelView.Model.Niceness,
			Readiness:         modelView.Model.Readiness,
			Competence:        modelView.Model.Competence,
			Capacity:          modelView.Model.Capacity,
			Confidence:        modelView.Model.Confidence,
			EvidenceCount:     modelView.Model.EvidenceCount,
			EvidenceMemoryIDs: modelView.EvidenceMemoryIDs,
			LastObservedAt:    formatOptionalTime(modelView.Model.LastObservedAt),
			Summary:           modelView.Summary,
			Metadata:          decodeJSONMap(modelView.Model.Metadata),
			CreatedAt:         modelView.Model.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt:         modelView.Model.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}

	return response
}

func decodeJSONMap(raw json.RawMessage) map[string]any {
	result := make(map[string]any)
	if len(strings.TrimSpace(string(raw))) == 0 {
		return result
	}
	_ = json.Unmarshal(raw, &result)
	return result
}

func formatOptionalTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339)
	return &formatted
}

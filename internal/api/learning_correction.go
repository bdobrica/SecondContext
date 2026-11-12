package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	memsvc "github.com/bdobrica/SecondContext/internal/memory"
	"github.com/bdobrica/SecondContext/internal/models"
	"github.com/bdobrica/SecondContext/internal/qdrant"
	"github.com/bdobrica/SecondContext/internal/retrieval"
	"github.com/go-chi/chi/v5"
)

// Retain the occurrence receipt but remove any queued source/output. This also
// prevents a partially indexed batch from overwriting corrections on retry.
func (s *Server) suppressMemoryCapture(ctx context.Context, id string) error {
	_, err := s.dbPool.Exec(ctx, `UPDATE learning_captures SET status='suppressed',source_text='',source_metadata='{}',observations=NULL,error_code=''
 WHERE id=(SELECT capture_id FROM memory_items WHERE id=$1) AND status IN ('pending','failed')`, id)
	return err
}

func (s *Server) handleCorrectObservation(w http.ResponseWriter, r *http.Request) {
	if authenticatedSubject(r.Context()) == "" {
		s.historyError(w, r, 401, "authentication_required")
		return
	}
	if s.dbPool == nil {
		s.historyError(w, r, 503, "learning_unavailable")
		return
	}
	var body struct {
		Summary string `json:"summary"`
	}
	if !s.decodeJSONRequest(w, r, &body, true) {
		return
	}
	body.Summary = strings.TrimSpace(body.Summary)
	if body.Summary == "" || len(body.Summary) > 2000 {
		s.historyError(w, r, 400, "invalid_observation_summary")
		return
	}
	record, err := s.correctObservation(r.Context(), chi.URLParam(r, "memoryID"), body.Summary)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	writeJSON(w, 200, toMemoryResponse(record))
}

func (s *Server) correctObservation(ctx context.Context, id, summary string) (models.MemoryItem, error) {
	fail := func(status int, code string) (models.MemoryItem, error) {
		return models.MemoryItem{}, &memsvc.Error{StatusCode: status, Message: code, Type: "invalid_request_error", Code: code}
	}
	repo := db.NewMemoryRepository(s.dbPool)
	record, err := repo.GetByID(ctx, id)
	if err != nil {
		return record, err
	}
	if err = s.ensureActorOwnsUserID(ctx, record.UserID, "memory not found", "memory_not_found", "memoryID"); err != nil {
		return fail(404, "memory_not_found")
	}
	var conversational bool
	if err = s.dbPool.QueryRow(ctx, "SELECT capture_id IS NOT NULL FROM memory_items WHERE id=$1", record.ID).Scan(&conversational); err != nil {
		return record, err
	}
	if !conversational {
		return fail(409, "not_conversational_memory")
	}
	embedded, err := s.llm.Embed(ctx, llm.EmbedRequest{Model: s.cfg.OpenAI.EmbeddingModel, Input: summary})
	if err != nil || len(embedded.Vector) == 0 {
		return fail(502, "embedding_failed")
	}
	if err = s.suppressMemoryCapture(ctx, record.ID); err != nil {
		return record, err
	}
	index := qdrant.NewClient(s.cfg.Qdrant)
	err = index.UpsertPoint(ctx, s.cfg.Qdrant.Collection, qdrant.Point{ID: record.ID, DenseVector: embedded.Vector, SparseVector: retrieval.BuildSparseVector(summary), Payload: map[string]any{"memory_id": record.ID, "user_id": record.UserID, "summary": summary, "type": record.MemoryType, "people": record.People, "topics": record.Topics, "expires_at": record.ExpiresAt}})
	if err != nil {
		return fail(502, "observation_index_failed")
	}
	metadata := map[string]any{}
	_ = json.Unmarshal(record.Metadata, &metadata)
	metadata["corrected_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	metadata["correction_source"] = "user"
	raw, _ := json.Marshal(metadata)
	// The database trigger invalidates dependent aggregates in this same statement.
	// Retrieval hydrates canonical SQL, never the vector payload's summary.
	_, err = s.dbPool.Exec(ctx, `UPDATE memory_items SET summary=$2,metadata=$3,updated_at=now(),qdrant_point_id=id::text,qdrant_status='completed' WHERE id=$1`, record.ID, summary, raw)
	if err != nil {
		return record, err
	}
	return repo.GetByID(ctx, record.ID)
}

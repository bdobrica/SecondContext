package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	"github.com/bdobrica/SecondContext/internal/qdrant"
	"github.com/bdobrica/SecondContext/internal/retrieval"
	"github.com/go-chi/chi/v5"
)

// Retain the occurrence receipt but remove any queued source/output. This also
// prevents a partially indexed batch from overwriting corrections on retry.
func (s *Server) suppressMemoryCapture(ctx context.Context, id string) error {
	_, err := s.dbPool.Exec(ctx, `UPDATE learning_captures SET status='suppressed',source_text='',observations=NULL,error_code=''
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
	repo := db.NewMemoryRepository(s.dbPool)
	record, err := repo.GetByID(r.Context(), chi.URLParam(r, "memoryID"))
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	if err = s.ensureActorOwnsUserID(r.Context(), record.UserID, "memory not found", "memory_not_found", "memoryID"); err != nil {
		if !s.writeRequestScopeError(w, r, err) {
			s.writeMemoryError(w, r, err)
		}
		return
	}
	var conversational bool
	if err = s.dbPool.QueryRow(r.Context(), "SELECT capture_id IS NOT NULL FROM memory_items WHERE id=$1", record.ID).Scan(&conversational); err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	if !conversational {
		s.historyError(w, r, 409, "not_conversational_memory")
		return
	}
	embedded, err := s.llm.Embed(r.Context(), llm.EmbedRequest{Model: s.cfg.OpenAI.EmbeddingModel, Input: body.Summary})
	if err != nil || len(embedded.Vector) == 0 {
		s.historyError(w, r, 502, "embedding_failed")
		return
	}
	if err = s.suppressMemoryCapture(r.Context(), record.ID); err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	// No person/belief derivatives exist for this path. Retrieval loads canonical
	// SQL summaries, so an index failure cannot serve a stale copy as a correction.
	index := qdrant.NewClient(s.cfg.Qdrant)
	err = index.UpsertPoint(r.Context(), s.cfg.Qdrant.Collection, qdrant.Point{ID: record.ID, DenseVector: embedded.Vector, SparseVector: retrieval.BuildSparseVector(body.Summary), Payload: map[string]any{"memory_id": record.ID, "user_id": record.UserID, "summary": body.Summary, "type": record.MemoryType, "people": record.People, "topics": record.Topics, "expires_at": record.ExpiresAt}})
	if err != nil {
		s.historyError(w, r, 502, "observation_index_failed")
		return
	}
	metadata := map[string]any{}
	_ = json.Unmarshal(record.Metadata, &metadata)
	metadata["corrected_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	metadata["correction_source"] = "user"
	raw, _ := json.Marshal(metadata)
	_, err = s.dbPool.Exec(r.Context(), `UPDATE memory_items SET summary=$2,metadata=$3,updated_at=now(),qdrant_point_id=id::text,qdrant_status='completed' WHERE id=$1`, record.ID, body.Summary, raw)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	record, err = repo.GetByID(r.Context(), record.ID)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	writeJSON(w, 200, toMemoryResponse(record))
}

package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/models"
)

// Search canonical summaries and labels, not stale vector payloads or the old
// source quote retained after correction. Offset pagination is for inspection;
// mutation always resolves a fresh subject-owned target under the subject fence.
func (s *Server) observations(ctx context.Context, user, query string, limit, offset int) ([]models.MemoryItem, error) {
	rows, err := s.dbPool.Query(ctx, `SELECT m.id::text FROM memory_items m
 WHERE m.user_id=$1 AND m.capture_id IS NOT NULL
 AND ($2='' OR m.id::text=$2 OR strpos(lower(m.summary),lower($2))>0
      OR strpos(lower(array_to_string(m.people,' ')),lower($2))>0
      OR strpos(lower(array_to_string(m.topics,' ')),lower($2))>0)
 AND NOT EXISTS(SELECT 1 FROM sessions s JOIN context_session_fences f ON f.user_id=s.user_id AND f.session_external_id=s.external_id WHERE s.id=m.session_id)
 ORDER BY m.created_at DESC,m.id DESC LIMIT $3 OFFSET $4`, user, query, limit, offset)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := make([]models.MemoryItem, 0, len(ids))
	for _, id := range ids {
		m, e := db.NewMemoryRepository(s.dbPool).GetByID(ctx, id)
		if e != nil {
			return nil, e
		}
		result = append(result, m)
	}
	if err := db.NewMemoryRepository(s.dbPool).AnnotateObservations(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Server) handleObservations(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok, err := s.actorUser(r.Context())
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	if !ok {
		s.historyError(w, r, 401, "authentication_required")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
	}
	if err != nil || offset < 0 || offset > 1000000 || len(query) > 300 {
		s.historyError(w, r, 400, "invalid_observation_query")
		return
	}
	memories, err := s.observations(r.Context(), user.ID, query, 51, offset)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	more := len(memories) > 50
	if more {
		memories = memories[:50]
	}
	ids := []string{}
	for _, m := range memories {
		ids = append(ids, m.ID)
	}
	ready, err := db.NewMemoryRepository(s.dbPool).ListByIDs(r.Context(), ids)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	states := map[string]string{}
	for _, m := range ready {
		states[m.ID] = "ready"
	}
	data := make([]memoryResponse, 0, len(memories))
	for _, m := range memories {
		item := toMemoryResponse(m)
		item.RecallStatus = states[m.ID]
		if item.RecallStatus == "" {
			item.RecallStatus = "unavailable"
		}
		if m.ExpiresAt != nil && !m.ExpiresAt.After(time.Now()) {
			item.RecallStatus = "expired"
		}
		data = append(data, item)
	}
	writeJSON(w, 200, map[string]any{"data": data, "has_more": more, "offset": offset})
}

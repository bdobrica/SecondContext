package db

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bdobrica/SecondContext/internal/models"
)

// Expand forward from a semantic hit so a paraphrased replacement cannot be
// missed merely because the index ranked its obsolete wording more highly.
func (r *MemoryRepository) ExpandObservationIDs(ctx context.Context, user string, ids []string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `WITH RECURSIVE related(id) AS (
 SELECT id FROM memory_items WHERE user_id=$1 AND id=ANY($2::uuid[])
 UNION SELECT r.newer_id FROM ready_observation_relations r JOIN related p ON p.id=r.prior_id WHERE r.user_id=$1
 ) SELECT id::text FROM related LIMIT 201`, user, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	if len(result) > 200 {
		return nil, fmt.Errorf("observation relation expansion limit exceeded")
	}
	return result, rows.Err()
}

// Derived metadata is read from current canonical relations, never persisted in
// a vector payload. Deletion and editing invalidate it immediately.
func (r *MemoryRepository) AnnotateObservations(ctx context.Context, memories []models.MemoryItem) error {
	if len(memories) == 0 {
		return nil
	}
	ids := make([]string, len(memories))
	for i, m := range memories {
		ids[i] = m.ID
	}
	rows, err := r.pool.Query(ctx, `SELECT r.prior_id::text,r.newer_id::text,r.kind,r.prior_quote,r.new_quote,r.reason,r.created_at
 FROM ready_observation_relations r
 WHERE (r.prior_id=ANY($1::uuid[]) OR r.newer_id=ANY($1::uuid[]))
 AND (r.kind<>'contradiction' OR NOT EXISTS(SELECT 1 FROM ready_observation_relations x
 WHERE x.user_id=r.user_id AND x.prior_id IN(r.prior_id,r.newer_id) AND x.kind IN ('correction','changed_circumstances')))
 ORDER BY r.created_at,r.prior_id,r.newer_id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	relations := map[string][]map[string]any{}
	states := map[string]string{}
	for rows.Next() {
		var prior, newer, kind, pq, nq, reason string
		var created any
		if err = rows.Scan(&prior, &newer, &kind, &pq, &nq, &reason, &created); err != nil {
			return err
		}
		relation := map[string]any{"prior_id": prior, "newer_id": newer, "kind": kind, "prior_quote": pq, "new_quote": nq, "reason": reason, "created_at": created}
		relations[prior] = append(relations[prior], relation)
		relations[newer] = append(relations[newer], relation)
		if kind == "correction" || kind == "changed_circumstances" {
			states[prior] = "superseded"
		}
		if kind == "contradiction" {
			if states[prior] != "superseded" {
				states[prior] = "disputed"
			}
			if states[newer] != "superseded" {
				states[newer] = "disputed"
			}
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for i := range memories {
		var meta map[string]any
		_ = json.Unmarshal(memories[i].Metadata, &meta)
		if meta == nil {
			meta = map[string]any{}
		}
		if memories[i].Source != "conversation" && memories[i].Source != "meeting_note" {
			continue
		}
		state := states[memories[i].ID]
		if state == "" {
			state = "current"
		}
		meta["working_state"] = state
		delete(meta, "relations")
		if len(relations[memories[i].ID]) > 0 {
			meta["relations"] = relations[memories[i].ID]
		}
		memories[i].Metadata, _ = json.Marshal(meta)
	}
	return nil
}

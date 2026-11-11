package db

import "context"

// Recent loads only canonical accepted candidates at the immutable reservation
// snapshot. Adaptive reservations keep selected_turns NULL until selection commits.
func (r HistoryRepository) Recent(ctx context.Context, s HistorySession, t HistoryTurn, limit int) ([]HistoryTurn, error) {
	if err := CheckHistorySession(ctx, r.Conn, s.UserID, s.ExternalID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 64 {
		return nil, HistoryError("history_unavailable")
	}
	rows, err := r.Conn.Query(ctx, "SELECT "+historyColumns+" FROM conversation_turns WHERE user_id=$1 AND session_id=$2 AND state='accepted' AND sequence<=$3 ORDER BY sequence DESC LIMIT $4", s.UserID, s.ID, t.Snapshot, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []HistoryTurn{}
	for rows.Next() {
		v, e := scanHistory(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, nil
}

// Commit exactly once, on the dedicated lock-owning connection, before generation.
// Reuse existing selection storage: no transcript copy or migration is needed.
func (r HistoryRepository) PinSelection(ctx context.Context, s HistorySession, t HistoryTurn, ids []string) error {
	if ids == nil || len(ids) > 64 {
		return HistoryError("history_unavailable")
	}
	if err := CheckHistorySession(ctx, r.Conn, s.UserID, s.ExternalID); err != nil {
		return err
	}
	t.Selected = ids
	if _, err := r.Selected(ctx, s, t); err != nil {
		return err
	}
	tag, err := r.Conn.Exec(ctx, `UPDATE conversation_turns SET selected_turns=$4 WHERE user_id=$1 AND session_id=$2 AND turn_id=$3 AND state='pending' AND result IS NULL AND selected_turns IS NULL AND expires_at>now() AND context_generation=$5`, s.UserID, s.ID, t.ID, ids, s.Generation)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return HistoryError("history_unavailable")
	}
	return nil
}

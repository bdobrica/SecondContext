package db

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// History operations use the SAME dedicated connection that owns the subject
// advisory lock. Losing that connection prevents a former worker from committing
// after another worker has acquired the lock and resumed the request.
type HistoryQueries interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}
type HistoryRepository struct{ Conn *pgx.Conn }
type HistoryError string

func (e HistoryError) Error() string { return string(e) }

type HistorySession struct {
	ID, UserID, ExternalID string
	Generation             int64
}
type HistoryTurn struct {
	ID                                                     string
	Sequence, Snapshot, Generation                         int64
	State, Acceptance, UserText, AssistantText, ResponseID string
	Expired                                                bool
	Selected                                               []string
	RequestHash, ConfigHash, FinalizeHash, Result, Receipt []byte
}
type HistoryReceipt struct {
	Version  int    `json:"contract_version"`
	User     string `json:"user"`
	Session  string `json:"session_id"`
	Turn     string `json:"turn_id"`
	Action   string `json:"action"`
	Status   string `json:"status"`
	Sequence int64  `json:"sequence"`
	Response string `json:"response_id,omitempty"`
	Coverage string `json:"history_coverage"`
}

func Receipt(user, session string, t HistoryTurn, action string) []byte {
	status := "accepted"
	response := ""
	if action == "abandon" {
		status = "abandoned"
	}
	if action == "accept_generated" {
		response = t.ResponseID
	}
	b, _ := json.Marshal(HistoryReceipt{1, user, session, t.ID, action, status, t.Sequence, response, "linked_sessions"})
	return b
}
func (r HistoryRepository) Session(ctx context.Context, subject, external string, create bool) (HistorySession, error) {
	var s HistorySession
	s.ExternalID = external
	err := r.Conn.QueryRow(ctx, "SELECT id::text FROM users WHERE external_id=$1", subject).Scan(&s.UserID)
	if errors.Is(err, pgx.ErrNoRows) && create {
		err = r.Conn.QueryRow(ctx, `INSERT INTO users(external_id,display_name) SELECT $1,$1 WHERE NOT EXISTS(SELECT 1 FROM subject_purges WHERE external_id=$1) ON CONFLICT(external_id) DO UPDATE SET external_id=EXCLUDED.external_id RETURNING id::text`, subject).Scan(&s.UserID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return s, HistoryError("turn_not_found")
	}
	if err != nil {
		return s, err
	}
	if err = CheckHistorySession(ctx, r.Conn, s.UserID, external); err != nil {
		return s, err
	}
	var owner string
	err = r.Conn.QueryRow(ctx, "SELECT id::text,user_id::text,context_generation FROM sessions WHERE external_id=$1", external).Scan(&s.ID, &owner, &s.Generation)
	if errors.Is(err, pgx.ErrNoRows) && create {
		_, err = r.Conn.Exec(ctx, `INSERT INTO sessions(user_id,external_id) VALUES($1,$2) ON CONFLICT(external_id) DO NOTHING`, s.UserID, external)
		if err == nil {
			err = r.Conn.QueryRow(ctx, "SELECT id::text,user_id::text,context_generation FROM sessions WHERE external_id=$1", external).Scan(&s.ID, &owner, &s.Generation)
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return s, HistoryError("turn_not_found")
	}
	if err != nil {
		return s, err
	}
	if owner != s.UserID {
		return s, HistoryError("session_not_found")
	}
	return s, nil
}

// Checks run even for requests with no new references and history=off.
func CheckHistorySession(ctx context.Context, q HistoryQueries, user, session string) error {
	var fenced bool
	if err := q.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM context_session_fences WHERE user_id=$1 AND session_external_id=$2)", user, session).Scan(&fenced); err != nil {
		return err
	}
	if fenced {
		return HistoryError("session_deleted")
	}
	var stale bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM context_projection_sessions s LEFT JOIN context_projections p ON p.user_id=s.user_id AND p.kind=s.kind AND p.record_id=s.record_id WHERE s.user_id=$1 AND s.session_external_id=$2 AND (p.record_id IS NULL OR p.deleted OR s.revision IS NULL OR s.revision<>p.revision))`, user, session).Scan(&stale)
	if err != nil {
		return err
	}
	if stale {
		return HistoryError("history_source_conflict")
	}
	return nil
}
func (r HistoryRepository) Expire(ctx context.Context, s HistorySession, subject string) error {
	// One pending row per session. This is a single atomic wipe + terminal receipt.
	_, err := r.Conn.Exec(ctx, `UPDATE conversation_turns SET state='abandoned',expired=true,terminal_at=now(),
 receipt=jsonb_build_object('contract_version',1,'user',$3::text,'session_id',$4::text,'turn_id',turn_id::text,'action','abandon','status','abandoned','sequence',sequence,'history_coverage','linked_sessions'),
 request_hash=NULL,config_hash=NULL,finalize_hash=NULL,result=NULL,response_id=NULL,user_text=NULL,assistant_text=NULL,selected_turns=NULL,snapshot_sequence=NULL,context_generation=NULL,acceptance=NULL
 WHERE user_id=$1 AND session_id=$2 AND state='pending' AND expires_at<=now()`, s.UserID, s.ID, subject, s.ExternalID)
	return err
}

const historyColumns = `turn_id::text,sequence,COALESCE(snapshot_sequence,0),COALESCE(context_generation,0),state,COALESCE(acceptance,''),COALESCE(user_text,''::bytea),COALESCE(assistant_text,''::bytea),COALESCE(response_id,''),expired,selected_turns,request_hash,config_hash,finalize_hash,result,receipt`

func scanHistory(row pgx.Row) (HistoryTurn, error) {
	var t HistoryTurn
	var userText, assistantText []byte
	err := row.Scan(&t.ID, &t.Sequence, &t.Snapshot, &t.Generation, &t.State, &t.Acceptance, &userText, &assistantText, &t.ResponseID, &t.Expired, &t.Selected, &t.RequestHash, &t.ConfigHash, &t.FinalizeHash, &t.Result, &t.Receipt)
	t.UserText = string(userText)
	t.AssistantText = string(assistantText)
	return t, err
}
func (r HistoryRepository) Get(ctx context.Context, s HistorySession, id string) (HistoryTurn, error) {
	return scanHistory(r.Conn.QueryRow(ctx, "SELECT "+historyColumns+" FROM conversation_turns WHERE user_id=$1 AND session_id=$2 AND turn_id=$3", s.UserID, s.ID, id))
}
func (r HistoryRepository) Pending(ctx context.Context, s HistorySession) error {
	var pending bool
	err := r.Conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM conversation_turns WHERE user_id=$1 AND session_id=$2 AND state='pending')", s.UserID, s.ID).Scan(&pending)
	if err != nil {
		return err
	}
	if pending {
		return HistoryError("turn_pending")
	}
	return nil
}
func (r HistoryRepository) Reserve(ctx context.Context, s HistorySession, t HistoryTurn, recent int, link func(pgx.Tx) error) (HistoryTurn, error) {
	tx, err := r.Conn.Begin(ctx)
	if err != nil {
		return t, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	// Bind dependencies atomically with the first retained inbound bytes. A crash
	// after reservation must not leave a source-dependent pending turn unlinked.
	if err = link(tx); err != nil {
		return t, err
	}
	if err = tx.QueryRow(ctx, "UPDATE sessions SET history_sequence=history_sequence+1 WHERE id=$1 AND user_id=$2 AND history_sequence<9007199254740991 RETURNING history_sequence", s.ID, s.UserID).Scan(&t.Sequence); err != nil {
		return t, err
	}
	err = tx.QueryRow(ctx, "SELECT COALESCE(max(sequence),0) FROM conversation_turns WHERE user_id=$1 AND session_id=$2 AND state='accepted'", s.UserID, s.ID).Scan(&t.Snapshot)
	if err != nil {
		return t, err
	}
	t.Generation = s.Generation
	t.Selected = []string{}
	rows, err := tx.Query(ctx, "SELECT turn_id::text FROM conversation_turns WHERE user_id=$1 AND session_id=$2 AND state='accepted' AND sequence<=$3 ORDER BY sequence DESC LIMIT $4", s.UserID, s.ID, t.Snapshot, recent)
	if err != nil {
		return t, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return t, err
		}
		t.Selected = append(t.Selected, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return t, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO conversation_turns(user_id,session_id,turn_id,sequence,state,snapshot_sequence,context_generation,selected_turns,request_hash,config_hash,acceptance,user_text) VALUES($1,$2,$3,$4,'pending',$5,$6,$7,$8,$9,$10,$11)`, s.UserID, s.ID, t.ID, t.Sequence, t.Snapshot, t.Generation, t.Selected, t.RequestHash, t.ConfigHash, t.Acceptance, []byte(t.UserText))
	if err != nil {
		return t, err
	}
	return t, tx.Commit(ctx)
}
func (r HistoryRepository) Selected(ctx context.Context, s HistorySession, t HistoryTurn) ([]HistoryTurn, error) {
	rows, err := r.Conn.Query(ctx, "SELECT "+historyColumns+" FROM conversation_turns WHERE user_id=$1 AND session_id=$2 AND turn_id=ANY($3::uuid[]) AND state='accepted' AND sequence<=$4 ORDER BY sequence", s.UserID, s.ID, t.Selected, t.Snapshot)
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
	if len(result) != len(t.Selected) {
		return nil, HistoryError("history_source_conflict")
	}
	return result, nil
}
func (r HistoryRepository) Complete(ctx context.Context, s HistorySession, t HistoryTurn, subject string) error {
	state := "pending"
	var receipt []byte
	var accepted []byte
	if t.Acceptance == "generated" {
		state = "accepted"
		receipt = Receipt(subject, s.ExternalID, t, "accept_generated")
		accepted = []byte(t.AssistantText)
	}
	tag, err := r.Conn.Exec(ctx, `UPDATE conversation_turns SET result=$4,response_id=$5,state=$6,assistant_text=$7,receipt=$8,terminal_at=CASE WHEN $6='accepted' THEN now() ELSE NULL END WHERE user_id=$1 AND session_id=$2 AND turn_id=$3 AND state='pending' AND expires_at>now()`, s.UserID, s.ID, t.ID, t.Result, t.ResponseID, state, accepted, receipt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return HistoryError("turn_expired")
	}
	return nil
}
func (r HistoryRepository) Accept(ctx context.Context, s HistorySession, t HistoryTurn, hash []byte, receipt []byte) error {
	tag, err := r.Conn.Exec(ctx, `UPDATE conversation_turns SET state='accepted',assistant_text=$4,finalize_hash=$5,receipt=$6,terminal_at=now() WHERE user_id=$1 AND session_id=$2 AND turn_id=$3 AND state='pending' AND expires_at>now()`, s.UserID, s.ID, t.ID, []byte(t.AssistantText), hash, receipt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return HistoryError("turn_expired")
	}
	return nil
}
func (r HistoryRepository) Abandon(ctx context.Context, s HistorySession, t HistoryTurn, receipt []byte) error {
	_, err := r.Conn.Exec(ctx, `UPDATE conversation_turns SET state='abandoned',expired=(expires_at<=now()),terminal_at=now(),receipt=$4,request_hash=NULL,config_hash=NULL,finalize_hash=NULL,result=NULL,response_id=NULL,user_text=NULL,assistant_text=NULL,selected_turns=NULL,snapshot_sequence=NULL,context_generation=NULL,acceptance=NULL WHERE user_id=$1 AND session_id=$2 AND turn_id=$3 AND state='pending'`, s.UserID, s.ID, t.ID, receipt)
	return err
}

// Local acceptance and dependency links commit together; no draft is introduced.
func (r HistoryRepository) Local(ctx context.Context, s HistorySession, t HistoryTurn, subject string, link func(pgx.Tx) error) ([]byte, error) {
	tx, err := r.Conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err = link(tx); err != nil {
		return nil, err
	}
	if err = tx.QueryRow(ctx, "UPDATE sessions SET history_sequence=history_sequence+1 WHERE id=$1 AND user_id=$2 AND history_sequence<9007199254740991 RETURNING history_sequence", s.ID, s.UserID).Scan(&t.Sequence); err != nil {
		return nil, err
	}
	receipt := Receipt(subject, s.ExternalID, t, "accept_local")
	_, err = tx.Exec(ctx, `INSERT INTO conversation_turns(user_id,session_id,turn_id,sequence,state,terminal_at,user_text,assistant_text,finalize_hash,receipt) VALUES($1,$2,$3,$4,'accepted',now(),$5,$6,$7,$8)`, s.UserID, s.ID, t.ID, t.Sequence, []byte(t.UserText), []byte(t.AssistantText), t.FinalizeHash, receipt)
	if err != nil {
		return nil, err
	}
	return receipt, tx.Commit(ctx)
}

// ObservationSegment rehydrates bounded accepted evidence at the immutable
// snapshot. It creates no transcript cache and cannot change replay selection.
// The caller owns the subject lock and rechecks fences before provider access.
func (r HistoryRepository) ObservationSegment(ctx context.Context, s HistorySession, t HistoryTurn, first int64) ([]HistoryTurn, error) {
	if err := CheckHistorySession(ctx, r.Conn, s.UserID, s.ExternalID); err != nil {
		return nil, err
	}
	rows, err := r.Conn.Query(ctx, "SELECT "+historyColumns+" FROM conversation_turns WHERE user_id=$1 AND session_id=$2 AND state='accepted' AND sequence >= $3 AND sequence <= $4 ORDER BY sequence LIMIT 129", s.UserID, s.ID, first, t.Snapshot)
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
	// At most 64 recent + 64 segment slots. Never silently truncate evidence.
	if len(result) > 128 {
		return nil, HistoryError("history_unavailable")
	}
	return result, nil
}

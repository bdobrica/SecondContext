package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bdobrica/SecondContext/internal/canonicaljson"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	memsvc "github.com/bdobrica/SecondContext/internal/memory"
	"github.com/bdobrica/SecondContext/internal/qdrant"
	"github.com/bdobrica/SecondContext/internal/retrieval"
	"github.com/jackc/pgx/v5"
)

type captureSource struct{ Key, Text string }

func chatCaptureMessages(request chatInput) []llm.Message {
	messages := append([]llm.Message(nil), request.text...)
	for i := range messages {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(request.messages[i], &fields)
		// Plain assistant text has the same identity across Chat and Responses,
		// including text-block versus string spellings. Tool structure still
		// distinguishes branches even when their displayed text is identical.
		if messages[i].Role == "tool" || (messages[i].Role == "assistant" && (len(fields["tool_calls"]) > 0 || len(fields["name"]) > 0)) {
			raw, _ := canonicaljson.Canonicalize(request.messages[i])
			messages[i].Content = string(raw)
		}
	}
	return messages
}

// Hash the ordered prefix, not the text alone. Equal reports at different positions
// or in different dialogue have different identities. No conversation is inferred
// from an API key. An identical fresh transcript without an ID is ambiguous.
func suppliedCaptureSources(messages []llm.Message, scope string) []captureSource {
	h := sha256.New()
	_, _ = h.Write([]byte("conversational-learning-1:" + scope))
	var sources []captureSource
	for _, message := range messages {
		if message.Role == "system" || message.Role == "developer" {
			continue
		}
		raw, _ := json.Marshal(message)
		_, _ = h.Write(raw)
		if message.Role == "user" && strings.TrimSpace(message.Content) != "" {
			sources = append(sources, captureSource{Key: hex.EncodeToString(h.Sum(nil)), Text: message.Content})
		}
	}
	return sources
}

func (s *Server) enqueueLearning(r *http.Request, request createResponseRequest, messages []llm.Message, sessionExternal, turn string) string {
	subject := authenticatedSubject(r.Context())
	if subject == "" || s.dbPool == nil {
		return "disabled"
	}
	// The answer is already generated. A disconnected caller must not cancel the
	// small durable handoff, but the handoff never includes an LLM/index call.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()
	conn, ok := r.Context().Value(subjectConnectionKey{}).(*pgx.Conn)
	if !ok {
		return "disabled"
	}
	var user string
	var enabled bool
	err := conn.QueryRow(ctx, `SELECT u.id::text,l.enabled FROM users u JOIN learning_settings l ON l.user_id=u.id WHERE u.external_id=$1`, subject).Scan(&user, &enabled)
	temporary := request.TemporaryChat || boolFromMap(request.Metadata, "temporary_chat")
	if errors.Is(err, pgx.ErrNoRows) && temporary {
		// An explicit temporary request must not become learnable merely because
		// the account first enables learning later and resends this transcript.
		err = conn.QueryRow(ctx, `INSERT INTO users(external_id,display_name) VALUES($1,$1) ON CONFLICT(external_id) DO UPDATE SET external_id=EXCLUDED.external_id RETURNING id::text`, subject).Scan(&user)
		if err == nil {
			_, err = conn.Exec(ctx, `INSERT INTO learning_settings(user_id,enabled) VALUES($1,false) ON CONFLICT DO NOTHING`, user)
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "disabled"
	}
	if err != nil {
		s.logger.Warn("learning handoff failed")
		return "failed"
	}
	status := "pending"
	result := "pending"
	if !enabled || temporary {
		status = "suppressed"
		result = "paused"
		if temporary {
			result = "temporary"
		}
	}
	sources := suppliedCaptureSources(messages, sessionExternal)
	if turn != "" && len(messages) > 0 {
		sources = []captureSource{{Key: "managed:" + sessionExternal + ":" + turn, Text: messages[len(messages)-1].Content}}
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return "failed"
	}
	defer tx.Rollback(ctx)
	var session string
	if sessionExternal != "" {
		err = tx.QueryRow(ctx, "SELECT id::text FROM sessions WHERE user_id=$1 AND external_id=$2", user, sessionExternal).Scan(&session)
		if errors.Is(err, pgx.ErrNoRows) && turn == "" {
			err = tx.QueryRow(ctx, `INSERT INTO sessions(user_id,external_id) VALUES($1,$2) ON CONFLICT(external_id) DO UPDATE SET external_id=EXCLUDED.external_id WHERE sessions.user_id=EXCLUDED.user_id RETURNING id::text`, user, sessionExternal).Scan(&session)
		}
		if err != nil {
			return "failed"
		}
	}
	inserted := int64(0)
	pendingInserted := int64(0)
	for _, source := range sources {
		text := source.Text
		sourceStatus := status
		if parseMemoryCommand([]llm.Message{{Role: "user", Content: text}}).Action != "" {
			sourceStatus = "suppressed"
		}
		if sourceStatus == "suppressed" {
			text = ""
		}
		tag, e := tx.Exec(ctx, `INSERT INTO learning_captures(user_id,session_id,turn_id,source_key,source_text,status,observed_at)
   VALUES($1,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,$4,$5,$6,
   COALESCE((SELECT created_at FROM conversation_turns WHERE user_id=$1 AND session_id=NULLIF($2,'')::uuid AND turn_id=NULLIF($3,'')::uuid),now()))
   ON CONFLICT(user_id,source_key) DO NOTHING`, user, session, turn, source.Key, text, sourceStatus)
		if e != nil {
			return "failed"
		}
		inserted += tag.RowsAffected()
		if sourceStatus == "pending" {
			pendingInserted += tag.RowsAffected()
		}
	}
	if tx.Commit(ctx) != nil {
		return "failed"
	}
	if inserted == 0 {
		return "unchanged"
	}
	if result == "pending" && pendingInserted == 0 {
		return "suppressed"
	}
	return result
}

// RunLearning is the restart-safe background consumer. Production owns its
// lifecycle; tests may call processLearningCapture directly without goroutines.
func (s *Server) RunLearning(ctx context.Context) {
	if s.dbPool == nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		ids, err := s.pendingLearning(ctx)
		if err != nil {
			s.logger.Warn("learning queue unavailable")
			continue
		}
		for _, id := range ids {
			if ctx.Err() != nil {
				return
			}
			work, cancel := context.WithTimeout(ctx, 90*time.Second)
			if err := s.processLearningCapture(work, id); err != nil {
				s.logger.Warn("learning capture failed", "capture_id", id)
			}
			cancel()
		}
	}
}

func (s *Server) pendingLearning(ctx context.Context) ([]string, error) {
	// Abandonment/expiry erases the queued copy as well as canonical history.
	_, err := s.dbPool.Exec(ctx, `UPDATE learning_captures c SET status='suppressed',source_text='',observations=NULL,error_code=''
	 FROM conversation_turns t WHERE t.user_id=c.user_id AND t.session_id=c.session_id AND t.turn_id=c.turn_id
	 AND c.status IN ('pending','failed') AND (t.state='abandoned' OR (t.state='pending' AND t.expires_at<=now()))`)
	if err != nil {
		return nil, err
	}
	rows, err := s.dbPool.Query(ctx, `SELECT c.id::text FROM learning_captures c JOIN learning_settings l ON l.user_id=c.user_id
  LEFT JOIN conversation_turns t ON t.user_id=c.user_id AND t.session_id=c.session_id AND t.turn_id=c.turn_id
  WHERE l.enabled AND c.status IN ('pending','failed') AND c.attempts<3 AND c.retry_at<=now()
   AND (c.turn_id IS NULL OR t.state='accepted') ORDER BY c.observed_at LIMIT 32`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type learningCapture struct {
	ID, User, Subject, Session, Text string
	Observed                         time.Time
	Observations                     []byte
}

func loadLearningCapture(ctx context.Context, conn *pgx.Conn, id string) (learningCapture, error) {
	var c learningCapture
	c.ID = id
	err := conn.QueryRow(ctx, `SELECT c.user_id::text,u.external_id,COALESCE(s.external_id,''),c.source_text,c.observed_at,c.observations
 FROM learning_captures c JOIN users u ON u.id=c.user_id JOIN learning_settings l ON l.user_id=c.user_id
 LEFT JOIN sessions s ON s.id=c.session_id
 LEFT JOIN conversation_turns t ON t.user_id=c.user_id AND t.session_id=c.session_id AND t.turn_id=c.turn_id
 WHERE c.id=$1 AND l.enabled AND c.status IN ('pending','failed') AND c.attempts<3 AND c.retry_at<=now() AND (c.turn_id IS NULL OR t.state='accepted')
 AND NOT EXISTS(SELECT 1 FROM subject_purges WHERE external_id=u.external_id)`, id).Scan(&c.User, &c.Subject, &c.Session, &c.Text, &c.Observed, &c.Observations)
	return c, err
}

func (s *Server) processLearningCapture(ctx context.Context, id string) (workErr error) {
	conn, err := pgx.ConnectConfig(ctx, s.dbPool.Config().ConnConfig.Copy())
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	var locked bool
	if err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1,0))", "secondcontext:capture:"+id).Scan(&locked); err != nil || !locked {
		return err
	}
	c, err := loadLearningCapture(ctx, conn, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1,0))", "secondcontext:learning-subject:"+c.Subject).Scan(&locked); err != nil || !locked {
		return err
	}
	// Errors are safe codes only; provider output and source text never enter logs
	// or error status. Conditional update cannot revive a paused/forgotten capture.
	defer func() {
		if workErr == nil {
			return
		}
		failureCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = conn.Exec(failureCtx, `UPDATE learning_captures SET status='failed',attempts=attempts+1,error_code='capture_failed',retry_at=now()+interval '30 seconds' WHERE id=$1 AND status IN ('pending','failed')`, id)
	}()
	if c.Session != "" {
		if err = db.CheckHistorySession(ctx, conn, c.User, c.Session); err != nil {
			return err
		}
	}
	var observations []memsvc.Observation
	if len(c.Observations) > 0 {
		err = json.Unmarshal(c.Observations, &observations)
	} else {
		observations, err = memsvc.ExtractObservations(ctx, s.llm, s.cfg.OpenAI.ChatModel, c.Text, c.Observed)
	}
	if err != nil {
		return err
	}
	// A pause/purge while extraction ran should stop subsequent provider stages,
	// as well as publication. The final fenced check still decides publication.
	if _, err = loadLearningCapture(ctx, conn, id); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if c.Session != "" {
		if err = db.CheckHistorySession(ctx, conn, c.User, c.Session); err != nil {
			return err
		}
	}
	plans, err := s.planObservationRelations(ctx, c, observations)
	if err != nil {
		return err
	}
	if _, err = loadLearningCapture(ctx, conn, id); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	vectors := make([][]float64, len(observations))
	for i, o := range observations {
		embedded, e := s.llm.Embed(ctx, llm.EmbedRequest{Model: s.cfg.OpenAI.EmbeddingModel, Input: o.Summary})
		if e != nil {
			return e
		}
		if len(embedded.Vector) == 0 {
			return fmt.Errorf("empty observation embedding")
		}
		vectors[i] = embedded.Vector
	}
	index := qdrant.NewClient(s.cfg.Qdrant)
	// Expensive extraction/embedding ran outside the subject lock. Revalidate all
	// consent, deletion and acceptance fences before publishing any personal memory.
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended($1,0))", "secondcontext:subject:"+c.Subject); err != nil {
		return err
	}
	c, err = loadLearningCapture(ctx, conn, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.Session != "" {
		if err = db.CheckHistorySession(ctx, conn, c.User, c.Session); err != nil {
			return err
		}
	}
	if len(vectors) > 0 {
		if err = index.EnsureCollection(ctx, s.cfg.Qdrant.Collection, len(vectors[0])); err != nil {
			return err
		}
	}
	raw, _ := json.Marshal(observations)
	if _, err = conn.Exec(ctx, "UPDATE learning_captures SET observations=$2 WHERE id=$1", id, raw); err != nil {
		return err
	}
	repo := db.NewMemoryRepositoryWithQueries(conn)
	recordIDs := make([]string, len(observations))
	for i, o := range observations {
		metadata, _ := json.Marshal(map[string]any{"capture_id": id, "observation_index": i, "source_reference": "capture:" + id, "speaker": "user", "observed_at": c.Observed.UTC().Format(time.RFC3339Nano), "evidence": o.Evidence, "source_quote": o.Quote, "time_scope": o.TimeScope})
		var sessionID string
		if c.Session != "" {
			if err = conn.QueryRow(ctx, "SELECT id::text FROM sessions WHERE user_id=$1 AND external_id=$2", c.User, c.Session).Scan(&sessionID); err != nil {
				return err
			}
		}
		record, e := repo.Create(ctx, db.CreateMemoryItemParams{CaptureID: id, UserID: c.User, SessionID: sessionID, MemoryType: o.Type, Source: "conversation", RawText: o.Quote, Summary: o.Summary, People: o.People, Topics: o.Topics, Importance: 0.5, Utility: 0.5, Confidence: 1, ExpiresAt: o.ExpiresAt, Metadata: metadata, IdempotencyKey: fmt.Sprintf("capture:%s:%d", id, i)})
		if e != nil {
			return e
		}
		recordIDs[i] = record.ID
		state, e := repo.GetProcessingState(ctx, record.ID)
		if e != nil {
			return e
		}
		if state.QdrantStatus == "completed" {
			continue
		}
		if err = index.UpsertPoint(ctx, s.cfg.Qdrant.Collection, qdrant.Point{ID: record.ID, DenseVector: vectors[i], SparseVector: retrieval.BuildSparseVector(record.Summary), Payload: map[string]any{"memory_id": record.ID, "user_id": record.UserID, "summary": record.Summary, "type": record.MemoryType, "people": record.People, "topics": record.Topics, "expires_at": record.ExpiresAt}}); err != nil {
			return err
		}
		if err = repo.UpdateQdrantPointID(ctx, record.ID, record.ID); err != nil {
			return err
		}
		if err = repo.UpdateProcessingStage(ctx, record.ID, "qdrant", "completed", ""); err != nil {
			return err
		}
		// These stages are deliberately skipped, rather than pretending modeling ran.
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for i, recordID := range recordIDs {
		if err = publishObservationRelations(ctx, tx, c.User, recordID, plans[i]); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE learning_captures SET status='completed',source_text='',observations=NULL,error_code='' WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Server) handleLearning(w http.ResponseWriter, r *http.Request) {
	if authenticatedSubject(r.Context()) == "" {
		s.historyError(w, r, 401, "authentication_required")
		return
	}
	if s.dbPool == nil {
		s.historyError(w, r, 503, "learning_unavailable")
		return
	}
	user, _, err := s.actorUser(r.Context())
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	if r.Method == http.MethodPut {
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if !s.decodeJSONRequest(w, r, &body, true) {
			return
		}
		if body.Enabled == nil {
			s.historyError(w, r, 400, "invalid_learning_setting")
			return
		}
		err = s.setLearning(r.Context(), user.ID, *body.Enabled)
		if err != nil {
			s.writeMemoryError(w, r, err)
			return
		}
	}
	var enabled bool
	err = s.dbPool.QueryRow(r.Context(), "SELECT enabled FROM learning_settings WHERE user_id=$1", user.ID).Scan(&enabled)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.writeMemoryError(w, r, err)
		return
	}
	rows, err := s.dbPool.Query(r.Context(), `SELECT c.id::text,
 CASE WHEN c.status IN ('pending','failed') AND t.state='abandoned' THEN 'suppressed'
 WHEN c.status IN ('pending','failed') AND c.turn_id IS NOT NULL AND t.state<>'accepted' THEN 'waiting_acceptance' ELSE c.status END,
 c.observed_at,c.attempts,c.error_code FROM learning_captures c
 LEFT JOIN conversation_turns t ON t.user_id=c.user_id AND t.session_id=c.session_id AND t.turn_id=c.turn_id
 WHERE c.user_id=$1 ORDER BY c.observed_at DESC LIMIT 100`, user.ID)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	defer rows.Close()
	captures := []map[string]any{}
	for rows.Next() {
		var id, status, code string
		var observed time.Time
		var attempts int
		if err = rows.Scan(&id, &status, &observed, &attempts, &code); err != nil {
			s.writeMemoryError(w, r, err)
			return
		}
		captures = append(captures, map[string]any{"id": id, "status": status, "observed_at": observed, "attempts": attempts, "error_code": code})
	}
	if err = rows.Err(); err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": enabled, "captures": captures, "recall": "Only completed captures are ready; pending work may be unavailable to immediate follow-ups."})
}

func (s *Server) handleLearningRetry(w http.ResponseWriter, r *http.Request) {
	if authenticatedSubject(r.Context()) == "" {
		s.historyError(w, r, 401, "authentication_required")
		return
	}
	if s.dbPool == nil {
		s.historyError(w, r, 503, "learning_unavailable")
		return
	}
	// Retrying is scoped to the authenticated account, and never revives suppressed
	// or completed work (including forgotten observations).
	_, err := s.dbPool.Exec(r.Context(), `UPDATE learning_captures c SET status='pending',attempts=0,error_code='',retry_at=now()
 FROM users u,learning_settings l WHERE c.user_id=u.id AND l.user_id=u.id AND l.enabled AND u.external_id=$1 AND c.status='failed'`, authenticatedSubject(r.Context()))
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	s.handleLearning(w, r)
}

func (s *Server) setLearning(ctx context.Context, user string, enabled bool) error {
	tx, err := s.dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO learning_settings(user_id,enabled) VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET enabled=EXCLUDED.enabled`, user, enabled)
	if err == nil && !enabled {
		_, err = tx.Exec(ctx, `UPDATE learning_captures SET status='suppressed',source_text='',observations=NULL,error_code='' WHERE user_id=$1 AND status IN ('pending','failed')`, user)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

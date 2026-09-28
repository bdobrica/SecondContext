package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/bdobrica/SecondContext/internal/qdrant"
	"github.com/jackc/pgx/v5"
)

// Hold a cross-process subject lock for the complete HTTP operation, including
// remote index/LLM calls. A dedicated connection avoids exhausting the repository
// pool with lock waiters. Closing it releases the lock even after cancellation.
func (s *Server) subjectFence(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subject := authenticatedSubject(r.Context())
		if subject == "" || s.dbPool == nil {
			next.ServeHTTP(w, r)
			return
		}
		conn, err := pgx.ConnectConfig(r.Context(), s.dbPool.Config().ConnConfig.Copy())
		if err != nil {
			s.writeAPIError(w, r, 503, "subject storage unavailable", "server_error", "subject_unavailable", "")
			return
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = conn.Close(ctx)
		}()
		_, err = conn.Exec(r.Context(), "SELECT pg_advisory_lock(hashtextextended($1, 0))", "secondcontext:subject:"+subject)
		if err != nil {
			s.writeAPIError(w, r, 503, "subject storage unavailable", "server_error", "subject_unavailable", "")
			return
		}
		var blocked bool
		err = conn.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM subject_purges WHERE external_id=$1)", subject).Scan(&blocked)
		if err != nil {
			s.writeAPIError(w, r, 503, "subject storage unavailable", "server_error", "subject_unavailable", "")
			return
		}
		if blocked && r.URL.Path != "/v1/subjects/purge" {
			s.writeAPIError(w, r, 410, "subject has been deleted or is being deleted", "invalid_request_error", "subject_deleted", "")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handlePurgeSubject(w http.ResponseWriter, r *http.Request) {
	// Destructive subject operations never infer a dev/default user.
	subject := authenticatedSubject(r.Context())
	if subject == "" {
		s.writeAPIError(w, r, 401, "authentication required", "authentication_error", "authentication_required", "")
		return
	}
	if s.dbPool == nil {
		s.writeAPIError(w, r, 503, "subject storage unavailable", "server_error", "subject_unavailable", "")
		return
	}
	var request struct {
		User string `json:"user"`
	}
	if !s.decodeJSONRequest(w, r, &request, true) {
		return
	}
	if request.User == "" {
		s.writeAPIError(w, r, 400, "user is required", "invalid_request_error", "missing_user", "user")
		return
	}
	if _, err := s.resolveUserExternalID(r.Context(), requestUserSelector{Param: "user", Value: request.User}); err != nil {
		s.writeRequestScopeError(w, r, err)
		return
	}
	// Durable before touching Qdrant. A failure leaves the subject fenced; retries
	// repeat the filter deletion, including after remote success/local commit loss.
	_, err := s.dbPool.Exec(r.Context(), "INSERT INTO subject_purges(external_id) VALUES($1) ON CONFLICT DO NOTHING", subject)
	if err != nil {
		s.purgeFailed(w, r)
		return
	}
	var userID string
	err = s.dbPool.QueryRow(r.Context(), "SELECT id::text FROM users WHERE external_id=$1", subject).Scan(&userID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.purgeFailed(w, r)
		return
	}
	if userID != "" {
		if err = qdrant.NewClient(s.cfg.Qdrant).DeleteSubject(r.Context(), s.cfg.Qdrant.Collection, userID); err != nil {
			s.purgeFailed(w, r)
			return
		}
	}
	tx, err := s.dbPool.Begin(r.Context())
	if err != nil {
		s.purgeFailed(w, r)
		return
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(ctx)
	}()
	if _, err = tx.Exec(r.Context(), "DELETE FROM users WHERE external_id=$1", subject); err != nil {
		s.purgeFailed(w, r)
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE subject_purges SET completed_at=COALESCE(completed_at,now()) WHERE external_id=$1", subject); err != nil {
		s.purgeFailed(w, r)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.purgeFailed(w, r)
		return
	}
	writeJSON(w, 200, map[string]any{"contract_version": 1, "user": subject, "status": "completed"})
}

func (s *Server) purgeFailed(w http.ResponseWriter, r *http.Request) {
	s.writeAPIError(w, r, 503, "subject purge incomplete; retry the same subject", "server_error", "purge_incomplete", "")
}

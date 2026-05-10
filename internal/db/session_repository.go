package db

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bdobrica/SecondContext/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SessionRepository struct {
	pool *pgxpool.Pool
}

type CreateSessionParams struct {
	UserID     string
	ExternalID string
	Title      string
	Metadata   json.RawMessage
}

func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

func (r *SessionRepository) Create(ctx context.Context, params CreateSessionParams) (models.Session, error) {
	query := `
		INSERT INTO sessions (user_id, external_id, title, metadata)
		VALUES ($1::uuid, NULLIF($2, ''), NULLIF($3, ''), $4::jsonb)
		RETURNING id::text, user_id::text, COALESCE(external_id, ''), COALESCE(title, ''), metadata, created_at, updated_at
	`

	var session models.Session
	var metadata []byte
	err := r.pool.QueryRow(ctx, query,
		params.UserID,
		strings.TrimSpace(params.ExternalID),
		strings.TrimSpace(params.Title),
		normalizeJSON(params.Metadata),
	).Scan(
		&session.ID,
		&session.UserID,
		&session.ExternalID,
		&session.Title,
		&metadata,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	session.Metadata = scanJSON(metadata)

	return session, err
}

func (r *SessionRepository) GetByID(ctx context.Context, sessionID string) (models.Session, error) {
	query := `
		SELECT id::text, user_id::text, COALESCE(external_id, ''), COALESCE(title, ''), metadata, created_at, updated_at
		FROM sessions
		WHERE id = $1::uuid
	`

	var session models.Session
	var metadata []byte
	err := r.pool.QueryRow(ctx, query, sessionID).Scan(
		&session.ID,
		&session.UserID,
		&session.ExternalID,
		&session.Title,
		&metadata,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	session.Metadata = scanJSON(metadata)

	return session, err
}

func (r *SessionRepository) GetByExternalID(ctx context.Context, externalID string) (models.Session, error) {
	query := `
		SELECT id::text, user_id::text, COALESCE(external_id, ''), COALESCE(title, ''), metadata, created_at, updated_at
		FROM sessions
		WHERE external_id = $1
	`

	var session models.Session
	var metadata []byte
	err := r.pool.QueryRow(ctx, query, strings.TrimSpace(externalID)).Scan(
		&session.ID,
		&session.UserID,
		&session.ExternalID,
		&session.Title,
		&metadata,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	session.Metadata = scanJSON(metadata)

	return session, err
}

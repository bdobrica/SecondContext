package db

import (
	"context"
	"encoding/json"

	"github.com/bdobrica/SecondContext/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MessageRepository struct {
	pool *pgxpool.Pool
}

type CreateMessageParams struct {
	SessionID string
	UserID    string
	Role      string
	Content   string
	Model     string
	RequestID string
	Metadata  json.RawMessage
}

func NewMessageRepository(pool *pgxpool.Pool) *MessageRepository {
	return &MessageRepository{pool: pool}
}

func (r *MessageRepository) Create(ctx context.Context, params CreateMessageParams) (models.Message, error) {
	query := `
		INSERT INTO messages (session_id, user_id, role, content, model, request_id, metadata)
		VALUES ($1::uuid, $2::uuid, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7::jsonb)
		RETURNING id::text, session_id::text, user_id::text, role, content, COALESCE(model, ''), COALESCE(request_id, ''), metadata, created_at
	`

	var message models.Message
	var metadata []byte
	err := r.pool.QueryRow(ctx, query,
		params.SessionID,
		params.UserID,
		params.Role,
		params.Content,
		params.Model,
		params.RequestID,
		normalizeJSON(params.Metadata),
	).Scan(
		&message.ID,
		&message.SessionID,
		&message.UserID,
		&message.Role,
		&message.Content,
		&message.Model,
		&message.RequestID,
		&metadata,
		&message.CreatedAt,
	)
	message.Metadata = scanJSON(metadata)

	return message, err
}

func (r *MessageRepository) ListBySession(ctx context.Context, sessionID string, limit int32) ([]models.Message, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT id::text, session_id::text, user_id::text, role, content, COALESCE(model, ''), COALESCE(request_id, ''), metadata, created_at
		FROM messages
		WHERE session_id = $1::uuid
		ORDER BY created_at ASC
		LIMIT $2
	`

	rows, err := r.pool.Query(ctx, query, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]models.Message, 0, limit)
	for rows.Next() {
		var message models.Message
		var metadata []byte

		if err := rows.Scan(
			&message.ID,
			&message.SessionID,
			&message.UserID,
			&message.Role,
			&message.Content,
			&message.Model,
			&message.RequestID,
			&metadata,
			&message.CreatedAt,
		); err != nil {
			return nil, err
		}

		message.Metadata = scanJSON(metadata)
		messages = append(messages, message)
	}

	return messages, rows.Err()
}

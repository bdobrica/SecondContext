package db

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bdobrica/SecondContext/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TopicRepository struct {
	pool *pgxpool.Pool
}

type UpsertTopicParams struct {
	UserID   string
	Name     string
	Aliases  []string
	Metadata json.RawMessage
}

func NewTopicRepository(pool *pgxpool.Pool) *TopicRepository {
	return &TopicRepository{pool: pool}
}

func (r *TopicRepository) Upsert(ctx context.Context, params UpsertTopicParams) (models.Topic, error) {
	query := `
		INSERT INTO topics (user_id, name, normalized_name, aliases, metadata)
		VALUES ($1::uuid, $2, $3, $4, $5::jsonb)
		ON CONFLICT (user_id, normalized_name) DO UPDATE
		SET name = EXCLUDED.name,
			aliases = EXCLUDED.aliases,
			metadata = EXCLUDED.metadata,
			updated_at = now()
		RETURNING id::text, user_id::text, name, normalized_name, aliases, metadata, created_at, updated_at
	`

	var topic models.Topic
	var metadata []byte
	err := r.pool.QueryRow(ctx, query,
		params.UserID,
		strings.TrimSpace(params.Name),
		normalizeName(params.Name),
		params.Aliases,
		normalizeJSON(params.Metadata),
	).Scan(
		&topic.ID,
		&topic.UserID,
		&topic.Name,
		&topic.NormalizedName,
		&topic.Aliases,
		&metadata,
		&topic.CreatedAt,
		&topic.UpdatedAt,
	)
	topic.Metadata = scanJSON(metadata)

	return topic, err
}

func (r *TopicRepository) GetByName(ctx context.Context, userID, name string) (models.Topic, error) {
	query := `
		SELECT id::text, user_id::text, name, normalized_name, aliases, metadata, created_at, updated_at
		FROM topics
		WHERE user_id = $1::uuid AND normalized_name = $2
	`

	var topic models.Topic
	var metadata []byte
	err := r.pool.QueryRow(ctx, query, userID, normalizeName(name)).Scan(
		&topic.ID,
		&topic.UserID,
		&topic.Name,
		&topic.NormalizedName,
		&topic.Aliases,
		&metadata,
		&topic.CreatedAt,
		&topic.UpdatedAt,
	)
	topic.Metadata = scanJSON(metadata)

	return topic, err
}

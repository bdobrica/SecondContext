package db

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bdobrica/SecondContext/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MemoryEntityRepository struct {
	pool *pgxpool.Pool
}

type CreateMemoryEntityParams struct {
	MemoryItemID string
	EntityType   string
	EntityName   string
	Confidence   float64
	Metadata     json.RawMessage
}

func NewMemoryEntityRepository(pool *pgxpool.Pool) *MemoryEntityRepository {
	return &MemoryEntityRepository{pool: pool}
}

func (r *MemoryEntityRepository) Create(ctx context.Context, params CreateMemoryEntityParams) (models.MemoryEntity, error) {
	query := `
		INSERT INTO memory_entities (memory_item_id, entity_type, entity_name, normalized_name, confidence, metadata)
		VALUES ($1::uuid, $2, $3, $4, $5, $6::jsonb)
		RETURNING id::text, memory_item_id::text, entity_type, entity_name, normalized_name, confidence, metadata, created_at
	`

	var entity models.MemoryEntity
	var metadata []byte
	err := r.pool.QueryRow(ctx, query,
		params.MemoryItemID,
		strings.TrimSpace(params.EntityType),
		strings.TrimSpace(params.EntityName),
		normalizeName(params.EntityName),
		params.Confidence,
		normalizeJSON(params.Metadata),
	).Scan(
		&entity.ID,
		&entity.MemoryItemID,
		&entity.EntityType,
		&entity.EntityName,
		&entity.NormalizedName,
		&entity.Confidence,
		&metadata,
		&entity.CreatedAt,
	)
	entity.Metadata = scanJSON(metadata)

	return entity, err
}

func (r *MemoryEntityRepository) ListByMemoryItemID(ctx context.Context, memoryItemID string) ([]models.MemoryEntity, error) {
	query := `
		SELECT id::text, memory_item_id::text, entity_type, entity_name, normalized_name, confidence, metadata, created_at
		FROM memory_entities
		WHERE memory_item_id = $1::uuid
		ORDER BY created_at ASC
	`

	rows, err := r.pool.Query(ctx, query, memoryItemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entities := make([]models.MemoryEntity, 0)
	for rows.Next() {
		var entity models.MemoryEntity
		var metadata []byte
		if err := rows.Scan(
			&entity.ID,
			&entity.MemoryItemID,
			&entity.EntityType,
			&entity.EntityName,
			&entity.NormalizedName,
			&entity.Confidence,
			&metadata,
			&entity.CreatedAt,
		); err != nil {
			return nil, err
		}
		entity.Metadata = scanJSON(metadata)
		entities = append(entities, entity)
	}

	return entities, rows.Err()
}

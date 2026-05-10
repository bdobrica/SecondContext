package db

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdobrica/SecondContext/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MemoryRepository struct {
	pool *pgxpool.Pool
}

type CreateMemoryItemParams struct {
	UserID          string
	SessionID       string
	SourceMessageID string
	QdrantPointID   string
	MemoryType      string
	Source          string
	RawText         string
	Summary         string
	People          []string
	Topics          []string
	Importance      float64
	Utility         float64
	BeliefImpact    float64
	Confidence      float64
	ExpiresAt       *time.Time
	Metadata        json.RawMessage
}

func NewMemoryRepository(pool *pgxpool.Pool) *MemoryRepository {
	return &MemoryRepository{pool: pool}
}

func (r *MemoryRepository) Create(ctx context.Context, params CreateMemoryItemParams) (models.MemoryItem, error) {
	query := `
		INSERT INTO memory_items (
			user_id,
			session_id,
			source_message_id,
			qdrant_point_id,
			memory_type,
			source,
			raw_text,
			summary,
			people,
			topics,
			importance,
			utility,
			belief_impact,
			confidence,
			expires_at,
			metadata
		)
		VALUES (
			$1::uuid,
			NULLIF($2, '')::uuid,
			NULLIF($3, '')::uuid,
			NULLIF($4, ''),
			$5,
			$6,
			$7,
			$8,
			$9,
			$10,
			$11,
			$12,
			$13,
			$14,
			$15,
			$16::jsonb
		)
		RETURNING id::text, user_id::text, COALESCE(session_id::text, ''), COALESCE(source_message_id::text, ''), COALESCE(qdrant_point_id, ''), memory_type, source, raw_text, summary, people, topics, importance, utility, belief_impact, confidence, expires_at, metadata, created_at, updated_at
	`

	var memory models.MemoryItem
	var metadata []byte
	err := r.pool.QueryRow(ctx, query,
		params.UserID,
		params.SessionID,
		params.SourceMessageID,
		params.QdrantPointID,
		params.MemoryType,
		defaultMemorySource(params.Source),
		params.RawText,
		params.Summary,
		params.People,
		params.Topics,
		params.Importance,
		params.Utility,
		params.BeliefImpact,
		params.Confidence,
		params.ExpiresAt,
		normalizeJSON(params.Metadata),
	).Scan(
		&memory.ID,
		&memory.UserID,
		&memory.SessionID,
		&memory.SourceMessageID,
		&memory.QdrantPointID,
		&memory.MemoryType,
		&memory.Source,
		&memory.RawText,
		&memory.Summary,
		&memory.People,
		&memory.Topics,
		&memory.Importance,
		&memory.Utility,
		&memory.BeliefImpact,
		&memory.Confidence,
		&memory.ExpiresAt,
		&metadata,
		&memory.CreatedAt,
		&memory.UpdatedAt,
	)
	memory.Metadata = scanJSON(metadata)

	return memory, err
}

func (r *MemoryRepository) GetByID(ctx context.Context, memoryID string) (models.MemoryItem, error) {
	query := `
		SELECT id::text, user_id::text, COALESCE(session_id::text, ''), COALESCE(source_message_id::text, ''), COALESCE(qdrant_point_id, ''), memory_type, source, raw_text, summary, people, topics, importance, utility, belief_impact, confidence, expires_at, metadata, created_at, updated_at
		FROM memory_items
		WHERE id = $1::uuid
	`

	var memory models.MemoryItem
	var metadata []byte
	err := r.pool.QueryRow(ctx, query, memoryID).Scan(
		&memory.ID,
		&memory.UserID,
		&memory.SessionID,
		&memory.SourceMessageID,
		&memory.QdrantPointID,
		&memory.MemoryType,
		&memory.Source,
		&memory.RawText,
		&memory.Summary,
		&memory.People,
		&memory.Topics,
		&memory.Importance,
		&memory.Utility,
		&memory.BeliefImpact,
		&memory.Confidence,
		&memory.ExpiresAt,
		&metadata,
		&memory.CreatedAt,
		&memory.UpdatedAt,
	)
	memory.Metadata = scanJSON(metadata)

	return memory, err
}

func (r *MemoryRepository) ListByUser(ctx context.Context, userID string, limit int32) ([]models.MemoryItem, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT id::text, user_id::text, COALESCE(session_id::text, ''), COALESCE(source_message_id::text, ''), COALESCE(qdrant_point_id, ''), memory_type, source, raw_text, summary, people, topics, importance, utility, belief_impact, confidence, expires_at, metadata, created_at, updated_at
		FROM memory_items
		WHERE user_id = $1::uuid
		ORDER BY created_at DESC
		LIMIT $2
	`

	rows, err := r.pool.Query(ctx, query, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	memories := make([]models.MemoryItem, 0, limit)
	for rows.Next() {
		var memory models.MemoryItem
		var metadata []byte

		if err := rows.Scan(
			&memory.ID,
			&memory.UserID,
			&memory.SessionID,
			&memory.SourceMessageID,
			&memory.QdrantPointID,
			&memory.MemoryType,
			&memory.Source,
			&memory.RawText,
			&memory.Summary,
			&memory.People,
			&memory.Topics,
			&memory.Importance,
			&memory.Utility,
			&memory.BeliefImpact,
			&memory.Confidence,
			&memory.ExpiresAt,
			&metadata,
			&memory.CreatedAt,
			&memory.UpdatedAt,
		); err != nil {
			return nil, err
		}

		memory.Metadata = scanJSON(metadata)
		memories = append(memories, memory)
	}

	return memories, rows.Err()
}

func defaultMemorySource(value string) string {
	if value == "" {
		return "manual"
	}

	return value
}

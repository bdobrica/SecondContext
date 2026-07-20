package db

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bdobrica/SecondContext/internal/models"
	"github.com/jackc/pgx/v5"
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
	IdempotencyKey  string
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
			metadata,
			idempotency_key
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
			$16::jsonb,
			NULLIF($17, '')
		)
		ON CONFLICT (user_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO UPDATE
		SET idempotency_key = EXCLUDED.idempotency_key
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
		strings.TrimSpace(params.IdempotencyKey),
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

type MemoryProcessingState struct {
	QdrantStatus      string
	PersonModelStatus string
	BeliefStatus      string
	ProcessingError   string
}

func (r *MemoryRepository) GetProcessingState(ctx context.Context, memoryID string) (MemoryProcessingState, error) {
	var state MemoryProcessingState
	err := r.pool.QueryRow(ctx, `
		SELECT qdrant_status, person_model_status, belief_status, COALESCE(processing_error, '')
		FROM memory_items WHERE id = $1::uuid
	`, memoryID).Scan(&state.QdrantStatus, &state.PersonModelStatus, &state.BeliefStatus, &state.ProcessingError)
	return state, err
}

func (r *MemoryRepository) UpdateProcessingStage(ctx context.Context, memoryID, stage, status, processingError string) error {
	var column string
	switch stage {
	case "qdrant":
		column = "qdrant_status"
	case "person_model":
		column = "person_model_status"
	case "belief":
		column = "belief_status"
	default:
		return fmt.Errorf("unknown memory processing stage %q", stage)
	}
	query := fmt.Sprintf(`UPDATE memory_items SET %s = $2, processing_error = NULLIF($3, '') WHERE id = $1::uuid`, column)
	tag, err := r.pool.Exec(ctx, query, memoryID, status, processingError)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
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

func (r *MemoryRepository) ListBySession(ctx context.Context, sessionID string, limit int32) ([]models.MemoryItem, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT id::text, user_id::text, COALESCE(session_id::text, ''), COALESCE(source_message_id::text, ''), COALESCE(qdrant_point_id, ''), memory_type, source, raw_text, summary, people, topics, importance, utility, belief_impact, confidence, expires_at, metadata, created_at, updated_at
		FROM memory_items
		WHERE session_id = $1::uuid
		ORDER BY created_at DESC
		LIMIT $2
	`

	rows, err := r.pool.Query(ctx, query, sessionID, limit)
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

func (r *MemoryRepository) ListByIDs(ctx context.Context, ids []string) ([]models.MemoryItem, error) {
	if len(ids) == 0 {
		return []models.MemoryItem{}, nil
	}

	query := `
		SELECT id::text, user_id::text, COALESCE(session_id::text, ''), COALESCE(source_message_id::text, ''), COALESCE(qdrant_point_id, ''), memory_type, source, raw_text, summary, people, topics, importance, utility, belief_impact, confidence, expires_at, metadata, created_at, updated_at
		FROM memory_items
		WHERE id = ANY($1::uuid[])
	`

	rows, err := r.pool.Query(ctx, query, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	memories := make([]models.MemoryItem, 0, len(ids))
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
	if err := rows.Err(); err != nil {
		return nil, err
	}

	order := make(map[string]int, len(ids))
	for index, id := range ids {
		order[id] = index
	}
	sort.Slice(memories, func(i, j int) bool {
		return order[memories[i].ID] < order[memories[j].ID]
	})

	return memories, nil
}

func (r *MemoryRepository) Delete(ctx context.Context, memoryID string) error {
	commandTag, err := r.pool.Exec(ctx, `DELETE FROM memory_items WHERE id = $1::uuid`, memoryID)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}

	return nil
}

func (r *MemoryRepository) UpdateQdrantPointID(ctx context.Context, memoryID, pointID string) error {
	commandTag, err := r.pool.Exec(ctx, `UPDATE memory_items SET qdrant_point_id = NULLIF($2, '') WHERE id = $1::uuid`, memoryID, pointID)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}

	return nil
}

func defaultMemorySource(value string) string {
	if value == "" {
		return "manual"
	}

	return value
}

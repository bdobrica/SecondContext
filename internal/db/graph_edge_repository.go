package db

import (
	"context"
	"encoding/json"

	"github.com/bdobrica/SecondContext/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type GraphEdgeRepository struct {
	pool *pgxpool.Pool
}

type CreateGraphEdgeParams struct {
	UserID            string
	SourceKind        string
	SourceName        string
	TargetKind        string
	TargetName        string
	Relationship      string
	Confidence        float64
	EvidenceMemoryIDs []string
	Metadata          json.RawMessage
}

func NewGraphEdgeRepository(pool *pgxpool.Pool) *GraphEdgeRepository {
	return &GraphEdgeRepository{pool: pool}
}

func (r *GraphEdgeRepository) Create(ctx context.Context, params CreateGraphEdgeParams) (models.GraphEdge, error) {
	query := `
		INSERT INTO graph_edges (
			user_id,
			source_kind,
			source_name,
			target_kind,
			target_name,
			relationship,
			confidence,
			evidence_memory_ids,
			metadata
		)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8::uuid[], $9::jsonb)
		ON CONFLICT (
			user_id,
			lower(source_kind),
			lower(source_name),
			lower(target_kind),
			lower(target_name),
			lower(relationship),
			((metadata ->> 'outcome_id'))
		) WHERE metadata ->> 'outcome_id' IS NOT NULL
		DO UPDATE SET
			confidence = EXCLUDED.confidence,
			evidence_memory_ids = EXCLUDED.evidence_memory_ids,
			metadata = EXCLUDED.metadata,
			updated_at = now()
		RETURNING id::text, user_id::text, source_kind, source_name, target_kind, target_name, relationship, confidence,
			COALESCE(ARRAY(SELECT value::text FROM unnest(evidence_memory_ids) AS value), ARRAY[]::text[]), metadata, created_at, updated_at
	`

	return r.scanOne(ctx, query,
		params.UserID,
		params.SourceKind,
		params.SourceName,
		params.TargetKind,
		params.TargetName,
		params.Relationship,
		params.Confidence,
		emptyStringSlice(params.EvidenceMemoryIDs),
		normalizeJSON(params.Metadata),
	)
}

func (r *GraphEdgeRepository) ListByUser(ctx context.Context, userID string, limit int32) ([]models.GraphEdge, error) {
	query := `
		SELECT id::text, user_id::text, source_kind, source_name, target_kind, target_name, relationship, confidence,
			COALESCE(ARRAY(SELECT value::text FROM unnest(evidence_memory_ids) AS value), ARRAY[]::text[]), metadata, created_at, updated_at
		FROM graph_edges
		WHERE user_id = $1::uuid
		ORDER BY created_at DESC
		LIMIT $2
	`

	rows, err := r.pool.Query(ctx, query, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	edges := make([]models.GraphEdge, 0)
	for rows.Next() {
		edge, err := scanGraphEdge(rows)
		if err != nil {
			return nil, err
		}
		edges = append(edges, edge)
	}

	return edges, rows.Err()
}

func (r *GraphEdgeRepository) scanOne(ctx context.Context, query string, args ...any) (models.GraphEdge, error) {
	row := r.pool.QueryRow(ctx, query, args...)
	return scanGraphEdge(row)
}

type graphEdgeRow interface {
	Scan(dest ...any) error
}

func scanGraphEdge(row graphEdgeRow) (models.GraphEdge, error) {
	var edge models.GraphEdge
	var metadata []byte
	err := row.Scan(
		&edge.ID,
		&edge.UserID,
		&edge.SourceKind,
		&edge.SourceName,
		&edge.TargetKind,
		&edge.TargetName,
		&edge.Relationship,
		&edge.Confidence,
		&edge.EvidenceMemoryIDs,
		&metadata,
		&edge.CreatedAt,
		&edge.UpdatedAt,
	)
	edge.Metadata = scanJSON(metadata)

	return edge, err
}

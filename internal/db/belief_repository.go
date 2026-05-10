package db

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/bdobrica/SecondContext/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type BeliefRepository struct {
	pool *pgxpool.Pool
}

type SaveBeliefParams struct {
	ID                string
	UserID            string
	TopicID           string
	Claim             string
	Stance            string
	Confidence        float64
	EvidenceMemoryIDs []string
	LastUpdatedAt     time.Time
	Metadata          json.RawMessage
}

func NewBeliefRepository(pool *pgxpool.Pool) *BeliefRepository {
	return &BeliefRepository{pool: pool}
}

func (r *BeliefRepository) Save(ctx context.Context, params SaveBeliefParams) (models.Belief, error) {
	if strings.TrimSpace(params.ID) == "" {
		return r.insert(ctx, params)
	}

	query := `
		UPDATE beliefs
		SET topic_id = NULLIF($2, '')::uuid,
			claim = $3,
			normalized_claim = $4,
			stance = $5,
			confidence = $6,
			evidence_memory_ids = $7::uuid[],
			last_updated_at = $8,
			metadata = $9::jsonb,
			updated_at = now()
		WHERE id = $1::uuid
		RETURNING id::text, user_id::text, COALESCE(topic_id::text, ''), claim, normalized_claim, stance, confidence,
			COALESCE(ARRAY(SELECT value::text FROM unnest(evidence_memory_ids) AS value), ARRAY[]::text[]),
			last_updated_at, metadata, created_at, updated_at
	`

	return r.scanOne(ctx, query,
		params.ID,
		strings.TrimSpace(params.TopicID),
		strings.TrimSpace(params.Claim),
		normalizeName(params.Claim),
		strings.TrimSpace(params.Stance),
		params.Confidence,
		emptyStringSlice(params.EvidenceMemoryIDs),
		params.LastUpdatedAt,
		normalizeJSON(params.Metadata),
	)
}

func (r *BeliefRepository) insert(ctx context.Context, params SaveBeliefParams) (models.Belief, error) {
	query := `
		INSERT INTO beliefs (
			user_id,
			topic_id,
			claim,
			normalized_claim,
			stance,
			confidence,
			evidence_memory_ids,
			last_updated_at,
			metadata
		)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7::uuid[], $8, $9::jsonb)
		RETURNING id::text, user_id::text, COALESCE(topic_id::text, ''), claim, normalized_claim, stance, confidence,
			COALESCE(ARRAY(SELECT value::text FROM unnest(evidence_memory_ids) AS value), ARRAY[]::text[]),
			last_updated_at, metadata, created_at, updated_at
	`

	return r.scanOne(ctx, query,
		params.UserID,
		strings.TrimSpace(params.TopicID),
		strings.TrimSpace(params.Claim),
		normalizeName(params.Claim),
		strings.TrimSpace(params.Stance),
		params.Confidence,
		emptyStringSlice(params.EvidenceMemoryIDs),
		params.LastUpdatedAt,
		normalizeJSON(params.Metadata),
	)
}

func (r *BeliefRepository) GetByClaimAndTopic(ctx context.Context, userID, claim, topicID string) (models.Belief, error) {
	query := `
		SELECT id::text, user_id::text, COALESCE(topic_id::text, ''), claim, normalized_claim, stance, confidence,
			COALESCE(ARRAY(SELECT value::text FROM unnest(evidence_memory_ids) AS value), ARRAY[]::text[]),
			last_updated_at, metadata, created_at, updated_at
		FROM beliefs
		WHERE user_id = $1::uuid AND normalized_claim = $2
		AND ((NULLIF($3, '') IS NULL AND topic_id IS NULL) OR topic_id = NULLIF($3, '')::uuid)
		ORDER BY updated_at DESC
		LIMIT 1
	`

	return r.scanOne(ctx, query, userID, normalizeName(claim), strings.TrimSpace(topicID))
}

func (r *BeliefRepository) ListByTopic(ctx context.Context, userID, topicID string, limit int32) ([]models.Belief, error) {
	query := `
		SELECT id::text, user_id::text, COALESCE(topic_id::text, ''), claim, normalized_claim, stance, confidence,
			COALESCE(ARRAY(SELECT value::text FROM unnest(evidence_memory_ids) AS value), ARRAY[]::text[]),
			last_updated_at, metadata, created_at, updated_at
		FROM beliefs
		WHERE user_id = $1::uuid AND topic_id = $2::uuid
		ORDER BY last_updated_at DESC, confidence DESC, updated_at DESC
		LIMIT $3
	`

	rows, err := r.pool.Query(ctx, query, userID, strings.TrimSpace(topicID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	beliefs := make([]models.Belief, 0)
	for rows.Next() {
		belief, err := scanBelief(rows)
		if err != nil {
			return nil, err
		}
		beliefs = append(beliefs, belief)
	}

	return beliefs, rows.Err()
}

func (r *BeliefRepository) ListByUser(ctx context.Context, userID string, limit int32) ([]models.Belief, error) {
	query := `
		SELECT id::text, user_id::text, COALESCE(topic_id::text, ''), claim, normalized_claim, stance, confidence,
			COALESCE(ARRAY(SELECT value::text FROM unnest(evidence_memory_ids) AS value), ARRAY[]::text[]),
			last_updated_at, metadata, created_at, updated_at
		FROM beliefs
		WHERE user_id = $1::uuid
		ORDER BY last_updated_at DESC, confidence DESC, updated_at DESC
		LIMIT $2
	`

	rows, err := r.pool.Query(ctx, query, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	beliefs := make([]models.Belief, 0)
	for rows.Next() {
		belief, err := scanBelief(rows)
		if err != nil {
			return nil, err
		}
		beliefs = append(beliefs, belief)
	}

	return beliefs, rows.Err()
}

func (r *BeliefRepository) scanOne(ctx context.Context, query string, args ...any) (models.Belief, error) {
	row := r.pool.QueryRow(ctx, query, args...)
	return scanBelief(row)
}

type beliefRow interface {
	Scan(dest ...any) error
}

func scanBelief(row beliefRow) (models.Belief, error) {
	var belief models.Belief
	var metadata []byte
	err := row.Scan(
		&belief.ID,
		&belief.UserID,
		&belief.TopicID,
		&belief.Claim,
		&belief.NormalizedClaim,
		&belief.Stance,
		&belief.Confidence,
		&belief.EvidenceMemoryIDs,
		&belief.LastUpdatedAt,
		&metadata,
		&belief.CreatedAt,
		&belief.UpdatedAt,
	)
	belief.Metadata = scanJSON(metadata)

	return belief, err
}

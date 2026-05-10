package db

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdobrica/SecondContext/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PersonTopicModelRepository struct {
	pool *pgxpool.Pool
}

type SavePersonTopicModelParams struct {
	UserID         string
	PersonID       string
	TopicID        string
	Niceness       float64
	Readiness      float64
	Competence     float64
	Capacity       float64
	Confidence     float64
	EvidenceCount  int
	LastObservedAt *time.Time
	Metadata       json.RawMessage
}

func NewPersonTopicModelRepository(pool *pgxpool.Pool) *PersonTopicModelRepository {
	return &PersonTopicModelRepository{pool: pool}
}

func (r *PersonTopicModelRepository) Save(ctx context.Context, params SavePersonTopicModelParams) (models.PersonTopicModel, error) {
	query := `
		INSERT INTO person_topic_models (
			user_id,
			person_id,
			topic_id,
			niceness,
			readiness,
			competence,
			capacity,
			confidence,
			evidence_count,
			last_observed_at,
			metadata
		)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11::jsonb)
		ON CONFLICT (user_id, person_id, topic_id) DO UPDATE
		SET niceness = EXCLUDED.niceness,
			readiness = EXCLUDED.readiness,
			competence = EXCLUDED.competence,
			capacity = EXCLUDED.capacity,
			confidence = EXCLUDED.confidence,
			evidence_count = EXCLUDED.evidence_count,
			last_observed_at = EXCLUDED.last_observed_at,
			metadata = EXCLUDED.metadata,
			updated_at = now()
		RETURNING id::text, user_id::text, person_id::text, topic_id::text, niceness, readiness, competence, capacity, confidence, evidence_count, last_observed_at, metadata, created_at, updated_at
	`

	return r.scanOne(ctx, query,
		params.UserID,
		params.PersonID,
		params.TopicID,
		params.Niceness,
		params.Readiness,
		params.Competence,
		params.Capacity,
		params.Confidence,
		params.EvidenceCount,
		params.LastObservedAt,
		normalizeJSON(params.Metadata),
	)
}

func (r *PersonTopicModelRepository) GetByPersonAndTopic(ctx context.Context, userID, personID, topicID string) (models.PersonTopicModel, error) {
	query := `
		SELECT id::text, user_id::text, person_id::text, topic_id::text, niceness, readiness, competence, capacity, confidence, evidence_count, last_observed_at, metadata, created_at, updated_at
		FROM person_topic_models
		WHERE user_id = $1::uuid AND person_id = $2::uuid AND topic_id = $3::uuid
	`

	return r.scanOne(ctx, query, userID, personID, topicID)
}

func (r *PersonTopicModelRepository) ListByPerson(ctx context.Context, userID, personID string) ([]models.PersonTopicModel, error) {
	query := `
		SELECT id::text, user_id::text, person_id::text, topic_id::text, niceness, readiness, competence, capacity, confidence, evidence_count, last_observed_at, metadata, created_at, updated_at
		FROM person_topic_models
		WHERE user_id = $1::uuid AND person_id = $2::uuid
		ORDER BY confidence DESC, evidence_count DESC, updated_at DESC
	`

	rows, err := r.pool.Query(ctx, query, userID, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	modelsList := make([]models.PersonTopicModel, 0)
	for rows.Next() {
		model, err := scanPersonTopicModel(rows)
		if err != nil {
			return nil, err
		}
		modelsList = append(modelsList, model)
	}

	return modelsList, rows.Err()
}

func (r *PersonTopicModelRepository) scanOne(ctx context.Context, query string, args ...any) (models.PersonTopicModel, error) {
	row := r.pool.QueryRow(ctx, query, args...)
	return scanPersonTopicModel(row)
}

type personTopicModelRow interface {
	Scan(dest ...any) error
}

func scanPersonTopicModel(row personTopicModelRow) (models.PersonTopicModel, error) {
	var model models.PersonTopicModel
	var metadata []byte
	err := row.Scan(
		&model.ID,
		&model.UserID,
		&model.PersonID,
		&model.TopicID,
		&model.Niceness,
		&model.Readiness,
		&model.Competence,
		&model.Capacity,
		&model.Confidence,
		&model.EvidenceCount,
		&model.LastObservedAt,
		&metadata,
		&model.CreatedAt,
		&model.UpdatedAt,
	)
	model.Metadata = scanJSON(metadata)

	return model, err
}

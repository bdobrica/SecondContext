package db

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bdobrica/SecondContext/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type InteractionOutcomeRepository struct {
	pool *pgxpool.Pool
}

type CreateInteractionOutcomeParams struct {
	UserID           string
	SessionID        string
	MessageID        string
	PersonID         string
	TopicID          string
	Goal             string
	PredictedOutcome string
	ActualOutcome    string
	SuccessScore     float64
	PredictionError  string
	Metadata         json.RawMessage
}

func NewInteractionOutcomeRepository(pool *pgxpool.Pool) *InteractionOutcomeRepository {
	return &InteractionOutcomeRepository{pool: pool}
}

func (r *InteractionOutcomeRepository) Create(ctx context.Context, params CreateInteractionOutcomeParams) (models.InteractionOutcome, error) {
	query := `
		INSERT INTO interaction_outcomes (
			user_id,
			session_id,
			message_id,
			person_id,
			topic_id,
			goal,
			predicted_outcome,
			actual_outcome,
			success_score,
			prediction_error,
			metadata
		)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, NULLIF($5, '')::uuid, NULLIF($6, ''), NULLIF($7, ''), $8, $9, NULLIF($10, ''), $11::jsonb)
		RETURNING id::text, user_id::text, COALESCE(session_id::text, ''), COALESCE(message_id::text, ''), COALESCE(person_id::text, ''), COALESCE(topic_id::text, ''),
			COALESCE(goal, ''), COALESCE(predicted_outcome, ''), actual_outcome, success_score, COALESCE(prediction_error, ''), metadata, created_at, updated_at
	`

	return r.scanOne(ctx, query,
		params.UserID,
		strings.TrimSpace(params.SessionID),
		strings.TrimSpace(params.MessageID),
		strings.TrimSpace(params.PersonID),
		strings.TrimSpace(params.TopicID),
		strings.TrimSpace(params.Goal),
		strings.TrimSpace(params.PredictedOutcome),
		strings.TrimSpace(params.ActualOutcome),
		params.SuccessScore,
		strings.TrimSpace(params.PredictionError),
		normalizeJSON(params.Metadata),
	)
}

func (r *InteractionOutcomeRepository) GetByID(ctx context.Context, outcomeID string) (models.InteractionOutcome, error) {
	query := `
		SELECT id::text, user_id::text, COALESCE(session_id::text, ''), COALESCE(message_id::text, ''), COALESCE(person_id::text, ''), COALESCE(topic_id::text, ''),
			COALESCE(goal, ''), COALESCE(predicted_outcome, ''), actual_outcome, success_score, COALESCE(prediction_error, ''), metadata, created_at, updated_at
		FROM interaction_outcomes
		WHERE id = $1::uuid
	`

	return r.scanOne(ctx, query, strings.TrimSpace(outcomeID))
}

func (r *InteractionOutcomeRepository) scanOne(ctx context.Context, query string, args ...any) (models.InteractionOutcome, error) {
	row := r.pool.QueryRow(ctx, query, args...)
	return scanInteractionOutcome(row)
}

type interactionOutcomeRow interface {
	Scan(dest ...any) error
}

func scanInteractionOutcome(row interactionOutcomeRow) (models.InteractionOutcome, error) {
	var outcome models.InteractionOutcome
	var metadata []byte
	err := row.Scan(
		&outcome.ID,
		&outcome.UserID,
		&outcome.SessionID,
		&outcome.MessageID,
		&outcome.PersonID,
		&outcome.TopicID,
		&outcome.Goal,
		&outcome.PredictedOutcome,
		&outcome.ActualOutcome,
		&outcome.SuccessScore,
		&outcome.PredictionError,
		&metadata,
		&outcome.CreatedAt,
		&outcome.UpdatedAt,
	)
	outcome.Metadata = scanJSON(metadata)

	return outcome, err
}

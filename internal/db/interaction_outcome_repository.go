package db

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bdobrica/SecondContext/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type InteractionOutcomeRepository struct {
	db outcomeDB
}

type outcomeDB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
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
	IdempotencyKey   string
	RequestHash      string
}

func NewInteractionOutcomeRepository(pool *pgxpool.Pool) *InteractionOutcomeRepository {
	return NewInteractionOutcomeRepositoryWithDB(pool)
}

func NewInteractionOutcomeRepositoryWithDB(database outcomeDB) *InteractionOutcomeRepository {
	return &InteractionOutcomeRepository{db: database}
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
			metadata,
			idempotency_key,
			request_hash
		)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, NULLIF($5, '')::uuid, NULLIF($6, ''), NULLIF($7, ''), $8, $9, NULLIF($10, ''), $11::jsonb, NULLIF($12, ''), NULLIF($13, ''))
		ON CONFLICT (user_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO UPDATE
		SET idempotency_key = EXCLUDED.idempotency_key
		RETURNING id::text, user_id::text, COALESCE(session_id::text, ''), COALESCE(message_id::text, ''), COALESCE(person_id::text, ''), COALESCE(topic_id::text, ''),
			COALESCE(goal, ''), COALESCE(predicted_outcome, ''), actual_outcome, success_score, COALESCE(prediction_error, ''), metadata,
			COALESCE(idempotency_key, ''), COALESCE(request_hash, ''), COALESCE(memory_id::text, ''), processing_status,
			COALESCE(failed_stage, ''), COALESCE(processing_error, ''), created_at, updated_at
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
		strings.TrimSpace(params.IdempotencyKey),
		strings.TrimSpace(params.RequestHash),
	)
}

func (r *InteractionOutcomeRepository) GetByIdempotencyKey(ctx context.Context, userID, key string) (models.InteractionOutcome, error) {
	query := `
		SELECT id::text, user_id::text, COALESCE(session_id::text, ''), COALESCE(message_id::text, ''), COALESCE(person_id::text, ''), COALESCE(topic_id::text, ''),
			COALESCE(goal, ''), COALESCE(predicted_outcome, ''), actual_outcome, success_score, COALESCE(prediction_error, ''), metadata,
			COALESCE(idempotency_key, ''), COALESCE(request_hash, ''), COALESCE(memory_id::text, ''), processing_status,
			COALESCE(failed_stage, ''), COALESCE(processing_error, ''), created_at, updated_at
		FROM interaction_outcomes
		WHERE user_id = $1::uuid AND idempotency_key = $2
	`
	return r.scanOne(ctx, query, userID, strings.TrimSpace(key))
}

func (r *InteractionOutcomeRepository) UpdateProcessing(ctx context.Context, outcomeID, status, stage, processingError, memoryID string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE interaction_outcomes
		SET processing_status = $2,
			failed_stage = NULLIF($3, ''),
			processing_error = NULLIF($4, ''),
			memory_id = COALESCE(NULLIF($5, '')::uuid, memory_id)
		WHERE id = $1::uuid
	`, outcomeID, status, stage, processingError, memoryID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (r *InteractionOutcomeRepository) Complete(ctx context.Context, outcomeID, memoryID, personID, topicID string, graphEdges int) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE interaction_outcomes
		SET processing_status = 'completed',
			failed_stage = NULL,
			processing_error = NULL,
			memory_id = $2::uuid,
			person_id = NULLIF($3, '')::uuid,
			topic_id = NULLIF($4, '')::uuid,
			metadata = metadata || jsonb_build_object(
				'outcome_memory_id', $2,
				'graph_edges_created', $5::integer
			)
		WHERE id = $1::uuid
	`, outcomeID, memoryID, strings.TrimSpace(personID), strings.TrimSpace(topicID), graphEdges)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (r *InteractionOutcomeRepository) GetByID(ctx context.Context, outcomeID string) (models.InteractionOutcome, error) {
	query := `
		SELECT id::text, user_id::text, COALESCE(session_id::text, ''), COALESCE(message_id::text, ''), COALESCE(person_id::text, ''), COALESCE(topic_id::text, ''),
			COALESCE(goal, ''), COALESCE(predicted_outcome, ''), actual_outcome, success_score, COALESCE(prediction_error, ''), metadata,
			COALESCE(idempotency_key, ''), COALESCE(request_hash, ''), COALESCE(memory_id::text, ''), processing_status,
			COALESCE(failed_stage, ''), COALESCE(processing_error, ''), created_at, updated_at
		FROM interaction_outcomes
		WHERE id = $1::uuid
	`

	return r.scanOne(ctx, query, strings.TrimSpace(outcomeID))
}

func (r *InteractionOutcomeRepository) ListBySession(ctx context.Context, sessionID string, limit int32) ([]models.InteractionOutcome, error) {
	if limit <= 0 {
		limit = 20
	}

	query := `
		SELECT id::text, user_id::text, COALESCE(session_id::text, ''), COALESCE(message_id::text, ''), COALESCE(person_id::text, ''), COALESCE(topic_id::text, ''),
			COALESCE(goal, ''), COALESCE(predicted_outcome, ''), actual_outcome, success_score, COALESCE(prediction_error, ''), metadata,
			COALESCE(idempotency_key, ''), COALESCE(request_hash, ''), COALESCE(memory_id::text, ''), processing_status,
			COALESCE(failed_stage, ''), COALESCE(processing_error, ''), created_at, updated_at
		FROM interaction_outcomes
		WHERE session_id = $1::uuid
		ORDER BY created_at DESC
		LIMIT $2
	`

	rows, err := r.db.Query(ctx, query, strings.TrimSpace(sessionID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	outcomes := make([]models.InteractionOutcome, 0, limit)
	for rows.Next() {
		outcome, err := scanInteractionOutcome(rows)
		if err != nil {
			return nil, err
		}
		outcomes = append(outcomes, outcome)
	}

	return outcomes, rows.Err()
}

func (r *InteractionOutcomeRepository) scanOne(ctx context.Context, query string, args ...any) (models.InteractionOutcome, error) {
	row := r.db.QueryRow(ctx, query, args...)
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
		&outcome.IdempotencyKey,
		&outcome.RequestHash,
		&outcome.MemoryID,
		&outcome.ProcessingStatus,
		&outcome.FailedStage,
		&outcome.ProcessingError,
		&outcome.CreatedAt,
		&outcome.UpdatedAt,
	)
	outcome.Metadata = scanJSON(metadata)

	return outcome, err
}

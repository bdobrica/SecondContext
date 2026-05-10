package db

import (
	"context"
	"errors"
	"strings"

	"github.com/bdobrica/SecondContext/internal/models"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UserRepository struct {
	pool *pgxpool.Pool
}

type EnsureUserParams struct {
	ExternalID  string
	Email       string
	DisplayName string
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

func (r *UserRepository) Ensure(ctx context.Context, params EnsureUserParams) (models.User, error) {
	return r.ensureWithEmail(ctx, params, strings.TrimSpace(params.Email))
}

func (r *UserRepository) ensureWithEmail(ctx context.Context, params EnsureUserParams, email string) (models.User, error) {
	query := `
		INSERT INTO users (external_id, email, display_name)
		VALUES ($1, NULLIF($2, ''), $3)
		ON CONFLICT (external_id) DO UPDATE
		SET email = COALESCE(EXCLUDED.email, users.email),
			display_name = EXCLUDED.display_name,
			updated_at = now()
		RETURNING id::text, external_id, COALESCE(email, ''), display_name, created_at, updated_at
	`

	var user models.User
	err := r.pool.QueryRow(ctx, query,
		strings.TrimSpace(params.ExternalID),
		email,
		strings.TrimSpace(params.DisplayName),
	).Scan(
		&user.ID,
		&user.ExternalID,
		&user.Email,
		&user.DisplayName,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil && email != "" && isUsersEmailConflict(err) {
		return r.ensureWithEmail(ctx, params, "")
	}

	return user, err
}

func isUsersEmailConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == "users_email_key"
}

func (r *UserRepository) GetByExternalID(ctx context.Context, externalID string) (models.User, error) {
	query := `
		SELECT id::text, external_id, COALESCE(email, ''), display_name, created_at, updated_at
		FROM users
		WHERE external_id = $1
	`

	var user models.User
	err := r.pool.QueryRow(ctx, query, strings.TrimSpace(externalID)).Scan(
		&user.ID,
		&user.ExternalID,
		&user.Email,
		&user.DisplayName,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	return user, err
}

func (r *UserRepository) GetByID(ctx context.Context, userID string) (models.User, error) {
	query := `
		SELECT id::text, external_id, COALESCE(email, ''), display_name, created_at, updated_at
		FROM users
		WHERE id = $1::uuid
	`

	var user models.User
	err := r.pool.QueryRow(ctx, query, strings.TrimSpace(userID)).Scan(
		&user.ID,
		&user.ExternalID,
		&user.Email,
		&user.DisplayName,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	return user, err
}

package db

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bdobrica/SecondContext/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PersonRepository struct {
	pool *pgxpool.Pool
}

type UpsertPersonParams struct {
	UserID   string
	Name     string
	Aliases  []string
	Metadata json.RawMessage
}

func NewPersonRepository(pool *pgxpool.Pool) *PersonRepository {
	return &PersonRepository{pool: pool}
}

func (r *PersonRepository) Upsert(ctx context.Context, params UpsertPersonParams) (models.Person, error) {
	query := `
		INSERT INTO people (user_id, name, normalized_name, aliases, metadata)
		VALUES ($1::uuid, $2, $3, $4, $5::jsonb)
		ON CONFLICT (user_id, normalized_name) DO UPDATE
		SET name = EXCLUDED.name,
			aliases = EXCLUDED.aliases,
			metadata = EXCLUDED.metadata,
			updated_at = now()
		RETURNING id::text, user_id::text, name, normalized_name, aliases, metadata, created_at, updated_at
	`

	var person models.Person
	var metadata []byte
	err := r.pool.QueryRow(ctx, query,
		params.UserID,
		strings.TrimSpace(params.Name),
		normalizeName(params.Name),
		emptyStringSlice(params.Aliases),
		normalizeJSON(params.Metadata),
	).Scan(
		&person.ID,
		&person.UserID,
		&person.Name,
		&person.NormalizedName,
		&person.Aliases,
		&metadata,
		&person.CreatedAt,
		&person.UpdatedAt,
	)
	person.Metadata = scanJSON(metadata)

	return person, err
}

func (r *PersonRepository) GetByName(ctx context.Context, userID, name string) (models.Person, error) {
	query := `
		SELECT id::text, user_id::text, name, normalized_name, aliases, metadata, created_at, updated_at
		FROM people
		WHERE user_id = $1::uuid AND normalized_name = $2
	`

	var person models.Person
	var metadata []byte
	err := r.pool.QueryRow(ctx, query, userID, normalizeName(name)).Scan(
		&person.ID,
		&person.UserID,
		&person.Name,
		&person.NormalizedName,
		&person.Aliases,
		&metadata,
		&person.CreatedAt,
		&person.UpdatedAt,
	)
	person.Metadata = scanJSON(metadata)

	return person, err
}

func (r *PersonRepository) GetByID(ctx context.Context, personID string) (models.Person, error) {
	query := `
		SELECT id::text, user_id::text, name, normalized_name, aliases, metadata, created_at, updated_at
		FROM people
		WHERE id = $1::uuid
	`

	var person models.Person
	var metadata []byte
	err := r.pool.QueryRow(ctx, query, strings.TrimSpace(personID)).Scan(
		&person.ID,
		&person.UserID,
		&person.Name,
		&person.NormalizedName,
		&person.Aliases,
		&metadata,
		&person.CreatedAt,
		&person.UpdatedAt,
	)
	person.Metadata = scanJSON(metadata)

	return person, err
}

package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	"github.com/bdobrica/SecondContext/internal/models"
	"github.com/bdobrica/SecondContext/internal/qdrant"
	"github.com/bdobrica/SecondContext/internal/retrieval"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	cfg    config.Config
	pool   *pgxpool.Pool
	llm    llm.Client
	qdrant *qdrant.Client
}

type Error struct {
	StatusCode int
	Message    string
	Type       string
	Code       string
	Param      string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}

	return e.Message
}

type RequestMetadata struct {
	SessionID      string
	UserExternalID string
	UserName       string
	UserEmail      string
	SessionTitle   string
}

type IngestParams struct {
	RawText       string
	Summary       string
	MemoryType    string
	Source        string
	People        []string
	Topics        []string
	Importance    *float64
	Utility       *float64
	BeliefImpact  *float64
	Confidence    *float64
	ExpiresInDays *int
	Metadata      map[string]any
	RequestUser   string
	Meta          RequestMetadata
}

type ListParams struct {
	UserExternalID string
	Limit          int32
}

type Record = models.MemoryItem

func NewService(cfg config.Config, pool *pgxpool.Pool, client llm.Client) *Service {
	return &Service{cfg: cfg, pool: pool, llm: client, qdrant: qdrant.NewClient(cfg.Qdrant)}
}

func (s *Service) Ingest(ctx context.Context, params IngestParams) (Record, error) {
	if s.pool == nil {
		return Record{}, &Error{StatusCode: http.StatusInternalServerError, Message: "postgres is not configured", Type: "server_error", Code: "postgres_disabled"}
	}
	if strings.TrimSpace(params.RawText) == "" {
		return Record{}, &Error{StatusCode: http.StatusBadRequest, Message: "raw_text is required", Type: "invalid_request_error", Code: "missing_raw_text", Param: "raw_text"}
	}
	if strings.TrimSpace(params.MemoryType) == "" {
		return Record{}, &Error{StatusCode: http.StatusBadRequest, Message: "type is required", Type: "invalid_request_error", Code: "missing_type", Param: "type"}
	}

	user, session, err := s.resolveUserSession(ctx, params.Meta, params.RequestUser)
	if err != nil {
		return Record{}, err
	}

	summary := strings.TrimSpace(params.Summary)
	if summary == "" {
		summary = strings.TrimSpace(params.RawText)
	}

	embedding, err := s.llm.Embed(ctx, llm.EmbedRequest{
		Model: s.cfg.OpenAI.EmbeddingModel,
		Input: summary,
	})
	if err != nil {
		return Record{}, &Error{StatusCode: http.StatusBadGateway, Message: "failed to generate embedding", Type: "server_error", Code: "embedding_failed"}
	}
	if len(embedding.Vector) == 0 {
		return Record{}, &Error{StatusCode: http.StatusBadGateway, Message: "embedding vector was empty", Type: "server_error", Code: "embedding_empty"}
	}

	if err := s.qdrant.EnsureCollection(ctx, s.cfg.Qdrant.Collection, len(embedding.Vector)); err != nil {
		return Record{}, &Error{StatusCode: http.StatusBadGateway, Message: "failed to ensure qdrant collection", Type: "server_error", Code: "qdrant_collection_failed"}
	}

	people := uniqueValues(params.People)
	topics := uniqueValues(params.Topics)
	if err := s.upsertTaxonomy(ctx, user.ID, people, topics); err != nil {
		return Record{}, err
	}

	metadataBytes, err := json.Marshal(mapOrEmpty(params.Metadata))
	if err != nil {
		return Record{}, err
	}

	var expiresAt *time.Time
	if params.ExpiresInDays != nil && *params.ExpiresInDays > 0 {
		timestamp := time.Now().UTC().Add(time.Duration(*params.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &timestamp
	}

	memories := db.NewMemoryRepository(s.pool)
	record, err := memories.Create(ctx, db.CreateMemoryItemParams{
		UserID:       user.ID,
		SessionID:    session.ID,
		MemoryType:   strings.TrimSpace(params.MemoryType),
		Source:       fallbackSource(params.Source),
		RawText:      strings.TrimSpace(params.RawText),
		Summary:      summary,
		People:       people,
		Topics:       topics,
		Importance:   clampScore(params.Importance),
		Utility:      clampScore(params.Utility),
		BeliefImpact: clampScore(params.BeliefImpact),
		Confidence:   clampScore(params.Confidence),
		ExpiresAt:    expiresAt,
		Metadata:     metadataBytes,
	})
	if err != nil {
		return Record{}, err
	}

	pointID := record.ID
	sparseVector := retrieval.BuildSparseVector(record.Summary)

	if err := s.qdrant.UpsertPoint(ctx, s.cfg.Qdrant.Collection, qdrant.Point{
		ID:           pointID,
		DenseVector:  embedding.Vector,
		SparseVector: sparseVector,
		Payload: map[string]any{
			"memory_id":     record.ID,
			"user_id":       record.UserID,
			"summary":       record.Summary,
			"type":          record.MemoryType,
			"people":        record.People,
			"topics":        record.Topics,
			"importance":    record.Importance,
			"utility":       record.Utility,
			"belief_impact": record.BeliefImpact,
			"confidence":    record.Confidence,
			"expires_at":    expiresAtPayload(expiresAt),
		},
	}); err != nil {
		_ = memories.Delete(ctx, record.ID)
		return Record{}, &Error{StatusCode: http.StatusBadGateway, Message: "failed to index memory in qdrant", Type: "server_error", Code: "qdrant_upsert_failed"}
	}

	if err := memories.UpdateQdrantPointID(ctx, record.ID, pointID); err != nil {
		_ = s.qdrant.DeletePoint(ctx, s.cfg.Qdrant.Collection, pointID)
		_ = memories.Delete(ctx, record.ID)
		return Record{}, err
	}
	record.QdrantPointID = pointID

	return record, nil
}

func (s *Service) List(ctx context.Context, params ListParams) ([]Record, error) {
	user, err := s.resolveUser(ctx, params.UserExternalID)
	if err != nil {
		return nil, err
	}

	return db.NewMemoryRepository(s.pool).ListByUser(ctx, user.ID, params.Limit)
}

func (s *Service) Delete(ctx context.Context, memoryID string) error {
	memories := db.NewMemoryRepository(s.pool)
	record, err := memories.GetByID(ctx, memoryID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return &Error{StatusCode: http.StatusNotFound, Message: "memory not found", Type: "invalid_request_error", Code: "memory_not_found", Param: "memoryID"}
		}
		return err
	}

	if record.QdrantPointID != "" {
		if err := s.qdrant.DeletePoint(ctx, s.cfg.Qdrant.Collection, record.QdrantPointID); err != nil {
			return &Error{StatusCode: http.StatusBadGateway, Message: "failed to delete memory from qdrant", Type: "server_error", Code: "qdrant_delete_failed"}
		}
	}

	return memories.Delete(ctx, memoryID)
}

func (s *Service) resolveUserSession(ctx context.Context, meta RequestMetadata, requestUser string) (models.User, models.Session, error) {
	user, err := s.resolveUser(ctx, firstNonEmpty(meta.UserExternalID, requestUser))
	if err != nil {
		return models.User{}, models.Session{}, err
	}

	if strings.TrimSpace(meta.SessionID) == "" {
		return user, models.Session{}, nil
	}

	sessions := db.NewSessionRepository(s.pool)
	session, err := sessions.GetByExternalID(ctx, strings.TrimSpace(meta.SessionID))
	if err == nil {
		return user, session, nil
	}
	if err != pgx.ErrNoRows {
		return models.User{}, models.Session{}, err
	}

	session, err = sessions.Create(ctx, db.CreateSessionParams{
		UserID:     user.ID,
		ExternalID: strings.TrimSpace(meta.SessionID),
		Title:      firstNonEmpty(meta.SessionTitle, "Memory Ingestion"),
		Metadata:   json.RawMessage(`{"source":"memory.ingest"}`),
	})
	if err != nil {
		return models.User{}, models.Session{}, err
	}

	return user, session, nil
}

func (s *Service) resolveUser(ctx context.Context, externalID string) (models.User, error) {
	resolvedExternalID := firstNonEmpty(externalID, s.cfg.Dev.UserExternalID)
	resolvedName := s.cfg.Dev.UserName
	resolvedEmail := s.cfg.Dev.UserEmail
	if resolvedExternalID != s.cfg.Dev.UserExternalID {
		resolvedName = resolvedExternalID
		resolvedEmail = ""
	}

	return db.NewUserRepository(s.pool).Ensure(ctx, db.EnsureUserParams{
		ExternalID:  resolvedExternalID,
		Email:       resolvedEmail,
		DisplayName: resolvedName,
	})
}

func (s *Service) upsertTaxonomy(ctx context.Context, userID string, people, topics []string) error {
	peopleRepo := db.NewPersonRepository(s.pool)
	for _, person := range people {
		if _, err := peopleRepo.Upsert(ctx, db.UpsertPersonParams{UserID: userID, Name: person}); err != nil {
			return err
		}
	}

	topicRepo := db.NewTopicRepository(s.pool)
	for _, topic := range topics {
		if _, err := topicRepo.Upsert(ctx, db.UpsertTopicParams{UserID: userID, Name: topic}); err != nil {
			return err
		}
	}

	return nil
}

func uniqueValues(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}

	return result
}

func clampScore(value *float64) float64 {
	if value == nil {
		return 0
	}
	if *value < 0 {
		return 0
	}
	if *value > 1 {
		return 1
	}

	return *value
}

func mapOrEmpty(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}

	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

func fallbackSource(value string) string {
	if strings.TrimSpace(value) == "" {
		return "manual"
	}

	return strings.TrimSpace(value)
}

func expiresAtPayload(value *time.Time) any {
	if value == nil {
		return nil
	}

	return value.UTC().Format(time.RFC3339)
}

package retrieval

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

type SearchParams struct {
	Query               string
	UserExternalID      string
	MemoryType          string
	People              []string
	Topics              []string
	ConfidenceThreshold *float64
	IncludeExpired      bool
	Limit               int
}

type ScoreBreakdown struct {
	Hybrid float64
	Dense  float64
	Sparse float64
}

type Result struct {
	Memory models.MemoryItem
	Scores ScoreBreakdown
}

func NewService(cfg config.Config, pool *pgxpool.Pool, client llm.Client) *Service {
	return &Service{cfg: cfg, pool: pool, llm: client, qdrant: qdrant.NewClient(cfg.Qdrant)}
}

func (s *Service) Search(ctx context.Context, params SearchParams) ([]Result, error) {
	if s.pool == nil {
		return nil, &Error{StatusCode: http.StatusInternalServerError, Message: "postgres is not configured", Type: "server_error", Code: "postgres_disabled"}
	}
	if strings.TrimSpace(params.Query) == "" {
		return nil, &Error{StatusCode: http.StatusBadRequest, Message: "query is required", Type: "invalid_request_error", Code: "missing_query", Param: "query"}
	}

	user, err := s.resolveUser(ctx, params.UserExternalID)
	if err != nil {
		return nil, err
	}

	dense, err := s.llm.Embed(ctx, llm.EmbedRequest{Model: s.cfg.OpenAI.EmbeddingModel, Input: strings.TrimSpace(params.Query)})
	if err != nil {
		return nil, &Error{StatusCode: http.StatusBadGateway, Message: "failed to generate query embedding", Type: "server_error", Code: "embedding_failed"}
	}
	sparse := BuildSparseVector(params.Query)
	limit := params.Limit
	if limit <= 0 {
		limit = 10
	}

	filter := buildFilter(user.ID, params)
	denseResults, err := s.qdrant.SearchDense(ctx, s.cfg.Qdrant.Collection, dense.Vector, limit*3, filter)
	if err != nil {
		return nil, &Error{StatusCode: http.StatusBadGateway, Message: "dense search failed", Type: "server_error", Code: "dense_search_failed"}
	}
	sparseResults, err := s.qdrant.SearchSparse(ctx, s.cfg.Qdrant.Collection, sparse, limit*3, filter)
	if err != nil {
		return nil, &Error{StatusCode: http.StatusBadGateway, Message: "sparse search failed", Type: "server_error", Code: "sparse_search_failed"}
	}
	hybridResults, err := s.qdrant.SearchHybrid(ctx, s.cfg.Qdrant.Collection, dense.Vector, sparse, limit, limit*3, filter)
	if err != nil {
		return nil, &Error{StatusCode: http.StatusBadGateway, Message: "hybrid search failed", Type: "server_error", Code: "hybrid_search_failed"}
	}

	idScores := make(map[string]ScoreBreakdown, len(hybridResults))
	ids := make([]string, 0, len(hybridResults))
	for _, result := range hybridResults {
		idScores[result.ID] = ScoreBreakdown{Hybrid: result.Score}
		ids = append(ids, result.ID)
	}
	for _, result := range denseResults {
		breakdown := idScores[result.ID]
		breakdown.Dense = result.Score
		idScores[result.ID] = breakdown
	}
	for _, result := range sparseResults {
		breakdown := idScores[result.ID]
		breakdown.Sparse = result.Score
		idScores[result.ID] = breakdown
	}

	memories, err := db.NewMemoryRepository(s.pool).ListByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]models.MemoryItem, len(memories))
	for _, memory := range memories {
		byID[memory.ID] = memory
	}

	results := make([]Result, 0, len(ids))
	now := time.Now().UTC()
	for _, id := range ids {
		memory, ok := byID[id]
		if !ok {
			continue
		}
		if !params.IncludeExpired && memory.ExpiresAt != nil && memory.ExpiresAt.Before(now) {
			continue
		}
		results = append(results, Result{Memory: memory, Scores: idScores[id]})
	}

	return results, nil
}

func buildFilter(userID string, params SearchParams) *qdrant.Filter {
	must := []map[string]any{{
		"key":   "user_id",
		"match": map[string]any{"value": userID},
	}}
	if strings.TrimSpace(params.MemoryType) != "" {
		must = append(must, map[string]any{
			"key":   "type",
			"match": map[string]any{"value": strings.TrimSpace(params.MemoryType)},
		})
	}
	if len(params.People) > 0 {
		must = append(must, map[string]any{
			"key":   "people",
			"match": map[string]any{"any": uniqueValues(params.People)},
		})
	}
	if len(params.Topics) > 0 {
		must = append(must, map[string]any{
			"key":   "topics",
			"match": map[string]any{"any": uniqueValues(params.Topics)},
		})
	}
	if params.ConfidenceThreshold != nil {
		must = append(must, map[string]any{
			"key":   "confidence",
			"range": map[string]any{"gte": clampScore(params.ConfidenceThreshold)},
		})
	}

	return &qdrant.Filter{Must: must}
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

func MetadataMap(raw json.RawMessage) map[string]any {
	values := make(map[string]any)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &values)
	}

	return values
}

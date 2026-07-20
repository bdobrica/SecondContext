package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	memsvc "github.com/bdobrica/SecondContext/internal/memory"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAuthenticatedUserCannotReuseForeignSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool, cleanup := openIsolationTestDB(t)
	defer cleanup()

	cfg := isolationTestConfig(t, "memory_items_isolation_session")
	server := NewServerWithClient(cfg, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), pool, &fakeLLMClient{response: llm.GenerateResponse{ID: "unused", Model: "gpt-4.1-mini", OutputText: "unused"}})

	foreignUser, err := db.NewUserRepository(pool).Ensure(context.Background(), db.EnsureUserParams{ExternalID: "tenant-b", DisplayName: "tenant-b"})
	if err != nil {
		t.Fatalf("ensure foreign user: %v", err)
	}
	foreignSessionID := fmt.Sprintf("foreign-session-%d", time.Now().UnixNano())
	if _, err := db.NewSessionRepository(pool).Create(context.Background(), db.CreateSessionParams{UserID: foreignUser.ID, ExternalID: foreignSessionID, Title: "Foreign Session"}); err != nil {
		t.Fatalf("create foreign session: %v", err)
	}

	body := []byte(fmt.Sprintf(`{"model":"context-agent-1","input":"Help me ask Alex to review the proposal.","metadata":{"session_id":"%s"}}`, foreignSessionID))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(authorizationHeaderName, "Bearer token-a")

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusNotFound, recorder.Code, recorder.Body.String())
	}
	assertAPIErrorCode(t, recorder.Body.Bytes(), "session_not_found")
}

func TestAuthenticatedResponseCannotSelectForeignTenantContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool, cleanup := openIsolationTestDB(t)
	defer cleanup()

	ctx := context.Background()
	cfg := isolationTestConfig(t, "memory_items_isolation_response_identity")
	fakeClient := &fakeLLMClient{response: llm.GenerateResponse{OutputText: "foreign marker from upstream"}}
	server := NewServerWithClient(cfg, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), pool, fakeClient)

	foreignUser, err := db.NewUserRepository(pool).Ensure(ctx, db.EnsureUserParams{ExternalID: "tenant-b", DisplayName: "tenant-b"})
	if err != nil {
		t.Fatalf("ensure foreign user: %v", err)
	}
	foreignMemory, err := db.NewMemoryRepository(pool).Create(ctx, db.CreateMemoryItemParams{
		UserID:     foreignUser.ID,
		MemoryType: "note",
		Source:     "test",
		RawText:    "tenant-b-private-memory-marker",
		Summary:    "tenant-b-private-memory-marker",
		People:     []string{"Tenant B Private Person"},
		Topics:     []string{"tenant-b-private-topic"},
		Confidence: 1,
	})
	if err != nil {
		t.Fatalf("create foreign memory: %v", err)
	}
	foreignPerson, err := db.NewPersonRepository(pool).Upsert(ctx, db.UpsertPersonParams{
		UserID: foreignUser.ID,
		Name:   "Tenant B Private Person",
	})
	if err != nil {
		t.Fatalf("create foreign person: %v", err)
	}
	foreignTopic, err := db.NewTopicRepository(pool).Upsert(ctx, db.UpsertTopicParams{
		UserID: foreignUser.ID,
		Name:   "tenant-b-private-topic",
	})
	if err != nil {
		t.Fatalf("create foreign topic: %v", err)
	}
	foreignBelief, err := db.NewBeliefRepository(pool).Save(ctx, db.SaveBeliefParams{
		UserID:            foreignUser.ID,
		TopicID:           foreignTopic.ID,
		Claim:             "tenant-b-private-belief-marker",
		Stance:            "supports",
		Confidence:        1,
		EvidenceMemoryIDs: []string{foreignMemory.ID},
		LastUpdatedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create foreign belief: %v", err)
	}
	var beforeMemories, beforePeople, beforeTopics, beforeBeliefs, beforeSessions, beforeMessages int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM memory_items WHERE user_id = $1::uuid),
			(SELECT count(*) FROM people WHERE user_id = $1::uuid),
			(SELECT count(*) FROM topics WHERE user_id = $1::uuid),
			(SELECT count(*) FROM beliefs WHERE user_id = $1::uuid),
			(SELECT count(*) FROM sessions WHERE user_id = $1::uuid),
			(SELECT count(*) FROM messages WHERE user_id = $1::uuid)
	`, foreignUser.ID).Scan(&beforeMemories, &beforePeople, &beforeTopics, &beforeBeliefs, &beforeSessions, &beforeMessages); err != nil {
		t.Fatalf("snapshot foreign state: %v", err)
	}

	tests := []struct {
		name string
		body string
	}{
		{
			name: "metadata user_external_id",
			body: `{"model":"context-agent-1","input":"tenant-b-private-memory-marker","metadata":{"user_external_id":"tenant-b"}}`,
		},
		{
			name: "top-level user",
			body: `{"model":"context-agent-1","input":"tenant-b-private-memory-marker","user":"tenant-b"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(authorizationHeaderName, "Bearer token-a")
			server.Handler().ServeHTTP(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d body=%s", http.StatusBadRequest, recorder.Code, recorder.Body.String())
			}
			assertAPIErrorCode(t, recorder.Body.Bytes(), "identity_conflict")
			for _, marker := range []string{"tenant-b-private-memory-marker", "Tenant B Private Person", "tenant-b-private-topic", "tenant-b-private-belief-marker", "context_packet", "foreign marker from upstream"} {
				if bytes.Contains(recorder.Body.Bytes(), []byte(marker)) {
					t.Fatalf("response disclosed foreign context marker %q: %s", marker, recorder.Body.String())
				}
			}
		})
	}

	if len(fakeClient.requests) != 0 || fakeClient.embedCount != 0 {
		t.Fatalf("conflicting requests reached retrieval/upstream: requests=%d embeds=%d", len(fakeClient.requests), fakeClient.embedCount)
	}
	storedMemory, err := db.NewMemoryRepository(pool).GetByID(ctx, foreignMemory.ID)
	if err != nil || storedMemory.UpdatedAt != foreignMemory.UpdatedAt {
		t.Fatalf("foreign memory changed: before=%#v after=%#v err=%v", foreignMemory, storedMemory, err)
	}
	storedPerson, err := db.NewPersonRepository(pool).GetByID(ctx, foreignPerson.ID)
	if err != nil || storedPerson.UpdatedAt != foreignPerson.UpdatedAt {
		t.Fatalf("foreign person changed: before=%#v after=%#v err=%v", foreignPerson, storedPerson, err)
	}
	storedBelief, err := db.NewBeliefRepository(pool).GetByClaimAndTopic(ctx, foreignUser.ID, foreignBelief.Claim, foreignTopic.ID)
	if err != nil || storedBelief.UpdatedAt != foreignBelief.UpdatedAt {
		t.Fatalf("foreign belief changed: before=%#v after=%#v err=%v", foreignBelief, storedBelief, err)
	}
	var afterMemories, afterPeople, afterTopics, afterBeliefs, afterSessions, afterMessages int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM memory_items WHERE user_id = $1::uuid),
			(SELECT count(*) FROM people WHERE user_id = $1::uuid),
			(SELECT count(*) FROM topics WHERE user_id = $1::uuid),
			(SELECT count(*) FROM beliefs WHERE user_id = $1::uuid),
			(SELECT count(*) FROM sessions WHERE user_id = $1::uuid),
			(SELECT count(*) FROM messages WHERE user_id = $1::uuid)
	`, foreignUser.ID).Scan(&afterMemories, &afterPeople, &afterTopics, &afterBeliefs, &afterSessions, &afterMessages); err != nil {
		t.Fatalf("verify foreign state: %v", err)
	}
	beforeCounts := [6]int{beforeMemories, beforePeople, beforeTopics, beforeBeliefs, beforeSessions, beforeMessages}
	afterCounts := [6]int{afterMemories, afterPeople, afterTopics, afterBeliefs, afterSessions, afterMessages}
	if afterCounts != beforeCounts {
		t.Fatalf("foreign state counts changed: before=%v after=%v", beforeCounts, afterCounts)
	}
}

func TestAuthenticatedUserCannotDeleteForeignMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool, cleanup := openIsolationTestDB(t)
	defer cleanup()

	cfg := isolationTestConfig(t, "memory_items_isolation_delete")
	qdrantServer := newFakeQdrantServer()
	defer qdrantServer.Close()
	cfg.Qdrant.URL = qdrantServer.URL
	server := NewServerWithClient(cfg, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), pool, &fakeLLMClient{embedResponse: llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}}})
	memoryService := memsvc.NewService(cfg, pool, &fakeLLMClient{embedResponse: llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}}})

	record, err := memoryService.Ingest(context.Background(), memsvc.IngestParams{
		RawText:     "Foreign tenant memory.",
		Summary:     "Foreign tenant memory.",
		MemoryType:  "note",
		RequestUser: "tenant-b",
	})
	if err != nil {
		t.Fatalf("seed foreign memory: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/memory/"+record.ID, nil)
	request.Header.Set(authorizationHeaderName, "Bearer token-a")
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusNotFound, recorder.Code, recorder.Body.String())
	}
	assertAPIErrorCode(t, recorder.Body.Bytes(), "memory_not_found")

	stored, err := db.NewMemoryRepository(pool).GetByID(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("verify memory still exists: %v", err)
	}
	if stored.ID != record.ID {
		t.Fatalf("unexpected stored memory %#v", stored)
	}

	_ = memoryService.Delete(context.Background(), record.ID)
}

func TestAuthenticatedUserCannotInspectForeignDebugContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool, cleanup := openIsolationTestDB(t)
	defer cleanup()

	cfg := isolationTestConfig(t, "memory_items_isolation_debug")
	server := NewServerWithClient(cfg, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), pool, &fakeLLMClient{})

	foreignUser, err := db.NewUserRepository(pool).Ensure(context.Background(), db.EnsureUserParams{ExternalID: "tenant-b", DisplayName: "tenant-b"})
	if err != nil {
		t.Fatalf("ensure foreign user: %v", err)
	}
	foreignSessionID := fmt.Sprintf("debug-foreign-session-%d", time.Now().UnixNano())
	if _, err := db.NewSessionRepository(pool).Create(context.Background(), db.CreateSessionParams{UserID: foreignUser.ID, ExternalID: foreignSessionID, Title: "Foreign Debug Session"}); err != nil {
		t.Fatalf("create foreign session: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/debug/context?session_id="+foreignSessionID, nil)
	request.Header.Set(authorizationHeaderName, "Bearer token-a")
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusNotFound, recorder.Code, recorder.Body.String())
	}
	assertAPIErrorCode(t, recorder.Body.Bytes(), "session_not_found")
}

func TestAuthenticatedUserCannotInspectOrUpdateForeignPerson(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool, cleanup := openIsolationTestDB(t)
	defer cleanup()

	cfg := isolationTestConfig(t, "memory_items_isolation_person")
	qdrantServer := newFakeQdrantServer()
	defer qdrantServer.Close()
	cfg.Qdrant.URL = qdrantServer.URL
	fakeClient := &fakeLLMClient{responses: []llm.GenerateResponse{{
		OutputText: `{"pairs":[{"person_name":"Alex","topic_name":"api_review","niceness":0.8,"readiness":0.7,"competence":0.9,"capacity":0.4,"confidence":0.85,"evidence_summary":"Foreign tenant evidence.","last_observed_at":"2025-01-02T15:04:05Z"}]}`,
	}}, embedResponse: llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}}}
	server := NewServerWithClient(cfg, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), pool, fakeClient)
	memoryService := memsvc.NewService(cfg, pool, fakeClient)

	record, err := memoryService.Ingest(context.Background(), memsvc.IngestParams{
		RawText:     "Alex responds well to tightly scoped API review requests.",
		Summary:     "Alex responds well to tightly scoped API review requests.",
		MemoryType:  "person_preference",
		People:      []string{"Alex"},
		Topics:      []string{"api_review"},
		RequestUser: "tenant-b",
	})
	if err != nil {
		t.Fatalf("seed foreign person: %v", err)
	}
	person, err := db.NewPersonRepository(pool).GetByName(context.Background(), record.UserID, "Alex")
	if err != nil {
		t.Fatalf("get foreign person: %v", err)
	}

	getRecorder := httptest.NewRecorder()
	getRequest := httptest.NewRequest(http.MethodGet, "/debug/person/"+person.ID, nil)
	getRequest.Header.Set(authorizationHeaderName, "Bearer token-a")
	server.Handler().ServeHTTP(getRecorder, getRequest)
	if getRecorder.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusNotFound, getRecorder.Code, getRecorder.Body.String())
	}
	assertAPIErrorCode(t, getRecorder.Body.Bytes(), "person_not_found")

	updateRecorder := httptest.NewRecorder()
	updateRequest := httptest.NewRequest(http.MethodPut, "/debug/person/"+person.ID, bytes.NewReader([]byte(`{"topic_name":"api_review","capacity":0.2}`)))
	updateRequest.Header.Set("Content-Type", "application/json")
	updateRequest.Header.Set(authorizationHeaderName, "Bearer token-a")
	server.Handler().ServeHTTP(updateRecorder, updateRequest)
	if updateRecorder.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusNotFound, updateRecorder.Code, updateRecorder.Body.String())
	}
	assertAPIErrorCode(t, updateRecorder.Body.Bytes(), "person_not_found")

	_ = memoryService.Delete(context.Background(), record.ID)
}

func openIsolationTestDB(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()

	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not set")
	}

	migrationsDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}

	pool, err := db.Open(context.Background(), config.PostgresConfig{Enabled: true, DSN: dsn, MaxConns: 4, MinConns: 1})
	if err != nil {
		t.Skipf("postgres is not reachable: %v", err)
	}
	if err := db.RunMigrationsUp(config.PostgresConfig{DSN: dsn}, migrationsDir); err != nil {
		db.Close(pool)
		t.Fatalf("run migrations: %v", err)
	}

	return pool, func() { db.Close(pool) }
}

func isolationTestConfig(t *testing.T, collection string) config.Config {
	t.Helper()

	return config.Config{
		App:     config.AppConfig{Name: "salience-graph", Env: "test"},
		Auth:    config.AuthConfig{Enabled: true, Realm: "second-context", Tokens: []config.AuthTokenConfig{{Subject: "tenant-a", Token: "token-a"}, {Subject: "tenant-b", Token: "token-b"}}},
		Dev:     config.DevConfig{UserExternalID: "dev-user", UserName: "Dev User", UserEmail: "dev@example.com"},
		OpenAI:  config.OpenAIConfig{ChatModel: "gpt-4.1-mini", EmbeddingModel: "text-embedding-3-small"},
		Qdrant:  config.QdrantConfig{URL: "http://127.0.0.1:6333", Collection: collection, VectorSize: 3, DenseVector: "dense", SparseVector: "sparse"},
		Scoring: config.ScoringConfig{RetrievalWeight: 0.35, RecencyWeight: 0.15, ImportanceWeight: 0.15, UtilityWeight: 0.15, GoalRelevanceWeight: 0.10, BeliefImpactWeight: 0.05, ConfidenceWeight: 0.05, RecencyHalfLifeDays: 30, RedundancyThreshold: 0.82},
	}
}

func assertAPIErrorCode(t *testing.T, body []byte, code string) {
	t.Helper()

	var payload apiErrorEnvelope
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode api error: %v body=%s", err, string(body))
	}
	if payload.Error.Code != code {
		t.Fatalf("expected error code %q, got %#v", code, payload)
	}
}

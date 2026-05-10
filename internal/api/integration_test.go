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
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	memsvc "github.com/bdobrica/SecondContext/internal/memory"
)

func TestResponsesEndpointPersistsMessages(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

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
	defer db.Close(pool)

	if err := db.RunMigrationsUp(config.PostgresConfig{DSN: dsn}, migrationsDir); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	fakeClient := &fakeLLMClient{response: llm.GenerateResponse{
		ID:         "chatcmpl_integration",
		Model:      "gpt-4.1-mini",
		OutputText: "Here is a concise review request.",
	}, embedResponse: llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}}}

	server := NewServerWithClient(config.Config{
		App:    config.AppConfig{Name: "salience-graph", Env: "test"},
		Dev:    config.DevConfig{UserExternalID: "dev-user", UserName: "Dev User", UserEmail: "dev@example.com"},
		OpenAI: config.OpenAIConfig{ChatModel: "gpt-4.1-mini"},
	}, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), pool, fakeClient)

	sessionExternalID := fmt.Sprintf("api-test-%d", time.Now().UnixNano())
	body := []byte(fmt.Sprintf(`{"model":"context-agent-1","input":"Help me ask Alex to review the proposal.","metadata":{"session_id":"%s"}}`, sessionExternalID))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d body=%s", recorder.Code, recorder.Body.String())
	}

	sessions := db.NewSessionRepository(pool)
	messages := db.NewMessageRepository(pool)
	session, err := sessions.GetByExternalID(context.Background(), sessionExternalID)
	if err != nil {
		t.Fatalf("get session by external id: %v", err)
	}

	storedMessages, err := messages.ListBySession(context.Background(), session.ID, 10)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(storedMessages) != 2 {
		t.Fatalf("expected 2 stored messages, got %d", len(storedMessages))
	}
	if storedMessages[0].Role != "user" || storedMessages[1].Role != "assistant" {
		t.Fatalf("unexpected stored roles %#v", storedMessages)
	}

	var payload createResponseResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Metadata["session_id"] != sessionExternalID {
		t.Fatalf("unexpected response metadata %#v", payload.Metadata)
	}
}

func TestResponsesEndpointUsesRetrievedContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

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
	defer db.Close(pool)

	if err := db.RunMigrationsUp(config.PostgresConfig{DSN: dsn}, migrationsDir); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	qdrantServer := newFakeQdrantServer()
	defer qdrantServer.Close()

	fakeClient := &fakeLLMClient{response: llm.GenerateResponse{
		ID:         "chatcmpl_context",
		Model:      "gpt-4.1-mini",
		OutputText: "Ask Alex for an API-only review with action items.",
	}, embedResponse: llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}}}

	cfg := config.Config{
		App:     config.AppConfig{Name: "salience-graph", Env: "test"},
		Dev:     config.DevConfig{UserExternalID: "dev-user", UserName: "Dev User", UserEmail: "dev@example.com"},
		OpenAI:  config.OpenAIConfig{ChatModel: "gpt-4.1-mini", EmbeddingModel: "text-embedding-3-small"},
		Qdrant:  config.QdrantConfig{URL: qdrantServer.URL, Collection: "memory_items", VectorSize: 3, DenseVector: "dense", SparseVector: "sparse"},
		Scoring: config.ScoringConfig{RetrievalWeight: 0.35, RecencyWeight: 0.15, ImportanceWeight: 0.15, UtilityWeight: 0.15, GoalRelevanceWeight: 0.10, BeliefImpactWeight: 0.05, ConfidenceWeight: 0.05, RecencyHalfLifeDays: 30, RedundancyThreshold: 0.82},
	}
	server := NewServerWithClient(cfg, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), pool, fakeClient)

	memoryService := memsvc.NewService(cfg, pool, fakeClient)
	record, err := memoryService.Ingest(context.Background(), memsvc.IngestParams{
		RawText:      "Alex prefers tightly scoped API review requests with action items and evidence.",
		Summary:      "Alex prefers tightly scoped API review requests with action items and evidence.",
		MemoryType:   "person_preference",
		People:       []string{"Alex"},
		Topics:       []string{"api_review"},
		RequestUser:  "dev-user",
		Importance:   floatPointer(0.95),
		Utility:      floatPointer(0.96),
		BeliefImpact: floatPointer(0.34),
		Confidence:   floatPointer(0.97),
	})
	if err != nil {
		t.Fatalf("ingest memory: %v", err)
	}
	defer func() { _ = memoryService.Delete(context.Background(), record.ID) }()

	sessionExternalID := fmt.Sprintf("api-context-test-%d", time.Now().UnixNano())
	body := []byte(fmt.Sprintf(`{"model":"context-agent-1","input":"Help me ask Alex to review the proposal.","metadata":{"session_id":"%s","goal":"get_review","people":["Alex"],"memory_mode":"social_strategy"}}`, sessionExternalID))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(fakeClient.request.Messages) < 2 {
		t.Fatalf("expected augmented llm messages, got %#v", fakeClient.request.Messages)
	}
	if fakeClient.request.Messages[0].Role != "system" {
		t.Fatalf("expected system prompt first, got %#v", fakeClient.request.Messages)
	}
	for _, expected := range []string{"communication advice", "Alex prefers tightly scoped API review requests with action items and evidence.", "Interaction goal:"} {
		if !strings.Contains(strings.ToLower(fakeClient.request.Messages[0].Content), strings.ToLower(expected)) {
			t.Fatalf("expected system prompt to contain %q, got %s", expected, fakeClient.request.Messages[0].Content)
		}
	}

	sessions := db.NewSessionRepository(pool)
	messages := db.NewMessageRepository(pool)
	session, err := sessions.GetByExternalID(context.Background(), sessionExternalID)
	if err != nil {
		t.Fatalf("get session by external id: %v", err)
	}

	storedMessages, err := messages.ListBySession(context.Background(), session.ID, 10)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(storedMessages) != 2 {
		t.Fatalf("expected 2 stored messages, got %d", len(storedMessages))
	}
	var assistantMetadata map[string]any
	if err := json.Unmarshal(storedMessages[1].Metadata, &assistantMetadata); err != nil {
		t.Fatalf("decode assistant metadata: %v", err)
	}
	contextPacket, ok := assistantMetadata["context_packet"].(map[string]any)
	if !ok {
		t.Fatalf("expected context packet metadata, got %#v", assistantMetadata)
	}
	memoryContext, ok := contextPacket["memory_context"].([]any)
	if !ok || len(memoryContext) == 0 {
		t.Fatalf("expected memory context in metadata, got %#v", contextPacket)
	}

	var payload createResponseResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := payload.Metadata["context_packet"]; !ok {
		t.Fatalf("expected response metadata to include context packet, got %#v", payload.Metadata)
	}
}

func floatPointer(value float64) *float64 {
	return &value
}

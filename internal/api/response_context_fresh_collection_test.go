package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	"log/slog"
)

func TestHandleCreateResponseFreshCollectionSkipsWarning(t *testing.T) {
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

	qdrantServer := newMissingCollectionQdrantServer()
	defer qdrantServer.Close()

	loggerOutput := bytes.NewBuffer(nil)
	server := NewServerWithClient(config.Config{
		App:    config.AppConfig{Name: "salience-graph", Env: "test"},
		Dev:    config.DevConfig{UserExternalID: "dev-user", UserName: "Dev User", UserEmail: "dev@example.com"},
		OpenAI: config.OpenAIConfig{ChatModel: "gpt-4.1-mini", EmbeddingModel: "text-embedding-3-small"},
		Qdrant: config.QdrantConfig{URL: qdrantServer.URL, Collection: "fresh_collection", VectorSize: 3, DenseVector: "dense", SparseVector: "sparse"},
	}, slog.New(slog.NewTextHandler(loggerOutput, nil)), pool, &fakeLLMClient{response: llm.GenerateResponse{
		ID:         "chatcmpl_fresh_collection",
		Model:      "gpt-4.1-mini",
		OutputText: "Start with a narrow API-only review request.",
	}, embedResponse: llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}}})

	body := []byte(`{"model":"context-agent-1","input":"Help me ask Alex to review the proposal.","metadata":{"goal":"get_review","people":["Alex"]}}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(loggerOutput.String(), "build response context") {
		t.Fatalf("expected fresh collection lookup to be treated as empty context, got logs: %s", loggerOutput.String())
	}

	var payload createResponseResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.OutputText == "" {
		t.Fatalf("expected non-empty response payload, got %#v", payload)
	}
	if _, ok := payload.Metadata["context_packet"]; !ok {
		t.Fatalf("expected context packet metadata, got %#v", payload.Metadata)
	}
}

func newMissingCollectionQdrantServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": map[string]any{"error": "Not found: Collection `fresh_collection` doesn't exist!"},
		})
	}))
}

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

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	"github.com/bdobrica/SecondContext/internal/qdrant"
)

func TestMemoryEndpoints(t *testing.T) {
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

	server := NewServerWithClient(config.Config{
		App:    config.AppConfig{Name: "salience-graph", Env: "test"},
		Dev:    config.DevConfig{UserExternalID: "dev-user", UserName: "Dev User", UserEmail: "dev@example.com"},
		OpenAI: config.OpenAIConfig{EmbeddingModel: "text-embedding-3-small"},
		Qdrant: config.QdrantConfig{URL: qdrantServer.URL, Collection: "memory_items", VectorSize: 3},
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)), pool, &fakeLLMClient{embedResponse: llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}}})

	ingestBody := []byte(`{"raw_text":"Alex prefers narrow review scopes.","summary":"Alex prefers narrow review scopes.","type":"person_preference","people":["Alex"],"topics":["infrastructure"],"importance":0.7,"utility":0.8,"belief_impact":0.2,"confidence":0.9}`)
	ingestRecorder := httptest.NewRecorder()
	ingestRequest := httptest.NewRequest(http.MethodPost, "/memory/ingest", bytes.NewReader(ingestBody))
	ingestRequest.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(ingestRecorder, ingestRequest)

	if ingestRecorder.Code != http.StatusCreated {
		t.Fatalf("unexpected ingest status %d body=%s", ingestRecorder.Code, ingestRecorder.Body.String())
	}

	var created memoryResponse
	if err := json.Unmarshal(ingestRecorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode ingest response: %v", err)
	}
	if created.QdrantPointID == "" {
		t.Fatal("expected qdrant point id")
	}

	listRecorder := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/memory?user_external_id=dev-user", nil)
	server.Handler().ServeHTTP(listRecorder, listRequest)

	if listRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected list status %d body=%s", listRecorder.Code, listRecorder.Body.String())
	}

	var listed memoryListResponse
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed.Data) == 0 {
		t.Fatal("expected at least one memory")
	}

	deleteRecorder := httptest.NewRecorder()
	deleteRequest := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/memory/%s", created.ID), nil)
	server.Handler().ServeHTTP(deleteRecorder, deleteRequest)

	if deleteRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected delete status %d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}

	results, err := qdrant.NewClient(config.QdrantConfig{URL: qdrantServer.URL}).Search(context.Background(), "memory_items", []float64{0.1, 0.2, 0.3}, 5)
	if err != nil {
		t.Fatalf("search qdrant: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected qdrant point to be deleted, got %#v", results)
	}
}

func newFakeQdrantServer() *httptest.Server {
	type storedPoint struct {
		ID      string
		Vector  []float64
		Payload map[string]any
	}
	collections := make(map[string]map[string]storedPoint)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		trimmedPath := strings.Trim(r.URL.Path, "/")
		parts := strings.Split(trimmedPath, "/")
		if len(parts) < 2 || parts[0] != "collections" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "error", "error": "not found"})
			return
		}

		collection := parts[1]
		switch {
		case r.Method == http.MethodPut && len(parts) == 2:
			if _, ok := collections[collection]; !ok {
				collections[collection] = make(map[string]storedPoint)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": true, "time": 0.001})
		case r.Method == http.MethodPut && len(parts) == 3 && parts[2] == "points":
			var payload struct {
				Points []struct {
					ID      string         `json:"id"`
					Vector  []float64      `json:"vector"`
					Payload map[string]any `json:"payload"`
				} `json:"points"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if _, ok := collections[collection]; !ok {
				collections[collection] = make(map[string]storedPoint)
			}
			for _, point := range payload.Points {
				collections[collection][point.ID] = storedPoint{ID: point.ID, Vector: point.Vector, Payload: point.Payload}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": map[string]any{"status": "acknowledged"}, "time": 0.001})
		case r.Method == http.MethodPost && len(parts) == 4 && parts[2] == "points" && parts[3] == "delete":
			var payload struct {
				Points []string `json:"points"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			for _, id := range payload.Points {
				delete(collections[collection], id)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": map[string]any{"status": "acknowledged"}, "time": 0.001})
		case r.Method == http.MethodPost && len(parts) == 4 && parts[2] == "points" && parts[3] == "search":
			results := make([]map[string]any, 0, len(collections[collection]))
			for _, point := range collections[collection] {
				results = append(results, map[string]any{"id": point.ID, "score": 1.0, "payload": point.Payload})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": results, "time": 0.001})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "error", "error": "not found"})
		}
	}))
}

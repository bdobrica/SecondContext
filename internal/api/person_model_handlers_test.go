package api

import (
	"bytes"
	"context"
	"encoding/json"
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
	memsvc "github.com/bdobrica/SecondContext/internal/memory"
)

func TestDebugPersonEndpointsInspectAndUpdateModel(t *testing.T) {
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

	fakeClient := &fakeLLMClient{responses: []llm.GenerateResponse{{
		OutputText: `{"pairs":[{"person_name":"Alex","person_aliases":["Alexander"],"topic_name":"api_review","topic_aliases":["api reviews"],"niceness":0.81,"readiness":0.74,"competence":0.93,"capacity":0.38,"confidence":0.88,"evidence_summary":"Responds well to tightly scoped API review requests.","last_observed_at":"2025-01-02T15:04:05Z"}]}`,
	}}, embedResponse: llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}}}

	cfg := config.Config{
		App:    config.AppConfig{Name: "salience-graph", Env: "test"},
		Dev:    config.DevConfig{UserExternalID: "dev-user", UserName: "Dev User", UserEmail: "dev@example.com"},
		OpenAI: config.OpenAIConfig{ChatModel: "gpt-4.1-mini", EmbeddingModel: "text-embedding-3-small"},
		Qdrant: config.QdrantConfig{URL: qdrantServer.URL, Collection: "memory_items", VectorSize: 3, DenseVector: "dense", SparseVector: "sparse"},
	}

	server := NewServerWithClient(cfg, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), pool, fakeClient)
	memoryService := memsvc.NewService(cfg, pool, fakeClient)
	record, err := memoryService.Ingest(context.Background(), memsvc.IngestParams{
		RawText:     "Alex responds well to tightly scoped API review requests with action items.",
		Summary:     "Alex responds well to tightly scoped API review requests with action items.",
		MemoryType:  "person_preference",
		People:      []string{"Alex"},
		Topics:      []string{"api_review"},
		RequestUser: "dev-user",
	})
	if err != nil {
		t.Fatalf("ingest memory: %v", err)
	}
	defer func() { _ = memoryService.Delete(context.Background(), record.ID) }()

	person, err := db.NewPersonRepository(pool).GetByName(context.Background(), record.UserID, "Alex")
	if err != nil {
		t.Fatalf("get person: %v", err)
	}

	getRecorder := httptest.NewRecorder()
	getRequest := httptest.NewRequest(http.MethodGet, "/debug/person/"+person.ID, nil)
	server.Handler().ServeHTTP(getRecorder, getRequest)

	if getRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected inspect status %d body=%s", getRecorder.Code, getRecorder.Body.String())
	}

	var inspectPayload personDebugResponse
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &inspectPayload); err != nil {
		t.Fatalf("decode inspect response: %v", err)
	}
	if len(inspectPayload.Models) != 1 {
		t.Fatalf("expected 1 model, got %#v", inspectPayload.Models)
	}
	if !strings.Contains(strings.ToLower(inspectPayload.Models[0].Summary), "working estimate only") {
		t.Fatalf("expected safe summary, got %#v", inspectPayload.Models[0])
	}
	if !containsStringFold(inspectPayload.Models[0].EvidenceMemoryIDs, record.ID) {
		t.Fatalf("expected evidence memory id %s, got %#v", record.ID, inspectPayload.Models[0].EvidenceMemoryIDs)
	}

	updateBody := []byte(`{"topic_name":"api_review","topic_aliases":["api"],"capacity":0.25,"confidence":0.9,"evidence_memory_ids":["` + record.ID + `"]}`)
	updateRecorder := httptest.NewRecorder()
	updateRequest := httptest.NewRequest(http.MethodPut, "/debug/person/"+person.ID, bytes.NewReader(updateBody))
	updateRequest.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(updateRecorder, updateRequest)

	if updateRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected update status %d body=%s", updateRecorder.Code, updateRecorder.Body.String())
	}

	var updatePayload personDebugResponse
	if err := json.Unmarshal(updateRecorder.Body.Bytes(), &updatePayload); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if len(updatePayload.Models) != 1 {
		t.Fatalf("expected single filtered model, got %#v", updatePayload.Models)
	}
	if updatePayload.Models[0].Capacity != 0.25 {
		t.Fatalf("expected updated capacity, got %#v", updatePayload.Models[0])
	}
	if !containsStringFold(updatePayload.Models[0].TopicAliases, "api") {
		t.Fatalf("expected updated topic aliases, got %#v", updatePayload.Models[0].TopicAliases)
	}
}

func containsStringFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}

	return false
}

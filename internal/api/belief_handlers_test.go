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

func TestDebugBeliefsEndpointTracksContradictions(t *testing.T) {
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
		OutputText: `{"beliefs":[{"claim":"The migration project is more risky than originally estimated.","topic_name":"migration","stance":"supports","confidence":0.74,"evidence_summary":"Recent cutover delays increased migration risk."}]}`,
	}, {
		OutputText: `{"beliefs":[{"claim":"The migration project is more risky than originally estimated.","topic_name":"migration","stance":"contradicts","confidence":0.68,"evidence_summary":"The rollback tests completed cleanly and reduced concern."}]}`,
	}}, embedResponse: llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}}}

	requestUser := fmt.Sprintf("belief-debug-user-%d", time.Now().UnixNano())
	cfg := config.Config{
		App:    config.AppConfig{Name: "salience-graph", Env: "test"},
		Dev:    config.DevConfig{UserExternalID: "dev-user", UserName: "Dev User", UserEmail: "dev@example.com"},
		OpenAI: config.OpenAIConfig{ChatModel: "gpt-4.1-mini", EmbeddingModel: "text-embedding-3-small"},
		Qdrant: config.QdrantConfig{URL: qdrantServer.URL, Collection: "memory_items", VectorSize: 3, DenseVector: "dense", SparseVector: "sparse"},
	}

	server := NewServerWithClient(cfg, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), pool, fakeClient)
	memoryService := memsvc.NewService(cfg, pool, fakeClient)

	firstRecord, err := memoryService.Ingest(context.Background(), memsvc.IngestParams{
		RawText:      "Recent cutover delays increased migration risk.",
		Summary:      "Recent cutover delays increased migration risk.",
		MemoryType:   "belief_update",
		Topics:       []string{"migration"},
		RequestUser:  requestUser,
		BeliefImpact: floatPointer(0.93),
		Confidence:   floatPointer(0.82),
	})
	if err != nil {
		t.Fatalf("ingest first memory: %v", err)
	}
	defer func() { _ = memoryService.Delete(context.Background(), firstRecord.ID) }()

	secondRecord, err := memoryService.Ingest(context.Background(), memsvc.IngestParams{
		RawText:      "The rollback tests completed cleanly and reduced migration risk concern.",
		Summary:      "The rollback tests completed cleanly and reduced migration risk concern.",
		MemoryType:   "belief_update",
		Topics:       []string{"migration"},
		RequestUser:  requestUser,
		BeliefImpact: floatPointer(0.87),
		Confidence:   floatPointer(0.8),
	})
	if err != nil {
		t.Fatalf("ingest second memory: %v", err)
	}
	defer func() { _ = memoryService.Delete(context.Background(), secondRecord.ID) }()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/debug/beliefs?topic_name=migration&user_external_id="+requestUser, nil)
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected inspect status %d body=%s", recorder.Code, recorder.Body.String())
	}

	var payload beliefListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode beliefs response: %v", err)
	}
	if len(payload.Data) != 1 {
		t.Fatalf("expected 1 merged belief, got %#v", payload.Data)
	}
	belief := payload.Data[0]
	if !strings.EqualFold(belief.TopicName, "migration") {
		t.Fatalf("expected migration topic, got %#v", belief)
	}
	if !strings.EqualFold(belief.Stance, "unknown") {
		t.Fatalf("expected contradictory merge to become unknown, got %#v", belief)
	}
	if !belief.HasContradiction {
		t.Fatalf("expected contradiction flag, got %#v", belief)
	}
	if !containsStringFold(belief.EvidenceMemoryIDs, firstRecord.ID) || !containsStringFold(belief.EvidenceMemoryIDs, secondRecord.ID) {
		t.Fatalf("expected both evidence memory ids, got %#v", belief.EvidenceMemoryIDs)
	}
	if !strings.Contains(strings.ToLower(belief.Summary), "conflicting evidence exists") {
		t.Fatalf("expected contradictory summary, got %#v", belief)
	}
}

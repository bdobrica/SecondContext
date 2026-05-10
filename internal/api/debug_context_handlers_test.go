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

func TestDebugContextEndpointNotExposedOutsideDev(t *testing.T) {
	cfg := config.Config{
		App: config.AppConfig{Name: "salience-graph", Env: "production"},
	}
	server := NewServerWithClient(cfg, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), nil, &fakeLLMClient{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/debug/context?input=test", nil)
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusNotFound, recorder.Code, recorder.Body.String())
	}
}

func TestDebugContextEndpointShowsStoredContextAndLatestUpdates(t *testing.T) {
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
		OutputText: `{"pairs":[{"person_name":"Alex","topic_name":"api_review","niceness":0.82,"readiness":0.79,"competence":0.91,"capacity":0.44,"confidence":0.86,"evidence_summary":"Prefers tightly scoped API review requests.","last_observed_at":"2025-01-01T00:00:00Z"}]}`,
	}, {
		OutputText: `{"beliefs":[]}`,
	}, {
		OutputText: `{"recommended_strategy_id":"scoped_direct","recommendation_rationale":"It best fits Alex's preference for narrowly scoped requests.","strategies":[{"id":"scoped_direct","label":"Scoped direct request","message_draft":"Alex, could you review just the API section and flag the top risks?","predicted_response":"Yes, that scope works well for me.","benefits":["clear ask"],"risks":["less broad feedback"],"likelihood_of_success":0.82,"fallback_option":"Keep the request API-only and shorter."},{"id":"context_first","label":"Context-first request","message_draft":"Alex, your API perspective would help de-risk the proposal. Could you review the API section first?","predicted_response":"I can start with the API section.","benefits":["shows context"],"risks":["slightly longer ask"],"likelihood_of_success":0.69,"fallback_option":"Trim the context and restate the concrete ask."},{"id":"deferential_short","label":"Short deferential ask","message_draft":"Alex, if you have bandwidth, would you be open to a short API-only review?","predicted_response":"Maybe, send the exact section.","benefits":["polite tone"],"risks":["can invite delay"],"likelihood_of_success":0.61,"fallback_option":"Reply with the exact section and deadline."}]}`,
	}, {
		OutputText: `{"summary":"Alex agreed to review and asked for the API section only.","success_score":0.78,"prediction_error":"The system underestimated Alex's need for a tightly scoped request.","people":["Alex"],"topics":["api_review"],"importance":0.83,"utility":0.91,"belief_impact":0.46,"confidence":0.88,"graph_edges":[{"source_kind":"person","source_name":"Alex","target_kind":"topic","target_name":"api_review","relationship":"prefers_narrow_scope","confidence":0.82}]}`,
	}, {
		OutputText: `{"pairs":[{"person_name":"Alex","topic_name":"api_review","niceness":0.82,"readiness":0.86,"competence":0.91,"capacity":0.39,"confidence":0.84,"evidence_summary":"Agreed to review once the request was narrowed to the API section.","last_observed_at":"2026-05-10T12:00:00Z"}]}`,
	}, {
		OutputText: `{"beliefs":[{"claim":"Alex prefers narrowly scoped API review requests.","topic_name":"api_review","stance":"supports","confidence":0.77,"evidence_summary":"He agreed once the request was limited to the API section."}]}`,
	}}, embedResponse: llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}}}

	requestUser := fmt.Sprintf("debug-context-user-%d", time.Now().UnixNano())
	cfg := config.Config{
		App:     config.AppConfig{Name: "salience-graph", Env: "test"},
		Dev:     config.DevConfig{UserExternalID: "dev-user", UserName: "Dev User", UserEmail: "dev@example.com"},
		OpenAI:  config.OpenAIConfig{ChatModel: "gpt-4.1-mini", EmbeddingModel: "text-embedding-3-small"},
		Qdrant:  config.QdrantConfig{URL: qdrantServer.URL, Collection: "memory_items", VectorSize: 3, DenseVector: "dense", SparseVector: "sparse"},
		Scoring: config.ScoringConfig{RetrievalWeight: 0.35, RecencyWeight: 0.15, ImportanceWeight: 0.15, UtilityWeight: 0.15, GoalRelevanceWeight: 0.10, BeliefImpactWeight: 0.05, ConfidenceWeight: 0.05, RecencyHalfLifeDays: 30, RedundancyThreshold: 0.82},
	}
	server := NewServerWithClient(cfg, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), pool, fakeClient)

	memoryService := memsvc.NewService(cfg, pool, fakeClient)
	seed, err := memoryService.Ingest(context.Background(), memsvc.IngestParams{
		RawText:      "Alex prefers tightly scoped API review requests with action items and evidence.",
		Summary:      "Alex prefers tightly scoped API review requests with action items and evidence.",
		MemoryType:   "person_preference",
		People:       []string{"Alex"},
		Topics:       []string{"api_review"},
		RequestUser:  requestUser,
		Importance:   floatPointer(0.95),
		Utility:      floatPointer(0.96),
		BeliefImpact: floatPointer(0.34),
		Confidence:   floatPointer(0.97),
	})
	if err != nil {
		t.Fatalf("ingest memory: %v", err)
	}
	defer func() { _ = memoryService.Delete(context.Background(), seed.ID) }()

	sessionExternalID := fmt.Sprintf("debug-context-session-%d", time.Now().UnixNano())
	responseBody := []byte(fmt.Sprintf(`{"model":"context-agent-1","input":"Help me ask Alex to review the proposal.","user":"%s","metadata":{"session_id":"%s","goal":"get_review","people":["Alex"],"topics":["api_review"],"memory_mode":"social_strategy"}}`, requestUser, sessionExternalID))
	responseRecorder := httptest.NewRecorder()
	responseRequest := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(responseBody))
	responseRequest.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(responseRecorder, responseRequest)
	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected response status %d body=%s", responseRecorder.Code, responseRecorder.Body.String())
	}

	outcomeBody := []byte(fmt.Sprintf(`{"session_id":"%s","raw_text":"Alex replied quickly and agreed to review, but asked me to narrow the request to the API section only.","goal":"get_review","people":["Alex"],"topics":["api_review"],"user":"%s"}`, sessionExternalID, requestUser))
	outcomeRecorder := httptest.NewRecorder()
	outcomeRequest := httptest.NewRequest(http.MethodPost, "/interactions/outcome", bytes.NewReader(outcomeBody))
	outcomeRequest.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(outcomeRecorder, outcomeRequest)
	if outcomeRecorder.Code != http.StatusCreated {
		t.Fatalf("unexpected outcome status %d body=%s", outcomeRecorder.Code, outcomeRecorder.Body.String())
	}

	debugRecorder := httptest.NewRecorder()
	debugRequest := httptest.NewRequest(http.MethodGet, "/debug/context?session_id="+sessionExternalID, nil)
	server.Handler().ServeHTTP(debugRecorder, debugRequest)
	if debugRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected debug status %d body=%s", debugRecorder.Code, debugRecorder.Body.String())
	}

	var payload debugContextResponse
	if err := json.Unmarshal(debugRecorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode debug response: %v", err)
	}
	if payload.StoredContextPacket == nil || len(payload.StoredContextPacket.MemoryContext) == 0 {
		t.Fatalf("expected stored context packet with memory context, got %#v", payload.StoredContextPacket)
	}
	if payload.CurrentContextPacket == nil || len(payload.CurrentContextPacket.MemoryContext) == 0 {
		t.Fatalf("expected current context packet with memory context, got %#v", payload.CurrentContextPacket)
	}
	if payload.ScenarioPlan == nil {
		t.Fatalf("expected scenario plan, got %#v", payload)
	}
	if len(payload.PeopleModels) == 0 {
		t.Fatalf("expected people models, got %#v", payload.PeopleModels)
	}
	if len(payload.RelevantBeliefs) == 0 {
		t.Fatalf("expected relevant beliefs, got %#v", payload.RelevantBeliefs)
	}
	if payload.LatestTurnUpdates.Outcome == nil {
		t.Fatalf("expected latest outcome update, got %#v", payload.LatestTurnUpdates)
	}
	if len(payload.LatestTurnUpdates.Memories) == 0 {
		t.Fatalf("expected latest memory updates, got %#v", payload.LatestTurnUpdates)
	}
	if len(payload.LatestTurnUpdates.GraphEdges) == 0 {
		t.Fatalf("expected latest graph updates, got %#v", payload.LatestTurnUpdates)
	}
}

func TestDebugContextEndpointCompareAnswers(t *testing.T) {
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
		OutputText: `{"pairs":[{"person_name":"Alex","topic_name":"api_review","niceness":0.82,"readiness":0.79,"competence":0.91,"capacity":0.44,"confidence":0.86,"evidence_summary":"Prefers tightly scoped API review requests.","last_observed_at":"2025-01-01T00:00:00Z"}]}`,
	}, {
		OutputText: `{"beliefs":[]}`,
	}, {
		ID:         "augmented_answer",
		Model:      "gpt-4.1-mini",
		OutputText: "Use a narrow API-only review request with concrete action items.",
	}, {
		ID:         "memory_disabled_answer",
		Model:      "gpt-4.1-mini",
		OutputText: "Ask Alex to review the proposal.",
	}}, embedResponse: llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}}}

	requestUser := fmt.Sprintf("debug-compare-user-%d", time.Now().UnixNano())
	cfg := config.Config{
		App:     config.AppConfig{Name: "salience-graph", Env: "test"},
		Dev:     config.DevConfig{UserExternalID: "dev-user", UserName: "Dev User", UserEmail: "dev@example.com"},
		OpenAI:  config.OpenAIConfig{ChatModel: "gpt-4.1-mini", EmbeddingModel: "text-embedding-3-small"},
		Qdrant:  config.QdrantConfig{URL: qdrantServer.URL, Collection: "memory_items", VectorSize: 3, DenseVector: "dense", SparseVector: "sparse"},
		Scoring: config.ScoringConfig{RetrievalWeight: 0.35, RecencyWeight: 0.15, ImportanceWeight: 0.15, UtilityWeight: 0.15, GoalRelevanceWeight: 0.10, BeliefImpactWeight: 0.05, ConfidenceWeight: 0.05, RecencyHalfLifeDays: 30, RedundancyThreshold: 0.82},
	}
	server := NewServerWithClient(cfg, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), pool, fakeClient)

	memoryService := memsvc.NewService(cfg, pool, fakeClient)
	seed, err := memoryService.Ingest(context.Background(), memsvc.IngestParams{
		RawText:     "Alex prefers tightly scoped API review requests with action items.",
		Summary:     "Alex prefers tightly scoped API review requests with action items.",
		MemoryType:  "person_preference",
		People:      []string{"Alex"},
		Topics:      []string{"api_review"},
		RequestUser: requestUser,
	})
	if err != nil {
		t.Fatalf("ingest memory: %v", err)
	}
	defer func() { _ = memoryService.Delete(context.Background(), seed.ID) }()

	debugRecorder := httptest.NewRecorder()
	debugRequest := httptest.NewRequest(http.MethodGet, "/debug/context?input=Help%20me%20ask%20Alex%20to%20review%20the%20proposal.&goal=get_review&people=Alex&topics=api_review&user_external_id="+requestUser+"&compare=true", nil)
	server.Handler().ServeHTTP(debugRecorder, debugRequest)
	if debugRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected debug status %d body=%s", debugRecorder.Code, debugRecorder.Body.String())
	}

	var payload debugContextResponse
	if err := json.Unmarshal(debugRecorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode debug response: %v", err)
	}
	if payload.Comparison == nil {
		t.Fatalf("expected comparison payload, got %#v", payload)
	}
	if payload.Comparison.MemoryAugmented.OutputText == "" || payload.Comparison.MemoryDisabled.OutputText == "" {
		t.Fatalf("expected both outputs, got %#v", payload.Comparison)
	}
	if payload.Comparison.MemoryAugmented.OutputText == payload.Comparison.MemoryDisabled.OutputText {
		t.Fatalf("expected different outputs, got %#v", payload.Comparison)
	}
	if !strings.Contains(payload.Comparison.MemoryAugmented.PromptPreview, "Memory context:") {
		t.Fatalf("expected augmented prompt preview to contain memory context, got %q", payload.Comparison.MemoryAugmented.PromptPreview)
	}
	if strings.Contains(payload.Comparison.MemoryDisabled.PromptPreview, "Alex prefers tightly scoped API review requests with action items.") {
		t.Fatalf("expected memory-disabled prompt preview to omit retrieved memory, got %q", payload.Comparison.MemoryDisabled.PromptPreview)
	}
}

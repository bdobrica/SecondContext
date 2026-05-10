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

	fakeClient := &fakeLLMClient{responses: []llm.GenerateResponse{{
		OutputText: `{"pairs":[{"person_name":"Alex","person_aliases":["A."],"topic_name":"api_review","topic_aliases":["api reviews"],"niceness":0.82,"readiness":0.79,"competence":0.91,"capacity":0.44,"confidence":0.86,"evidence_summary":"Prefers tightly scoped API review requests with action items.","last_observed_at":"2025-01-01T00:00:00Z"}]}`,
	}, {
		OutputText: `{"beliefs":[]}`,
	}, {
		ID:         "chatcmpl_context",
		Model:      "gpt-4.1-mini",
		OutputText: `{"recommended_strategy_id":"scoped_direct","recommendation_rationale":"It best fits Alex's preference for narrowly scoped requests while keeping the ask actionable.","strategies":[{"id":"scoped_direct","label":"Scoped direct request","message_draft":"Alex, could you review just the API section and flag the top risks and action items?","predicted_response":"Yes, that scope works well for me.","benefits":["matches Alex's preferred scope","easy to act on"],"risks":["may omit broader architecture feedback"],"likelihood_of_success":0.82,"fallback_option":"Keep the ask API-only and offer a later follow-up for broader issues."},{"id":"context_first","label":"Context-first request","message_draft":"Alex, I'm trying to de-risk the proposal and your API perspective would help. Could you review the API section first?","predicted_response":"I can do the API section first, then decide if more is needed.","benefits":["shows why the request matters"],"risks":["slightly longer ask"],"likelihood_of_success":0.69,"fallback_option":"Trim the background and send only the concrete API questions."},{"id":"deferential_short","label":"Short deferential ask","message_draft":"Alex, if you have bandwidth, would you be open to a short API-only review this week?","predicted_response":"Possibly, send the exact section and deadline.","benefits":["polite tone"],"risks":["can invite delay"],"likelihood_of_success":0.63,"fallback_option":"Reply with the exact section and a firm deadline."}]}`,
	}}, embedResponse: llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}}}

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
	for _, expected := range []string{"structured interaction scenarios", "Alex prefers tightly scoped API review requests with action items and evidence.", "Interaction goal:", "working estimate only", "Alex on api_review"} {
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
	if _, ok := assistantMetadata["scenario_plan"].(map[string]any); !ok {
		t.Fatalf("expected scenario plan in assistant metadata, got %#v", assistantMetadata)
	}

	var payload createResponseResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := payload.Metadata["context_packet"]; !ok {
		t.Fatalf("expected response metadata to include context packet, got %#v", payload.Metadata)
	}
	if _, ok := payload.Metadata["scenario_plan"]; !ok {
		t.Fatalf("expected response metadata to include scenario plan, got %#v", payload.Metadata)
	}
	if !strings.Contains(payload.OutputText, "Recommended approach:") {
		t.Fatalf("expected communication advice output, got %q", payload.OutputText)
	}
}

func TestResponsesEndpointIncludesBeliefContext(t *testing.T) {
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
		OutputText: `{"beliefs":[{"claim":"The migration project is more risky than originally estimated.","topic_name":"migration","stance":"supports","confidence":0.71,"evidence_summary":"Recent cutover delays increased perceived migration risk."}]}`,
	}, {
		ID:         "chatcmpl_belief_context",
		Model:      "gpt-4.1-mini",
		OutputText: "The migration project looks riskier than previously assumed.",
	}}, embedResponse: llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}}}

	requestUser := fmt.Sprintf("belief-context-user-%d", time.Now().UnixNano())
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
		RawText:      "Recent cutover delays suggest the migration project is more risky than originally estimated.",
		Summary:      "Recent cutover delays suggest the migration project is more risky than originally estimated.",
		MemoryType:   "belief_update",
		Topics:       []string{"migration"},
		RequestUser:  requestUser,
		BeliefImpact: floatPointer(0.92),
		Confidence:   floatPointer(0.88),
	})
	if err != nil {
		t.Fatalf("ingest memory: %v", err)
	}
	defer func() { _ = memoryService.Delete(context.Background(), record.ID) }()

	sessionExternalID := fmt.Sprintf("belief-context-test-%d", time.Now().UnixNano())
	body := []byte(fmt.Sprintf(`{"model":"context-agent-1","input":"Summarize the current migration risk.","user":"%s","metadata":{"session_id":"%s","goal":"assess risk","topics":["migration"]}}`, requestUser, sessionExternalID))

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
	for _, expected := range []string{"Belief context:", "The migration project is more risky than originally estimated.", "currently supported"} {
		if !strings.Contains(strings.ToLower(fakeClient.request.Messages[0].Content), strings.ToLower(expected)) {
			t.Fatalf("expected system prompt to contain %q, got %s", expected, fakeClient.request.Messages[0].Content)
		}
	}

	var payload createResponseResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	metadata, ok := payload.Metadata["context_packet"].(map[string]any)
	if !ok {
		t.Fatalf("expected context packet metadata, got %#v", payload.Metadata)
	}
	beliefContext, ok := metadata["belief_context"].([]any)
	if !ok || len(beliefContext) == 0 {
		t.Fatalf("expected belief context in metadata, got %#v", metadata)
	}
}

func TestResponsesEndpointDisableMemorySkipsRetrievedContext(t *testing.T) {
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
		ID:         "chatcmpl_disable_memory",
		Model:      "gpt-4.1-mini",
		OutputText: "Ask Alex to review the proposal.",
	}, embedResponse: llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}}}

	requestUser := fmt.Sprintf("disable-memory-user-%d", time.Now().UnixNano())
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
		RequestUser:  requestUser,
		Importance:   floatPointer(0.95),
		Utility:      floatPointer(0.96),
		BeliefImpact: floatPointer(0.34),
		Confidence:   floatPointer(0.97),
	})
	if err != nil {
		t.Fatalf("ingest memory: %v", err)
	}
	defer func() { _ = memoryService.Delete(context.Background(), record.ID) }()

	embedCountBefore := fakeClient.embedCount
	sessionExternalID := fmt.Sprintf("disable-memory-test-%d", time.Now().UnixNano())
	body := []byte(fmt.Sprintf(`{"model":"context-agent-1","input":"Help me ask Alex to review the proposal.","user":"%s","disable_memory":true,"metadata":{"session_id":"%s","goal":"get_review","people":["Alex"],"topics":["api_review"],"memory_mode":"social_strategy"}}`, requestUser, sessionExternalID))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d body=%s", recorder.Code, recorder.Body.String())
	}
	if fakeClient.embedCount != embedCountBefore {
		t.Fatalf("expected disable_memory to skip embeddings, before=%d after=%d", embedCountBefore, fakeClient.embedCount)
	}
	if len(fakeClient.request.Messages) < 2 {
		t.Fatalf("expected llm request messages, got %#v", fakeClient.request.Messages)
	}
	for _, unexpected := range []string{"Alex prefers tightly scoped API review requests with action items and evidence.", "Alex on api_review", "currently supported"} {
		if strings.Contains(fakeClient.request.Messages[0].Content, unexpected) {
			t.Fatalf("expected disable_memory prompt to omit %q, got %s", unexpected, fakeClient.request.Messages[0].Content)
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
	if assistantMetadata["disable_memory"] != true {
		t.Fatalf("expected assistant metadata to record disable_memory, got %#v", assistantMetadata)
	}
	contextPacket, ok := assistantMetadata["context_packet"].(map[string]any)
	if !ok {
		t.Fatalf("expected context packet metadata, got %#v", assistantMetadata)
	}
	if _, ok := contextPacket["memory_context"]; ok {
		t.Fatalf("expected sparse context packet without memory context, got %#v", contextPacket)
	}
	if _, ok := contextPacket["people_context"]; ok {
		t.Fatalf("expected sparse context packet without people context, got %#v", contextPacket)
	}
	if _, ok := contextPacket["belief_context"]; ok {
		t.Fatalf("expected sparse context packet without belief context, got %#v", contextPacket)
	}

	var payload createResponseResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Metadata["disable_memory"] != true {
		t.Fatalf("expected response metadata to include disable_memory, got %#v", payload.Metadata)
	}
	payloadContextPacket, ok := payload.Metadata["context_packet"].(map[string]any)
	if !ok {
		t.Fatalf("expected response metadata context packet, got %#v", payload.Metadata)
	}
	if _, ok := payloadContextPacket["memory_context"]; ok {
		t.Fatalf("expected response context packet without memory context, got %#v", payloadContextPacket)
	}
}

func floatPointer(value float64) *float64 {
	return &value
}

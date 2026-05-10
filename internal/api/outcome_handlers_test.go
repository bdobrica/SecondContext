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

	beliefsvc "github.com/bdobrica/SecondContext/internal/beliefs"
	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	"github.com/bdobrica/SecondContext/internal/prompts"
	"github.com/bdobrica/SecondContext/internal/scenarios"
)

func TestCreateInteractionOutcomeEndToEnd(t *testing.T) {
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
		OutputText: `{"summary":"Alex agreed to review and asked for the API section only.","success_score":0.78,"prediction_error":"The system underestimated Alex's need for a tightly scoped request.","people":["Alex"],"topics":["api_review"],"importance":0.83,"utility":0.9,"belief_impact":0.44,"confidence":0.87,"graph_edges":[{"source_kind":"person","source_name":"Alex","target_kind":"topic","target_name":"api_review","relationship":"prefers_narrow_scope","confidence":0.81}]}`,
	}, {
		OutputText: `{"pairs":[{"person_name":"Alex","topic_name":"api_review","niceness":0.83,"readiness":0.86,"competence":0.91,"capacity":0.37,"confidence":0.84,"evidence_summary":"Agreed to review, but wanted the API scope narrowed.","last_observed_at":"2026-05-10T12:00:00Z"}]}`,
	}, {
		OutputText: `{"beliefs":[{"claim":"Alex prefers narrowly scoped API review requests.","topic_name":"api_review","stance":"supports","confidence":0.76,"evidence_summary":"He agreed to review once the scope was narrowed to the API section."}]}`,
	}}, embedResponse: llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}}}

	requestUser := fmt.Sprintf("outcome-user-%d", time.Now().UnixNano())
	cfg := config.Config{
		App:     config.AppConfig{Name: "salience-graph", Env: "test"},
		Dev:     config.DevConfig{UserExternalID: "dev-user", UserName: "Dev User", UserEmail: "dev@example.com"},
		OpenAI:  config.OpenAIConfig{ChatModel: "gpt-4.1-mini", EmbeddingModel: "text-embedding-3-small"},
		Qdrant:  config.QdrantConfig{URL: qdrantServer.URL, Collection: "memory_items", VectorSize: 3, DenseVector: "dense", SparseVector: "sparse"},
		Scoring: config.ScoringConfig{RetrievalWeight: 0.35, RecencyWeight: 0.15, ImportanceWeight: 0.15, UtilityWeight: 0.15, GoalRelevanceWeight: 0.10, BeliefImpactWeight: 0.05, ConfidenceWeight: 0.05, RecencyHalfLifeDays: 30, RedundancyThreshold: 0.82},
	}

	server := NewServerWithClient(cfg, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), pool, fakeClient)

	users := db.NewUserRepository(pool)
	sessions := db.NewSessionRepository(pool)
	messages := db.NewMessageRepository(pool)
	user, err := users.Ensure(context.Background(), db.EnsureUserParams{ExternalID: requestUser, DisplayName: requestUser})
	if err != nil {
		t.Fatalf("ensure user: %v", err)
	}
	sessionExternalID := fmt.Sprintf("outcome-session-%d", time.Now().UnixNano())
	session, err := sessions.Create(context.Background(), db.CreateSessionParams{UserID: user.ID, ExternalID: sessionExternalID, Title: "Outcome Session"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	metadataBytes, err := json.Marshal(map[string]any{
		"context_packet": prompts.ContextPacket{
			Mode:   prompts.ResponseModeCommunicationAdvice,
			Goal:   "get_review",
			People: []string{"Alex"},
			Topics: []string{"api_review"},
		},
		"scenario_plan": scenarios.Plan{
			RecommendedStrategyID:   "scoped_direct",
			RecommendationRationale: "Alex responds best to a narrowly scoped request.",
			Strategies: []scenarios.Strategy{{
				ID:                  "scoped_direct",
				Label:               "Scoped direct request",
				MessageDraft:        "Alex, could you review just the API section and flag the main risks?",
				PredictedResponse:   "Yes, I can review the API section if you keep the scope narrow.",
				Benefits:            []string{"clear scope"},
				Risks:               []string{"less broad feedback"},
				LikelihoodOfSuccess: 0.82,
				FallbackOption:      "Narrow the ask even further to only the highest-risk API changes.",
			}},
		},
	})
	if err != nil {
		t.Fatalf("marshal assistant metadata: %v", err)
	}
	assistantMessage, err := messages.Create(context.Background(), db.CreateMessageParams{
		SessionID: session.ID,
		UserID:    user.ID,
		Role:      "assistant",
		Content:   "Recommended approach: Scoped direct request",
		Model:     "gpt-4.1-mini",
		Metadata:  metadataBytes,
	})
	if err != nil {
		t.Fatalf("create assistant message: %v", err)
	}

	body := []byte(fmt.Sprintf(`{"session_id":"%s","assistant_message_id":"%s","raw_text":"Alex replied quickly and agreed to review, but asked me to narrow the request to the API section only.","user":"%s"}`, sessionExternalID, assistantMessage.ID, requestUser))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/interactions/outcome", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("unexpected status %d body=%s", recorder.Code, recorder.Body.String())
	}

	var payload createInteractionOutcomeResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Outcome.PredictedOutcome != "Yes, I can review the API section if you keep the scope narrow." {
		t.Fatalf("unexpected predicted outcome %#v", payload.Outcome)
	}
	if payload.Outcome.SuccessScore != 0.78 {
		t.Fatalf("unexpected success score %#v", payload.Outcome)
	}
	if payload.Memory.MemoryType != "outcome" {
		t.Fatalf("expected outcome memory, got %#v", payload.Memory)
	}
	if len(payload.GraphEdges) != 1 || !strings.EqualFold(payload.GraphEdges[0].Relationship, "prefers_narrow_scope") {
		t.Fatalf("expected graph edge update, got %#v", payload.GraphEdges)
	}

	storedOutcome, err := db.NewInteractionOutcomeRepository(pool).GetByID(context.Background(), payload.Outcome.ID)
	if err != nil {
		t.Fatalf("get interaction outcome: %v", err)
	}
	if storedOutcome.MessageID != assistantMessage.ID {
		t.Fatalf("expected linked assistant message, got %#v", storedOutcome)
	}

	person, err := db.NewPersonRepository(pool).GetByName(context.Background(), user.ID, "Alex")
	if err != nil {
		t.Fatalf("get person: %v", err)
	}
	modelsList, err := db.NewPersonTopicModelRepository(pool).ListByPerson(context.Background(), user.ID, person.ID)
	if err != nil {
		t.Fatalf("list person topic models: %v", err)
	}
	if len(modelsList) == 0 {
		t.Fatalf("expected person model updates, got %#v", modelsList)
	}
	beliefsList, err := beliefsvc.NewService(cfg, pool, fakeClient).ListBeliefs(context.Background(), beliefsvc.ListBeliefsParams{UserExternalID: requestUser, TopicName: "api_review", Limit: 10})
	if err != nil {
		t.Fatalf("list beliefs: %v", err)
	}
	if len(beliefsList) == 0 {
		t.Fatalf("expected belief update, got %#v", beliefsList)
	}
	edges, err := db.NewGraphEdgeRepository(pool).ListByUser(context.Background(), user.ID, 10)
	if err != nil {
		t.Fatalf("list graph edges: %v", err)
	}
	if len(edges) == 0 {
		t.Fatalf("expected graph edge records, got %#v", edges)
	}
}

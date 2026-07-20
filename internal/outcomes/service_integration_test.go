package outcomes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOutcomeFailureRetriesConverge(t *testing.T) {
	if os.Getenv("OUTCOME_INTEGRATION") != "1" {
		t.Skip("set OUTCOME_INTEGRATION=1 to run the mandatory outcome recovery lane")
	}
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("POSTGRES_DSN is required when OUTCOME_INTEGRATION=1")
	}
	migrations, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RunMigrationsUp(config.PostgresConfig{DSN: dsn}, migrations); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	qdrant := newOutcomeQdrant(t)
	defer qdrant.Close()
	cfg := outcomeTestConfig(qdrant.URL)
	stages := []string{
		"analysis_completed",
		"outcome_inserted",
		"memory_created",
		"qdrant_upserted",
		"person_model_updated",
		"belief_updated",
		"graph_edge_1_created",
		"graph_edge_2_created",
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			key := fmt.Sprintf("%s-%d", stage, time.Now().UnixNano())
			user, err := db.NewUserRepository(pool).Ensure(context.Background(), db.EnsureUserParams{ExternalID: key, DisplayName: key})
			if err != nil {
				t.Fatal(err)
			}
			service := NewService(cfg, pool, outcomeLLM{})
			failed := false
			service.fail = func(current string) error {
				if current == stage && !failed {
					failed = true
					return errors.New("injected " + stage)
				}
				return nil
			}
			params := CreateOutcomeParams{
				RawText: "Alex agreed to review the API proposal.", Goal: "get review",
				People: []string{"Alex"}, Topics: []string{"API"}, UserExternalID: key, IdempotencyKey: key,
			}
			if _, err := service.CreateOutcome(context.Background(), params); err == nil {
				t.Fatalf("expected injected %s failure", stage)
			}
			result, err := service.CreateOutcome(context.Background(), params)
			if err != nil {
				t.Fatalf("retry %s: %v", stage, err)
			}
			if result.Outcome.ProcessingStatus != "completed" {
				t.Fatalf("outcome status = %q", result.Outcome.ProcessingStatus)
			}
			assertOutcomeCounts(t, pool, user.ID, result.Outcome.ID, result.Memory.ID)
		})
	}
}

func assertOutcomeCounts(t *testing.T, pool *pgxpool.Pool, userID, outcomeID, memoryID string) {
	t.Helper()
	var outcomes, memories, edges int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM interaction_outcomes WHERE user_id=$1`, userID).Scan(&outcomes); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM memory_items WHERE user_id=$1 AND source='interaction.outcome'`, userID).Scan(&memories); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM graph_edges WHERE user_id=$1 AND metadata->>'outcome_id'=$2`, userID, outcomeID).Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if outcomes != 1 || memories != 1 || edges != 2 {
		t.Fatalf("non-converged counts outcomes=%d memories=%d edges=%d", outcomes, memories, edges)
	}
	var evidenceCount int
	var evidenceIDs []string
	if err := pool.QueryRow(context.Background(), `SELECT evidence_count FROM person_topic_models WHERE user_id=$1`, userID).Scan(&evidenceCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT ARRAY(SELECT value::text FROM unnest(evidence_memory_ids) value) FROM beliefs WHERE user_id=$1`, userID).Scan(&evidenceIDs); err != nil {
		t.Fatal(err)
	}
	if evidenceCount != 1 || len(evidenceIDs) != 1 || evidenceIDs[0] != memoryID {
		t.Fatalf("derived evidence duplicated: model_count=%d belief_ids=%v", evidenceCount, evidenceIDs)
	}
}

type outcomeLLM struct{}

func (outcomeLLM) Embed(context.Context, llm.EmbedRequest) (llm.EmbedResponse, error) {
	return llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}}, nil
}

func (outcomeLLM) Generate(_ context.Context, request llm.GenerateRequest) (llm.GenerateResponse, error) {
	system := request.Messages[0].Content
	var output string
	switch {
	case strings.Contains(system, "topic-specific working models"):
		output = `{"pairs":[{"person_name":"Alex","topic_name":"API","niceness":0.8,"readiness":0.8,"competence":0.8,"capacity":0.8,"confidence":0.8,"evidence_summary":"Alex agreed.","last_observed_at":"2026-07-20T12:00:00Z"}]}`
	case strings.Contains(system, "explicit belief or claim"):
		output = `{"beliefs":[{"claim":"Alex will review the API proposal.","topic_name":"API","stance":"supports","confidence":0.8,"evidence_summary":"Alex agreed."}]}`
	default:
		output = `{"summary":"Alex agreed to review.","success_score":0.9,"prediction_error":"","people":["Alex"],"topics":["API"],"importance":0.8,"utility":0.8,"belief_impact":0.8,"confidence":0.8,"graph_edges":[{"source_kind":"person","source_name":"Alex","target_kind":"topic","target_name":"API","relationship":"reviews","confidence":0.8},{"source_kind":"person","source_name":"Alex","target_kind":"goal","target_name":"get review","relationship":"supports","confidence":0.8}]}`
	}
	return llm.GenerateResponse{OutputText: output}, nil
}

func outcomeTestConfig(qdrantURL string) config.Config {
	return config.Config{
		Dev:    config.DevConfig{UserExternalID: "dev", UserName: "Dev"},
		OpenAI: config.OpenAIConfig{ChatModel: "test", EmbeddingModel: "test"},
		Qdrant: config.QdrantConfig{URL: qdrantURL, Collection: "outcomes", VectorSize: 3, DenseVector: "dense", SparseVector: "sparse"},
	}
}

func newOutcomeQdrant(t *testing.T) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	points := map[string]json.RawMessage{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/points") {
			var body struct {
				Points []struct {
					ID string `json:"id"`
				} `json:"points"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			for _, point := range body.Points {
				points[point.ID] = json.RawMessage(`{}`)
			}
			mu.Unlock()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": map[string]any{"status": "acknowledged"}})
	}))
}

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
	"sort"
	"strings"
	"testing"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	memsvc "github.com/bdobrica/SecondContext/internal/memory"
	"github.com/bdobrica/SecondContext/internal/qdrant"
)

type storedPoint struct {
	ID           string
	DenseVector  []float64
	SparseVector qdrant.SparseVector
	Payload      map[string]any
}

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
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)), pool, &fakeLLMClient{
		response:      emptyDerivedUpdatesResponse(),
		embedResponse: llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}},
	})

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

func TestMemoryExtractEndpoint(t *testing.T) {
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

	fakeClient := &fakeLLMClient{
		responses: []llm.GenerateResponse{{
			OutputText: "not valid json",
		}, {
			OutputText: `{"summary":"Alex prefers narrow review scopes.","type":"person_preference","people":["Alex"],"topics":["infrastructure","review_process"],"entities":[{"type":"person","name":"Alex","confidence":0.92},{"type":"topic","name":"Infrastructure","confidence":0.81}],"importance":0.72,"utility":0.84,"belief_impact":0.19,"confidence":0.91,"expires_in_days":45}`,
		},
			emptyDerivedUpdatesResponse(),
			emptyDerivedUpdatesResponse(),
		},
		embedResponse: llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}},
	}

	server := NewServerWithClient(config.Config{
		App:    config.AppConfig{Name: "salience-graph", Env: "test"},
		Dev:    config.DevConfig{UserExternalID: "dev-user", UserName: "Dev User", UserEmail: "dev@example.com"},
		OpenAI: config.OpenAIConfig{ChatModel: "gpt-4.1-mini", EmbeddingModel: "text-embedding-3-small"},
		Qdrant: config.QdrantConfig{URL: qdrantServer.URL, Collection: "memory_items", VectorSize: 3},
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)), pool, fakeClient)

	body := []byte(`{"raw_text":"Alex wants tightly scoped infrastructure review requests.","user":"dev-user","metadata":{"session_id":"extract-test-session"}}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/memory/extract", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("unexpected extract status %d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(fakeClient.requests) < 2 {
		t.Fatalf("expected repair flow to call llm at least twice, got %d", len(fakeClient.requests))
	}

	var payload extractMemoryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Memory.QdrantPointID == "" {
		t.Fatal("expected qdrant point id")
	}
	if len(payload.Extraction.Entities) != 2 {
		t.Fatalf("unexpected extraction entities %#v", payload.Extraction.Entities)
	}

	entities, err := db.NewMemoryEntityRepository(pool).ListByMemoryItemID(context.Background(), payload.Memory.ID)
	if err != nil {
		t.Fatalf("list memory entities: %v", err)
	}
	if len(entities) != 2 {
		t.Fatalf("expected 2 stored entities, got %d", len(entities))
	}

	listService := memsvc.NewService(config.Config{
		App:    config.AppConfig{Name: "salience-graph", Env: "test"},
		Dev:    config.DevConfig{UserExternalID: "dev-user", UserName: "Dev User", UserEmail: "dev@example.com"},
		OpenAI: config.OpenAIConfig{ChatModel: "gpt-4.1-mini", EmbeddingModel: "text-embedding-3-small"},
		Qdrant: config.QdrantConfig{URL: qdrantServer.URL, Collection: "memory_items", VectorSize: 3},
	}, pool, fakeClient)
	if err := listService.Delete(context.Background(), payload.Memory.ID); err != nil {
		t.Fatalf("cleanup delete memory: %v", err)
	}
}

func TestMemorySearchEndpoint(t *testing.T) {
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

	fakeClient := &fakeLLMClient{
		response:      emptyDerivedUpdatesResponse(),
		embedResponse: llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}},
	}
	server := NewServerWithClient(config.Config{
		App:    config.AppConfig{Name: "salience-graph", Env: "test"},
		Dev:    config.DevConfig{UserExternalID: "dev-user", UserName: "Dev User", UserEmail: "dev@example.com"},
		OpenAI: config.OpenAIConfig{EmbeddingModel: "text-embedding-3-small"},
		Qdrant: config.QdrantConfig{URL: qdrantServer.URL, Collection: "memory_items", VectorSize: 3, DenseVector: "dense", SparseVector: "sparse"},
	}, slog.New(slog.NewTextHandler(os.Stderr, nil)), pool, fakeClient)

	createdIDs := make([]string, 0, 2)
	for _, body := range [][]byte{
		[]byte(`{"raw_text":"Alex prefers tightly scoped API reviews.","summary":"Alex prefers tightly scoped API reviews.","type":"person_preference","people":["Alex"],"topics":["api_review"],"importance":0.8,"utility":0.9,"belief_impact":0.1,"confidence":0.95,"user":"search-test-user"}`),
		[]byte(`{"raw_text":"Dana wants quantified risk summaries.","summary":"Dana wants quantified risk summaries.","type":"person_preference","people":["Dana"],"topics":["risk"],"importance":0.7,"utility":0.8,"belief_impact":0.2,"confidence":0.85,"user":"search-test-user"}`),
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/memory/ingest", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("unexpected ingest status %d body=%s", recorder.Code, recorder.Body.String())
		}
		var created memoryResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode ingest response: %v", err)
		}
		createdIDs = append(createdIDs, created.ID)
	}

	searchRecorder := httptest.NewRecorder()
	searchRequest := httptest.NewRequest(http.MethodPost, "/memory/search", bytes.NewReader([]byte(`{"query":"api scoped review request","goal":"choose the best review strategy for Alex","user_external_id":"search-test-user","people":["Alex"],"confidence_threshold":0.5,"limit":5}`)))
	searchRequest.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(searchRecorder, searchRequest)

	if searchRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected search status %d body=%s", searchRecorder.Code, searchRecorder.Body.String())
	}

	var payload memorySearchResponse
	if err := json.Unmarshal(searchRecorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	if len(payload.Data) == 0 {
		t.Fatal("expected search results")
	}
	if payload.Data[0].Memory.Summary != "Alex prefers tightly scoped API reviews." {
		t.Fatalf("unexpected top result %#v", payload.Data[0])
	}
	if payload.Data[0].Scores.Dense == 0 || payload.Data[0].Scores.Sparse == 0 || payload.Data[0].Scores.Hybrid == 0 {
		t.Fatalf("expected score breakdown %#v", payload.Data[0].Scores)
	}
	if payload.Data[0].Rank != 1 {
		t.Fatalf("expected first result rank 1, got %#v", payload.Data[0])
	}
	if payload.Data[0].Scores.Final == 0 || payload.Data[0].Scores.Recency == 0 || payload.Data[0].Scores.GoalRelevance == 0 {
		t.Fatalf("expected rerank scores %#v", payload.Data[0].Scores)
	}

	for _, memoryID := range createdIDs {
		_ = memsvc.NewService(config.Config{
			App:    config.AppConfig{Name: "salience-graph", Env: "test"},
			Dev:    config.DevConfig{UserExternalID: "dev-user", UserName: "Dev User", UserEmail: "dev@example.com"},
			OpenAI: config.OpenAIConfig{EmbeddingModel: "text-embedding-3-small"},
			Qdrant: config.QdrantConfig{URL: qdrantServer.URL, Collection: "memory_items", VectorSize: 3, DenseVector: "dense", SparseVector: "sparse"},
		}, pool, fakeClient).Delete(context.Background(), memoryID)
	}
}

func newFakeQdrantServer() *httptest.Server {
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
					ID      string                     `json:"id"`
					Vector  map[string]json.RawMessage `json:"vector"`
					Payload map[string]any             `json:"payload"`
				} `json:"points"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if _, ok := collections[collection]; !ok {
				collections[collection] = make(map[string]storedPoint)
			}
			for _, point := range payload.Points {
				var dense []float64
				var sparse qdrant.SparseVector
				if rawDense, ok := point.Vector["dense"]; ok {
					_ = json.Unmarshal(rawDense, &dense)
				}
				if rawSparse, ok := point.Vector["sparse"]; ok {
					_ = json.Unmarshal(rawSparse, &sparse)
				}
				collections[collection][point.ID] = storedPoint{ID: point.ID, DenseVector: dense, SparseVector: sparse, Payload: point.Payload}
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
			results := handleFakeSearch(r, collections[collection])
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": results, "time": 0.001})
		case r.Method == http.MethodPost && len(parts) == 4 && parts[2] == "points" && parts[3] == "query":
			results := handleFakeQuery(r, collections[collection])
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": results, "time": 0.001})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "error", "error": "not found"})
		}
	}))
}

func handleFakeSearch(r *http.Request, points map[string]storedPoint) []map[string]any {
	var payload struct {
		Vector any            `json:"vector"`
		Using  string         `json:"using"`
		Filter map[string]any `json:"filter"`
	}
	_ = json.NewDecoder(r.Body).Decode(&payload)

	results := make([]map[string]any, 0)
	for _, point := range points {
		if !matchesFakeFilter(point.Payload, payload.Filter) {
			continue
		}
		score := 0.0
		switch payload.Using {
		case "sparse":
			vectorBytes, _ := json.Marshal(payload.Vector)
			var queryVector qdrant.SparseVector
			_ = json.Unmarshal(vectorBytes, &queryVector)
			score = sparseOverlap(point.SparseVector, queryVector)
		default:
			vectorBytes, _ := json.Marshal(payload.Vector)
			var queryVector []float64
			_ = json.Unmarshal(vectorBytes, &queryVector)
			score = denseSimilarity(point.DenseVector, queryVector)
		}
		if score <= 0 {
			continue
		}
		results = append(results, map[string]any{"id": point.ID, "score": score, "payload": point.Payload})
	}
	sort.Slice(results, func(i, j int) bool { return results[i]["score"].(float64) > results[j]["score"].(float64) })
	return results
}

func handleFakeQuery(r *http.Request, points map[string]storedPoint) []map[string]any {
	var envelope struct {
		Query    any    `json:"query"`
		Using    string `json:"using"`
		Limit    int    `json:"limit"`
		Prefetch []struct {
			Query  any            `json:"query"`
			Using  string         `json:"using"`
			Filter map[string]any `json:"filter"`
		} `json:"prefetch"`
		Filter map[string]any `json:"filter"`
	}
	_ = json.NewDecoder(r.Body).Decode(&envelope)
	if len(envelope.Prefetch) == 0 {
		searchPayload := map[string]any{"vector": envelope.Query, "using": envelope.Using, "filter": envelope.Filter}
		buffer, _ := json.Marshal(searchPayload)
		request := httptest.NewRequest(http.MethodPost, "/collections/x/points/search", bytes.NewReader(buffer))
		results := handleFakeSearch(request, points)
		if envelope.Limit > 0 && len(results) > envelope.Limit {
			results = results[:envelope.Limit]
		}
		return results
	}

	ranks := make(map[string]float64)
	for _, prefetch := range envelope.Prefetch {
		searchPayload := map[string]any{"vector": prefetch.Query, "using": prefetch.Using, "filter": prefetch.Filter}
		buffer, _ := json.Marshal(searchPayload)
		request := httptest.NewRequest(http.MethodPost, "/collections/x/points/search", bytes.NewReader(buffer))
		results := handleFakeSearch(request, points)
		for index, result := range results {
			id := result["id"].(string)
			ranks[id] += 1.0 / float64(60+index+1)
		}
	}

	results := make([]map[string]any, 0, len(ranks))
	for id, score := range ranks {
		results = append(results, map[string]any{"id": id, "score": score, "payload": points[id].Payload})
	}
	sort.Slice(results, func(i, j int) bool { return results[i]["score"].(float64) > results[j]["score"].(float64) })
	if envelope.Limit > 0 && len(results) > envelope.Limit {
		results = results[:envelope.Limit]
	}
	return results
}

func denseSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	total := 0.0
	for i := 0; i < limit; i++ {
		total += a[i] * b[i]
	}
	return total
}

func sparseOverlap(a, b qdrant.SparseVector) float64 {
	lookup := make(map[uint32]float64, len(a.Indices))
	for index, id := range a.Indices {
		lookup[id] = a.Values[index]
	}
	total := 0.0
	for index, id := range b.Indices {
		total += lookup[id] * b.Values[index]
	}
	return total
}

func matchesFakeFilter(payload map[string]any, filter map[string]any) bool {
	if len(filter) == 0 {
		return true
	}
	must, _ := filter["must"].([]any)
	for _, rawCondition := range must {
		condition, _ := rawCondition.(map[string]any)
		key, _ := condition["key"].(string)
		if match, ok := condition["match"].(map[string]any); ok {
			if value, ok := match["value"]; ok {
				if payload[key] != value {
					return false
				}
			}
			if values, ok := match["any"].([]any); ok {
				payloadValues, _ := payload[key].([]any)
				if len(payloadValues) == 0 {
					if stringValues, ok := payload[key].([]string); ok {
						payloadValues = make([]any, 0, len(stringValues))
						for _, value := range stringValues {
							payloadValues = append(payloadValues, value)
						}
					}
				}
				matched := false
				for _, expected := range values {
					for _, actual := range payloadValues {
						if actual == expected {
							matched = true
						}
					}
				}
				if !matched {
					return false
				}
			}
		}
		if valueRange, ok := condition["range"].(map[string]any); ok {
			if gte, ok := valueRange["gte"].(float64); ok {
				actual, _ := payload[key].(float64)
				if actual < gte {
					return false
				}
			}
		}
	}

	return true
}

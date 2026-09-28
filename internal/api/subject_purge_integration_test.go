package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	"github.com/bdobrica/SecondContext/internal/qdrant"
)

func TestSubjectPurgeRecoveryIsolationAndContinuity(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool, cleanup := openIsolationTestDB(t)
	defer cleanup()
	ctx := context.Background()
	var uidA, uidB string
	if err := pool.QueryRow(ctx, "SELECT gen_random_uuid()::text,gen_random_uuid()::text").Scan(&uidA, &uidB); err != nil {
		t.Fatal(err)
	}
	subjectA, subjectB := "oria:"+uidA, "oria:"+uidB
	port := os.Getenv("INTEGRATION_QDRANT_PORT")
	if port == "" {
		port = "56333"
	}
	targetURL := os.Getenv("QDRANT_URL")
	if targetURL == "" {
		targetURL = "http://127.0.0.1:" + port
	}
	target, err := url.Parse(targetURL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	var fail atomic.Bool
	gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/points/delete") && fail.Load() {
			w.WriteHeader(503)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer gate.Close()
	cfg := isolationTestConfig(t, "purge_"+strings.ReplaceAll(uidA, "-", ""))
	cfg.Auth.ServiceTokens = []config.AuthTokenConfig{{Subject: "oria:", Token: "service-secret"}}
	cfg.Qdrant.URL = gate.URL
	fake := &fakeLLMClient{response: llm.GenerateResponse{OutputText: `{"pairs":[],"beliefs":[]}`}}
	server := NewServerWithClient(cfg, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), pool, fake)
	invoke := func(subject, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer service-secret")
		req.Header.Set("X-SecondContext-Subject", subject)
		out := httptest.NewRecorder()
		server.Handler().ServeHTTP(out, req)
		return out
	}
	ingest := func(subject, session string) {
		t.Helper()
		body := fmt.Sprintf(`{"user":%q,"raw_text":"The user prefers concise readings.","summary":"The user prefers concise readings.","type":"preference","source":"oria","confidence":1,"metadata":{"session_id":%q}}`, subject, session)
		out := invoke(subject, "/memory/ingest", body)
		if out.Code != 201 {
			t.Fatalf("ingest %d %s", out.Code, out.Body)
		}
	}
	sessionA, sessionB := "session-"+uidA, "session-"+uidB
	ingest(subjectA, sessionA)
	responseBody := fmt.Sprintf(`{"user":%q,"input":"Explain my chart concisely.","metadata":{"session_id":%q},"instructions":"ephemeral-fact-marker"}`, subjectA, sessionA)
	out := invoke(subjectA, "/v1/responses", responseBody)
	if out.Code != 200 {
		t.Fatalf("response %d %s", out.Code, out.Body)
	}
	if !strings.Contains(fake.request.Messages[0].Content, "prefers concise readings") {
		t.Fatal("real retrieval omitted prior preference")
	}
	if strings.Contains(out.Body.String(), "ephemeral-fact-marker") {
		t.Fatal("ephemeral instructions leaked into context metadata")
	}
	// Different subject cannot see or reuse the first subject's memory/session.
	out = invoke(subjectB, "/v1/responses", fmt.Sprintf(`{"user":%q,"input":"hello","metadata":{"session_id":%q}}`, subjectB, sessionB))
	if out.Code != 200 || strings.Contains(out.Body.String(), "prefers concise readings") {
		t.Fatalf("foreign context: %d %s", out.Code, out.Body)
	}
	out = invoke(subjectB, "/v1/responses", fmt.Sprintf(`{"user":%q,"input":"hello","metadata":{"session_id":%q}}`, subjectB, sessionA))
	if out.Code != 404 {
		t.Fatalf("foreign session accepted: %d", out.Code)
	}
	ingest(subjectB, sessionB)
	userA, err := db.NewUserRepository(pool).GetByExternalID(ctx, subjectA)
	if err != nil {
		t.Fatal(err)
	}
	userB, err := db.NewUserRepository(pool).GetByExternalID(ctx, subjectB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE external_id=$1", subjectB)
		_, _ = pool.Exec(ctx, "DELETE FROM subject_purges WHERE external_id=$1", subjectA)
	}()
	// Exercise every owned SQL data family and a vector orphan with no memory row.
	var personID, topicID, memoryID, entityID string
	for _, item := range []struct {
		sql  string
		args []any
		dst  *string
	}{
		{"INSERT INTO people(user_id,name,normalized_name) VALUES($1,'synthetic','synthetic') RETURNING id::text", []any{userA.ID}, &personID},
		{"INSERT INTO topics(user_id,name,normalized_name) VALUES($1,'synthetic','synthetic') RETURNING id::text", []any{userA.ID}, &topicID},
		{"SELECT id::text FROM memory_items WHERE user_id=$1 LIMIT 1", []any{userA.ID}, &memoryID},
		{"INSERT INTO memory_entities(memory_item_id,entity_type,entity_name,normalized_name) VALUES($1,'topic','synthetic','synthetic') RETURNING id::text", []any{memoryID}, &entityID},
	} { // memoryID is assigned before constructing its dependent insert below.
		if strings.Contains(item.sql, "INSERT INTO memory_entities") {
			item.args = []any{memoryID}
		}
		if err := pool.QueryRow(ctx, item.sql, item.args...).Scan(item.dst); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		"INSERT INTO person_topic_models(user_id,person_id,topic_id) VALUES($1,$2,$3)",
		"INSERT INTO beliefs(user_id,topic_id,claim,normalized_claim,stance) VALUES($1,$3,'synthetic','synthetic','unknown')",
		"INSERT INTO graph_edges(user_id,source_kind,source_name,target_kind,target_name,relationship) VALUES($1,'topic','a','topic','b','related')",
		"INSERT INTO interaction_outcomes(user_id,actual_outcome) VALUES($1,'synthetic')",
	} {
		var args []any
		switch {
		case strings.Contains(query, "person_topic_models"):
			args = []any{userA.ID, personID, topicID}
		case strings.Contains(query, "INSERT INTO beliefs"):
			query = strings.ReplaceAll(query, "$3", "$2")
			args = []any{userA.ID, topicID}
		default:
			args = []any{userA.ID}
		}
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	qc := qdrant.NewClient(cfg.Qdrant)
	if err := qc.UpsertPoint(ctx, cfg.Qdrant.Collection, qdrant.Point{ID: uidA, DenseVector: []float64{.1, .2, .3}, SparseVector: qdrant.SparseVector{Indices: []uint32{1}, Values: []float64{1}}, Payload: map[string]any{"user_id": userA.ID}}); err != nil {
		t.Fatal(err)
	}
	// A failed external deletion keeps canonical records and the durable fence.
	fail.Store(true)
	purgeBody := fmt.Sprintf(`{"user":%q}`, subjectA)
	out = invoke(subjectA, "/v1/subjects/purge", purgeBody)
	if out.Code != 503 {
		t.Fatalf("purge should fail: %d %s", out.Code, out.Body)
	}
	out = invoke(subjectA, "/v1/responses", responseBody)
	if out.Code != 410 {
		t.Fatalf("pending purge did not fence: %d", out.Code)
	}
	if _, err := db.NewUserRepository(pool).GetByExternalID(ctx, subjectA); err != nil {
		t.Fatal("canonical row lost before index acknowledgement")
	}
	// Restart API object: durable state, not in-process memory, anchors retry.
	server = NewServerWithClient(cfg, slog.Default(), pool, fake)
	fail.Store(false)
	// Simulate a local crash/commit failure AFTER successful remote deletion.
	functionName := "purge_failure_" + strings.ReplaceAll(uidA, "-", "")
	sql := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'synthetic failure'; END; $$ LANGUAGE plpgsql;
      CREATE TRIGGER %s BEFORE DELETE ON users FOR EACH ROW WHEN (OLD.external_id = '%s') EXECUTE FUNCTION %s();`, functionName, functionName, subjectA, functionName)
	if _, err := pool.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
	dropFailure := func() {
		_, _ = pool.Exec(ctx, "DROP TRIGGER IF EXISTS "+functionName+" ON users; DROP FUNCTION IF EXISTS "+functionName+"();")
	}
	defer dropFailure()
	out = invoke(subjectA, "/v1/subjects/purge", purgeBody)
	if out.Code != 503 {
		t.Fatalf("SQL failure did not leave retryable purge: %d", out.Code)
	}
	if _, err := db.NewUserRepository(pool).GetByExternalID(ctx, subjectA); err != nil {
		t.Fatal("SQL rollback lost canonical row")
	}
	dropFailure()
	for range 2 {
		out = invoke(subjectA, "/v1/subjects/purge", purgeBody)
		if out.Code != 200 {
			t.Fatalf("retry %d %s", out.Code, out.Body)
		}
		var result map[string]any
		_ = json.Unmarshal(out.Body.Bytes(), &result)
		if result["status"] != "completed" || result["user"] != subjectA {
			t.Fatal(result)
		}
	}
	for _, table := range []string{"sessions", "messages", "memory_items", "people", "topics", "person_topic_models", "beliefs", "graph_edges", "interaction_outcomes"} {
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE user_id=$1", userA.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
	var count int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM memory_entities WHERE id=$1", entityID).Scan(&count)
	if count != 0 {
		t.Fatal("entity survived")
	}
	for _, item := range []struct {
		id   string
		want int
	}{{userA.ID, 0}, {userB.ID, 1}} {
		result, err := qc.SearchDense(ctx, cfg.Qdrant.Collection, []float64{.1, .2, .3}, 10, &qdrant.Filter{Must: []map[string]any{{"key": "user_id", "match": map[string]any{"value": item.id}}}})
		if err != nil || len(result) != item.want {
			t.Fatalf("index count=%d want=%d err=%v", len(result), item.want, err)
		}
	}
	if _, err := db.NewUserRepository(pool).Ensure(ctx, db.EnsureUserParams{ExternalID: subjectA, DisplayName: "late retry"}); err == nil {
		t.Fatal("subject resurrected")
	}
	out = invoke(subjectA, "/memory/ingest", fmt.Sprintf(`{"user":%q}`, subjectA))
	if out.Code != 410 {
		t.Fatalf("late write accepted %d", out.Code)
	}
	out = invoke(subjectB, "/v1/subjects/purge", purgeBody)
	if out.Code != 400 {
		t.Fatalf("foreign purge accepted %d", out.Code)
	}
	// Unknown subjects get a durable fence and an idempotent completed response.
	unknown := "oria:00000000-0000-4000-8000-000000000001"
	out = invoke(unknown, "/v1/subjects/purge", fmt.Sprintf(`{"user":%q}`, unknown))
	if out.Code != 200 {
		t.Fatalf("absent purge %d", out.Code)
	}
	_, _ = pool.Exec(ctx, "DELETE FROM subject_purges WHERE external_id=$1", unknown)
	// Existing ordinary credentials can purge only themselves, without delegation.
	ordinary := "ordinary-" + uidA
	cfg.Auth.Tokens = append(cfg.Auth.Tokens, config.AuthTokenConfig{Subject: ordinary, Token: "ordinary-secret"})
	server = NewServerWithClient(cfg, slog.Default(), pool, fake)
	req := httptest.NewRequest("POST", "/v1/subjects/purge", bytes.NewBufferString(fmt.Sprintf(`{"user":%q}`, ordinary)))
	req.Header.Set("Authorization", "Bearer ordinary-secret")
	ordinaryResult := httptest.NewRecorder()
	server.Handler().ServeHTTP(ordinaryResult, req)
	if ordinaryResult.Code != 200 {
		t.Fatalf("ordinary purge: %d", ordinaryResult.Code)
	}
	_, _ = pool.Exec(ctx, "DELETE FROM subject_purges WHERE external_id=$1", ordinary)

}

type blockedSubjectLLM struct{ entered, release chan struct{} }

func (b *blockedSubjectLLM) Generate(ctx context.Context, _ llm.GenerateRequest) (llm.GenerateResponse, error) {
	close(b.entered)
	select {
	case <-b.release:
		return llm.GenerateResponse{OutputText: "synthetic"}, nil
	case <-ctx.Done():
		return llm.GenerateResponse{}, ctx.Err()
	}
}
func (b *blockedSubjectLLM) Embed(context.Context, llm.EmbedRequest) (llm.EmbedResponse, error) {
	return llm.EmbedResponse{Vector: []float64{.1, .2, .3}}, nil
}
func TestPurgeWaitsForInflightRequestAcrossServers(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool, cleanup := openIsolationTestDB(t)
	defer cleanup()
	var identifier string
	if err := pool.QueryRow(context.Background(), "SELECT gen_random_uuid()::text").Scan(&identifier); err != nil {
		t.Fatal(err)
	}
	subject := "oria:" + identifier
	cfg := isolationTestConfig(t, "unused")
	cfg.Auth.ServiceTokens = []config.AuthTokenConfig{{Subject: "oria:", Token: "service-secret"}}
	// Missing collection is a valid empty index; a generic 404 is not.
	q := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"status":{"error":"Collection unused not found"}}`))
	}))
	defer q.Close()
	cfg.Qdrant.URL = q.URL
	blocking := &blockedSubjectLLM{make(chan struct{}), make(chan struct{})}
	first := NewServerWithClient(cfg, slog.Default(), pool, blocking)
	second := NewServerWithClient(cfg, slog.Default(), pool, &fakeLLMClient{})
	request := func(server *Server, path, body string) int {
		req := httptest.NewRequest("POST", path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer service-secret")
		req.Header.Set("X-SecondContext-Subject", subject)
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		return res.Code
	}
	conversation := make(chan int, 1)
	purge := make(chan int, 1)
	go func() {
		conversation <- request(first, "/v1/responses", fmt.Sprintf(`{"user":%q,"input":"synthetic","disable_memory":true}`, subject))
	}()
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("response never reached LLM")
	}
	go func() { purge <- request(second, "/v1/subjects/purge", fmt.Sprintf(`{"user":%q}`, subject)) }()
	select {
	case result := <-purge:
		close(blocking.release)
		t.Fatalf("purge passed in-flight request: %d", result)
	case <-time.After(100 * time.Millisecond):
	}
	close(blocking.release)
	if result := <-conversation; result != 200 {
		t.Fatal(result)
	}
	if result := <-purge; result != 200 {
		t.Fatal(result)
	}
	if result := request(first, "/v1/responses", fmt.Sprintf(`{"user":%q,"input":"late"}`, subject)); result != 410 {
		t.Fatal(result)
	}
	_, _ = pool.Exec(context.Background(), "DELETE FROM subject_purges WHERE external_id=$1", subject)
}

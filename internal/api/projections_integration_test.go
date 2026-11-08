package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/llm"
)

func TestLinkedProjectionLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool, cleanup := openIsolationTestDB(t)
	defer cleanup()
	var id string
	if err := pool.QueryRow(context.Background(), "SELECT gen_random_uuid()::text").Scan(&id); err != nil {
		t.Fatal(err)
	}
	subject := "oria:" + id
	other := "oria:22222222-2222-4222-8222-222222222222"
	cfg := isolationTestConfig(t, "unused")
	cfg.Qdrant.URL = os.Getenv("QDRANT_URL")
	if cfg.Qdrant.URL == "" {
		cfg.Qdrant.URL = "http://127.0.0.1:56333"
	}
	cfg.Auth.ServiceTokens = []config.AuthTokenConfig{{Subject: "oria:", Token: "test-service"}}
	fake := &fakeLLMClient{response: llm.GenerateResponse{OutputText: "A remembered interpretive theme."}}
	server := NewServerWithClient(cfg, slog.Default(), pool, fake)
	call := func(who, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", path, bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer test-service")
		req.Header.Set("X-SecondContext-Subject", who)
		out := httptest.NewRecorder()
		server.Handler().ServeHTTP(out, req)
		return out
	}
	defer pool.Exec(context.Background(), "DELETE FROM users WHERE external_id IN ($1,$2)", subject, other) //nolint:errcheck
	ref := projectionRef{"event", "11111111-1111-4111-8111-111111111111", 1}
	p := contextProjection{Record: ref, Label: "wedding", Summary: "Synthetic wedding significance marker", KnowledgeKind: "user_statement", Sources: []projectionRef{ref}, HouseThemes: []int{4, 7, 8}, Salience: &projectionSalience{1, "user_stated", "linked-salience-1"}}
	up := projectionRequest{Version: 1, Operation: "upsert", User: subject, OperationID: "33333333-3333-4333-8333-333333333333", Projection: &p}
	for range 2 {
		out := call(subject, "/v1/knowledge/upsert", up)
		if out.Code != 200 {
			t.Fatalf("upsert %d %s", out.Code, out.Body)
		}
	}
	conflict := up
	conflict.Projection = &contextProjection{}
	*conflict.Projection = p
	conflict.Projection.Summary = "conflict"
	if out := call(subject, "/v1/knowledge/upsert", conflict); out.Code != 409 {
		t.Fatalf("conflict %d", out.Code)
	}
	query := projectionRequest{Version: 1, Operation: "query", User: subject, References: []projectionRef{ref}, Limit: 6}
	out := call(subject, "/v1/knowledge/query", query)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "significance marker") {
		t.Fatal(out.Code, out.Body)
	}
	query.User = other
	out = call(other, "/v1/knowledge/query", query)
	if out.Code != 200 || strings.Contains(out.Body.String(), "significance marker") {
		t.Fatal("scope leak", out.Code, out.Body)
	}
	// New protocol cannot be used anonymously, with conflicting selectors or to
	// expand authority to the ordinary extraction API.
	if out = call(other, "/v1/knowledge/upsert", up); out.Code != 400 {
		t.Fatal(out.Code)
	}
	if out = call(subject, "/memory/extract", map[string]any{"raw_text": "test"}); out.Code != 403 {
		t.Fatal(out.Code)
	}
	// Every response using this projection is linked BEFORE generation. Erasure
	// removes its full retained session, user text, assistant draft and packet.
	out = call(subject, "/v1/responses", map[string]any{"user": subject, "input": "Interpret it", "disable_memory": true, "disable_knowledge": true, "knowledge_refs": []projectionRef{ref}, "metadata": map[string]any{"session_id": "linked-" + id}})
	if out.Code != 200 || !strings.Contains(fake.request.Messages[0].Content, "significance marker") {
		t.Fatal("linked generation", out.Code, out.Body)
	}
	var count int
	_ = pool.QueryRow(context.Background(), "SELECT count(*) FROM messages m JOIN users u ON u.id=m.user_id WHERE u.external_id=$1", subject).Scan(&count)
	if count != 2 {
		t.Fatal(count)
	}
	// SQL failure rolls back the complete operation; no false deletion receipt.
	fn := "projection_failure_" + strings.ReplaceAll(id, "-", "")
	_, err := pool.Exec(context.Background(), fmt.Sprintf("CREATE FUNCTION %s() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'synthetic'; END; $$ LANGUAGE plpgsql; CREATE TRIGGER %s BEFORE DELETE ON sessions FOR EACH ROW WHEN (OLD.external_id='linked-%s') EXECUTE FUNCTION %s();", fn, fn, id, fn))
	if err != nil {
		t.Fatal(err)
	}
	drop := func() {
		_, _ = pool.Exec(context.Background(), "DROP TRIGGER IF EXISTS "+fn+" ON sessions; DROP FUNCTION IF EXISTS "+fn+"();")
	}
	defer drop()
	gone := ref
	gone.Revision = 2
	del := projectionRequest{Version: 1, Operation: "delete", User: subject, OperationID: "44444444-4444-4444-8444-444444444444", Record: &gone}
	if out = call(subject, "/v1/knowledge/delete", del); out.Code != 503 {
		t.Fatal(out.Code, out.Body)
	}
	// The deletion fence is committed even though linked-session cleanup failed.
	query.User = subject
	out = call(subject, "/v1/knowledge/query", query)
	if out.Code != 200 || strings.Contains(out.Body.String(), "significance marker") {
		t.Fatal("fence lost", out.Code, out.Body)
	}
	late := up
	late.OperationID = "66666666-6666-4666-8666-666666666666"
	if out = call(subject, "/v1/knowledge/upsert", late); out.Code != 409 {
		t.Fatal("pending delete did not fence", out.Code)
	}
	drop()
	server = NewServerWithClient(cfg, slog.Default(), pool, fake)
	for range 2 {
		if out = call(subject, "/v1/knowledge/delete", del); out.Code != 200 {
			t.Fatal(out.Code, out.Body)
		}
	}
	_ = pool.QueryRow(context.Background(), "SELECT count(*) FROM messages m JOIN users u ON u.id=m.user_id WHERE u.external_id=$1", subject).Scan(&count)
	if count != 0 {
		t.Fatal("linked history survived", count)
	}
	query.User = subject
	if out = call(subject, "/v1/knowledge/query", query); out.Code != 200 || strings.Contains(out.Body.String(), "significance marker") {
		t.Fatal(out.Code, out.Body)
	}
	p.Record.Revision = 99
	up.OperationID = "55555555-5555-4555-8555-555555555555"
	if out = call(subject, "/v1/knowledge/upsert", up); out.Code != 409 {
		t.Fatal("resurrection", out.Code, out.Body)
	}
	// Unknown IDs also receive terminal fences, including after process restart.
	absent := projectionRef{"entity", "77777777-7777-4777-8777-777777777777", 2}
	absentDelete := projectionRequest{Version: 1, Operation: "delete", User: subject, OperationID: "88888888-8888-4888-8888-888888888888", Record: &absent}
	if out = call(subject, "/v1/knowledge/delete", absentDelete); out.Code != 200 {
		t.Fatal(out.Code, out.Body)
	}
	// Capabilities are explicit, versioned and authenticated, not a health signal.
	if out = call(subject, "/v1/knowledge/capabilities", map[string]any{"user": subject}); out.Code != 200 || !strings.Contains(out.Body.String(), "linked-context-1") {
		t.Fatal(out.Code, out.Body)
	}
	var userID string
	if err = pool.QueryRow(context.Background(), "SELECT id::text FROM users WHERE external_id=$1", subject).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if out = call(subject, "/v1/subjects/purge", map[string]any{"user": subject}); out.Code != 200 {
		t.Fatal("full purge", out.Code, out.Body)
	}
	for _, table := range []string{"context_projections", "context_projection_operations", "context_projection_sessions"} {
		_ = pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE user_id=$1", userID).Scan(&count)
		if count != 0 {
			t.Fatal("projection survived subject purge", table, count)
		}
	}
	if out = call(subject, "/v1/knowledge/upsert", up); out.Code != 410 {
		t.Fatal("purged subject resurrected", out.Code)
	}
	_, _ = pool.Exec(context.Background(), "DELETE FROM subject_purges WHERE external_id=$1", subject)
}

func TestProjectionSchemaValidation(t *testing.T) {
	ref := projectionRef{"event", "11111111-1111-4111-8111-111111111111", 1}
	p := contextProjection{Record: ref, Label: "a", Summary: "b", KnowledgeKind: "user_statement", Sources: []projectionRef{ref}}
	if !p.valid() {
		t.Fatal("valid rejected")
	}
	p.HouseThemes = []int{1, 1}
	if p.valid() {
		t.Fatal("duplicate houses accepted")
	}
}

func TestLinkedProjectionCorrectionAndExplicitSalience(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool, cleanup := openIsolationTestDB(t)
	defer cleanup()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx, "SELECT gen_random_uuid()::text").Scan(&id); err != nil {
		t.Fatal(err)
	}
	subject := "oria:" + id
	cfg := isolationTestConfig(t, "unused")
	cfg.Auth.ServiceTokens = []config.AuthTokenConfig{{Subject: "oria:", Token: "test-service"}}
	server := NewServerWithClient(cfg, slog.Default(), pool, &fakeLLMClient{response: llm.GenerateResponse{OutputText: "Interpretive reflection."}})
	defer pool.Exec(ctx, "DELETE FROM users WHERE external_id=$1", subject) //nolint:errcheck
	call := func(path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", path, bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer test-service")
		req.Header.Set("X-SecondContext-Subject", subject)
		out := httptest.NewRecorder()
		server.Handler().ServeHTTP(out, req)
		return out
	}
	sequence := 0
	put := func(p contextProjection) projectionRequest {
		t.Helper()
		sequence++
		request := projectionRequest{Version: 1, Operation: "upsert", User: subject, OperationID: fmt.Sprintf("00000000-0000-4000-8000-%012d", sequence), Projection: &p}
		out := call("/v1/knowledge/upsert", request)
		if out.Code != 200 {
			t.Fatal(out.Code, out.Body)
		}
		return request
	}
	root := projectionRef{"event", "11111111-1111-4111-8111-111111111111", 1}
	person := projectionRef{"entity", "22222222-2222-4222-8222-222222222222", 1}
	base := contextProjection{Record: root, Label: "wedding", Summary: "Wedding statement", KnowledgeKind: "user_statement", Sources: []projectionRef{root}, HouseThemes: []int{7}, Salience: &projectionSalience{.5, "inferred", "linked-salience-1"}}
	put(base)
	entity := base
	entity.Record = person
	entity.Sources = []projectionRef{person}
	entity.Label = "Alice"
	put(entity)
	reading := base
	reading.Record = projectionRef{"reading", "33333333-3333-4333-8333-333333333333", 1}
	reading.Sources = []projectionRef{root, person}
	reading.KnowledgeKind = "interpretation"
	reading.Summary = "Linked interpretation marker"
	old := put(reading)
	out := call("/v1/responses", map[string]any{"user": subject, "input": "Reflect", "disable_memory": true, "disable_knowledge": true, "knowledge_refs": []projectionRef{reading.Record}, "metadata": map[string]any{"session_id": "correction-" + id}})
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body)
	}
	root.Revision = 2
	base.Record = root
	base.Sources = []projectionRef{root}
	put(base)
	query := projectionRequest{Version: 1, Operation: "query", User: subject, References: []projectionRef{person}, Limit: 20}
	out = call("/v1/knowledge/query", query)
	if out.Code != 200 || strings.Contains(out.Body.String(), "Linked interpretation marker") {
		t.Fatal("stale interpretation", out.Code, out.Body)
	}
	var count int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE external_id=$1", "correction-"+id).Scan(&count)
	if count != 0 {
		t.Fatal("corrected history survived")
	}
	// A cached old operation acknowledges its original completion without restoring
	// invalidated content; a new operation using old sources must fail.
	if out = call("/v1/knowledge/upsert", old); out.Code != 200 {
		t.Fatal(out.Code, out.Body)
	}
	old.OperationID = "99999999-9999-4999-8999-999999999999"
	if out = call("/v1/knowledge/upsert", old); out.Code != 409 {
		t.Fatal("stale sources accepted", out.Code, out.Body)
	}
	high := base
	high.Record = projectionRef{"reading", "44444444-4444-4444-8444-444444444444", 1}
	high.Summary = "Especially important"
	high.Salience = &projectionSalience{1, "user_stated", "linked-salience-1"}
	put(high)
	low := high
	low.Record = projectionRef{"reading", "55555555-5555-4555-8555-555555555555", 1}
	low.Summary = "Less important now"
	low.Salience = &projectionSalience{.1, "user_stated", "linked-salience-1"}
	put(low)
	query.References = []projectionRef{root}
	out = call("/v1/knowledge/query", query)
	var results struct {
		Matches []contextProjection `json:"matches"`
	}
	_ = json.Unmarshal(out.Body.Bytes(), &results)
	if out.Code != 200 || len(results.Matches) == 0 || results.Matches[0].Summary != "Less important now" {
		t.Fatal("latest significance ignored", out.Code, out.Body)
	}
}

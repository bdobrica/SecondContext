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
	"testing"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
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

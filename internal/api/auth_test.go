package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/llm"
)

func TestAuthenticationMiddlewareRequiresBearerToken(t *testing.T) {
	server := NewServerWithClient(config.Config{
		App:    config.AppConfig{Name: "salience-graph", Env: "test"},
		Auth:   config.AuthConfig{Enabled: true, Realm: "second-context", Tokens: []config.AuthTokenConfig{{Subject: "auth-user", Token: "secret-token"}}},
		OpenAI: config.OpenAIConfig{ChatModel: "gpt-4.1-mini"},
	}, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), nil, &fakeLLMClient{})

	unauthenticated := httptest.NewRecorder()
	unauthenticatedRequest := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	server.Handler().ServeHTTP(unauthenticated, unauthenticatedRequest)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusUnauthorized, unauthenticated.Code, unauthenticated.Body.String())
	}
	if unauthenticated.Header().Get(wwwAuthenticateHeaderName) == "" {
		t.Fatalf("expected %s header, got %#v", wwwAuthenticateHeaderName, unauthenticated.Header())
	}

	invalid := httptest.NewRecorder()
	invalidRequest := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	invalidRequest.Header.Set(authorizationHeaderName, "Bearer wrong-token")
	server.Handler().ServeHTTP(invalid, invalidRequest)
	if invalid.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusUnauthorized, invalid.Code, invalid.Body.String())
	}

	authenticated := httptest.NewRecorder()
	authenticatedRequest := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	authenticatedRequest.Header.Set(authorizationHeaderName, "Bearer secret-token")
	server.Handler().ServeHTTP(authenticated, authenticatedRequest)
	if authenticated.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, authenticated.Code, authenticated.Body.String())
	}
}

func TestAuthenticationMiddlewareSkipsHealthz(t *testing.T) {
	server := NewServerWithClient(config.Config{
		App:  config.AppConfig{Name: "salience-graph", Env: "test"},
		Auth: config.AuthConfig{Enabled: true, Realm: "second-context", Tokens: []config.AuthTokenConfig{{Subject: "auth-user", Token: "secret-token"}}},
	}, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), nil, &fakeLLMClient{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, recorder.Code, recorder.Body.String())
	}
}

func TestAuthenticatedSubjectBecomesDefaultRequestUser(t *testing.T) {
	fakeClient := &fakeLLMClient{response: llm.GenerateResponse{
		ID:         "chatcmpl_auth_test",
		Model:      "gpt-4.1-mini",
		OutputText: "Draft a narrow review request.",
	}}

	server := NewServerWithClient(config.Config{
		App:    config.AppConfig{Name: "salience-graph", Env: "test"},
		Auth:   config.AuthConfig{Enabled: true, Realm: "second-context", Tokens: []config.AuthTokenConfig{{Subject: "auth-user", Token: "secret-token"}}},
		Dev:    config.DevConfig{UserExternalID: "dev-user", UserName: "Dev User", UserEmail: "dev@example.com"},
		OpenAI: config.OpenAIConfig{ChatModel: "gpt-4.1-mini"},
	}, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), nil, fakeClient)

	body := []byte(`{"model":"context-agent-1","input":"Help me ask Alex to review the proposal."}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(authorizationHeaderName, "Bearer secret-token")

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d body=%s", recorder.Code, recorder.Body.String())
	}

	var payload createResponseResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	packet, ok := payload.Metadata["context_packet"].(map[string]any)
	if !ok {
		t.Fatalf("expected context packet metadata, got %#v", payload.Metadata)
	}
	if packet["user_external_id"] != "auth-user" {
		t.Fatalf("expected authenticated subject in context packet, got %#v", packet)
	}
	if len(fakeClient.request.Messages) != 2 {
		t.Fatalf("unexpected llm request %#v", fakeClient.request)
	}
}

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func TestAuthenticationMiddlewareRejectsTokenWithoutSubject(t *testing.T) {
	server := NewServerWithClient(config.Config{
		App:    config.AppConfig{Name: "salience-graph", Env: "test"},
		Auth:   config.AuthConfig{Enabled: true, Tokens: []config.AuthTokenConfig{{Token: "subjectless-token"}}},
		OpenAI: config.OpenAIConfig{ChatModel: "gpt-4.1-mini"},
	}, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), nil, &fakeLLMClient{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set(authorizationHeaderName, "Bearer subjectless-token")
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusUnauthorized, recorder.Code, recorder.Body.String())
	}
	if subject := authenticatedSubject(request.Context()); subject != "" {
		t.Fatalf("subjectless token produced authenticated subject %q", subject)
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

func TestAuthenticatedResponseRejectsConflictingRequestUser(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		param string
	}{
		{
			name:  "top-level user",
			body:  `{"model":"context-agent-1","input":"private tenant-b marker","user":"tenant-b"}`,
			param: "user",
		},
		{
			name:  "metadata user",
			body:  `{"model":"context-agent-1","input":"private tenant-b marker","metadata":{"user_external_id":"tenant-b"}}`,
			param: "metadata.user_external_id",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fakeClient := &fakeLLMClient{response: llm.GenerateResponse{OutputText: "must not be returned"}}
			server := NewServerWithClient(config.Config{
				App:    config.AppConfig{Name: "salience-graph", Env: "test"},
				Auth:   config.AuthConfig{Enabled: true, Tokens: []config.AuthTokenConfig{{Subject: "tenant-a", Token: "token-a"}}},
				Dev:    config.DevConfig{UserExternalID: "dev-user"},
				OpenAI: config.OpenAIConfig{ChatModel: "gpt-4.1-mini"},
			}, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), nil, fakeClient)

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(authorizationHeaderName, "Bearer token-a")
			server.Handler().ServeHTTP(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d body=%s", http.StatusBadRequest, recorder.Code, recorder.Body.String())
			}
			var payload apiErrorEnvelope
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload.Error.Code != "identity_conflict" || payload.Error.Param != test.param {
				t.Fatalf("unexpected error: %#v", payload.Error)
			}
			if len(fakeClient.requests) != 0 || fakeClient.embedCount != 0 {
				t.Fatalf("conflicting identity reached upstream LLM: requests=%d embeds=%d", len(fakeClient.requests), fakeClient.embedCount)
			}
			if bytes.Contains(recorder.Body.Bytes(), []byte("private tenant-b marker")) ||
				bytes.Contains(recorder.Body.Bytes(), []byte("must not be returned")) {
				t.Fatalf("response disclosed request or upstream content: %s", recorder.Body.String())
			}
		})
	}
}

func TestResolveRequestMetadataUsesOneAuthenticatedIdentity(t *testing.T) {
	server := NewServerWithClient(config.Config{
		Dev: config.DevConfig{UserExternalID: "dev-user", UserName: "Dev User", UserEmail: "dev@example.com"},
	}, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), nil, &fakeLLMClient{})
	ctx := context.WithValue(context.Background(), authContextKey{}, authPrincipal{Subject: "tenant-a"})

	metadata, err := server.resolveRequestMetadata(ctx, map[string]any{"user_external_id": "tenant-a"}, "tenant-a")
	if err != nil {
		t.Fatalf("resolve matching selectors: %v", err)
	}
	if metadata.UserExternalID != "tenant-a" || metadata.UserName != "tenant-a" {
		t.Fatalf("unexpected resolved metadata: %#v", metadata)
	}

	_, err = server.resolveRequestMetadata(ctx, map[string]any{"user_external_id": "tenant-b"}, "tenant-a")
	var scopeErr *requestScopeError
	if !errors.As(err, &scopeErr) {
		t.Fatalf("expected request scope error, got %v", err)
	}
	if scopeErr.Code != "identity_conflict" || scopeErr.Param != "metadata.user_external_id" {
		t.Fatalf("unexpected scope error: %#v", scopeErr)
	}
}

func TestAuthenticatedUserSelectorsUseOneConflictPolicy(t *testing.T) {
	tests := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{name: "memory ingest user", method: http.MethodPost, target: "/memory/ingest", body: `{"raw_text":"test","user":"tenant-b"}`},
		{name: "memory extract metadata user", method: http.MethodPost, target: "/memory/extract", body: `{"raw_text":"test","metadata":{"user_external_id":"tenant-b"}}`},
		{name: "memory search user", method: http.MethodPost, target: "/memory/search", body: `{"query":"test","user_external_id":"tenant-b"}`},
		{name: "memory list user", method: http.MethodGet, target: "/memory?user_external_id=tenant-b"},
		{name: "outcome user", method: http.MethodPost, target: "/interactions/outcome", body: `{"raw_text":"test","user":"tenant-b"}`},
		{name: "debug context user", method: http.MethodGet, target: "/debug/context?input=test&user_external_id=tenant-b"},
		{name: "debug beliefs user", method: http.MethodGet, target: "/debug/beliefs?user_external_id=tenant-b"},
	}

	server := NewServerWithClient(config.Config{
		App:  config.AppConfig{Name: "salience-graph", Env: "test"},
		Auth: config.AuthConfig{Enabled: true, Tokens: []config.AuthTokenConfig{{Subject: "tenant-a", Token: "token-a"}}},
		Dev:  config.DevConfig{UserExternalID: "dev-user"},
	}, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), nil, &fakeLLMClient{})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.target, bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(authorizationHeaderName, "Bearer token-a")
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d body=%s", http.StatusBadRequest, recorder.Code, recorder.Body.String())
			}
			var payload apiErrorEnvelope
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload.Error.Code != "identity_conflict" {
				t.Fatalf("unexpected error: %#v", payload.Error)
			}
		})
	}
}

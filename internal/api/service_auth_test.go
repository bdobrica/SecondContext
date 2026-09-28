package api

import (
	"bytes"
	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/llm"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServiceDelegationScope(t *testing.T) {
	subject := "oria:11111111-1111-4111-8111-111111111111"
	for _, tc := range []struct {
		name, token, header, path, body string
		status                          int
	}{
		{"valid", "service-secret", subject, "/v1/responses", `{"user":"` + subject + `","input":"hello","disable_memory":true}`, 200},
		{"missing subject", "service-secret", "", "/v1/responses", `{}`, 403},
		{"foreign namespace", "service-secret", "other:11111111-1111-4111-8111-111111111111", "/v1/responses", `{}`, 403},
		{"invalid uuid", "service-secret", "oria:invalid", "/v1/responses", `{}`, 403},
		{"foreign selector", "service-secret", subject, "/v1/responses", `{"user":"alice","input":"hello"}`, 400},
		{"metadata selector", "service-secret", subject, "/v1/responses", `{"metadata":{"user_external_id":"alice"},"input":"hello"}`, 400},
		{"user cannot delegate", "user-secret", subject, "/v1/responses", `{}`, 403},
		{"service cannot extract", "service-secret", subject, "/memory/extract", `{}`, 403},
		{"service cannot outcomes", "service-secret", subject, "/interactions/outcome", `{}`, 403},
		{"service cannot debug", "service-secret", subject, "/debug/context", `{}`, 403},
		{"invalid token", "unknown", subject, "/v1/responses", `{}`, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{Auth: config.AuthConfig{Enabled: true, Tokens: []config.AuthTokenConfig{{Subject: "alice", Token: "user-secret"}}, ServiceTokens: []config.AuthTokenConfig{{Subject: "oria:", Token: "service-secret"}}}}
			server := NewServerWithClient(cfg, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), nil, &fakeLLMClient{response: llm.GenerateResponse{OutputText: "hello"}})
			req := httptest.NewRequest("POST", tc.path, bytes.NewBufferString(tc.body))
			req.Header.Set("Authorization", "Bearer "+tc.token)
			if tc.header != "" {
				req.Header.Set("X-SecondContext-Subject", tc.header)
			}
			res := httptest.NewRecorder()
			server.Handler().ServeHTTP(res, req)
			if res.Code != tc.status {
				t.Fatalf("got %d want %d: %s", res.Code, tc.status, res.Body)
			}
		})
	}
	// No development fallback may delete a subject, even with AUTH_ENABLED=false.
	s := NewServerWithClient(config.Config{}, slog.Default(), nil, &fakeLLMClient{})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/v1/subjects/purge", bytes.NewBufferString(`{"user":"dev-user"}`)))
	if res.Code != 401 {
		t.Fatalf("unauthenticated purge status %d", res.Code)
	}
}

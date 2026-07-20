package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/llm"
)

func TestMetricsEndpointExposesHTTPAndLLMMetrics(t *testing.T) {
	fakeClient := &fakeLLMClient{response: llm.GenerateResponse{
		ID:         "chatcmpl_metrics_test",
		Model:      "gpt-4.1-mini",
		OutputText: "Draft a narrow review request.",
		Usage:      llm.Usage{InputTokens: 10, OutputTokens: 8, TotalTokens: 18},
	}}

	server := NewServerWithClient(config.Config{
		App:    config.AppConfig{Name: "salience-graph", Env: "test"},
		Dev:    config.DevConfig{UserExternalID: "dev-user", UserName: "Dev User", UserEmail: "dev@example.com"},
		HTTP:   config.HTTPConfig{MetricsEnabled: true, MetricsPath: "/metrics"},
		OpenAI: config.OpenAIConfig{ChatModel: "gpt-4.1-mini"},
	}, slog.New(slog.NewJSONHandler(bytes.NewBuffer(nil), nil)), nil, fakeClient)

	modelsRecorder := httptest.NewRecorder()
	modelsRequest := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	server.Handler().ServeHTTP(modelsRecorder, modelsRequest)
	if modelsRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected models status %d body=%s", modelsRecorder.Code, modelsRecorder.Body.String())
	}

	responseRecorder := httptest.NewRecorder()
	responseRequest := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader([]byte(`{"model":"context-agent-1","input":"Help me ask Alex to review the proposal."}`)))
	responseRequest.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(responseRecorder, responseRequest)
	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected response status %d body=%s", responseRecorder.Code, responseRecorder.Body.String())
	}

	metricsRecorder := httptest.NewRecorder()
	metricsRequest := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	server.Handler().ServeHTTP(metricsRecorder, metricsRequest)
	if metricsRecorder.Code != http.StatusOK {
		t.Fatalf("unexpected metrics status %d body=%s", metricsRecorder.Code, metricsRecorder.Body.String())
	}

	body := metricsRecorder.Body.String()
	for _, expected := range []string{
		`second_context_http_requests_total{method="GET",route="/v1/models",status="200"} 1`,
		`second_context_http_requests_total{method="POST",route="/v1/responses",status="200"} 1`,
		`second_context_llm_requests_total{model="gpt-4.1-mini",operation="generate",outcome="success"} 1`,
		`second_context_llm_input_tokens_total{model="gpt-4.1-mini",operation="generate",outcome="success"} 10`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected metrics output to contain %q, got %s", expected, body)
		}
	}
}

func TestRequestLoggingIncludesRouteAndAuthSubject(t *testing.T) {
	var buffer bytes.Buffer
	server := NewServerWithClient(config.Config{
		App:    config.AppConfig{Name: "salience-graph", Env: "test"},
		Auth:   config.AuthConfig{Enabled: true, Realm: "second-context", Tokens: []config.AuthTokenConfig{{Subject: "auth-user", Token: "secret-token"}}},
		HTTP:   config.HTTPConfig{MetricsEnabled: true, MetricsPath: "/metrics"},
		OpenAI: config.OpenAIConfig{ChatModel: "gpt-4.1-mini"},
	}, slog.New(slog.NewJSONHandler(&buffer, nil)), nil, &fakeLLMClient{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.RemoteAddr = "203.0.113.10:1234"
	request.Header.Set("User-Agent", "observability-test")
	request.Header.Set(authorizationHeaderName, "Bearer secret-token")

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d body=%s", recorder.Code, recorder.Body.String())
	}

	lines := strings.Split(strings.TrimSpace(buffer.String()), "\n")
	if len(lines) == 0 {
		t.Fatal("expected request log output")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &payload); err != nil {
		t.Fatalf("decode log line: %v", err)
	}
	if payload["msg"] != "http request" {
		t.Fatalf("unexpected log payload %#v", payload)
	}
	if payload["route"] != "/v1/models" {
		t.Fatalf("expected route label, got %#v", payload)
	}
	if payload["auth_subject"] != "auth-user" {
		t.Fatalf("expected auth_subject, got %#v", payload)
	}
	if payload["remote_ip"] != "203.0.113.10" {
		t.Fatalf("expected remote_ip, got %#v", payload)
	}
}

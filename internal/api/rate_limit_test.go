package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bdobrica/SecondContext/internal/config"
)

func TestRateLimitMiddlewareBlocksRepeatedRequests(t *testing.T) {
	server := NewServerWithClient(config.Config{
		App:    config.AppConfig{Name: "salience-graph", Env: "test"},
		HTTP:   config.HTTPConfig{RateLimitRPM: 2},
		OpenAI: config.OpenAIConfig{ChatModel: "gpt-4.1-mini"},
	}, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), nil, &fakeLLMClient{})

	for attempt := range 3 {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		request.RemoteAddr = "203.0.113.10:1234"

		server.Handler().ServeHTTP(recorder, request)

		if attempt < 2 && recorder.Code != http.StatusOK {
			t.Fatalf("attempt %d expected status %d, got %d body=%s", attempt+1, http.StatusOK, recorder.Code, recorder.Body.String())
		}
		if attempt == 2 {
			if recorder.Code != http.StatusTooManyRequests {
				t.Fatalf("expected status %d, got %d body=%s", http.StatusTooManyRequests, recorder.Code, recorder.Body.String())
			}

			var payload apiErrorEnvelope
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if payload.Error.Type != rateLimitErrorType {
				t.Fatalf("unexpected error payload %#v", payload)
			}
			if payload.Error.Code != rateLimitExceededErrorCode {
				t.Fatalf("unexpected error payload %#v", payload)
			}
			if recorder.Header().Get("Retry-After") == "" {
				t.Fatalf("expected Retry-After header, got %#v", recorder.Header())
			}
		}
	}
}

func TestRateLimitMiddlewareSeparatesClientsAndSkipsHealthz(t *testing.T) {
	server := NewServerWithClient(config.Config{
		App:    config.AppConfig{Name: "salience-graph", Env: "test"},
		HTTP:   config.HTTPConfig{RateLimitRPM: 1},
		OpenAI: config.OpenAIConfig{ChatModel: "gpt-4.1-mini"},
	}, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), nil, &fakeLLMClient{})

	firstClient := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	firstClient.RemoteAddr = "203.0.113.10:1234"
	firstRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(firstRecorder, firstClient)
	if firstRecorder.Code != http.StatusOK {
		t.Fatalf("expected first client status %d, got %d body=%s", http.StatusOK, firstRecorder.Code, firstRecorder.Body.String())
	}

	secondClient := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	secondClient.RemoteAddr = "203.0.113.11:1234"
	secondRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(secondRecorder, secondClient)
	if secondRecorder.Code != http.StatusOK {
		t.Fatalf("expected second client status %d, got %d body=%s", http.StatusOK, secondRecorder.Code, secondRecorder.Body.String())
	}

	blockedRecorder := httptest.NewRecorder()
	blockedRequest := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	blockedRequest.RemoteAddr = "203.0.113.10:1234"
	server.Handler().ServeHTTP(blockedRecorder, blockedRequest)
	if blockedRecorder.Code != http.StatusTooManyRequests {
		t.Fatalf("expected blocked status %d, got %d body=%s", http.StatusTooManyRequests, blockedRecorder.Code, blockedRecorder.Body.String())
	}

	healthRecorder := httptest.NewRecorder()
	healthRequest := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	healthRequest.RemoteAddr = "203.0.113.10:1234"
	server.Handler().ServeHTTP(healthRecorder, healthRequest)
	if healthRecorder.Code != http.StatusOK {
		t.Fatalf("expected healthz status %d, got %d body=%s", http.StatusOK, healthRecorder.Code, healthRecorder.Body.String())
	}
}

func TestRateLimitDirectClientCannotBypassWithForwardedFor(t *testing.T) {
	server := NewServerWithClient(config.Config{
		App:    config.AppConfig{Name: "salience-graph", Env: "test"},
		HTTP:   config.HTTPConfig{RateLimitRPM: 1},
		OpenAI: config.OpenAIConfig{ChatModel: "gpt-4.1-mini"},
	}, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), nil, &fakeLLMClient{})

	for attempt, forwarded := range []string{"198.51.100.1", "198.51.100.2"} {
		request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		request.RemoteAddr = "203.0.113.10:4000"
		request.Header.Set(forwardedForHeader, forwarded)
		recorder := httptest.NewRecorder()

		server.Handler().ServeHTTP(recorder, request)

		want := http.StatusOK
		if attempt == 1 {
			want = http.StatusTooManyRequests
		}
		if recorder.Code != want {
			t.Fatalf("attempt %d with X-Forwarded-For %q got status %d, want %d", attempt+1, forwarded, recorder.Code, want)
		}
	}
}

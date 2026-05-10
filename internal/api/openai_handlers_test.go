package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/llm"
)

type fakeLLMClient struct {
	response llm.GenerateResponse
	err      error
	request  llm.GenerateRequest
}

func (f *fakeLLMClient) Generate(_ context.Context, request llm.GenerateRequest) (llm.GenerateResponse, error) {
	f.request = request
	if f.err != nil {
		return llm.GenerateResponse{}, f.err
	}

	return f.response, nil
}

func TestHandleListModels(t *testing.T) {
	server := NewServerWithClient(config.Config{
		App:    config.AppConfig{Name: "salience-graph", Env: "test"},
		OpenAI: config.OpenAIConfig{ChatModel: "gpt-4.1-mini"},
	}, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), nil, &fakeLLMClient{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", recorder.Code)
	}

	var payload listModelsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Data) < 1 || payload.Data[0].ID != defaultPublicModel {
		t.Fatalf("unexpected models %#v", payload.Data)
	}
}

func TestHandleCreateResponse(t *testing.T) {
	fakeClient := &fakeLLMClient{response: llm.GenerateResponse{
		ID:         "chatcmpl_test",
		Model:      "gpt-4.1-mini",
		OutputText: "Draft a narrow review request.",
		Usage:      llm.Usage{InputTokens: 10, OutputTokens: 8, TotalTokens: 18},
	}}

	server := NewServerWithClient(config.Config{
		App:    config.AppConfig{Name: "salience-graph", Env: "test"},
		Dev:    config.DevConfig{UserExternalID: "dev-user", UserName: "Dev User", UserEmail: "dev@example.com"},
		OpenAI: config.OpenAIConfig{ChatModel: "gpt-4.1-mini"},
	}, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), nil, fakeClient)

	body := []byte(`{"model":"context-agent-1","input":"Help me ask Alex to review the proposal."}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d body=%s", recorder.Code, recorder.Body.String())
	}

	if len(fakeClient.request.Messages) != 1 || fakeClient.request.Messages[0].Content != "Help me ask Alex to review the proposal." {
		t.Fatalf("unexpected llm request %#v", fakeClient.request)
	}

	var payload createResponseResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.OutputText != "Draft a narrow review request." {
		t.Fatalf("unexpected output text %q", payload.OutputText)
	}
}

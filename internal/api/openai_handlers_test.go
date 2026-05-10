package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/llm"
)

type fakeLLMClient struct {
	response      llm.GenerateResponse
	responses     []llm.GenerateResponse
	embedResponse llm.EmbedResponse
	err           error
	request       llm.GenerateRequest
	requests      []llm.GenerateRequest
}

func (f *fakeLLMClient) Generate(_ context.Context, request llm.GenerateRequest) (llm.GenerateResponse, error) {
	f.request = request
	f.requests = append(f.requests, request)
	if f.err != nil {
		return llm.GenerateResponse{}, f.err
	}
	if len(f.responses) > 0 {
		response := f.responses[0]
		f.responses = f.responses[1:]
		return response, nil
	}

	return f.response, nil
}

func (f *fakeLLMClient) Embed(_ context.Context, _ llm.EmbedRequest) (llm.EmbedResponse, error) {
	if f.err != nil {
		return llm.EmbedResponse{}, f.err
	}
	if len(f.embedResponse.Vector) == 0 {
		return llm.EmbedResponse{Vector: []float64{0.1, 0.2, 0.3}}, nil
	}

	return f.embedResponse, nil
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

	if len(fakeClient.request.Messages) != 2 {
		t.Fatalf("unexpected llm request %#v", fakeClient.request)
	}
	if fakeClient.request.Messages[0].Role != "system" || !strings.Contains(fakeClient.request.Messages[0].Content, "Memory context:") {
		t.Fatalf("unexpected llm request %#v", fakeClient.request)
	}
	if fakeClient.request.Messages[1].Content != "Help me ask Alex to review the proposal." {
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

func TestHandleCreateResponseScenarioGeneration(t *testing.T) {
	fakeClient := &fakeLLMClient{response: llm.GenerateResponse{
		ID:         "chatcmpl_scenario_test",
		Model:      "gpt-4.1-mini",
		OutputText: `{"recommended_strategy_id":"direct_scoped","recommendation_rationale":"It best matches the goal with clear scope and low social friction.","strategies":[{"id":"direct_scoped","label":"Scoped direct request","message_draft":"Alex, could you review just the API section by Thursday and call out the biggest risks?","predicted_response":"Yes, I can review the API section by Thursday.","benefits":["clear ask","respects limited capacity"],"risks":["may feel narrow if Alex wants full context"],"likelihood_of_success":0.79,"fallback_option":"Shorten the ask further and offer a follow-up sync."},{"id":"context_heavy","label":"High-context request","message_draft":"Alex, I know the full proposal is broad, but your API perspective would help me de-risk the plan. Could you review it this week?","predicted_response":"I can help, but please narrow the section first.","benefits":["shows context"],"risks":["still feels broad"],"likelihood_of_success":0.55,"fallback_option":"Follow up with a narrowed API-only request."},{"id":"deferential","label":"Deferential ask","message_draft":"Alex, if you have time this week, would you be open to a short API-only review of the proposal?","predicted_response":"Maybe later this week if you send the specific section.","benefits":["polite tone"],"risks":["can invite delay"],"likelihood_of_success":0.64,"fallback_option":"Add a concrete deadline and exact section."}]}`,
		Usage:      llm.Usage{InputTokens: 42, OutputTokens: 61, TotalTokens: 103},
	}}

	server := NewServerWithClient(config.Config{
		App:    config.AppConfig{Name: "salience-graph", Env: "test"},
		Dev:    config.DevConfig{UserExternalID: "dev-user", UserName: "Dev User", UserEmail: "dev@example.com"},
		OpenAI: config.OpenAIConfig{ChatModel: "gpt-4.1-mini"},
	}, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)), nil, fakeClient)

	body := []byte(`{"model":"context-agent-1","input":"Help me ask Alex to review the proposal.","metadata":{"goal":"get_review","people":["Alex"],"memory_mode":"scenario_generation"}}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(fakeClient.request.Messages) != 2 {
		t.Fatalf("unexpected llm request %#v", fakeClient.request)
	}
	if fakeClient.request.Messages[0].Role != "system" || !strings.Contains(strings.ToLower(fakeClient.request.Messages[0].Content), "structured interaction scenarios") {
		t.Fatalf("unexpected llm request %#v", fakeClient.request)
	}

	var payload createResponseResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.Contains(payload.OutputText, "Recommended strategy:") {
		t.Fatalf("unexpected output text %q", payload.OutputText)
	}
	if _, ok := payload.Metadata["scenario_plan"]; !ok {
		t.Fatalf("expected scenario plan metadata, got %#v", payload.Metadata)
	}
}

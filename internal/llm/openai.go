package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/bdobrica/SecondContext/internal/config"
)

type OpenAIClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

type openAIChatCompletionsRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type openAIChatCompletionsResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type openAIErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

type openAIEmbeddingsRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type openAIEmbeddingsResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

func NewOpenAIClient(cfg config.OpenAIConfig) *OpenAIClient {
	return &OpenAIClient{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  strings.TrimSpace(cfg.APIKey),
		httpClient: &http.Client{
			Timeout: cfg.RequestTimeout,
		},
	}
}

func (c *OpenAIClient) Generate(ctx context.Context, request GenerateRequest) (GenerateResponse, error) {
	if c.apiKey == "" {
		return GenerateResponse{}, &Error{
			StatusCode: http.StatusBadGateway,
			Message:    "OPENAI_API_KEY is not configured",
			Type:       "upstream_auth_error",
			Code:       "missing_api_key",
		}
	}

	body, err := json.Marshal(openAIChatCompletionsRequest{
		Model:    request.Model,
		Messages: request.Messages,
		Stream:   false,
	})
	if err != nil {
		return GenerateResponse{}, err
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return GenerateResponse{}, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")

	httpResponse, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return GenerateResponse{}, err
	}
	defer httpResponse.Body.Close()

	payload, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return GenerateResponse{}, err
	}

	if httpResponse.StatusCode >= http.StatusBadRequest {
		var upstreamError openAIErrorResponse
		if err := json.Unmarshal(payload, &upstreamError); err == nil && upstreamError.Error.Message != "" {
			return GenerateResponse{}, &Error{
				StatusCode: http.StatusBadGateway,
				Message:    upstreamError.Error.Message,
				Type:       upstreamError.Error.Type,
				Code:       upstreamError.Error.Code,
			}
		}

		return GenerateResponse{}, &Error{
			StatusCode: http.StatusBadGateway,
			Message:    fmt.Sprintf("upstream returned status %d", httpResponse.StatusCode),
			Type:       "upstream_error",
			Code:       "http_error",
		}
	}

	var response openAIChatCompletionsResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return GenerateResponse{}, err
	}
	if len(response.Choices) == 0 {
		return GenerateResponse{}, &Error{
			StatusCode: http.StatusBadGateway,
			Message:    "upstream returned no choices",
			Type:       "upstream_error",
			Code:       "empty_choices",
		}
	}

	return GenerateResponse{
		ID:         response.ID,
		Model:      response.Model,
		OutputText: strings.TrimSpace(response.Choices[0].Message.Content),
		Usage: Usage{
			InputTokens:  response.Usage.PromptTokens,
			OutputTokens: response.Usage.CompletionTokens,
			TotalTokens:  response.Usage.TotalTokens,
		},
	}, nil
}

func (c *OpenAIClient) Embed(ctx context.Context, request EmbedRequest) (EmbedResponse, error) {
	if c.apiKey == "" {
		return EmbedResponse{}, &Error{
			StatusCode: http.StatusBadGateway,
			Message:    "OPENAI_API_KEY is not configured",
			Type:       "upstream_auth_error",
			Code:       "missing_api_key",
		}
	}

	body, err := json.Marshal(openAIEmbeddingsRequest{
		Model: request.Model,
		Input: request.Input,
	})
	if err != nil {
		return EmbedResponse{}, err
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return EmbedResponse{}, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")

	httpResponse, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return EmbedResponse{}, err
	}
	defer httpResponse.Body.Close()

	payload, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return EmbedResponse{}, err
	}

	if httpResponse.StatusCode >= http.StatusBadRequest {
		var upstreamError openAIErrorResponse
		if err := json.Unmarshal(payload, &upstreamError); err == nil && upstreamError.Error.Message != "" {
			return EmbedResponse{}, &Error{
				StatusCode: http.StatusBadGateway,
				Message:    upstreamError.Error.Message,
				Type:       upstreamError.Error.Type,
				Code:       upstreamError.Error.Code,
			}
		}

		return EmbedResponse{}, &Error{
			StatusCode: http.StatusBadGateway,
			Message:    fmt.Sprintf("upstream returned status %d", httpResponse.StatusCode),
			Type:       "upstream_error",
			Code:       "http_error",
		}
	}

	var response openAIEmbeddingsResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return EmbedResponse{}, err
	}
	if len(response.Data) == 0 || len(response.Data[0].Embedding) == 0 {
		return EmbedResponse{}, &Error{
			StatusCode: http.StatusBadGateway,
			Message:    "upstream returned no embeddings",
			Type:       "upstream_error",
			Code:       "empty_embedding",
		}
	}

	return EmbedResponse{
		Vector: response.Data[0].Embedding,
		Usage: Usage{
			InputTokens: response.Usage.PromptTokens,
			TotalTokens: response.Usage.TotalTokens,
		},
	}, nil
}

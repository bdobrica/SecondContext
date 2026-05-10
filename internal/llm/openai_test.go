package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
)

func TestOpenAIClientGenerate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}

		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		if request["model"] != "gpt-4.1-mini" {
			t.Fatalf("unexpected model %#v", request["model"])
		}

		writeTestJSON(w, http.StatusOK, map[string]any{
			"id":    "chatcmpl_test",
			"model": "gpt-4.1-mini",
			"choices": []map[string]any{{
				"message": map[string]any{"content": "Hello from upstream."},
			}},
			"usage": map[string]any{
				"prompt_tokens":     11,
				"completion_tokens": 5,
				"total_tokens":      16,
			},
		})
	}))
	defer server.Close()

	client := NewOpenAIClient(config.OpenAIConfig{
		BaseURL:        server.URL,
		APIKey:         "test-key",
		RequestTimeout: time.Second,
	})

	response, err := client.Generate(context.Background(), GenerateRequest{
		Model: "gpt-4.1-mini",
		Messages: []Message{{
			Role:    "user",
			Content: "Say hello",
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if response.OutputText != "Hello from upstream." {
		t.Fatalf("unexpected output text %q", response.OutputText)
	}
	if response.Usage.TotalTokens != 16 {
		t.Fatalf("unexpected usage %#v", response.Usage)
	}
}

func TestOpenAIClientEmbed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}

		writeTestJSON(w, http.StatusOK, map[string]any{
			"data": []map[string]any{{"embedding": []float64{0.1, 0.2, 0.3}}},
			"usage": map[string]any{
				"prompt_tokens": 7,
				"total_tokens":  7,
			},
		})
	}))
	defer server.Close()

	client := NewOpenAIClient(config.OpenAIConfig{
		BaseURL:        server.URL,
		APIKey:         "test-key",
		RequestTimeout: time.Second,
	})

	response, err := client.Embed(context.Background(), EmbedRequest{
		Model: "text-embedding-3-small",
		Input: "Alex prefers narrow review scopes.",
	})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}

	if len(response.Vector) != 3 {
		t.Fatalf("unexpected embedding %#v", response.Vector)
	}
}

func writeTestJSON(w http.ResponseWriter, statusCode int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		panic(err)
	}
}

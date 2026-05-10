package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
)

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

type Point struct {
	ID      string
	Vector  []float64
	Payload map[string]any
}

type SearchResult struct {
	ID      string
	Score   float64
	Payload map[string]any
}

type qdrantErrorResponse struct {
	Status struct {
		Error string `json:"error"`
	} `json:"status"`
	Error string `json:"error"`
}

func NewClient(cfg config.QdrantConfig) *Client {
	return &Client{
		baseURL: strings.TrimRight(cfg.URL, "/"),
		apiKey:  strings.TrimSpace(cfg.APIKey),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Client) EnsureCollection(ctx context.Context, collection string, vectorSize int) error {
	body, err := json.Marshal(map[string]any{
		"vectors": map[string]any{
			"size":     vectorSize,
			"distance": "Cosine",
		},
	})
	if err != nil {
		return err
	}

	err = c.do(ctx, http.MethodPut, "/collections/"+collection, body, nil)
	if err != nil && strings.Contains(err.Error(), "already exists") {
		return nil
	}

	return err
}

func (c *Client) UpsertPoint(ctx context.Context, collection string, point Point) error {
	body, err := json.Marshal(map[string]any{
		"points": []map[string]any{{
			"id":      point.ID,
			"vector":  point.Vector,
			"payload": point.Payload,
		}},
	})
	if err != nil {
		return err
	}

	return c.do(ctx, http.MethodPut, "/collections/"+collection+"/points", body, nil)
}

func (c *Client) DeletePoint(ctx context.Context, collection, pointID string) error {
	body, err := json.Marshal(map[string]any{"points": []string{pointID}})
	if err != nil {
		return err
	}

	return c.do(ctx, http.MethodPost, "/collections/"+collection+"/points/delete", body, nil)
}

func (c *Client) Search(ctx context.Context, collection string, vector []float64, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 5
	}
	body, err := json.Marshal(map[string]any{
		"vector":       vector,
		"limit":        limit,
		"with_payload": true,
	})
	if err != nil {
		return nil, err
	}

	var response struct {
		Result []struct {
			ID      string         `json:"id"`
			Score   float64        `json:"score"`
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	if err := c.do(ctx, http.MethodPost, "/collections/"+collection+"/points/search", body, &response); err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(response.Result))
	for _, item := range response.Result {
		results = append(results, SearchResult{ID: item.ID, Score: item.Score, Payload: item.Payload})
	}

	return results, nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		request.Header.Set("api-key", c.apiKey)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}

	if response.StatusCode >= http.StatusBadRequest {
		var qdrantError qdrantErrorResponse
		if err := json.Unmarshal(payload, &qdrantError); err == nil {
			message := qdrantError.Error
			if message == "" {
				message = qdrantError.Status.Error
			}
			if message != "" {
				return fmt.Errorf("qdrant %s %s: %s", method, path, message)
			}
		}

		return fmt.Errorf("qdrant %s %s returned status %d", method, path, response.StatusCode)
	}

	if out != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			return err
		}
	}

	return nil
}

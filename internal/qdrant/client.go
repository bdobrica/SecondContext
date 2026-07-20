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

const (
	MaxSearchLimit       = 200
	MaxSearchPrefetch    = 600
	MaxResponseBodyBytes = 16 << 20
)

type ResponseTooLargeError struct {
	Limit int64
}

func (e *ResponseTooLargeError) Error() string {
	return fmt.Sprintf("qdrant response exceeds %d-byte limit", e.Limit)
}

type Client struct {
	baseURL      string
	apiKey       string
	httpClient   *http.Client
	denseVector  string
	sparseVector string
}

type Point struct {
	ID           string
	DenseVector  []float64
	SparseVector SparseVector
	Payload      map[string]any
}

type SearchResult struct {
	ID      string
	Score   float64
	Payload map[string]any
}

type searchResultWire struct {
	ID      any            `json:"id"`
	Score   float64        `json:"score"`
	Payload map[string]any `json:"payload"`
}

type SparseVector struct {
	Indices []uint32  `json:"indices"`
	Values  []float64 `json:"values"`
}

type Filter struct {
	Must []map[string]any `json:"must,omitempty"`
}

type qdrantErrorResponse struct {
	Status struct {
		Error string `json:"error"`
	} `json:"status"`
	Error string `json:"error"`
}

func NewClient(cfg config.QdrantConfig) *Client {
	return &Client{
		baseURL:      strings.TrimRight(cfg.URL, "/"),
		apiKey:       strings.TrimSpace(cfg.APIKey),
		denseVector:  firstNonEmpty(cfg.DenseVector, "dense"),
		sparseVector: firstNonEmpty(cfg.SparseVector, "sparse"),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Client) EnsureCollection(ctx context.Context, collection string, vectorSize int) error {
	body, err := json.Marshal(map[string]any{
		"vectors": map[string]any{
			c.denseVector: map[string]any{
				"size":     vectorSize,
				"distance": "Cosine",
			},
		},
		"sparse_vectors": map[string]any{
			c.sparseVector: map[string]any{},
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
	vector := map[string]any{
		c.denseVector: point.DenseVector,
	}
	if len(point.SparseVector.Indices) > 0 {
		vector[c.sparseVector] = point.SparseVector
	}

	body, err := json.Marshal(map[string]any{
		"points": []map[string]any{{
			"id":      point.ID,
			"vector":  vector,
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
	return c.SearchDense(ctx, collection, vector, limit, nil)
}

func (c *Client) SearchDense(ctx context.Context, collection string, vector []float64, limit int, filter *Filter) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 5
	}
	if limit > MaxSearchLimit {
		return nil, fmt.Errorf("qdrant search limit must not exceed %d", MaxSearchLimit)
	}
	bodyMap := map[string]any{
		"query":        vector,
		"using":        c.denseVector,
		"limit":        limit,
		"with_payload": true,
	}
	if filter != nil && len(filter.Must) > 0 {
		bodyMap["filter"] = filter
	}
	body, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, err
	}

	var response struct {
		Result json.RawMessage `json:"result"`
	}
	if err := c.do(ctx, http.MethodPost, "/collections/"+collection+"/points/query", body, &response); err != nil {
		return nil, err
	}

	return parseSearchResults(response.Result)
}

func (c *Client) SearchSparse(ctx context.Context, collection string, vector SparseVector, limit int, filter *Filter) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 5
	}
	if limit > MaxSearchLimit {
		return nil, fmt.Errorf("qdrant search limit must not exceed %d", MaxSearchLimit)
	}
	bodyMap := map[string]any{
		"query":        vector,
		"using":        c.sparseVector,
		"limit":        limit,
		"with_payload": true,
	}
	if filter != nil && len(filter.Must) > 0 {
		bodyMap["filter"] = filter
	}
	body, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, err
	}

	var response struct {
		Result json.RawMessage `json:"result"`
	}
	if err := c.do(ctx, http.MethodPost, "/collections/"+collection+"/points/query", body, &response); err != nil {
		return nil, err
	}

	return parseSearchResults(response.Result)
}

func (c *Client) SearchHybrid(ctx context.Context, collection string, dense []float64, sparse SparseVector, limit, prefetchLimit int, filter *Filter) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 5
	}
	if limit > MaxSearchLimit {
		return nil, fmt.Errorf("qdrant search limit must not exceed %d", MaxSearchLimit)
	}
	if prefetchLimit <= 0 {
		prefetchLimit = limit * 3
	}
	if prefetchLimit > MaxSearchPrefetch {
		return nil, fmt.Errorf("qdrant search limits exceed maximums (%d result, %d prefetch)", MaxSearchLimit, MaxSearchPrefetch)
	}

	densePrefetch := map[string]any{
		"query": dense,
		"using": c.denseVector,
		"limit": prefetchLimit,
	}
	sparsePrefetch := map[string]any{
		"query": sparse,
		"using": c.sparseVector,
		"limit": prefetchLimit,
	}
	if filter != nil && len(filter.Must) > 0 {
		densePrefetch["filter"] = filter
		sparsePrefetch["filter"] = filter
	}

	body, err := json.Marshal(map[string]any{
		"prefetch": []map[string]any{densePrefetch, sparsePrefetch},
		"query": map[string]any{
			"fusion": "rrf",
		},
		"limit":        limit,
		"with_payload": true,
	})
	if err != nil {
		return nil, err
	}

	var response struct {
		Result json.RawMessage `json:"result"`
	}
	if err := c.do(ctx, http.MethodPost, "/collections/"+collection+"/points/query", body, &response); err != nil {
		return nil, err
	}

	return parseSearchResults(response.Result)
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

	payload, err := readLimitedResponse(response.Body, MaxResponseBodyBytes)
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
				return fmt.Errorf("qdrant %s %s returned an error", method, path)
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

func readLimitedResponse(reader io.Reader, limit int64) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > limit {
		return nil, &ResponseTooLargeError{Limit: limit}
	}
	return payload, nil
}

func stringifyID(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return fmt.Sprintf("%.0f", typed)
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

func parseSearchResults(raw json.RawMessage) ([]SearchResult, error) {
	var direct []searchResultWire
	if err := json.Unmarshal(raw, &direct); err == nil {
		return mapSearchResults(direct), nil
	}

	var nested struct {
		Points []searchResultWire `json:"points"`
	}
	if err := json.Unmarshal(raw, &nested); err != nil {
		return nil, err
	}

	return mapSearchResults(nested.Points), nil
}

func mapSearchResults(items []searchResultWire) []SearchResult {
	results := make([]SearchResult, 0, len(items))
	for _, item := range items {
		results = append(results, SearchResult{ID: stringifyID(item.ID), Score: item.Score, Payload: item.Payload})
	}

	return results
}

func IsCollectionNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "collection") {
		return false
	}

	return strings.Contains(message, "doesn't exist") || strings.Contains(message, "does not exist") || strings.Contains(message, "not found")
}

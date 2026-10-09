// Package knowledge consumes reference evidence through HTTP only. It has no storage dependencies.
package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bdobrica/SecondContext/internal/config"
)

// KnowledgeProvider is the consumer contract. Subject selects a server-configured
// credential, never an upstream owner ID or a caller-supplied token.
type KnowledgeProvider interface {
	Search(ctx context.Context, subject, query string, filters Filters, limit int) ([]KnowledgeEvidence, error)
}

type Filters struct {
	SourceIDs   []string `json:"source_ids,omitempty"`
	DocumentIDs []string `json:"document_ids,omitempty"`
	Formats     []string `json:"formats,omitempty"`
}

func (f Filters) Valid() bool {
	if len(f.SourceIDs) > 100 || len(f.DocumentIDs) > 100 || len(f.Formats) > 7 {
		return false
	}
	for _, ids := range [][]string{f.SourceIDs, f.DocumentIDs} {
		for _, id := range ids {
			if len(id) != 36 {
				return false
			}
			for i, c := range id {
				if i == 8 || i == 13 || i == 18 || i == 23 {
					if c != '-' {
						return false
					}
					continue
				}
				if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
					return false
				}
			}
		}
	}
	for _, format := range f.Formats {
		switch format {
		case "text", "markdown", "json", "yaml", "pdf", "docx", "html":
		default:
			return false
		}
	}
	return true
}

type KnowledgeEvidence struct {
	ChunkID    string   `json:"chunk_id"`
	DocumentID string   `json:"document_id"`
	SourceID   string   `json:"source_id"`
	Text       string   `json:"text"`
	Score      float64  `json:"score"`
	Title      string   `json:"title"`
	Section    []string `json:"heading_path"`
	URI        string   `json:"uri"`
	SourceURI  *string  `json:"source_uri,omitempty"`
	Format     *string  `json:"format,omitempty"`
	PageStart  *int     `json:"page_start,omitempty"`
	PageEnd    *int     `json:"page_end,omitempty"`
	Truncated  bool     `json:"truncated,omitempty"`
}

type Error struct{ Code string }

func (e *Error) Error() string { return "knowledge search: " + e.Code }
func ErrorCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "unavailable"
}

type HTTPProvider struct {
	endpoint string
	tokens   map[string]string
	client   *http.Client
}

func NewHTTPProvider(c config.KnowledgeConfig) *HTTPProvider {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableCompression = true
	return &HTTPProvider{
		endpoint: strings.TrimRight(c.URL, "/") + "/v1/search",
		tokens:   tokenMap(c.Tokens),
		client:   &http.Client{Timeout: timeout, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}
func tokenMap(entries []config.AuthTokenConfig) map[string]string {
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		m[e.Subject] = e.Token
	}
	return m
}

const MaxResponseBytes = 2 << 20

func (p *HTTPProvider) Search(ctx context.Context, subject, query string, filters Filters, limit int) ([]KnowledgeEvidence, error) {
	token := p.tokens[subject]
	if token == "" {
		return nil, &Error{Code: "not_configured"}
	}
	if limit < 1 || limit > 20 || !filters.Valid() || strings.TrimSpace(query) == "" || strings.ContainsRune(query, 0) {
		return nil, &Error{Code: "invalid_request"}
	}
	// Bound arbitrary conversation histories independently of the service's tokenizer.
	query = ClipUTF8(strings.TrimSpace(query), 1024)
	body, _ := json.Marshal(struct {
		Query   string  `json:"query"`
		Filters Filters `json:"filters"`
		Limit   int     `json:"limit"`
		Mode    string  `json:"mode"`
	}{query, filters, limit, "hybrid"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, &Error{Code: "unavailable"}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := p.client.Do(req)
	if err != nil {
		code := "unavailable"
		if errors.Is(ctx.Err(), context.Canceled) {
			code = "canceled"
		} else if errors.Is(err, context.DeadlineExceeded) {
			code = "timeout"
		}
		return nil, &Error{Code: code}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		code := "unavailable"
		switch response.StatusCode {
		case 401, 403:
			code = "unauthorized"
		case 400, 422:
			code = "invalid_request"
		case 429:
			code = "rate_limited"
		}
		return nil, &Error{Code: code}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil {
		return nil, &Error{Code: "unavailable"}
	}
	if len(raw) > MaxResponseBytes {
		return nil, &Error{Code: "invalid_response"}
	}
	var envelope struct {
		Results *[]KnowledgeEvidence `json:"results"`
	}
	if !utf8.Valid(raw) || json.Unmarshal(raw, &envelope) != nil || envelope.Results == nil || len(*envelope.Results) > limit {
		return nil, &Error{Code: "invalid_response"}
	}
	seen := map[string]bool{}
	for _, e := range *envelope.Results {
		ids := Filters{SourceIDs: []string{e.SourceID, e.DocumentID, e.ChunkID}}
		if !ids.Valid() || seen[e.ChunkID] || strings.TrimSpace(e.Text) == "" || e.URI == "" || math.IsNaN(e.Score) || math.IsInf(e.Score, 0) || e.Score < 0 || e.Score > 1 || (e.PageStart != nil && *e.PageStart < 1) || (e.PageEnd != nil && (e.PageStart == nil || *e.PageEnd < *e.PageStart)) {
			return nil, &Error{Code: "invalid_response"}
		}
		seen[e.ChunkID] = true
	}
	return *envelope.Results, nil
}

// ClipUTF8 limits bytes without splitting a Unicode code point.
func ClipUTF8(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}

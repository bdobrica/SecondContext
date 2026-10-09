package knowledge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bdobrica/SecondContext/internal/config"
)

func fixtureEvidence() KnowledgeEvidence {
	return KnowledgeEvidence{ChunkID: "00000000-0000-0000-0000-000000000001", DocumentID: "00000000-0000-0000-0000-000000000002", SourceID: "00000000-0000-0000-0000-000000000003", Text: "Production requires two approvers.", Score: .9, Title: "Handbook", URI: "https://example.com/handbook", Section: []string{"Releases"}}
}
func TestHTTPProviderContractAndIsolation(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/v1/search" || r.Header.Get("Authorization") != "Bearer scoped-secret" {
			t.Errorf("wrong HTTP contract")
		}
		var body struct {
			Query   string
			Limit   int
			Filters Filters
			Mode    string
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Mode != "hybrid" || body.Limit != 2 || body.Filters.SourceIDs[0] != fixtureEvidence().SourceID || len(body.Query) > 1024 || !utf8.ValidString(body.Query) {
			t.Errorf("wrong search payload")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []KnowledgeEvidence{fixtureEvidence()}})
	}))
	defer upstream.Close()
	provider := NewHTTPProvider(config.KnowledgeConfig{URL: upstream.URL, Tokens: []config.AuthTokenConfig{{Subject: "alice", Token: "scoped-secret"}}})
	for _, subject := range []string{"bob", "alice:other", ""} {
		_, err := provider.Search(context.Background(), subject, "query", Filters{}, 2)
		if ErrorCode(err) != "not_configured" {
			t.Errorf("foreign subject got %v", err)
		}
	}
	if calls != 0 {
		t.Fatal("unmapped subjects contacted upstream")
	}
	results, err := provider.Search(context.Background(), "alice", strings.Repeat("知識", 600), Filters{SourceIDs: []string{fixtureEvidence().SourceID}}, 2)
	if err != nil || len(results) != 1 || results[0].ChunkID != fixtureEvidence().ChunkID {
		t.Fatalf("search: %v %v", results, err)
	}
}
func TestHTTPProviderFailuresAreStableAndPrivate(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		code   string
	}{
		{"auth", 401, "secret detail", "unauthorized"}, {"forbidden", 403, "", "unauthorized"},
		{"bad request", 422, "private query", "invalid_request"}, {"rate", 429, "", "rate_limited"},
		{"backend", 503, "credential", "unavailable"}, {"redirect", 307, "", "unavailable"},
		{"malformed", 200, "no json", "invalid_response"}, {"missing results", 200, "{}", "invalid_response"},
		{"null results", 200, `{"results":null}`, "invalid_response"},
		{"bad evidence", 200, `{"results":[{"text":"x"}]}`, "invalid_response"},
		{"oversized", 200, strings.Repeat(" ", MaxResponseBytes+1), "invalid_response"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "http://invalid.example")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			p := NewHTTPProvider(config.KnowledgeConfig{URL: server.URL, Tokens: []config.AuthTokenConfig{{Subject: "a", Token: "secret"}}})
			_, err := p.Search(context.Background(), "a", "private query", Filters{}, 1)
			if err == nil || ErrorCode(err) != test.code || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private query") {
				t.Fatalf("unsafe error %v", err)
			}
		})
	}
}
func TestHTTPProviderTimeoutCancellationAndCount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []KnowledgeEvidence{fixtureEvidence(), fixtureEvidence()}})
	}))
	defer server.Close()
	cfg := config.KnowledgeConfig{URL: server.URL, Timeout: 10 * time.Millisecond, Tokens: []config.AuthTokenConfig{{Subject: "a", Token: "x"}}}
	p := NewHTTPProvider(cfg)
	_, err := p.Search(context.Background(), "a", "q", Filters{}, 1)
	if ErrorCode(err) != "timeout" {
		t.Fatalf("timeout: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = p.Search(ctx, "a", "q", Filters{}, 1)
	if ErrorCode(err) != "canceled" {
		t.Fatalf("cancel: %v", err)
	}
	cfg.Timeout = time.Second
	p = NewHTTPProvider(cfg)
	_, err = p.Search(context.Background(), "a", "q", Filters{}, 1)
	if ErrorCode(err) != "invalid_response" {
		t.Fatalf("too many results: %v", err)
	}
	_, err = p.Search(context.Background(), "a", "q", Filters{}, 21)
	if ErrorCode(err) != "invalid_request" {
		t.Fatalf("limit: %v", err)
	}
}

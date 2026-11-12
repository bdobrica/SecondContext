package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
)

func TestSharedAndPrivateSearch(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		mu.Lock()
		calls[token]++
		mu.Unlock()
		var request struct {
			Filters Filters
			Limit   int
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if r.URL.Path != "/v1/search" || r.Method != "POST" || len(request.Filters.Formats) != 1 || request.Filters.Formats[0] != "markdown" {
			t.Error("shared retrieval changed search contract")
		}
		base := 10
		if token == "Bearer alice-private" {
			base = 20
		} else if token != "Bearer shared" {
			t.Error("unexpected owner credential")
		}
		items := []KnowledgeEvidence{}
		for i := 0; i < request.Limit; i++ {
			e := fixtureEvidence()
			e.ChunkID = fmt.Sprintf("00000000-0000-0000-0000-%012d", base+i)
			e.Scope = "untrusted-upstream-scope"
			items = append(items, e)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": items})
	}))
	defer upstream.Close()
	p := NewHTTPProvider(config.KnowledgeConfig{URL: upstream.URL, SharedToken: "shared", Tokens: []config.AuthTokenConfig{{Subject: "alice", Token: "alice-private"}}})
	for _, subject := range []string{"alice", "bob"} {
		items, err := p.Search(context.Background(), subject, "approval policy", Filters{Formats: []string{"markdown"}}, 3)
		if err != nil || len(items) != 3 {
			t.Fatalf("search: %v %v", items, err)
		}
		for i, e := range items {
			want := "shared"
			if subject == "alice" && i == 1 {
				want = "private"
			}
			if e.Scope != want {
				t.Fatalf("scope: %v", items)
			}
		}
	}
	if _, err := p.Search(context.Background(), "", "q", Filters{}, 2); ErrorCode(err) != "not_configured" {
		t.Fatal(err)
	}
	if _, err := p.Search(context.Background(), "bob", "q", Filters{SourceIDs: []string{"foreign-invalid"}}, 2); ErrorCode(err) != "invalid_request" {
		t.Fatal(err)
	}
	// Shared reads must never become an implicit destination for either kind of note.
	for _, purpose := range []string{"reference", "personal"} {
		_, err := p.IngestNote(context.Background(), "bob", NoteInput{Purpose: purpose, Name: "Private note", Text: "secret"}, "key")
		if ErrorCode(err) != "not_configured" {
			t.Fatal("shared upload fallback", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls["Bearer shared"] != 2 || calls["Bearer alice-private"] != 1 || len(calls) != 2 {
		t.Fatal(calls)
	}
}

func TestSharedSearchPartialFailureAndConcurrentDeadline(t *testing.T) {
	for _, failed := range []string{"shared", "private", "both"} {
		t.Run(failed, func(t *testing.T) {
			started := make(chan struct{}, 2)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				started <- struct{}{}
				// Both searches must start without waiting for the other response.
				deadline := time.After(time.Second)
				for len(started) < 2 {
					select {
					case <-deadline:
						t.Error("searches were serialized")
						w.WriteHeader(503)
						return
					default:
						time.Sleep(time.Millisecond)
					}
				}
				if failed == "both" || r.Header.Get("Authorization") == "Bearer "+failed {
					w.WriteHeader(503)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"results": []KnowledgeEvidence{fixtureEvidence()}})
			}))
			defer upstream.Close()
			p := NewHTTPProvider(config.KnowledgeConfig{URL: upstream.URL, SharedToken: "shared", Tokens: []config.AuthTokenConfig{{Subject: "alice", Token: "private"}}})
			items, err := p.Search(context.Background(), "alice", "q", Filters{}, 2)
			if failed == "both" {
				if ErrorCode(err) != "unavailable" || len(items) != 0 {
					t.Fatal(items, err)
				}
			} else if ErrorCode(err) != "partial" || len(items) != 1 || items[0].Scope == failed {
				t.Fatal(items, err)
			}
		})
	}
}

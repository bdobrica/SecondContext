package knowledge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bdobrica/SecondContext/internal/config"
)

func TestNoteIngestionTransport(t *testing.T) {
	for _, purpose := range []string{"personal", "reference"} {
		t.Run(purpose, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/notes" || r.Header.Get("Authorization") != "Bearer private-owner-token" || r.Header.Get("Idempotency-Key") != "stable" {
					t.Error("wrong upstream scope")
				}
				if err := r.ParseMultipartForm(10000); err != nil {
					t.Fatal(err)
				}
				if r.FormValue("purpose") != purpose || r.FormValue("text") != "Alex: proposal" || r.FormValue("meeting_date") != "2026-11-10" {
					t.Error("lost input")
				}
				w.Header().Set("Content-Type", "application/json")
				if purpose == "personal" {
					_, _ = w.Write([]byte(`{"purpose":"personal","text":"Alex: proposal","format":"text"}`))
				} else {
					_, _ = w.Write([]byte(`{"purpose":"reference","source_id":"00000000-0000-4000-8000-000000000001","status":"pending"}`))
				}
			}))
			defer upstream.Close()
			provider := NewHTTPProvider(config.KnowledgeConfig{URL: upstream.URL + "/", Tokens: []config.AuthTokenConfig{{Subject: "alice", Token: "private-owner-token"}}})
			input := NoteInput{Purpose: purpose, Name: "Review", Format: "text", Text: "Alex: proposal", MeetingDate: "2026-11-10"}
			if _, err := provider.IngestNote(context.Background(), "alice", input, "stable"); err != nil {
				t.Fatal(err)
			}
			if _, err := provider.IngestNote(context.Background(), "bob", input, "stable"); ErrorCode(err) != "not_configured" || calls != 1 {
				t.Fatal("unmapped subject used another credential", err)
			}
		})
	}
}

func TestNoteIngestionRejectsBadResponse(t *testing.T) {
	for _, payload := range []string{`{}`, `{"purpose":"reference","text":"wrong mode"}`, `{"purpose":"personal","text":""}`, `{"purpose":"personal","text":"` + strings.Repeat("x", 65537) + `"}`} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(payload)) }))
		provider := NewHTTPProvider(config.KnowledgeConfig{URL: upstream.URL, Tokens: []config.AuthTokenConfig{{Subject: "alice", Token: "token"}}})
		_, err := provider.IngestNote(context.Background(), "alice", NoteInput{Purpose: "personal"}, "stable")
		upstream.Close()
		if ErrorCode(err) != "invalid_response" {
			t.Fatal("bad parser response accepted", err)
		}
	}
}

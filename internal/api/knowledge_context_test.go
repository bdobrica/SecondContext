package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/knowledge"
	"github.com/bdobrica/SecondContext/internal/llm"
	"github.com/bdobrica/SecondContext/internal/prompts"
)

func knowledgeFixture() knowledge.KnowledgeEvidence {
	page := 3
	return knowledge.KnowledgeEvidence{ChunkID: "00000000-0000-0000-0000-000000000001", DocumentID: "00000000-0000-0000-0000-000000000002", SourceID: "00000000-0000-0000-0000-000000000003", Text: "Production deployment requires exactly two approvers.", Score: .9, Title: "Engineering handbook", Section: []string{"Releases", "Approval"}, URI: "https://example.com/handbook", PageStart: &page, PageEnd: &page}
}
func knowledgeTestConfig(url string) config.Config {
	return config.Config{App: config.AppConfig{Env: "test"}, Dev: config.DevConfig{UserExternalID: "knowledge-adapter-test"}, Knowledge: config.KnowledgeConfig{Enabled: true, URL: url, Limit: 4, Tokens: []config.AuthTokenConfig{{Subject: "knowledge-adapter-test", Token: "scoped-secret"}}}}
}
func TestKnowledgeIndependentControlsAndDegradation(t *testing.T) {
	for _, test := range []struct {
		name                                             string
		disableMemory, disableKnowledge, metadataDisable bool
		upstreamStatus                                   int
		wantCalls                                        int
		wantStatus                                       string
	}{
		{"normal", false, false, false, 200, 1, "ready"},
		{"memory disabled", true, false, false, 200, 1, "ready"},
		{"knowledge disabled", false, true, false, 200, 0, "disabled"},
		{"metadata disabled", false, false, true, 200, 0, "disabled"},
		{"both disabled", true, true, false, 200, 0, "disabled"},
		{"unavailable", false, false, false, 503, 1, "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(test.upstreamStatus)
				_ = json.NewEncoder(w).Encode(map[string]any{"results": []knowledge.KnowledgeEvidence{knowledgeFixture()}})
			}))
			defer upstream.Close()
			client := &fakeLLMClient{response: llm.GenerateResponse{OutputText: "answer"}}
			server := NewServerWithClient(knowledgeTestConfig(upstream.URL), slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), nil, client)
			body, _ := json.Marshal(map[string]any{"input": "How many production approvers?", "disable_memory": test.disableMemory, "disable_knowledge": test.disableKnowledge, "metadata": map[string]any{"disable_knowledge": test.metadataDisable}})
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body)))
			if recorder.Code != 200 {
				t.Fatalf("response: %s", recorder.Body.String())
			}
			var response createResponseResult
			_ = json.Unmarshal(recorder.Body.Bytes(), &response)
			packet := response.Metadata["context_packet"].(map[string]any)
			if packet["knowledge_status"] != test.wantStatus || calls != test.wantCalls {
				t.Fatalf("state: %v calls=%d", packet, calls)
			}
			_, hasEvidence := packet["knowledge_context"]
			if hasEvidence != (test.wantStatus == "ready") {
				t.Fatalf("evidence: %v", packet)
			}
			if test.wantStatus == "ready" && !strings.Contains(client.request.Messages[0].Content, "https://example.com/handbook") {
				t.Fatal("citation missing from prompt")
			}
		})
	}
}

func TestKnowledgeUsesResolvedSubjectAndRejectsInvalidFilters(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer upstream.Close()
	cfg := knowledgeTestConfig(upstream.URL)
	cfg.Auth = config.AuthConfig{Enabled: true, Tokens: []config.AuthTokenConfig{{Subject: "alice", Token: "alice-token"}, {Subject: "bob", Token: "bob-token"}}}
	cfg.Knowledge.Tokens = []config.AuthTokenConfig{{Subject: "alice", Token: "scoped-secret"}}
	server := NewServerWithClient(cfg, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), nil, &fakeLLMClient{})
	for _, test := range []struct {
		token, body string
		status      int
	}{
		{"bob-token", `{"input":"q"}`, 200},
		{"bob-token", `{"input":"q","user":"alice"}`, 400},
		{"alice-token", `{"input":"q","knowledge_filters":{"source_ids":["invalid"]}}`, 400},
		{"alice-token", `{"input":"q"}`, 200},
	} {
		req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(test.body))
		req.Header.Set("Authorization", "Bearer "+test.token)
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, req)
		if rec.Code != test.status {
			t.Fatalf("scope: status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("unmapped/foreign subject contacted upstream: %d", calls)
	}
}

type evidenceAnswerClient struct{ fakeLLMClient }

func (c *evidenceAnswerClient) Generate(_ context.Context, request llm.GenerateRequest) (llm.GenerateResponse, error) {
	c.request = request
	for _, message := range request.Messages {
		if message.Role == "system" && strings.Contains(message.Content, "Production deployment requires exactly two approvers.") {
			return llm.GenerateResponse{OutputText: "Two approvers, per Engineering handbook (https://example.com/handbook, page 3)."}, nil
		}
	}
	return llm.GenerateResponse{OutputText: "No approval policy was supplied."}, nil
}
func (c *evidenceAnswerClient) Embed(context.Context, llm.EmbedRequest) (llm.EmbedResponse, error) {
	return llm.EmbedResponse{}, errors.New("memory embedding unavailable")
}

// Real SecondContext Postgres, HTTP-only knowledge boundary and deterministic
// answer client: inspect exactly what changed without model variance or writes.
func TestKnowledgeImprovesAnswerWithoutCreatingMemories(t *testing.T) {
	if testing.Short() {
		t.Skip("Postgres integration")
	}
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not set")
	}
	pool, err := db.Open(context.Background(), config.PostgresConfig{Enabled: true, DSN: dsn, MaxConns: 4, MinConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dir, err := filepath.Abs("../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.RunMigrationsUp(config.PostgresConfig{DSN: dsn}, dir); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/search" || r.Header.Get("Authorization") != "Bearer scoped-secret" {
			t.Error("invalid knowledge boundary")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []knowledge.KnowledgeEvidence{knowledgeFixture()}})
	}))
	defer upstream.Close()
	cfg := knowledgeTestConfig(upstream.URL)
	cfg.Dev.UserExternalID = fmt.Sprintf("knowledge-adapter-%d", time.Now().UnixNano())
	cfg.Knowledge.Tokens[0].Subject = cfg.Dev.UserExternalID
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE external_id=$1", cfg.Dev.UserExternalID)
	})
	server := NewServerWithClient(cfg, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), pool, &evidenceAnswerClient{})
	var before int
	if err = pool.QueryRow(context.Background(), "SELECT count(*) FROM memory_items m JOIN users u ON u.id=m.user_id WHERE u.external_id=$1", cfg.Dev.UserExternalID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, disabled := range []bool{true, false} {
		body, _ := json.Marshal(map[string]any{"input": "How many approvers for production deployment?", "disable_knowledge": disabled})
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body)))
		if rec.Code != 200 {
			t.Fatalf("answer: %s", rec.Body.String())
		}
		var response struct {
			OutputText string `json:"output_text"`
			Metadata   struct {
				ContextPacket prompts.ContextPacket `json:"context_packet"`
			} `json:"metadata"`
		}
		if err = json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if disabled {
			if response.OutputText != "No approval policy was supplied." {
				t.Fatal("baseline has knowledge")
			}
		} else {
			if !strings.Contains(response.OutputText, "Two approvers") || len(response.Metadata.ContextPacket.KnowledgeContext) != 1 || len(response.Metadata.ContextPacket.MemoryContext) != 0 {
				t.Fatalf("knowledge did not survive memory failure: %+v", response)
			}
		}
	}

	var stored json.RawMessage
	if err = pool.QueryRow(context.Background(), "SELECT m.metadata FROM messages m JOIN users u ON u.id=m.user_id WHERE u.external_id=$1 AND m.role='assistant' ORDER BY m.created_at DESC LIMIT 1", cfg.Dev.UserExternalID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var persisted struct {
		Packet prompts.ContextPacket `json:"context_packet"`
	}
	if err = json.Unmarshal(stored, &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.Packet.KnowledgeContext) != 1 || persisted.Packet.KnowledgeContext[0].ChunkID != knowledgeFixture().ChunkID {
		t.Fatal("stored context lost provenance")
	}
	for _, disabled := range []bool{false, true} {
		debug, err := server.buildDebugContextResponse(context.Background(), debugContextQuery{UserExternalID: cfg.Dev.UserExternalID, Input: "How many production approvers?", DisableKnowledge: disabled})
		if err != nil {
			t.Fatal(err)
		}
		if debug.CurrentContextPacket == nil {
			t.Fatal("missing debug packet")
		}
		if disabled {
			if len(debug.CurrentContextPacket.KnowledgeContext) != 0 || debug.CurrentContextPacket.KnowledgeStatus != "disabled" {
				t.Fatal("debug disable control failed")
			}
		} else if len(debug.CurrentContextPacket.KnowledgeContext) != 1 || !strings.Contains(debug.CurrentPromptPreview, knowledgeFixture().URI) {
			t.Fatal("debug lost reference provenance during memory failure")
		}
	}

	var after int
	if err = pool.QueryRow(context.Background(), "SELECT count(*) FROM memory_items m JOIN users u ON u.id=m.user_id WHERE u.external_id=$1", cfg.Dev.UserExternalID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("answer created memory: before=%d after=%d", before, after)
	}
}

package db

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
)

func TestRepositoriesIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not set")
	}

	ctx := context.Background()
	pool, err := Open(ctx, config.PostgresConfig{
		Enabled:  true,
		DSN:      dsn,
		MaxConns: 4,
		MinConns: 1,
	})
	if err != nil {
		t.Skipf("postgres is not reachable: %v", err)
	}
	defer Close(pool)

	users := NewUserRepository(pool)
	sessions := NewSessionRepository(pool)
	messages := NewMessageRepository(pool)
	people := NewPersonRepository(pool)
	topics := NewTopicRepository(pool)
	memories := NewMemoryRepository(pool)

	unique := fmt.Sprintf("repo-test-%d", time.Now().UnixNano())
	user, err := users.Ensure(ctx, EnsureUserParams{
		ExternalID:  unique,
		Email:       unique + "@example.com",
		DisplayName: "Repository Test",
	})
	if err != nil {
		t.Fatalf("ensure user: %v", err)
	}

	session, err := sessions.Create(ctx, CreateSessionParams{
		UserID:     user.ID,
		ExternalID: unique,
		Title:      "Test Session",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	message, err := messages.Create(ctx, CreateMessageParams{
		SessionID: session.ID,
		UserID:    user.ID,
		Role:      "user",
		Content:   "Help me ask Alex to review the proposal.",
		Model:     "test-model",
		RequestID: unique,
	})
	if err != nil {
		t.Fatalf("create message: %v", err)
	}

	person, err := people.Upsert(ctx, UpsertPersonParams{
		UserID:  user.ID,
		Name:    "Alex",
		Aliases: []string{"Alexander"},
	})
	if err != nil {
		t.Fatalf("upsert person: %v", err)
	}

	topic, err := topics.Upsert(ctx, UpsertTopicParams{
		UserID:  user.ID,
		Name:    "Infrastructure",
		Aliases: []string{"infra"},
	})
	if err != nil {
		t.Fatalf("upsert topic: %v", err)
	}

	memory, err := memories.Create(ctx, CreateMemoryItemParams{
		UserID:          user.ID,
		SessionID:       session.ID,
		SourceMessageID: message.ID,
		MemoryType:      "person_preference",
		Source:          "manual",
		RawText:         "Alex prefers a narrow review scope for infrastructure requests.",
		Summary:         "Alex prefers narrow review scopes.",
		People:          []string{"Alex"},
		Topics:          []string{"infrastructure"},
		Importance:      0.7,
		Utility:         0.8,
		BeliefImpact:    0.2,
		Confidence:      0.9,
	})
	if err != nil {
		t.Fatalf("create memory: %v", err)
	}

	if _, err := people.GetByName(ctx, user.ID, person.Name); err != nil {
		t.Fatalf("get person by name: %v", err)
	}

	if _, err := topics.GetByName(ctx, user.ID, topic.Name); err != nil {
		t.Fatalf("get topic by name: %v", err)
	}

	storedMessages, err := messages.ListBySession(ctx, session.ID, 10)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(storedMessages) == 0 {
		t.Fatal("expected stored messages")
	}

	storedMemory, err := memories.GetByID(ctx, memory.ID)
	if err != nil {
		t.Fatalf("get memory by id: %v", err)
	}
	if storedMemory.Summary != memory.Summary {
		t.Fatalf("unexpected memory summary: got %q want %q", storedMemory.Summary, memory.Summary)
	}
}

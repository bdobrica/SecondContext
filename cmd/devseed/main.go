package main

import (
	"context"
	"fmt"
	"os"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
)

func main() {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		fatalf("load config: %v", err)
	}
	cfg.Postgres.Enabled = true

	pool, err := db.Open(ctx, cfg.Postgres)
	if err != nil {
		fatalf("connect postgres: %v", err)
	}
	defer db.Close(pool)

	users := db.NewUserRepository(pool)
	user, err := users.Ensure(ctx, db.EnsureUserParams{
		ExternalID:  cfg.Dev.UserExternalID,
		Email:       cfg.Dev.UserEmail,
		DisplayName: cfg.Dev.UserName,
	})
	if err != nil {
		fatalf("seed dev user: %v", err)
	}

	fmt.Printf("seeded dev user id=%s external_id=%s\n", user.ID, user.ExternalID)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

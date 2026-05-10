package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/golang-migrate/migrate/v4"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
)

func main() {
	if len(os.Args) < 2 {
		fatalf("usage: go run ./cmd/migrate <up|down|version> [steps]")
	}

	cfg, err := config.Load()
	if err != nil {
		fatalf("load config: %v", err)
	}
	cfg.Postgres.Enabled = true

	migrationsDir, err := filepath.Abs("migrations")
	if err != nil {
		fatalf("resolve migrations dir: %v", err)
	}

	switch os.Args[1] {
	case "up":
		if err := db.RunMigrationsUp(cfg.Postgres, migrationsDir); err != nil {
			fatalf("migrate up: %v", err)
		}
		fmt.Println("migrations applied")
	case "down":
		steps := 1
		if len(os.Args) > 2 {
			parsed, err := strconv.Atoi(os.Args[2])
			if err != nil || parsed <= 0 {
				fatalf("invalid down steps %q", os.Args[2])
			}
			steps = parsed
		}

		if err := db.RunMigrationsDown(cfg.Postgres, migrationsDir, steps); err != nil {
			fatalf("migrate down: %v", err)
		}
		fmt.Printf("rolled back %d migration step(s)\n", steps)
	case "version":
		version, dirty, err := db.MigrationVersion(cfg.Postgres, migrationsDir)
		if err != nil {
			if errors.Is(err, migrate.ErrNilVersion) {
				fmt.Println("no migrations applied")
				return
			}
			fatalf("migration version: %v", err)
		}

		fmt.Printf("version=%d dirty=%t\n", version, dirty)
	default:
		fatalf("unsupported command %q", os.Args[1])
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

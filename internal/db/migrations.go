package db

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/bdobrica/SecondContext/internal/config"
)

func RunMigrationsUp(cfg config.PostgresConfig, migrationsDir string) error {
	m, err := newMigrator(cfg, migrationsDir)
	if err != nil {
		return err
	}
	defer closeMigrator(m)

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}

	return nil
}

func RunMigrationsDown(cfg config.PostgresConfig, migrationsDir string, steps int) error {
	m, err := newMigrator(cfg, migrationsDir)
	if err != nil {
		return err
	}
	defer closeMigrator(m)

	if steps <= 0 {
		steps = 1
	}

	if err := m.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}

	return nil
}

func MigrationVersion(cfg config.PostgresConfig, migrationsDir string) (uint, bool, error) {
	m, err := newMigrator(cfg, migrationsDir)
	if err != nil {
		return 0, false, err
	}
	defer closeMigrator(m)

	return m.Version()
}

func newMigrator(cfg config.PostgresConfig, migrationsDir string) (*migrate.Migrate, error) {
	resolvedDir, err := filepath.Abs(migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("resolve migrations dir: %w", err)
	}

	m, err := migrate.New("file://"+resolvedDir, cfg.ConnectionString())
	if err != nil {
		return nil, err
	}

	return m, nil
}

func closeMigrator(m *migrate.Migrate) {
	if m == nil {
		return
	}

	_, _ = m.Close()
}

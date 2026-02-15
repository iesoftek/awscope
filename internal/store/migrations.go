package store

import (
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func (s *Store) migrate() error {
	// Goose defaults to a postgres dialect; set explicitly for sqlite.
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	// Avoid noisy stdout logs during normal runs; callers can add their own logging.
	goose.SetLogger(goose.NopLogger())

	goose.SetBaseFS(migrationsFS)

	if err := goose.Up(s.db, "migrations"); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

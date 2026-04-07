package postgres

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/lib/pq"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate runs all pending database migrations.
// Safe to call on every startup — already-applied migrations are skipped.
// Uses an advisory lock so concurrent calls (e.g. api + ingestion starting together) are safe.
func Migrate(dbURL string) error {
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return fmt.Errorf("migrate: open db: %w", err)
	}
	defer db.Close()

	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("migrate: load source: %w", err)
	}

	driver, err := migratepg.WithInstance(db, &migratepg.Config{
		SchemaName:      "axiaops",
		MigrationsTable: "schema_migrations",
	})
	if err != nil {
		return fmt.Errorf("migrate: driver init: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		return fmt.Errorf("migrate: init: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate: up: %w", err)
	}
	return nil
}

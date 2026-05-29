package main

import (
	"log/slog"
	"os"

	"axiaops.io/shared/logging"
	"axiaops.io/shared/storage/postgres"
)

func main() {
	logging.Init("migrate")

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		slog.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	migrationURL := os.Getenv("MIGRATION_DATABASE_URL")
	if migrationURL == "" {
		migrationURL = dbURL
	}
	runtimeAdminURL := os.Getenv("RUNTIME_ADMIN_DATABASE_URL")

	if err := postgres.Bootstrap(migrationURL, dbURL, runtimeAdminURL); err != nil {
		slog.Error("bootstrap failed", "error", err)
		os.Exit(1)
	}

	if err := postgres.Migrate(migrationURL); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	slog.Info("migrations completed successfully")
}

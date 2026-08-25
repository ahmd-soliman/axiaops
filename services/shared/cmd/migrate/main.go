package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"text/tabwriter"

	_ "github.com/lib/pq"

	"axiaops.io/shared/logging"
	"axiaops.io/shared/storage/postgres"
)

// Subcommand layout (see docs/ARCHITECTURE.md (§5, Migration system)):
//
//	axiaopsctl migrate up           Bootstrap + Migrate (default; argv-less call also lands here)
//	axiaopsctl migrate down N       Steps(-N) with history recording
//	axiaopsctl migrate force N      Force(N) + write a force history row
//	axiaopsctl migrate drift        Compare on-disk SHAs with recorded SHAs
//	axiaopsctl migrate history [V]  Pretty-print migration_history rows (optionally for one version)
//
// Argv-less invocation (`axiaopsctl` with no args) is treated as `migrate up`
// so the existing `services/migrate` Dockerfile and migrate-image Make target
// keep working unchanged while we transition.

func main() {
	logging.Init("migrate")

	args := os.Args[1:]
	// Tolerate `migrate <subcmd>` and bare `<subcmd>` so back-compat callers
	// (Dockerfiles invoking the binary with no args) and operators using the
	// new shape both succeed.
	if len(args) > 0 && args[0] == "migrate" {
		args = args[1:]
	}
	sub := "up"
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}

	switch sub {
	case "up":
		runUp()
	case "down":
		runDown(args)
	case "force":
		runForce(args)
	case "drift":
		runDrift()
	case "history":
		runHistory(args)
	case "-h", "--help", "help":
		printUsage()
	default:
		slog.Error("unknown subcommand", "subcommand", sub)
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage: axiaopsctl migrate <subcommand> [args]

Subcommands:
  up              Bootstrap + apply pending migrations (default)
  down N          Roll back N migrations
  force N         Mark migration_state at version N (no DDL)
  drift           Print versions whose on-disk SHA differs from the recorded SHA
  history [V]     Show migration_history rows (optionally filtered by version V)

Required env:
  MIGRATION_DATABASE_URL      owner role (always required; defaults to DATABASE_URL when unset)
  DATABASE_URL                app role  (only required for 'up' — Bootstrap reads it to sync the app password)
  RUNTIME_ADMIN_DATABASE_URL  runtime RLS-bypass role (optional; 'up' syncs its LOGIN+password when set)`)
}

func dbURLs() (dbURL, migrationURL string) {
	dbURL = os.Getenv("DATABASE_URL")
	migrationURL = os.Getenv("MIGRATION_DATABASE_URL")
	if migrationURL == "" {
		migrationURL = dbURL
	}
	return dbURL, migrationURL
}

func runUp() {
	dbURL, migrationURL := dbURLs()
	if dbURL == "" {
		slog.Error("DATABASE_URL is required")
		os.Exit(1)
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

func runDown(args []string) {
	if len(args) < 1 {
		slog.Error("down requires N (number of steps)")
		os.Exit(2)
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n <= 0 {
		slog.Error("down N must be a positive integer", "got", args[0])
		os.Exit(2)
	}
	_, migrationURL := dbURLs()
	if migrationURL == "" {
		slog.Error("MIGRATION_DATABASE_URL (or DATABASE_URL) is required")
		os.Exit(1)
	}
	if err := postgres.MigrateDown(migrationURL, n); err != nil {
		slog.Error("migrate down failed", "error", err)
		os.Exit(1)
	}
	slog.Info("migrate down completed", "steps", n)
}

func runForce(args []string) {
	if len(args) < 1 {
		slog.Error("force requires N (target version)")
		os.Exit(2)
	}
	v, err := strconv.Atoi(args[0])
	if err != nil || v < 0 {
		slog.Error("force N must be a non-negative integer", "got", args[0])
		os.Exit(2)
	}
	_, migrationURL := dbURLs()
	if migrationURL == "" {
		slog.Error("MIGRATION_DATABASE_URL (or DATABASE_URL) is required")
		os.Exit(1)
	}
	if err := postgres.MigrateForce(migrationURL, v); err != nil {
		slog.Error("migrate force failed", "error", err)
		os.Exit(1)
	}
	slog.Info("migrate force completed", "version", v)
}

func runDrift() {
	_, migrationURL := dbURLs()
	if migrationURL == "" {
		slog.Error("MIGRATION_DATABASE_URL (or DATABASE_URL) is required")
		os.Exit(1)
	}
	rows, err := postgres.QueryDrift(context.Background(), migrationURL)
	if err != nil {
		slog.Error("drift query failed", "error", err)
		os.Exit(1)
	}
	if len(rows) == 0 {
		fmt.Println("no drift detected")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "VERSION\tNAME\tEXPECTED\tOBSERVED")
	for _, r := range rows {
		_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", r.Version, r.Name, short(r.ExpectedSHA), short(r.ObservedSHA))
	}
	_ = tw.Flush()
}

func runHistory(args []string) {
	_, migrationURL := dbURLs()
	if migrationURL == "" {
		slog.Error("MIGRATION_DATABASE_URL (or DATABASE_URL) is required")
		os.Exit(1)
	}
	var filterV *int64
	if len(args) >= 1 {
		v, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			slog.Error("history V must be an integer", "got", args[0])
			os.Exit(2)
		}
		filterV = &v
	}
	rows, err := postgres.QueryHistory(context.Background(), migrationURL, filterV)
	if err != nil {
		slog.Error("history query failed", "error", err)
		os.Exit(1)
	}
	if len(rows) == 0 {
		fmt.Println("no history rows")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tV\tNAME\tDIR\tSTATUS\tSTARTED_AT\tDUR_MS\tDIRTY\tSHA8\tIMAGE")
	for _, r := range rows {
		dur := "-"
		if r.DurationMS.Valid {
			dur = strconv.FormatInt(r.DurationMS.Int64, 10)
		}
		dirty := "-"
		if r.DirtyAfter.Valid {
			dirty = strconv.FormatBool(r.DirtyAfter.Bool)
		}
		image := "-"
		if r.AppliedByImage.Valid {
			image = r.AppliedByImage.String
		}
		_, _ = fmt.Fprintf(tw, "%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.ID, r.Version, r.Name, r.Direction, r.Status,
			r.StartedAt.Format("2006-01-02T15:04:05Z"),
			dur, dirty, r.FileSHAShort, image)
	}
	_ = tw.Flush()
}

func short(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}

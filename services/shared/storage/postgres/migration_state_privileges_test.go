package postgres_test

import (
	"errors"
	"testing"

	"github.com/lib/pq"
)

// TestMigrationState_AppUserCanSelectButNotInsert is the migration_state
// analogue of TestMigrationHistory_AppUserCanSelectButNotInsert. The same
// 000_init blanket grant that landed DML on migration_history landed it on
// migration_state too — migration 026 closes that gap.
//
// migration_state is golang-migrate's internal bookkeeping (renamed from
// schema_migrations 2026-05-10); the app role keeps SELECT (harmless to
// read) but must not be able to mutate version / dirty.
//
// Old name TestSchemaMigrations_AppUserCanSelectButNotInsert (pre-rename).
func TestMigrationState_AppUserCanSelectButNotInsert(t *testing.T) {
	db := openApp(t)

	if _, err := db.Query(`SELECT version, dirty FROM axiaops.migration_state LIMIT 1`); err != nil {
		t.Fatalf("app user SELECT failed: %v", err)
	}

	// Wrap the destructive attempt in a transaction so the row never
	// materialises even if the privilege check (incorrectly) passes —
	// privilege failures fire at execution time but before tuple commit,
	// so a successful auto-commit Exec would leave a stray row otherwise.
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Use a deliberately invalid version (-1) so even if the privilege
	// check were to pass and the rollback were to fail, the row wouldn't
	// be reachable by golang-migrate's "max(version)" lookup.
	_, err = tx.Exec(`
		INSERT INTO axiaops.migration_state (version, dirty)
		VALUES (-1, false)
	`)
	if err == nil {
		t.Fatal("expected permission-denied INSERT on migration_state, got nil")
	}
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		t.Fatalf("expected *pq.Error, got: %T %v", err, err)
	}
	if pqErr.Code != "42501" { // insufficient_privilege
		t.Fatalf("expected SQLSTATE 42501, got %s: %v", pqErr.Code, err)
	}
}

// TestMigrationState_AppUserCannotUpdate guards against the specific DoS
// vector the migration was written to close: an UPDATE that flips the dirty
// bit. Same defence-in-depth as the INSERT test above.
func TestMigrationState_AppUserCannotUpdate(t *testing.T) {
	db := openApp(t)

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Exec(`UPDATE axiaops.migration_state SET dirty = dirty WHERE FALSE`)
	if err == nil {
		t.Fatal("expected permission-denied UPDATE on migration_state, got nil")
	}
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		t.Fatalf("expected *pq.Error, got: %T %v", err, err)
	}
	if pqErr.Code != "42501" {
		t.Fatalf("expected SQLSTATE 42501, got %s: %v", pqErr.Code, err)
	}
}

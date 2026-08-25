#!/usr/bin/env bash
# clean_db.sh — clean the local AxiaOps database
#
# Usage:
#   ./scripts/clean_db.sh                # Truncate all tables
#   ./scripts/clean_db.sh --drop-schema  # Drop the schema + user entirely

set -euo pipefail

# ── Parse arguments ───────────────────────────────────────────────────────────

DROP_SCHEMA=false
AUTO_YES=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --drop-schema) DROP_SCHEMA=true ;;
    --yes|-y) AUTO_YES=true ;;
    *) echo "Error: Unknown flag '$1'"; exit 1 ;;
  esac
  shift
done

# ── Connection setup ──────────────────────────────────────────────────────────

psql_exec()  { docker compose exec -T postgres psql -U axiaops_owner -d axiaops -c "$1"; }
psql_super() { docker compose exec -T postgres psql -U axiaops_owner -d axiaops -c "$1"; }

if [[ "$DROP_SCHEMA" == "true" ]]; then
  echo "=== DROPPING local AxiaOps schema ==="
  echo ""
  echo "⚠️  ⚠️  ⚠️  DESTRUCTIVE OPERATION ⚠️  ⚠️  ⚠️"
  echo "This will DROP the entire 'axiaops' schema and user."
  echo "All data will be permanently deleted."
  echo "You will need to re-run migrations to recreate the schema."
else
  echo "=== Cleaning local AxiaOps database ==="
  echo ""
  echo "WARNING: This will DELETE ALL rows from all tables."
fi

echo ""

# ── Confirmation ──────────────────────────────────────────────────────────────

if [[ "$AUTO_YES" != "true" ]]; then
  if [[ -t 0 ]]; then
    read -r -p "Are you sure? [y/N] " CONFIRM
    if [[ "$CONFIRM" != "y" && "$CONFIRM" != "Y" ]]; then
      echo "Aborted."
      exit 0
    fi
  else
    echo "Error: stdin is not a terminal. Use --yes to confirm non-interactively."
    exit 1
  fi
fi
echo ""

# ── Drop schema (destructive) ─────────────────────────────────────────────────

if [[ "$DROP_SCHEMA" == "true" ]]; then
  echo "Dropping schema 'axiaops'..."
  # schema_migrations lives inside axiaops now, so CASCADE handles it.
  psql_super "DROP SCHEMA IF EXISTS axiaops CASCADE;"
  echo "Revoking privileges from user 'axiaops'..."
  psql_super "REVOKE ALL PRIVILEGES ON DATABASE axiaops FROM axiaops;" 2>/dev/null || true
  echo "Dropping user 'axiaops'..."
  psql_super "DROP USER IF EXISTS axiaops;"
  echo ""
  echo "=== Schema dropped ==="
  echo "Run migrations to recreate the schema."
  exit 0
fi

# ── Truncate all data ─────────────────────────────────────────────────────────

echo "Truncating all tables..."
# schema_migrations is intentionally NOT truncated — that's what --drop-schema is for.
# Truncating it would leave data tables intact but make golang-migrate think no
# migrations have been applied, causing re-run failures on next service startup.
psql_exec "TRUNCATE TABLE axiaops.zombie_snapshot_services, axiaops.zombie_snapshots, axiaops.dismissed_zombies, axiaops.resource_records, axiaops.zombie_records, axiaops.cost_records, axiaops.accounts, axiaops.users, axiaops.organizations RESTART IDENTITY CASCADE;" 2>/dev/null || true
echo "  Done."
echo ""

echo "=== Clean complete ==="

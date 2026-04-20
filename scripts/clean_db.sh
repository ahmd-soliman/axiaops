#!/usr/bin/env bash
# clean_db.sh — clean AxiaOps database (local docker or remote)
#
# Usage:
#   ./scripts/clean_db.sh                          # Local docker (truncate)
#   ./scripts/clean_db.sh --drop-schema            # Local docker (drop schema)
#   ./scripts/clean_db.sh --remote dev             # Remote dev (truncate)
#   ./scripts/clean_db.sh --remote staging --drop-schema  # Remote staging (drop schema)

set -euo pipefail

# ── Parse arguments ───────────────────────────────────────────────────────────

REMOTE_ENV=""
DROP_SCHEMA=false
AUTO_YES=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --remote)
      shift
      REMOTE_ENV="${1:-}"
      if [[ "$REMOTE_ENV" != "dev" && "$REMOTE_ENV" != "staging" ]]; then
        echo "Error: --remote requires 'dev' or 'staging', got '$REMOTE_ENV'"
        exit 1
      fi
      ;;
    --drop-schema) DROP_SCHEMA=true ;;
    --yes|-y) AUTO_YES=true ;;
    *) echo "Error: Unknown flag '$1'"; exit 1 ;;
  esac
  shift
done

# ── Connection setup ──────────────────────────────────────────────────────────

if [[ -n "$REMOTE_ENV" ]]; then
  # Remote mode
  MODE="remote"
  HOSTNAME="NAS.local"
  
  if [[ "$REMOTE_ENV" == "dev" ]]; then
    DB_PORT=5432
  else
    DB_PORT=5433
  fi
  
  SUPERUSER_URL="postgres://axiaops_owner:axiaops_owner@$HOSTNAME:$DB_PORT/axiaops?sslmode=disable"
  
  psql_exec()  { PGOPTIONS="-c search_path=axiaops" psql "$SUPERUSER_URL" --quiet -c "$1"; }
  psql_query() { PGOPTIONS="-c search_path=axiaops" psql "$SUPERUSER_URL" -t --no-align -c "$1"; }
  psql_super() { psql "$SUPERUSER_URL" --quiet -c "$1"; }
  
  if [[ "$DROP_SCHEMA" == "true" ]]; then
    echo "=== DROPPING AxiaOps $REMOTE_ENV schema ==="
    echo "Target:    $HOSTNAME:$DB_PORT"
    echo ""
    echo "⚠️  ⚠️  ⚠️  DESTRUCTIVE OPERATION ⚠️  ⚠️  ⚠️"
    echo "This will DROP the entire 'axiaops' schema and user."
    echo "All data will be permanently deleted."
    echo "You will need to re-run migrations to recreate the schema."
  else
    echo "=== Cleaning AxiaOps $REMOTE_ENV database ==="
    echo "Target:    $HOSTNAME:$DB_PORT"
    echo ""
    echo "WARNING: This will DELETE ALL rows from all tables."
  fi
else
  # Local mode
  MODE="local"
  
  psql_exec()  { docker-compose exec -T postgres psql -U axiaops_owner -d axiaops -c "$1"; }
  psql_super() { docker-compose exec -T postgres psql -U axiaops_owner -d axiaops -c "$1"; }
  
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

# ── Verify connection (remote only) ───────────────────────────────────────────

if [[ "$MODE" == "remote" ]]; then
  echo -n "Checking connection to $HOSTNAME..."
  if ! psql "$SUPERUSER_URL" -c 'SELECT 1' > /dev/null 2>&1; then
    echo " Failed."
    echo "Error: Cannot reach PostgreSQL at $HOSTNAME:$DB_PORT"
    exit 1
  fi
  echo " Connected."
  echo ""
fi

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

echo "Resetting migration state (axiaops.schema_migrations)..."
psql_super "DROP TABLE IF EXISTS axiaops.schema_migrations;" 2>/dev/null || true
echo ""

echo "Truncating all tables..."
psql_exec "TRUNCATE TABLE axiaops.ghost_snapshots, axiaops.resource_records, axiaops.ghost_records, axiaops.cost_records, axiaops.accounts, axiaops.users, axiaops.tenants RESTART IDENTITY CASCADE;" 2>/dev/null || true
echo "  Done."
echo ""

echo "=== Clean complete ==="

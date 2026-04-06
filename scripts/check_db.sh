#!/usr/bin/env bash
# check_db.sh — inspect the AxiaOps database
#
# Usage:
#   ./scripts/check_db.sh              PostgreSQL (default)
#   ./scripts/check_db.sh --sqlite     SQLite fallback
#   ./scripts/check_db.sh --sqlite path/to/axiaops.db

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

USE_SQLITE=false
DB_PATH="$ROOT/axiaops.db"

for arg in "$@"; do
  case "$arg" in
    --sqlite) USE_SQLITE=true ;;
    --*)      ;;
    *)        DB_PATH="$arg" ;;
  esac
done

# ── Query helpers ─────────────────────────────────────────────────────────────

if [[ "$USE_SQLITE" == "true" ]]; then
  if [[ ! -f "$DB_PATH" ]]; then
    echo "SQLite database not found: $DB_PATH"
    exit 1
  fi
  q() { sqlite3 "$DB_PATH" "$1"; }
  echo "Database: SQLite ($DB_PATH)"
else
  if ! docker ps --format '{{.Names}}' 2>/dev/null | grep -q "^axiaops-postgres$"; then
    echo "PostgreSQL is not running. Start it with: ./scripts/dev.sh"
    exit 1
  fi
  q() { docker exec -i axiaops-postgres psql -U axiaops_owner -d axiaops -t --no-align \
          -c "SET search_path TO axiaops" -c "$1"; }
  echo "Database: PostgreSQL (axiaops schema)"
fi

echo ""

# ── Tenants ───────────────────────────────────────────────────────────────────

echo "=== Tenants ==="
q "SELECT id, org_code, name, created_at FROM tenants ORDER BY created_at;"

echo ""

# ── Users ─────────────────────────────────────────────────────────────────────

echo "=== Users ==="
q "SELECT id, tenant_id, kinde_sub, email, name, last_seen FROM users ORDER BY last_seen DESC;"

echo ""

# ── Cost Records ──────────────────────────────────────────────────────────────

echo "=== Cost Records ==="
q "SELECT COUNT(*) || ' records' FROM cost_records;"

echo ""

# ── Ghost Records ─────────────────────────────────────────────────────────────

echo "=== Ghost Records ==="
q "SELECT tenant_id, service, resource_id, monthly_cost, currency, reason FROM ghost_records ORDER BY monthly_cost DESC;"

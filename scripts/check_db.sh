#!/usr/bin/env bash
# check_db.sh — inspect the AxiaOps database
#
# Usage:
#   ./scripts/check_db.sh              PostgreSQL (default)

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# ── Query helpers ─────────────────────────────────────────────────────────────

if ! docker ps --format '{{.Names}}' 2>/dev/null | grep -q "^axiaops-postgres$"; then
  echo "PostgreSQL is not running. Start it with: ./scripts/start.sh"
  exit 1
fi
q() { docker exec -i axiaops-postgres psql -U axiaops_owner -d axiaops -t --no-align \
        -c "SET search_path TO axiaops" -c "$1"; }
echo "Database: PostgreSQL (axiaops schema)"

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

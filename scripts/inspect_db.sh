#!/usr/bin/env bash
# inspect_db.sh — inspect the AxiaOps database
#
# Usage:
#   ./scripts/inspect_db.sh

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

# ── Accounts ──────────────────────────────────────────────────────────────────

echo "=== Accounts ==="
q "SELECT id, tenant_id, provider, label, region, status, last_scanned_at FROM accounts ORDER BY created_at;"

echo ""

# ── Cost Records ──────────────────────────────────────────────────────────────

echo "=== Cost Records ==="
q "SELECT COUNT(*) || ' records' FROM cost_records;"

echo ""

# ── Ghost Records ─────────────────────────────────────────────────────────────

echo "=== Ghost Records ==="
q "SELECT tenant_id, service, resource_id, monthly_cost, currency, reason FROM ghost_records ORDER BY monthly_cost DESC;"

echo ""

# ── Resource Records ──────────────────────────────────────────────────────────

echo "=== Resource Records ==="
q "SELECT tenant_id, service, resource_id, monthly_cost, is_ghost, currency FROM resource_records ORDER BY monthly_cost DESC;"

echo ""

# ── Ghost Snapshots ───────────────────────────────────────────────────────────

echo "=== Ghost Snapshots (latest 5 per tenant) ==="
q "SELECT tenant_id, account_id, snapshot_at, ghost_count, total_monthly_cost, currency
   FROM (
     SELECT *, ROW_NUMBER() OVER (PARTITION BY tenant_id ORDER BY snapshot_at DESC) AS rn
     FROM ghost_snapshots
   ) ranked WHERE rn <= 5 ORDER BY tenant_id, snapshot_at DESC;"

echo ""
echo "=== Ghost Snapshots Total Count ==="
q "SELECT tenant_id, COUNT(*) || ' snapshots' FROM ghost_snapshots GROUP BY tenant_id ORDER BY tenant_id;"

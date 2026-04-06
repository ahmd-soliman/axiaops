#!/usr/bin/env bash
# seed_test_data.sh — seed multi-tenant test data and verify RLS isolation
#
# Usage:
#   ./scripts/seed_test_data.sh
#
# Requires PostgreSQL running (start with: ./scripts/dev.sh)

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
INGESTION_DIR="$ROOT/services/ingestion"
DATABASE_URL="postgres://axiaops:axiaops@localhost:5432/axiaops"

# Use axiaops_owner for direct DB access — axiaops (app user) for application connections
psql_exec()  { docker exec -i -e "PGOPTIONS=-c search_path=axiaops" axiaops-postgres psql -U axiaops_owner -d axiaops --quiet -c "$1"; }
psql_query() { docker exec -i -e "PGOPTIONS=-c search_path=axiaops" axiaops-postgres psql -U axiaops_owner -d axiaops -t --no-align -c "$1"; }

# ── Check postgres is running ─────────────────────────────────────────────────

if ! docker ps --format '{{.Names}}' 2>/dev/null | grep -q "^axiaops-postgres$"; then
  echo "PostgreSQL is not running. Start it with: ./scripts/dev.sh"
  exit 1
fi

echo "=== AxiaOps — Seeding test data ==="
echo ""

# ── Create two test tenants ───────────────────────────────────────────────────

NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

echo "Creating dev tenant: AxiaOps"
psql_exec "INSERT INTO tenants (id, org_code, name, created_at)
  VALUES (gen_random_uuid()::text, 'org_fea50718baf', 'AxiaOps', '$NOW')
  ON CONFLICT (org_code) DO UPDATE SET name = EXCLUDED.name;"
DEV_TENANT_ID=$(psql_query "SELECT id FROM tenants WHERE org_code = 'org_fea50718baf';" | tr -d '[:space:]')
echo "  ID: $DEV_TENANT_ID"

echo "Creating tenant A: Acme Corp"
psql_exec "INSERT INTO tenants (id, org_code, name, created_at)
  VALUES (gen_random_uuid()::text, 'org_acme', 'Acme Corp', '$NOW')
  ON CONFLICT (org_code) DO UPDATE SET name = EXCLUDED.name;"
TENANT_A_ID=$(psql_query "SELECT id FROM tenants WHERE org_code = 'org_acme';" | tr -d '[:space:]')
echo "  ID: $TENANT_A_ID"

echo "Creating tenant B: Globex Inc"
psql_exec "INSERT INTO tenants (id, org_code, name, created_at)
  VALUES (gen_random_uuid()::text, 'org_globex', 'Globex Inc', '$NOW')
  ON CONFLICT (org_code) DO UPDATE SET name = EXCLUDED.name;"
TENANT_B_ID=$(psql_query "SELECT id FROM tenants WHERE org_code = 'org_globex';" | tr -d '[:space:]')
echo "  ID: $TENANT_B_ID"
echo ""

# ── Run ingestion for each tenant ─────────────────────────────────────────────

echo "Running ingestion for dev tenant (AxiaOps)..."
cd "$INGESTION_DIR"
TENANT_ID="$DEV_TENANT_ID" DATABASE_URL="$DATABASE_URL" DEV_MODE=true \
  go run ./cmd/main.go
echo "  done."

echo "Running ingestion for tenant A (Acme Corp)..."
TENANT_ID="$TENANT_A_ID" DATABASE_URL="$DATABASE_URL" DEV_MODE=true \
  go run ./cmd/main.go
echo "  done."

echo "Running ingestion for tenant B (Globex Inc)..."
TENANT_ID="$TENANT_B_ID" DATABASE_URL="$DATABASE_URL" DEV_MODE=true \
  go run ./cmd/main.go
echo "  done."
echo ""

# ── Verify RLS isolation ──────────────────────────────────────────────────────

echo "=== Verifying RLS isolation ==="
echo ""

# Use session-level SET (not SET LOCAL) — SET LOCAL only works inside a transaction
COUNT_A=$(psql_query "SET app.tenant_id = '$TENANT_A_ID'; SELECT COUNT(*) FROM ghost_records;")
echo "Tenant A ghost count (should be > 0): $COUNT_A"

COUNT_B=$(psql_query "SET app.tenant_id = '$TENANT_B_ID'; SELECT COUNT(*) FROM ghost_records;")
echo "Tenant B ghost count (should be > 0): $COUNT_B"

# As tenant A, try to read tenant B rows directly — RLS should return 0
CROSS=$(psql_query "SET app.tenant_id = '$TENANT_A_ID'; SELECT COUNT(*) FROM ghost_records WHERE tenant_id = '$TENANT_B_ID';")
echo "Tenant A sees tenant B rows (should be 0): $CROSS"

TOTAL=$(psql_query "SELECT COUNT(*) FROM ghost_records;")
echo "Total rows in DB (both tenants): $TOTAL"
echo ""

echo "=== Summary ==="
echo "Tenant A (Acme Corp):  $TENANT_A_ID"
echo "Tenant B (Globex Inc): $TENANT_B_ID"
echo ""
echo "To run the API as tenant A, add to ingestion .env:"
echo "  TENANT_ID=$TENANT_A_ID"
echo ""
echo "Logs: $ROOT/.dev.log"

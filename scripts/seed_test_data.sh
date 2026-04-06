#!/usr/bin/env bash
# seed_test_data.sh — seed multi-tenant test data and verify RLS isolation
#
# Usage:
#   ./scripts/seed_test_data.sh
#
# Requires PostgreSQL running (start with: ./scripts/dev.sh or docker compose up -d postgres)

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
INGESTION_DIR="$ROOT/services/ingestion"
DATABASE_URL="postgres://axiaops:axiaops@localhost:5432/axiaops"

PSQL="docker exec -i axiaops-postgres psql -U axiaops -d axiaops"

# ── Helpers ───────────────────────────────────────────────────────────────────

psql_exec() { echo "$1" | $PSQL --quiet; }
psql_query() { echo "$1" | $PSQL -t --no-align; }

# ── Check postgres is running ─────────────────────────────────────────────────

if ! docker ps --format '{{.Names}}' 2>/dev/null | grep -q "^axiaops-postgres$"; then
  echo "PostgreSQL is not running. Start it with:"
  echo "  ./scripts/dev.sh"
  exit 1
fi

echo "=== AxiaOps — Seeding test data ==="
echo ""

# ── Create two test tenants ───────────────────────────────────────────────────

TENANT_A_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
TENANT_B_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

echo "Creating tenant A: acme-corp  ($TENANT_A_ID)"
psql_exec "
  INSERT INTO tenants (id, org_code, name, created_at)
  VALUES ('$TENANT_A_ID', 'org_acme', 'Acme Corp', '$NOW')
  ON CONFLICT (org_code) DO UPDATE SET name = EXCLUDED.name
  RETURNING id;
"

echo "Creating tenant B: globex     ($TENANT_B_ID)"
psql_exec "
  INSERT INTO tenants (id, org_code, name, created_at)
  VALUES ('$TENANT_B_ID', 'org_globex', 'Globex Inc', '$NOW')
  ON CONFLICT (org_code) DO UPDATE SET name = EXCLUDED.name
  RETURNING id;
"
echo ""

# ── Run ingestion for tenant A ────────────────────────────────────────────────

echo "Running ingestion for tenant A (Acme Corp)..."
cd "$INGESTION_DIR"
set -a; [ -f .env ] && source .env; set +a
TENANT_ID="$TENANT_A_ID" DATABASE_URL="$DATABASE_URL" DEV_MODE=true \
  go run ./cmd/main.go >> "$ROOT/.dev.log" 2>&1
echo "  done."

# ── Run ingestion for tenant B ────────────────────────────────────────────────

echo "Running ingestion for tenant B (Globex Inc)..."
TENANT_ID="$TENANT_B_ID" DATABASE_URL="$DATABASE_URL" DEV_MODE=true \
  go run ./cmd/main.go >> "$ROOT/.dev.log" 2>&1
echo "  done."
echo ""

# ── Verify RLS isolation ──────────────────────────────────────────────────────

echo "=== Verifying RLS isolation ==="
echo ""

COUNT_A=$(psql_query "
  SET LOCAL app.tenant_id = '$TENANT_A_ID';
  SELECT COUNT(*) FROM ghost_records WHERE tenant_id = '$TENANT_A_ID';
")
echo "Tenant A ghosts (should be > 0): $COUNT_A"

COUNT_B=$(psql_query "
  SET LOCAL app.tenant_id = '$TENANT_B_ID';
  SELECT COUNT(*) FROM ghost_records WHERE tenant_id = '$TENANT_B_ID';
")
echo "Tenant B ghosts (should be > 0): $COUNT_B"

CROSS=$(psql_query "
  SET LOCAL app.tenant_id = '$TENANT_A_ID';
  SELECT COUNT(*) FROM ghost_records WHERE tenant_id = '$TENANT_B_ID';
")
echo "Tenant A can see tenant B rows (should be 0): $CROSS"
echo ""

TOTAL=$(psql_query "SELECT COUNT(*) FROM ghost_records;")
echo "Total ghost_records in DB: $TOTAL"
echo ""

echo "=== Summary ==="
echo "Tenant A ID: $TENANT_A_ID"
echo "Tenant B ID: $TENANT_B_ID"
echo ""
echo "To test the API as tenant A, set TENANT_ID=$TENANT_A_ID in your ingestion .env"
echo "Logs: $ROOT/.dev.log"

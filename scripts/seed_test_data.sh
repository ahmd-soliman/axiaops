#!/usr/bin/env bash
# seed_test_data.sh — seed dev tenant with dummy data for local development or remote servers
#
# Prerequisites:
#   Requires psql. If not installed:
#     brew install libpq
#     echo 'export PATH="/opt/homebrew/opt/libpq/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc
#
# Usage:
#   ./scripts/seed_test_data.sh                           # Local docker container
#   DATABASE_URL="postgres://..." ./scripts/seed_test_data.sh  # Remote postgres

#   - Dev
#   DATABASE_URL="postgres://axiaops_owner:axiaops_owner@192.168.1.100:5432/axiaops?sslmode=disable" ./scripts/seed_test_data.sh
#   - Staging
#   DATABASE_URL="postgres://axiaops_owner:axiaops_owner@192.168.1.100:5433/axiaops?sslmode=disable" ./scripts/seed_test_data.sh


#
# Supports both local (docker) and remote database connections.
# Safe to re-run — all inserts are idempotent (ON CONFLICT DO NOTHING / DO UPDATE).
#
# Dev tenant ID is fixed: dev-tenant-axiaops
# This matches DEV_TENANT_ID exported by dev.sh so the API resolves it without auth.

set -euo pipefail

# Default Tenant ID (can be overridden by environment variable)
TENANT_ID="${TENANT_ID:-dev-tenant-axiaops}"

# ── Determine connection mode (local docker or remote) ──────────────────────────

if [ -z "${DATABASE_URL:-}" ]; then
  # Local mode: use docker container
  MODE="docker"
  echo "DATABASE_URL not set — using local docker container (axiaops-postgres)"
else
  # Remote mode: use direct psql connection
  MODE="remote"
  echo "DATABASE_URL set — connecting to remote postgres"
fi

# Use axiaops_owner for direct DB access (bypasses RLS — owner privilege)
if [ "$MODE" = "docker" ]; then
  psql_exec()  { docker exec -i -e "PGOPTIONS=-c search_path=axiaops" axiaops-postgres psql -U axiaops_owner -d axiaops --quiet -c "$1"; }
  psql_query() { docker exec -i -e "PGOPTIONS=-c search_path=axiaops" axiaops-postgres psql -U axiaops_owner -d axiaops -t --no-align -c "$1"; }
else
  # Remote: parse DATABASE_URL and connect directly
  psql_exec()  { PGOPTIONS="-c search_path=axiaops" psql "$DATABASE_URL" --quiet -c "$1"; }
  psql_query() { PGOPTIONS="-c search_path=axiaops" psql "$DATABASE_URL" -t --no-align -c "$1"; }
fi

# ── Ensure postgres is running ────────────────────────────────────────────────

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

if [ "$MODE" = "docker" ]; then
  if ! docker ps --format '{{.Names}}' 2>/dev/null | grep -q "^axiaops-postgres$"; then
    echo "PostgreSQL not running — starting..."
    cd "$ROOT"
    docker compose up -d postgres
    echo -n "Waiting for PostgreSQL..."
    until docker exec axiaops-postgres pg_isready -U axiaops_owner -d axiaops &>/dev/null; do
      echo -n "."
      sleep 1
    done
    echo " Ready."
  fi
else
  echo -n "Waiting for remote PostgreSQL to be ready..."
  for i in {1..30}; do
    if psql "$DATABASE_URL" -c "SELECT 1" &>/dev/null; then
      echo " Ready."
      break
    fi
    echo -n "."
    sleep 2
  done
fi

echo "=== AxiaOps — Seeding dev data ==="
echo ""

NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
PERIOD_START=$(date -u -v-30d +"%Y-%m-%dT00:00:00Z" 2>/dev/null || date -u -d '30 days ago' +"%Y-%m-%dT00:00:00Z")
PERIOD_END="$NOW"

# ── Dev tenant (fixed ID used by DEV_TENANT_ID in dev.sh) ────────────────────

echo "Creating dev tenant (id: ${TENANT_ID})..."
psql_exec "INSERT INTO tenants (id, org_code, name, created_at)
  VALUES ('${TENANT_ID}', 'org_dev_local', 'AxiaOps Dev', '$NOW')
  ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name;"
echo "  Done."

# ── Additional tenants for RLS isolation testing ──────────────────────────────

echo "Creating tenant: Acme Corp..."
psql_exec "INSERT INTO tenants (id, org_code, name, created_at)
  VALUES (gen_random_uuid()::text, 'org_acme', 'Acme Corp', '$NOW')
  ON CONFLICT (org_code) DO NOTHING;"

echo "Creating tenant: Globex Inc..."
psql_exec "INSERT INTO tenants (id, org_code, name, created_at)
  VALUES (gen_random_uuid()::text, 'org_globex', 'Globex Inc', '$NOW')
  ON CONFLICT (org_code) DO NOTHING;"
echo ""

# ── Seed AWS account for the dev tenant ──────────────────────────────────────
# secret_encrypted is a placeholder — scanning this account will fail gracefully.
# It exists so the dashboard shows a connected account instead of the "connect" screen.

echo "Creating dev AWS account..."
psql_exec "INSERT INTO accounts (id, tenant_id, provider, label, access_key_id, secret_encrypted, region, status, created_at)
  VALUES ('dev-account-001', '${TENANT_ID}', 'aws', 'Dev AWS (seed data)', 'AKIAIOSFODNN7EXAMPLE', '', 'eu-central-1', 'connected', '$NOW')
  ON CONFLICT (id) DO NOTHING;"
echo "  Done."
echo ""

# ── Ghost records — 5 zombie resources ───────────────────────────────────────

echo "Inserting ghost records..."
psql_exec "DELETE FROM ghost_records WHERE tenant_id = '${TENANT_ID}';"

psql_exec "INSERT INTO ghost_records
  (tenant_id, provider, account_id, service, region, resource_id, tags, monthly_cost, currency,
   period_start, period_end, usage_metric, usage_avg, usage_unit, reason, owner, detected_at)
VALUES
  -- Idle EC2 instance
  ('${TENANT_ID}', 'aws', '123456789012', 'AmazonEC2', 'eu-central-1',
   'i-0abc123dev0001', '{\"env\":\"staging\",\"team\":\"backend\"}',
   45.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 1.2, 'Percent',
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  -- Abandoned RDS instance
  ('${TENANT_ID}', 'aws', '123456789012', 'AmazonRDS', 'eu-central-1',
   'db-dev-abandoned', '{\"env\":\"dev\",\"team\":\"data\"}',
   89.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count',
   'Zero connections — likely abandoned', 'data', '$NOW'),

  -- Unused Lambda
  ('${TENANT_ID}', 'aws', '123456789012', 'AWSLambda', 'eu-west-1',
   'unused-email-sender', '{\"env\":\"prod\",\"team\":\"backend\"}',
   2.30, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count',
   'Zero invocations — likely unused', 'backend', '$NOW'),

  -- Unused load balancer
  ('${TENANT_ID}', 'aws', '123456789012', 'AmazonElasticLoadBalancing', 'eu-central-1',
   'app/legacy-api/abc123dev456', '{\"env\":\"staging\",\"team\":\"platform\"}',
   18.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RequestCount', 0, 'Count',
   'Zero requests — likely abandoned', 'platform', '$NOW'),

  -- Unattached Elastic IP
  ('${TENANT_ID}', 'aws', '123456789012', 'AmazonVPC', 'eu-west-1',
   'eipalloc-dev00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count',
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW')
;"
echo "  Inserted 5 ghost records."
echo ""

# ── Resource records — ghosts + active resources ──────────────────────────────

echo "Inserting resource records..."
psql_exec "DELETE FROM resource_records WHERE tenant_id = '${TENANT_ID}';"

psql_exec "INSERT INTO resource_records
  (tenant_id, provider, account_id, service, region, resource_id, tags, monthly_cost, currency,
   period_start, period_end, usage_metric, usage_avg, usage_unit, is_ghost, reason, owner, detected_at)
VALUES
  -- Ghost: idle EC2
  ('${TENANT_ID}', 'aws', '123456789012', 'AmazonEC2', 'eu-central-1',
   'i-0abc123dev0001', '{\"env\":\"staging\",\"team\":\"backend\"}',
   45.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 1.2, 'Percent', true,
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  -- Ghost: abandoned RDS
  ('${TENANT_ID}', 'aws', '123456789012', 'AmazonRDS', 'eu-central-1',
   'db-dev-abandoned', '{\"env\":\"dev\",\"team\":\"data\"}',
   89.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count', true,
   'Zero connections — likely abandoned', 'data', '$NOW'),

  -- Ghost: unused Lambda
  ('${TENANT_ID}', 'aws', '123456789012', 'AWSLambda', 'eu-west-1',
   'unused-email-sender', '{\"env\":\"prod\",\"team\":\"backend\"}',
   2.30, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count', true,
   'Zero invocations — likely unused', 'backend', '$NOW'),

  -- Ghost: unused ELB
  ('${TENANT_ID}', 'aws', '123456789012', 'AmazonElasticLoadBalancing', 'eu-central-1',
   'app/legacy-api/abc123dev456', '{\"env\":\"staging\",\"team\":\"platform\"}',
   18.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RequestCount', 0, 'Count', true,
   'Zero requests — likely abandoned', 'platform', '$NOW'),

  -- Ghost: unattached EIP
  ('${TENANT_ID}', 'aws', '123456789012', 'AmazonVPC', 'eu-west-1',
   'eipalloc-dev00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count', true,
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Active: healthy EC2
  ('${TENANT_ID}', 'aws', '123456789012', 'AmazonEC2', 'eu-central-1',
   'i-0abc123dev0099', '{\"env\":\"prod\",\"team\":\"backend\"}',
   45.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 67.3, 'Percent', false, '', 'backend', '$NOW'),

  -- Active: healthy RDS
  ('${TENANT_ID}', 'aws', '123456789012', 'AmazonRDS', 'eu-central-1',
   'db-production-main', '{\"env\":\"prod\",\"team\":\"data\"}',
   156.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 142, 'Count', false, '', 'data', '$NOW')
;"
echo "  Inserted 7 resource records (5 ghosts, 2 active)."
echo ""

# ── Ghost snapshots — 1000 days of historical trend data ─────────────────────────
# Creates 1000 snapshots simulating daily scans over ~4 months, with realistic
# savings variation (upward trend as resources are optimized, with noise).

echo "Inserting 1000 ghost snapshots (1000 days of trend data)..."

# Build a single INSERT with 1000 rows (one per day going back 1000 days)
SNAP_INSERT="INSERT INTO ghost_snapshots (id, tenant_id, account_id, snapshot_at, ghost_count, total_monthly_cost, currency) VALUES"

for i in {1000..1}; do
  # Calculate date i days ago
  SNAP_DATE=$(date -u -v-${i}d +"%Y-%m-%dT12:00:00Z" 2>/dev/null || TZ=UTC date -d "$i days ago" +"%Y-%m-%dT12:00:00Z" 2>/dev/null)

  # Generate completely random cost between $100 and $500
  BASE_COST=$(awk -v seed=$RANDOM "BEGIN {srand(seed); printf \"%.2f\", 100 + (rand() * 400)}")
  GHOSTS=$((8 + RANDOM % 5))  # ghost count 8-13

  if [ $i -eq 1 ]; then
    SNAP_INSERT="$SNAP_INSERT (gen_random_uuid()::text, '${TENANT_ID}', 'dev-account-001', '$SNAP_DATE', $GHOSTS, $BASE_COST, 'USD')"
  else
    SNAP_INSERT="$SNAP_INSERT (gen_random_uuid()::text, '${TENANT_ID}', 'dev-account-001', '$SNAP_DATE', $GHOSTS, $BASE_COST, 'USD'),"
  fi
done

SNAP_INSERT="$SNAP_INSERT ON CONFLICT DO NOTHING;"

psql_exec "$SNAP_INSERT"
echo "  Inserted 1000 ghost snapshots (1000-day trend from \$280 → \$159/month)."
echo ""

# ── RLS isolation check (using app user, not owner) ───────────────────────────

echo "=== Verifying dev tenant data ==="
GHOST_COUNT=$(psql_query "SELECT COUNT(*) FROM ghost_records WHERE tenant_id = '${TENANT_ID}';")
RESOURCE_COUNT=$(psql_query "SELECT COUNT(*) FROM resource_records WHERE tenant_id = '${TENANT_ID}';")
SNAPSHOT_COUNT=$(psql_query "SELECT COUNT(*) FROM ghost_snapshots WHERE tenant_id = '${TENANT_ID}';")
echo "Dev tenant ghost records:     $GHOST_COUNT"
echo "Dev tenant resource records:  $RESOURCE_COUNT"
echo "Dev tenant ghost snapshots:   $SNAPSHOT_COUNT"
echo ""

echo "=== Done ==="
echo "Dev tenant ID: ${TENANT_ID}"
echo "DEV_TENANT_ID=${TENANT_ID} is set automatically by dev.sh"
echo ""
echo "Workflow:"
echo "  make start   — start all services (dev mode, no auth)"
echo "  make seed    — (re-)populate dummy data"
echo "  open http://localhost:3000"
echo "  make start-dev   — start all services (dev mode, no auth)"
echo "  make seed        — (re-)populate dummy data"
echo "  open http://<host>:<port>"
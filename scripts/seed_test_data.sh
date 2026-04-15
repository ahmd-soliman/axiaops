#!/usr/bin/env bash
# seed_test_data.sh — seed database with test data
# Usage: ./seed_test_data.sh [dev|staging]

set -euo pipefail

ENV=${1:-dev}

# Check if DATABASE_URL is set, use default for local dev
if [[ -z "${DATABASE_URL:-}" ]]; then
  DATABASE_URL="postgres://axiaops_owner:axiaops_owner@localhost:5432/axiaops?sslmode=disable"
  echo "Using default DATABASE_URL for local development"
fi

echo "DATABASE_URL set — seeding $ENV environment"
# Safe to re-run — all inserts are idempotent (ON CONFLICT DO NOTHING / DO UPDATE).
#
# Dev tenant ID is fixed: $TENANT_ID
# This matches DEV_TENANT_ID exported by dev.sh so the API resolves it without auth.

set -euo pipefail

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

echo "=== AxiaOps — Seeding $ENV data ==="
echo ""

NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
PERIOD_START=$(date -u -v-30d +"%Y-%m-%dT00:00:00Z" 2>/dev/null || date -u -d '30 days ago' +"%Y-%m-%dT00:00:00Z")
PERIOD_END="$NOW"

# Set tenant ID based on environment
if [ "$ENV" = "staging" ]; then
  TENANT_ID="staging-tenant-axiaops"
  ORG_CODE="org_staging"
  TENANT_NAME="AxiaOps Staging"
else
  TENANT_ID="dev-tenant-axiaops"
  ORG_CODE="org_dev_local"
  TENANT_NAME="AxiaOps Dev"
fi

# ── Create tenant ────────────────────────────────────────────────────────────

echo "Creating $ENV tenant (id: $TENANT_ID)..."
psql_exec "INSERT INTO tenants (id, org_code, name, created_at)
  VALUES ('$TENANT_ID', '$ORG_CODE', '$TENANT_NAME', '$NOW')
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

# ── Seed AWS accounts for the dev tenant ──────────────────────────────────────
# secret_encrypted is a placeholder — scanning these accounts will fail gracefully.
# They exist so the dashboard shows connected accounts instead of the "connect" screen.

echo "Creating dev AWS accounts..."
psql_exec "INSERT INTO accounts (id, tenant_id, provider, label, access_key_id, secret_encrypted, region, status, created_at)
  VALUES 
    ('dev-account-001', '$TENANT_ID', 'aws', 'Production AWS', 'AKIAIOSFODNN7EXAMPLE', '', 'eu-central-1', 'connected', '$NOW'),
    ('dev-account-002', '$TENANT_ID', 'aws', 'Staging AWS', 'AKIAI2EXAMPLE2STAGE', '', 'us-east-1', 'connected', '$NOW'),
    ('dev-account-003', '$TENANT_ID', 'aws', 'Development AWS', 'AKIAI3EXAMPLE3DEVLP', '', 'eu-west-1', 'connected', '$NOW')
  ON CONFLICT (id) DO NOTHING;"
echo "  Created 3 AWS accounts."
echo ""

# ── Ghost records — distributed across 3 accounts ───────────────────────────────────────

echo "Inserting ghost records across 3 accounts..."
psql_exec "DELETE FROM ghost_records WHERE tenant_id = '$TENANT_ID';"

psql_exec "INSERT INTO ghost_records
  (tenant_id, provider, account_id, service, region, resource_id, tags, monthly_cost, currency,
   period_start, period_end, usage_metric, usage_avg, usage_unit, reason, owner, detected_at)
VALUES
  -- Account 1 (Production) - 2 ghosts
  ('$TENANT_ID', 'aws', 'dev-account-001', 'AmazonEC2', 'eu-central-1',
   'i-0abc123prod001', '{\"env\":\"production\",\"team\":\"backend\"}',
   45.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 1.2, 'Percent',
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  ('$TENANT_ID', 'aws', 'dev-account-001', 'AmazonRDS', 'eu-central-1',
   'db-prod-legacy', '{\"env\":\"production\",\"team\":\"data\"}',
   156.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count',
   'Zero connections — likely abandoned', 'data', '$NOW'),

  -- Account 2 (Staging) - 2 ghosts
  ('$TENANT_ID', 'aws', 'dev-account-002', 'AWSLambda', 'us-east-1',
   'staging-email-sender', '{\"env\":\"staging\",\"team\":\"backend\"}',
   2.30, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count',
   'Zero invocations — likely unused', 'backend', '$NOW'),

  ('$TENANT_ID', 'aws', 'dev-account-002', 'AmazonElasticLoadBalancing', 'us-east-1',
   'app/staging-api/xyz789', '{\"env\":\"staging\",\"team\":\"platform\"}',
   18.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RequestCount', 0, 'Count',
   'Zero requests — likely abandoned', 'platform', '$NOW'),

  -- Account 3 (Development) - 1 ghost
  ('$TENANT_ID', 'aws', 'dev-account-003', 'AmazonVPC', 'eu-west-1',
   'eipalloc-dev00001', '{\"env\":\"dev\",\"team\":\"platform\"}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count',
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'platform', '$NOW')
;"
echo "  Inserted 5 ghost records across 3 accounts."
echo ""

# ── Resource records — ghosts + active resources across 3 accounts ──────────────────────────────

echo "Inserting resource records across 3 accounts..."
psql_exec "DELETE FROM resource_records WHERE tenant_id = '$TENANT_ID';"

psql_exec "INSERT INTO resource_records
  (tenant_id, provider, account_id, service, region, resource_id, tags, monthly_cost, currency,
   period_start, period_end, usage_metric, usage_avg, usage_unit, is_ghost, reason, owner, detected_at)
VALUES
  -- Account 1 (Production) - 2 ghosts + 2 active
  ('$TENANT_ID', 'aws', 'dev-account-001', 'AmazonEC2', 'eu-central-1',
   'i-0abc123prod001', '{\"env\":\"production\",\"team\":\"backend\"}',
   45.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 1.2, 'Percent', true,
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  ('$TENANT_ID', 'aws', 'dev-account-001', 'AmazonRDS', 'eu-central-1',
   'db-prod-legacy', '{\"env\":\"production\",\"team\":\"data\"}',
   156.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count', true,
   'Zero connections — likely abandoned', 'data', '$NOW'),

  ('$TENANT_ID', 'aws', 'dev-account-001', 'AmazonEC2', 'eu-central-1',
   'i-0abc123prod099', '{\"env\":\"production\",\"team\":\"backend\"}',
   89.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 67.3, 'Percent', false, '', 'backend', '$NOW'),

  ('$TENANT_ID', 'aws', 'dev-account-001', 'AmazonRDS', 'eu-central-1',
   'db-production-main', '{\"env\":\"production\",\"team\":\"data\"}',
   234.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 142, 'Count', false, '', 'data', '$NOW'),

  -- Account 2 (Staging) - 2 ghosts + 1 active
  ('$TENANT_ID', 'aws', 'dev-account-002', 'AWSLambda', 'us-east-1',
   'staging-email-sender', '{\"env\":\"staging\",\"team\":\"backend\"}',
   2.30, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count', true,
   'Zero invocations — likely unused', 'backend', '$NOW'),

  ('$TENANT_ID', 'aws', 'dev-account-002', 'AmazonElasticLoadBalancing', 'us-east-1',
   'app/staging-api/xyz789', '{\"env\":\"staging\",\"team\":\"platform\"}',
   18.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RequestCount', 0, 'Count', true,
   'Zero requests — likely abandoned', 'platform', '$NOW'),

  ('$TENANT_ID', 'aws', 'dev-account-002', 'AmazonEC2', 'us-east-1',
   'i-0staging001', '{\"env\":\"staging\",\"team\":\"backend\"}',
   32.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 45.8, 'Percent', false, '', 'backend', '$NOW'),

  -- Account 3 (Development) - 1 ghost + 1 active
  ('$TENANT_ID', 'aws', 'dev-account-003', 'AmazonVPC', 'eu-west-1',
   'eipalloc-dev00001', '{\"env\":\"dev\",\"team\":\"platform\"}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count', true,
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'platform', '$NOW'),

  ('$TENANT_ID', 'aws', 'dev-account-003', 'AmazonEC2', 'eu-west-1',
   'i-0dev001', '{\"env\":\"dev\",\"team\":\"backend\"}',
   22.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 23.1, 'Percent', false, '', 'backend', '$NOW')
;"
echo "  Inserted 9 resource records across 3 accounts (5 ghosts, 4 active)."
echo ""

# ── Ghost snapshots — trend data for all 3 accounts ─────────────────────────────
# Creates snapshots for each account simulating daily scans over ~30 days

echo "Inserting ghost snapshots for all 3 accounts (30 days of trend data)..."

# Build INSERT for all 3 accounts with 30 days of data each
SNAP_INSERT="INSERT INTO ghost_snapshots (id, tenant_id, account_id, snapshot_at, ghost_count, total_monthly_cost, currency) VALUES"

ACCOUNTS=("dev-account-001" "dev-account-002" "dev-account-003")
ACCOUNT_COSTS=(202.40 20.80 3.60)  # Production, Staging, Development

for account_idx in {0..2}; do
  ACCOUNT_ID="${ACCOUNTS[$account_idx]}"
  BASE_COST="${ACCOUNT_COSTS[$account_idx]}"
  
  for i in {30..1}; do
    # Calculate date i days ago
    SNAP_DATE=$(date -u -v-${i}d +"%Y-%m-%dT12:00:00Z" 2>/dev/null || TZ=UTC date -d "$i days ago" +"%Y-%m-%dT12:00:00Z" 2>/dev/null)

    # Add some variation to the base cost (±20%)
    VARIATION=$(awk -v seed=$((RANDOM + account_idx * 1000 + i)) "BEGIN {srand(seed); printf \"%.2f\", -0.2 + (rand() * 0.4)}")
    COST=$(awk -v base="$BASE_COST" -v var="$VARIATION" "BEGIN {printf \"%.2f\", base * (1 + var)}")
    
    # Ghost count varies by account
    case $account_idx in
      0) GHOSTS=$((2 + RANDOM % 2));;  # Production: 2-3 ghosts
      1) GHOSTS=$((2 + RANDOM % 2));;  # Staging: 2-3 ghosts  
      2) GHOSTS=$((1 + RANDOM % 2));;  # Development: 1-2 ghosts
    esac

    if [ $account_idx -eq 2 ] && [ $i -eq 1 ]; then
      # Last entry, no comma
      SNAP_INSERT="$SNAP_INSERT (gen_random_uuid()::text, '$TENANT_ID', '$ACCOUNT_ID', '$SNAP_DATE', $GHOSTS, $COST, 'USD')"
    else
      SNAP_INSERT="$SNAP_INSERT (gen_random_uuid()::text, '$TENANT_ID', '$ACCOUNT_ID', '$SNAP_DATE', $GHOSTS, $COST, 'USD'),"
    fi
  done
done

SNAP_INSERT="$SNAP_INSERT ON CONFLICT DO NOTHING;"

psql_exec "$SNAP_INSERT"
echo "  Inserted 90 ghost snapshots (30 days × 3 accounts)."
echo ""

# ── RLS isolation check (using app user, not owner) ───────────────────────────

echo "=== Verifying dev tenant data ==="
GHOST_COUNT=$(psql_query "SELECT COUNT(*) FROM ghost_records WHERE tenant_id = '$TENANT_ID';")
RESOURCE_COUNT=$(psql_query "SELECT COUNT(*) FROM resource_records WHERE tenant_id = '$TENANT_ID';")
SNAPSHOT_COUNT=$(psql_query "SELECT COUNT(*) FROM ghost_snapshots WHERE tenant_id = '$TENANT_ID';")
ACCOUNT_COUNT=$(psql_query "SELECT COUNT(*) FROM accounts WHERE tenant_id = '$TENANT_ID';")

echo "Dev tenant accounts:          $ACCOUNT_COUNT"
echo "Dev tenant ghost records:     $GHOST_COUNT"
echo "Dev tenant resource records:  $RESOURCE_COUNT"
echo "Dev tenant ghost snapshots:   $SNAPSHOT_COUNT"
echo ""

echo "Per-account breakdown:"
for account_id in "dev-account-001" "dev-account-002" "dev-account-003"; do
  ACCOUNT_GHOSTS=$(psql_query "SELECT COUNT(*) FROM ghost_records WHERE tenant_id = '$TENANT_ID' AND account_id = '$account_id';")
  ACCOUNT_RESOURCES=$(psql_query "SELECT COUNT(*) FROM resource_records WHERE tenant_id = '$TENANT_ID' AND account_id = '$account_id';")
  ACCOUNT_LABEL=$(psql_query "SELECT label FROM accounts WHERE id = '$account_id';")
  echo "  $ACCOUNT_LABEL ($account_id): $ACCOUNT_GHOSTS ghosts, $ACCOUNT_RESOURCES resources"
done
echo ""

echo "=== Done ==="
echo "Dev tenant ID: $TENANT_ID"
echo "DEV_TENANT_ID=$TENANT_ID is set automatically by dev.sh"
echo ""
echo "Workflow:"
echo "  make start-dev   — start all services (dev mode, no auth)"
echo "  make seed        — (re-)populate dummy data"
echo "  open http://<host>:<port>"

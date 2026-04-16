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
  ON CONFLICT (org_code) DO UPDATE SET name = EXCLUDED.name;"
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

# ── Seed AWS accounts for the dev tenant ─────────────────────────────────────
# secret_encrypted is a placeholder — scanning these accounts will fail gracefully.
# They exist so the dashboard shows connected accounts instead of the "connect" screen.

echo "Creating dev AWS accounts..."
psql_exec "INSERT INTO accounts (id, tenant_id, provider, label, access_key_id, secret_encrypted, region, status, created_at)
  VALUES
    ('dev-account-001', '${TENANT_ID}', 'aws', 'Production AWS',   'AKIAIOSFODNN7EXAMPLE', '', 'eu-central-1', 'connected', '$NOW'),
    ('dev-account-002', '${TENANT_ID}', 'aws', 'Staging AWS',      'AKIAI44QH8DHBEXAMPLE', '', 'us-east-1',    'connected', '$NOW'),
    ('dev-account-003', '${TENANT_ID}', 'aws', 'Development AWS',  'AKIAJ5RKEXAMPLEDEVEX', '', 'eu-west-1',    'connected', '$NOW')
  ON CONFLICT (id) DO NOTHING;"
echo "  Done."
echo ""

# ── Ghost records — zombie resources spread across all 3 accounts ─────────────

echo "Inserting ghost records..."
psql_exec "DELETE FROM ghost_records WHERE tenant_id = '${TENANT_ID}';"

psql_exec "INSERT INTO ghost_records
  (tenant_id, provider, account_id, service, region, resource_id, tags, monthly_cost, currency,
   period_start, period_end, usage_metric, usage_avg, usage_unit, reason, owner, detected_at)
VALUES
  -- Production (dev-account-001) ghosts
  ('${TENANT_ID}', 'aws', 'dev-account-001', 'AmazonEC2', 'eu-central-1',
   'i-0abc123prod0001', '{\"env\":\"prod\",\"team\":\"backend\"}',
   45.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 1.2, 'Percent',
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  ('${TENANT_ID}', 'aws', 'dev-account-001', 'AmazonRDS', 'eu-central-1',
   'db-prod-legacy-reporting', '{\"env\":\"prod\",\"team\":\"data\"}',
   210.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count',
   'Zero connections — likely abandoned', 'data', '$NOW'),

  ('${TENANT_ID}', 'aws', 'dev-account-001', 'AmazonElasticLoadBalancing', 'eu-central-1',
   'app/legacy-api/abc123prod', '{\"env\":\"prod\",\"team\":\"platform\"}',
   18.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RequestCount', 0, 'Count',
   'Zero requests — likely abandoned', 'platform', '$NOW'),

  ('${TENANT_ID}', 'aws', 'dev-account-001', 'AmazonVPC', 'eu-central-1',
   'eipalloc-prod00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count',
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Staging (dev-account-002) ghosts
  ('${TENANT_ID}', 'aws', 'dev-account-002', 'AmazonEC2', 'us-east-1',
   'i-0abc123stg0001', '{\"env\":\"staging\",\"team\":\"backend\"}',
   38.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 0.8, 'Percent',
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  ('${TENANT_ID}', 'aws', 'dev-account-002', 'AmazonEC2', 'us-east-1',
   'i-0abc123stg0002', '{\"env\":\"staging\",\"team\":\"platform\"}',
   38.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 2.1, 'Percent',
   'Instance CPU below 5% — likely idle', 'platform', '$NOW'),

  ('${TENANT_ID}', 'aws', 'dev-account-002', 'AWSLambda', 'us-east-1',
   'stg-image-resizer', '{\"env\":\"staging\",\"team\":\"backend\"}',
   4.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count',
   'Zero invocations — likely unused', 'backend', '$NOW'),

  ('${TENANT_ID}', 'aws', 'dev-account-002', 'AmazonVPC', 'us-east-1',
   'eipalloc-stg00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count',
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Development (dev-account-003) ghosts
  ('${TENANT_ID}', 'aws', 'dev-account-003', 'AmazonEC2', 'eu-west-1',
   'i-0abc123dev0001', '{\"env\":\"dev\",\"team\":\"backend\"}',
   22.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 0.3, 'Percent',
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  ('${TENANT_ID}', 'aws', 'dev-account-003', 'AmazonRDS', 'eu-west-1',
   'db-dev-abandoned', '{\"env\":\"dev\",\"team\":\"data\"}',
   89.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count',
   'Zero connections — likely abandoned', 'data', '$NOW'),

  ('${TENANT_ID}', 'aws', 'dev-account-003', 'AWSLambda', 'eu-west-1',
   'dev-unused-email-sender', '{\"env\":\"dev\",\"team\":\"backend\"}',
   2.30, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count',
   'Zero invocations — likely unused', 'backend', '$NOW'),

  ('${TENANT_ID}', 'aws', 'dev-account-003', 'AmazonVPC', 'eu-west-1',
   'eipalloc-dev00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count',
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW')
;"
echo "  Inserted 12 ghost records (4 prod, 4 staging, 4 dev)."
echo ""

# ── Resource records — ghosts + active resources across all 3 accounts ────────

echo "Inserting resource records..."
psql_exec "DELETE FROM resource_records WHERE tenant_id = '${TENANT_ID}';"

psql_exec "INSERT INTO resource_records
  (tenant_id, provider, account_id, service, region, resource_id, tags, monthly_cost, currency,
   period_start, period_end, usage_metric, usage_avg, usage_unit, is_ghost, reason, owner, detected_at)
VALUES
  -- ── Production (dev-account-001) ──────────────────────────────────────────
  -- Ghost: idle EC2
  ('${TENANT_ID}', 'aws', 'dev-account-001', 'AmazonEC2', 'eu-central-1',
   'i-0abc123prod0001', '{\"env\":\"prod\",\"team\":\"backend\"}',
   45.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 1.2, 'Percent', true,
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  -- Ghost: abandoned RDS
  ('${TENANT_ID}', 'aws', 'dev-account-001', 'AmazonRDS', 'eu-central-1',
   'db-prod-legacy-reporting', '{\"env\":\"prod\",\"team\":\"data\"}',
   210.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count', true,
   'Zero connections — likely abandoned', 'data', '$NOW'),

  -- Ghost: unused ELB
  ('${TENANT_ID}', 'aws', 'dev-account-001', 'AmazonElasticLoadBalancing', 'eu-central-1',
   'app/legacy-api/abc123prod', '{\"env\":\"prod\",\"team\":\"platform\"}',
   18.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RequestCount', 0, 'Count', true,
   'Zero requests — likely abandoned', 'platform', '$NOW'),

  -- Ghost: unattached EIP
  ('${TENANT_ID}', 'aws', 'dev-account-001', 'AmazonVPC', 'eu-central-1',
   'eipalloc-prod00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count', true,
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Active: healthy EC2
  ('${TENANT_ID}', 'aws', 'dev-account-001', 'AmazonEC2', 'eu-central-1',
   'i-0abc123prod0099', '{\"env\":\"prod\",\"team\":\"backend\"}',
   182.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 71.5, 'Percent', false, '', 'backend', '$NOW'),

  -- Active: healthy RDS
  ('${TENANT_ID}', 'aws', 'dev-account-001', 'AmazonRDS', 'eu-central-1',
   'db-production-main', '{\"env\":\"prod\",\"team\":\"data\"}',
   312.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 284, 'Count', false, '', 'data', '$NOW'),

  -- Active: healthy ELB
  ('${TENANT_ID}', 'aws', 'dev-account-001', 'AmazonElasticLoadBalancing', 'eu-central-1',
   'app/prod-api/xyz789prod', '{\"env\":\"prod\",\"team\":\"platform\"}',
   24.30, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RequestCount', 94200, 'Count', false, '', 'platform', '$NOW'),

  -- ── Staging (dev-account-002) ─────────────────────────────────────────────
  -- Ghost: idle EC2 #1
  ('${TENANT_ID}', 'aws', 'dev-account-002', 'AmazonEC2', 'us-east-1',
   'i-0abc123stg0001', '{\"env\":\"staging\",\"team\":\"backend\"}',
   38.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 0.8, 'Percent', true,
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  -- Ghost: idle EC2 #2
  ('${TENANT_ID}', 'aws', 'dev-account-002', 'AmazonEC2', 'us-east-1',
   'i-0abc123stg0002', '{\"env\":\"staging\",\"team\":\"platform\"}',
   38.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 2.1, 'Percent', true,
   'Instance CPU below 5% — likely idle', 'platform', '$NOW'),

  -- Ghost: unused Lambda
  ('${TENANT_ID}', 'aws', 'dev-account-002', 'AWSLambda', 'us-east-1',
   'stg-image-resizer', '{\"env\":\"staging\",\"team\":\"backend\"}',
   4.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count', true,
   'Zero invocations — likely unused', 'backend', '$NOW'),

  -- Ghost: unattached EIP
  ('${TENANT_ID}', 'aws', 'dev-account-002', 'AmazonVPC', 'us-east-1',
   'eipalloc-stg00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count', true,
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Active: healthy EC2
  ('${TENANT_ID}', 'aws', 'dev-account-002', 'AmazonEC2', 'us-east-1',
   'i-0abc123stg0099', '{\"env\":\"staging\",\"team\":\"backend\"}',
   76.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 48.2, 'Percent', false, '', 'backend', '$NOW'),

  -- Active: healthy RDS
  ('${TENANT_ID}', 'aws', 'dev-account-002', 'AmazonRDS', 'us-east-1',
   'db-staging-main', '{\"env\":\"staging\",\"team\":\"data\"}',
   98.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 37, 'Count', false, '', 'data', '$NOW'),

  -- ── Development (dev-account-003) ────────────────────────────────────────
  -- Ghost: idle EC2
  ('${TENANT_ID}', 'aws', 'dev-account-003', 'AmazonEC2', 'eu-west-1',
   'i-0abc123dev0001', '{\"env\":\"dev\",\"team\":\"backend\"}',
   22.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 0.3, 'Percent', true,
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  -- Ghost: abandoned RDS
  ('${TENANT_ID}', 'aws', 'dev-account-003', 'AmazonRDS', 'eu-west-1',
   'db-dev-abandoned', '{\"env\":\"dev\",\"team\":\"data\"}',
   89.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count', true,
   'Zero connections — likely abandoned', 'data', '$NOW'),

  -- Ghost: unused Lambda
  ('${TENANT_ID}', 'aws', 'dev-account-003', 'AWSLambda', 'eu-west-1',
   'dev-unused-email-sender', '{\"env\":\"dev\",\"team\":\"backend\"}',
   2.30, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count', true,
   'Zero invocations — likely unused', 'backend', '$NOW'),

  -- Ghost: unattached EIP
  ('${TENANT_ID}', 'aws', 'dev-account-003', 'AmazonVPC', 'eu-west-1',
   'eipalloc-dev00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count', true,
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Active: healthy EC2
  ('${TENANT_ID}', 'aws', 'dev-account-003', 'AmazonEC2', 'eu-west-1',
   'i-0abc123dev0099', '{\"env\":\"dev\",\"team\":\"backend\"}',
   22.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 34.7, 'Percent', false, '', 'backend', '$NOW'),

  -- Active: healthy Lambda
  ('${TENANT_ID}', 'aws', 'dev-account-003', 'AWSLambda', 'eu-west-1',
   'dev-auth-handler', '{\"env\":\"dev\",\"team\":\"backend\"}',
   1.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 1840, 'Count', false, '', 'backend', '$NOW')
;"
echo "  Inserted 19 resource records (12 ghosts, 7 active) across 3 accounts."
echo ""

# ── Ghost snapshots — 1000 days of historical trend data per account ──────────
# Creates 1000 snapshots per account simulating daily scans with realistic variation.

echo "Inserting ghost snapshots (1000 days × 3 accounts)..."

for ACCT_ID in dev-account-001 dev-account-002 dev-account-003; do
  SNAP_INSERT="INSERT INTO ghost_snapshots (id, tenant_id, account_id, snapshot_at, ghost_count, total_monthly_cost, currency) VALUES"

  for i in {1000..1}; do
    SNAP_DATE=$(date -u -v-${i}d +"%Y-%m-%dT12:00:00Z" 2>/dev/null || TZ=UTC date -d "$i days ago" +"%Y-%m-%dT12:00:00Z" 2>/dev/null)
    BASE_COST=$(awk -v seed=$RANDOM "BEGIN {srand(seed); printf \"%.2f\", 100 + (rand() * 400)}")
    GHOSTS=$((4 + RANDOM % 5))

    if [ $i -eq 1 ]; then
      SNAP_INSERT="$SNAP_INSERT (gen_random_uuid()::text, '${TENANT_ID}', '$ACCT_ID', '$SNAP_DATE', $GHOSTS, $BASE_COST, 'USD')"
    else
      SNAP_INSERT="$SNAP_INSERT (gen_random_uuid()::text, '${TENANT_ID}', '$ACCT_ID', '$SNAP_DATE', $GHOSTS, $BASE_COST, 'USD'),"
    fi
  done

  SNAP_INSERT="$SNAP_INSERT ON CONFLICT DO NOTHING;"
  psql_exec "$SNAP_INSERT"
  echo "  Inserted 1000 snapshots for $ACCT_ID."
done
echo ""

# ── RLS isolation check (using app user, not owner) ───────────────────────────

echo "=== Verifying dev tenant data ==="
GHOST_COUNT=$(psql_query "SELECT COUNT(*) FROM ghost_records WHERE tenant_id = '${TENANT_ID}';")
RESOURCE_COUNT=$(psql_query "SELECT COUNT(*) FROM resource_records WHERE tenant_id = '${TENANT_ID}';")
SNAPSHOT_COUNT=$(psql_query "SELECT COUNT(*) FROM ghost_snapshots WHERE tenant_id = '${TENANT_ID}';")
echo "Dev tenant ghost records:     $GHOST_COUNT  (expected 12)"
echo "Dev tenant resource records:  $RESOURCE_COUNT  (expected 19)"
echo "Dev tenant ghost snapshots:   $SNAPSHOT_COUNT  (expected 3000)"
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
#!/usr/bin/env bash
# seed_test_data.sh — seed dev tenant with dummy data for local development or remote servers
#
# Prerequisites:
#   Requires psql. If not installed:
#     brew install libpq
#     echo 'export PATH="/opt/homebrew/opt/libpq/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc
#
# Usage:
#   ./scripts/seed_test_data.sh                                    # Local docker, 1000 days
#   ./scripts/seed_test_data.sh --with-trends                      # Local docker, 90 days with trends
#   ./scripts/seed_test_data.sh --remote dev                       # Remote dev (192.168.1.100:5432)
#   ./scripts/seed_test_data.sh --remote staging --with-trends     # Remote staging with trends
#   DATABASE_URL="postgres://..." ./scripts/seed_test_data.sh      # Custom remote URL
#
# Supports both local (docker) and remote database connections.
# Safe to re-run — all inserts are idempotent (ON CONFLICT DO NOTHING / DO UPDATE).

set -euo pipefail

# ── Parse arguments ───────────────────────────────────────────────────────────

WITH_TRENDS=false
REMOTE_ENV=""
AUTO_YES=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --with-trends) WITH_TRENDS=true ;;
    --remote)
      shift
      REMOTE_ENV="${1:-}"
      if [[ "$REMOTE_ENV" != "dev" && "$REMOTE_ENV" != "staging" ]]; then
        echo "Error: --remote requires 'dev' or 'staging', got '$REMOTE_ENV'"
        exit 1
      fi
      ;;
    --yes|-y) AUTO_YES=true ;;
    *) echo "Error: Unknown flag '$1'"; exit 1 ;;
  esac
  shift
done

# ── Remote connection setup ───────────────────────────────────────────────────

if [[ -n "$REMOTE_ENV" ]]; then
  HOSTNAME="NAS.local"
  
  if [[ "$REMOTE_ENV" == "dev" ]]; then
    DB_PORT=5432
  else
    DB_PORT=5433
  fi
  
  export DATABASE_URL="postgres://axiaops_owner:axiaops_owner@$HOSTNAME:$DB_PORT/axiaops?sslmode=disable"
  
  echo "=== Seeding AxiaOps $REMOTE_ENV database ==="
  echo "Target:    $HOSTNAME:$DB_PORT"
  echo "URL:       $DATABASE_URL"
  echo ""
  
  if [[ "$AUTO_YES" != "true" ]]; then
    read -r -p "Seed the $REMOTE_ENV database at $HOSTNAME:$DB_PORT? This will insert data. [y/N] " confirm
    if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
      echo "Aborted."
      exit 0
    fi
    echo ""
  fi
  
  # Verify connection
  echo -n "Checking connection to $HOSTNAME..."
  if ! psql "$DATABASE_URL" -c 'SELECT 1' > /dev/null 2>&1; then
    echo " Failed."
    echo "Error: Cannot reach PostgreSQL at $HOSTNAME:$DB_PORT"
    exit 1
  fi
  echo " Connected."
  echo ""
  
  # Look up tenant ID for staging
  if [[ "$REMOTE_ENV" == "staging" ]]; then
    LOOKED_UP=$(psql "$DATABASE_URL" -t -c "SELECT id FROM axiaops.tenants ORDER BY created_at LIMIT 1;" 2>/dev/null | tr -d ' \n')
    if [[ -n "$LOOKED_UP" ]]; then
      export TENANT_ID="$LOOKED_UP"
      echo "Using tenant ID from DB: $TENANT_ID"
    fi
  fi
fi

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

# ── Resolve tenant ID from DB, fall back to default ──────────────────────────

EXISTING_TENANT=$(psql_query "SELECT id FROM tenants ORDER BY created_at LIMIT 1;" 2>/dev/null | tr -d '[:space:]')
if [ -n "$EXISTING_TENANT" ]; then
  TENANT_ID="$EXISTING_TENANT"
  echo "Using existing tenant from DB: ${TENANT_ID}"
else
  echo "No existing tenant found — creating dev tenant (id: ${TENANT_ID})..."
  psql_exec "INSERT INTO tenants (id, org_code, name, created_at)
    VALUES ('${TENANT_ID}', 'org_dev_local', 'AxiaOps Dev', '$NOW')
    ON CONFLICT DO NOTHING;"
  echo "  Done."
fi

# ── Additional tenants for RLS isolation testing (local only) ─────────────────

if [ "$MODE" = "docker" ]; then
  echo "Creating tenant: Acme Corp..."
  psql_exec "INSERT INTO tenants (id, org_code, name, created_at)
    VALUES (gen_random_uuid()::text, 'org_acme', 'Acme Corp', '$NOW')
    ON CONFLICT (org_code) DO NOTHING;"

  echo "Creating tenant: Globex Inc..."
  psql_exec "INSERT INTO tenants (id, org_code, name, created_at)
    VALUES (gen_random_uuid()::text, 'org_globex', 'Globex Inc', '$NOW')
    ON CONFLICT (org_code) DO NOTHING;"
  echo ""
fi

# ── Seed AWS accounts for the dev tenant ─────────────────────────────────────
# If real accounts already exist for this tenant (e.g. on staging), use them.
# Otherwise insert placeholder accounts so the dashboard shows something.

echo "Creating seed accounts for tenant ${TENANT_ID}..."
ACCT1="seed-account-001"
ACCT2="seed-account-002"
ACCT3="seed-account-003"
psql_exec "INSERT INTO accounts (id, tenant_id, provider, label, access_key_id, secret_encrypted, region, status, created_at)
  VALUES
    ('${ACCT1}', '${TENANT_ID}', 'aws', 'Seed Production AWS', '', '', 'eu-central-1', 'connected', '$NOW'),
    ('${ACCT2}', '${TENANT_ID}', 'aws', 'Seed Staging AWS',    '', '', 'us-east-1',    'connected', '$NOW'),
    ('${ACCT3}', '${TENANT_ID}', 'aws', 'Seed Dev AWS',        '', '', 'eu-west-1',    'connected', '$NOW')
  ON CONFLICT (id) DO NOTHING;"
echo "  Done."
echo ""

# ── Ghost records — zombie resources spread across all 3 accounts ─────────────

echo "Inserting ghost records..."
psql_exec "DELETE FROM ghost_records WHERE tenant_id = '${TENANT_ID}' AND internal_account_id IN ('seed-account-001','seed-account-002','seed-account-003');"

psql_exec "INSERT INTO ghost_records
  (tenant_id, provider, account_id, internal_account_id, service, region, resource_id, tags, monthly_cost, currency,
   period_start, period_end, usage_metric, usage_avg, usage_unit, reason, owner, detected_at)
VALUES
  -- Account 1 ghosts
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEC2', 'eu-central-1',
   'i-0abc123prod0001', '{\"env\":\"prod\",\"team\":\"backend\"}',
   45.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 1.2, 'Percent',
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonRDS', 'eu-central-1',
   'db-prod-legacy-reporting', '{\"env\":\"prod\",\"team\":\"data\"}',
   210.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count',
   'Zero connections — likely abandoned', 'data', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonElasticLoadBalancing', 'eu-central-1',
   'app/legacy-api/abc123prod', '{\"env\":\"prod\",\"team\":\"platform\"}',
   18.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RequestCount', 0, 'Count',
   'Zero requests — likely abandoned', 'platform', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonVPC', 'eu-central-1',
   'eipalloc-prod00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count',
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Account 2 ghosts
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'us-east-1',
   'i-0abc123stg0001', '{\"env\":\"staging\",\"team\":\"backend\"}',
   38.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 0.8, 'Percent',
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'us-east-1',
   'i-0abc123stg0002', '{\"env\":\"staging\",\"team\":\"platform\"}',
   38.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 2.1, 'Percent',
   'Instance CPU below 5% — likely idle', 'platform', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AWSLambda', 'us-east-1',
   'stg-image-resizer', '{\"env\":\"staging\",\"team\":\"backend\"}',
   4.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count',
   'Zero invocations — likely unused', 'backend', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonVPC', 'us-east-1',
   'eipalloc-stg00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count',
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Account 3 ghosts
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'eu-west-1',
   'i-0abc123dev0001', '{\"env\":\"dev\",\"team\":\"backend\"}',
   22.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 0.3, 'Percent',
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonRDS', 'eu-west-1',
   'db-dev-abandoned', '{\"env\":\"dev\",\"team\":\"data\"}',
   89.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count',
   'Zero connections — likely abandoned', 'data', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AWSLambda', 'eu-west-1',
   'dev-unused-email-sender', '{\"env\":\"dev\",\"team\":\"backend\"}',
   2.30, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count',
   'Zero invocations — likely unused', 'backend', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonVPC', 'eu-west-1',
   'eipalloc-dev00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count',
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW')
;"
echo "  Inserted 12 ghost records (4 prod, 4 staging, 4 dev)."
echo ""

# ── Resource records — ghosts + active resources across all 3 accounts ────────

echo "Inserting resource records..."
psql_exec "DELETE FROM resource_records WHERE tenant_id = '${TENANT_ID}' AND internal_account_id IN ('seed-account-001','seed-account-002','seed-account-003');"

psql_exec "INSERT INTO resource_records
  (tenant_id, provider, account_id, internal_account_id, service, region, resource_id, tags, monthly_cost, currency,
   period_start, period_end, usage_metric, usage_avg, usage_unit, is_ghost, reason, owner, detected_at)
VALUES
  -- ── Production (${ACCT1}) ──────────────────────────────────────────
  -- Ghost: idle EC2
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEC2', 'eu-central-1',
   'i-0abc123prod0001', '{\"env\":\"prod\",\"team\":\"backend\"}',
   45.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 1.2, 'Percent', true,
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  -- Ghost: abandoned RDS
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonRDS', 'eu-central-1',
   'db-prod-legacy-reporting', '{\"env\":\"prod\",\"team\":\"data\"}',
   210.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count', true,
   'Zero connections — likely abandoned', 'data', '$NOW'),

  -- Ghost: unused ELB
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonElasticLoadBalancing', 'eu-central-1',
   'app/legacy-api/abc123prod', '{\"env\":\"prod\",\"team\":\"platform\"}',
   18.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RequestCount', 0, 'Count', true,
   'Zero requests — likely abandoned', 'platform', '$NOW'),

  -- Ghost: unattached EIP
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonVPC', 'eu-central-1',
   'eipalloc-prod00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count', true,
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Active: healthy EC2
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEC2', 'eu-central-1',
   'i-0abc123prod0099', '{\"env\":\"prod\",\"team\":\"backend\"}',
   182.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 71.5, 'Percent', false, '', 'backend', '$NOW'),

  -- Active: healthy RDS
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonRDS', 'eu-central-1',
   'db-production-main', '{\"env\":\"prod\",\"team\":\"data\"}',
   312.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 284, 'Count', false, '', 'data', '$NOW'),

  -- Active: healthy ELB
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonElasticLoadBalancing', 'eu-central-1',
   'app/prod-api/xyz789prod', '{\"env\":\"prod\",\"team\":\"platform\"}',
   24.30, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RequestCount', 94200, 'Count', false, '', 'platform', '$NOW'),

  -- ── Staging (${ACCT2}) ─────────────────────────────────────────────
  -- Ghost: idle EC2 #1
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'us-east-1',
   'i-0abc123stg0001', '{\"env\":\"staging\",\"team\":\"backend\"}',
   38.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 0.8, 'Percent', true,
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  -- Ghost: idle EC2 #2
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'us-east-1',
   'i-0abc123stg0002', '{\"env\":\"staging\",\"team\":\"platform\"}',
   38.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 2.1, 'Percent', true,
   'Instance CPU below 5% — likely idle', 'platform', '$NOW'),

  -- Ghost: unused Lambda
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AWSLambda', 'us-east-1',
   'stg-image-resizer', '{\"env\":\"staging\",\"team\":\"backend\"}',
   4.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count', true,
   'Zero invocations — likely unused', 'backend', '$NOW'),

  -- Ghost: unattached EIP
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonVPC', 'us-east-1',
   'eipalloc-stg00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count', true,
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Active: healthy EC2
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'us-east-1',
   'i-0abc123stg0099', '{\"env\":\"staging\",\"team\":\"backend\"}',
   76.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 48.2, 'Percent', false, '', 'backend', '$NOW'),

  -- Active: healthy RDS
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonRDS', 'us-east-1',
   'db-staging-main', '{\"env\":\"staging\",\"team\":\"data\"}',
   98.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 37, 'Count', false, '', 'data', '$NOW'),

  -- ── Development (${ACCT3}) ────────────────────────────────────────
  -- Ghost: idle EC2
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'eu-west-1',
   'i-0abc123dev0001', '{\"env\":\"dev\",\"team\":\"backend\"}',
   22.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 0.3, 'Percent', true,
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  -- Ghost: abandoned RDS
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonRDS', 'eu-west-1',
   'db-dev-abandoned', '{\"env\":\"dev\",\"team\":\"data\"}',
   89.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count', true,
   'Zero connections — likely abandoned', 'data', '$NOW'),

  -- Ghost: unused Lambda
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AWSLambda', 'eu-west-1',
   'dev-unused-email-sender', '{\"env\":\"dev\",\"team\":\"backend\"}',
   2.30, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count', true,
   'Zero invocations — likely unused', 'backend', '$NOW'),

  -- Ghost: unattached EIP
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonVPC', 'eu-west-1',
   'eipalloc-dev00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count', true,
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Active: healthy EC2
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'eu-west-1',
   'i-0abc123dev0099', '{\"env\":\"dev\",\"team\":\"backend\"}',
   22.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 34.7, 'Percent', false, '', 'backend', '$NOW'),

  -- Active: healthy Lambda
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AWSLambda', 'eu-west-1',
   'dev-auth-handler', '{\"env\":\"dev\",\"team\":\"backend\"}',
   1.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 1840, 'Count', false, '', 'backend', '$NOW')
;"
echo "  Inserted 19 resource records (12 ghosts, 7 active) across 3 accounts."
echo ""

# ── Ghost snapshots — historical trend data per account ───────────────────────
# Creates snapshots per account simulating daily scans with realistic variation.
# Use --with-trends for 90 days of realistic daily variation (for chart development)
# Default: 1000 days of simple random data

generate_snapshots() {
  local acct_id=$1
  local base_ghosts=$2
  local base_savings=$3
  local days=$4
  local with_trends=$5
  
  local snap_insert="INSERT INTO ghost_snapshots (id, tenant_id, account_id, snapshot_at, ghost_count, total_monthly_cost, currency) VALUES"
  
  for i in $(seq $days -1 1); do
    local snap_date=$(date -u -v-${i}d +"%Y-%m-%dT12:00:00Z" 2>/dev/null || TZ=UTC date -d "$i days ago" +"%Y-%m-%dT12:00:00Z" 2>/dev/null)
    
    if [ "$with_trends" = "true" ]; then
      # Realistic variation: gradual trend + weekly pattern + random noise
      local trend_factor=$(awk -v days=$days -v i=$i "BEGIN {printf \"%.2f\", 1.0 + (($days - $i) / $days) * 0.3}")
      local weekly_factor=$(awk -v i=$i "BEGIN {printf \"%.2f\", 1.0 + 0.1 * sin(($i / 7) * 3.14159)}")
      local noise=$(awk -v seed=$RANDOM "BEGIN {srand(seed); printf \"%.2f\", 0.9 + (rand() * 0.2)}")
      
      local ghosts=$(awk -v base=$base_ghosts -v trend=$trend_factor -v weekly=$weekly_factor -v noise=$noise \
        "BEGIN {printf \"%d\", int(base * trend * weekly * noise)}")
      local cost=$(awk -v base=$base_savings -v trend=$trend_factor -v weekly=$weekly_factor -v noise=$noise \
        "BEGIN {printf \"%.2f\", base * trend * weekly * noise}")
    else
      # Simple random variation
      local ghosts=$((base_ghosts + RANDOM % 5 - 2))
      local cost=$(awk -v base=$base_savings -v seed=$RANDOM "BEGIN {srand(seed); printf \"%.2f\", base * (0.8 + rand() * 0.4)}")
    fi
    
    if [ $i -eq 1 ]; then
      snap_insert="$snap_insert (gen_random_uuid()::text, '${TENANT_ID}', '$acct_id', '$snap_date', $ghosts, $cost, 'USD')"
    else
      snap_insert="$snap_insert (gen_random_uuid()::text, '${TENANT_ID}', '$acct_id', '$snap_date', $ghosts, $cost, 'USD'),"
    fi
  done
  
  snap_insert="$snap_insert ON CONFLICT DO NOTHING;"
  psql_exec "$snap_insert"
  echo "  Inserted $days snapshots for $acct_id."
}

if [ "$WITH_TRENDS" = "true" ]; then
  DAYS=90
  echo "Inserting ghost snapshots with realistic trends (90 days × 3 accounts)..."
else
  DAYS=1000
  echo "Inserting ghost snapshots (1000 days × 3 accounts)..."
fi

generate_snapshots "$ACCT1" 12 480.0 $DAYS $WITH_TRENDS  # Production
generate_snapshots "$ACCT2" 8  320.0 $DAYS $WITH_TRENDS  # Staging
generate_snapshots "$ACCT3" 5  200.0 $DAYS $WITH_TRENDS  # Dev
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
echo "  make start-dev   — start all services (dev mode, no auth)"
echo "  make seed        — (re-)populate dummy data"
echo "  open http://localhost:3000"
#!/usr/bin/env bash
# seed_test_data.sh — seed dev tenant with dummy data for local development or remote servers
#
# Prerequisites:
#   Requires psql. If not installed:
#     brew install libpq
#     echo 'export PATH="/opt/homebrew/opt/libpq/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc
#
# Usage:
#   ./scripts/seed_test_data.sh                                    # Local docker
#   ./scripts/seed_test_data.sh --remote dev                       # Remote dev (NAS.local:5432)
#   ./scripts/seed_test_data.sh --remote staging                   # Remote staging (NAS.local:5433)
#   MIGRATION_DATABASE_URL="postgres://..." ./scripts/seed_test_data.sh      # Custom connection (owner user, bypasses RLS)
#
# Supports both local (docker) and remote database connections.
# Safe to re-run — all inserts are idempotent (ON CONFLICT DO NOTHING / DO UPDATE).

set -euo pipefail

# ── Parse arguments ───────────────────────────────────────────────────────────

REMOTE_ENV=""
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
    --yes|-y) AUTO_YES=true ;;
    *) echo "Error: Unknown flag '$1'"; exit 1 ;;
  esac
  shift
done

# ── Remote connection setup ───────────────────────────────────────────────────
# When --remote is passed, build a MIGRATION_DATABASE_URL pointing to the remote host.
# For staging, look up the real tenant ID from the DB (created by Kinde auth).
# Prompts for confirmation unless --yes/-y is passed.

if [[ -n "$REMOTE_ENV" ]]; then
  HOSTNAME="NAS.local"
  
  if [[ "$REMOTE_ENV" == "dev" ]]; then
    DB_PORT=5432
  else
    DB_PORT=5433
  fi
  
  export MIGRATION_DATABASE_URL="postgres://axiaops_owner:axiaops_owner@$HOSTNAME:$DB_PORT/axiaops?sslmode=disable"
  
  echo "=== Seeding AxiaOps $REMOTE_ENV database ==="
  echo "Target:    $HOSTNAME:$DB_PORT"
  echo "URL:       $MIGRATION_DATABASE_URL"
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
  if ! psql "$MIGRATION_DATABASE_URL" -c 'SELECT 1' > /dev/null 2>&1; then
    echo " Failed."
    echo "Error: Cannot reach PostgreSQL at $HOSTNAME:$DB_PORT"
    exit 1
  fi
  echo " Connected."
  echo ""


  # Look up tenant ID for staging
  if [[ "$REMOTE_ENV" == "staging" ]]; then
    echo "aaaa"
    LOOKED_UP=$(psql "$MIGRATION_DATABASE_URL" -t -c "SELECT id FROM axiaops.tenants ORDER BY created_at LIMIT 1;" 2>/dev/null | tr -d ' \n')
    echo "bbbb" $LOOKED_UP
    if [[ -n "$LOOKED_UP" ]]; then
      export TENANT_ID="$LOOKED_UP"
      echo "Using tenant ID from DB: $TENANT_ID"
    fi
  fi
fi

# Default Tenant ID (can be overridden by environment variable)
TENANT_ID="${TENANT_ID:-dev-tenant-axiaops}"

# ── Determine connection mode (local docker or remote) ────────────────────────
# If MIGRATION_DATABASE_URL is set (by --remote or the caller), connect directly as schema owner.
# Otherwise, shell into the local docker container.

if [ -z "${MIGRATION_DATABASE_URL:-}" ]; then
  # Local mode: use docker container
  MODE="docker"
  echo "MIGRATION_DATABASE_URL not set — using local docker container (axiaops-postgres)"
else
  # Remote mode: use direct psql connection
  MODE="remote"
  echo "MIGRATION_DATABASE_URL set — connecting to remote postgres"
fi

# ── psql helpers ──────────────────────────────────────────────────────────────
# All DB access goes through psql_base, which handles connection details and
# sets search_path=axiaops so unqualified table names resolve correctly.
# We connect as axiaops_owner (bypasses RLS) since this is a seed script.

if [ "$MODE" = "docker" ]; then
  psql_base() { docker exec -i -e "PGOPTIONS=-c search_path=axiaops" axiaops-postgres psql -U axiaops_owner -d axiaops "$@"; }
else
  psql_base() { PGOPTIONS="-c search_path=axiaops" psql "$MIGRATION_DATABASE_URL" "$@"; }
fi

psql_exec()  { psql_base --quiet -c "$1"; }           # Run single statement, no output (writes)
psql_query() { psql_base -t --no-align -c "$1"; }    # Run single statement, return raw value (reads)
psql_pipe()  { psql_base --quiet; }                   # Read multi-statement SQL from stdin (bulk inserts)

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
    if psql "$MIGRATION_DATABASE_URL" -c "SELECT 1" &>/dev/null; then
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
# On staging/remote, a real tenant already exists (created by Kinde login).
# On fresh local DBs, we create a placeholder dev tenant.

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

# ── Seed AWS accounts ────────────────────────────────────────────────────────
# Three dummy accounts (prod/staging/dev) with empty credentials.
# ON CONFLICT DO NOTHING so re-runs are safe and real accounts aren't overwritten.

echo "Creating seed accounts for tenant ${TENANT_ID}..."
ACCT1="seed-account-001"
ACCT2="seed-account-002"
ACCT3="seed-account-003"
AWS_ACCT_ID="111111111111"
psql_exec "INSERT INTO accounts (id, tenant_id, provider, label, account_id, access_key_id, secret_encrypted, region, status, created_at)
  VALUES
    ('${ACCT1}', '${TENANT_ID}', 'aws', 'Seed Production AWS', '${AWS_ACCT_ID}', '', '', 'eu-central-1', 'connected', '$NOW'),
    ('${ACCT2}', '${TENANT_ID}', 'aws', 'Seed Staging AWS',    '${AWS_ACCT_ID}', '', '', 'us-east-1',    'connected', '$NOW'),
    ('${ACCT3}', '${TENANT_ID}', 'aws', 'Seed Dev AWS',        '${AWS_ACCT_ID}', '', '', 'eu-west-1',    'connected', '$NOW')
  ON CONFLICT (id) DO UPDATE SET account_id = '${AWS_ACCT_ID}';"
echo "  Done."
echo ""

# ── Ghost records ─────────────────────────────────────────────────────────────
# 24 zombie resources across all 3 accounts (8 each):
#   - Tier 2 (CloudWatch): idle EC2, abandoned RDS, unused Lambda/ELB, unattached EIP
#   - Tier 1 (API-only):   unattached EBS, orphaned snapshots, long-stopped EC2, old AMIs
#   - Other:               idle CE anomaly monitors, empty EKS clusters
# Deletes existing seed data first, then re-inserts (idempotent on re-run).

echo "Inserting ghost records..."
psql_exec "DELETE FROM ghost_records WHERE tenant_id = '${TENANT_ID}' AND internal_account_id IN ('seed-account-001','seed-account-002','seed-account-003');"

psql_exec "INSERT INTO ghost_records
  (tenant_id, provider, account_id, internal_account_id, service, resource_type, region, resource_id, tags, monthly_cost, currency,
   period_start, period_end, usage_metric, usage_avg, usage_unit, reason, owner, detected_at)
VALUES
  -- Account 1 ghosts
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEC2', 'instance', 'eu-central-1',
   'i-0abc123prod0001', '{\"env\":\"prod\",\"team\":\"backend\"}',
   45.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 1.2, 'Percent',
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonRDS', 'primary', 'eu-central-1',
   'db-prod-legacy-reporting', '{\"env\":\"prod\",\"team\":\"data\"}',
   210.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count',
   'Zero connections — likely abandoned', 'data', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonElasticLoadBalancing', 'alb', 'eu-central-1',
   'app/legacy-api/abc123prod', '{\"env\":\"prod\",\"team\":\"platform\"}',
   18.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RequestCount', 0, 'Count',
   'Zero requests — likely abandoned', 'platform', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonVPC', 'eip', 'eu-central-1',
   'eipalloc-prod00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count',
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Account 2 ghosts
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'instance', 'us-east-1',
   'i-0abc123stg0001', '{\"env\":\"staging\",\"team\":\"backend\"}',
   38.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 0.8, 'Percent',
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'instance', 'us-east-1',
   'i-0abc123stg0002', '{\"env\":\"staging\",\"team\":\"platform\"}',
   38.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 2.1, 'Percent',
   'Instance CPU below 5% — likely idle', 'platform', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AWSLambda', '', 'us-east-1',
   'stg-image-resizer', '{\"env\":\"staging\",\"team\":\"backend\"}',
   4.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count',
   'Zero invocations — likely unused', 'backend', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonVPC', 'eip', 'us-east-1',
   'eipalloc-stg00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count',
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Account 3 ghosts
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'instance', 'eu-west-1',
   'i-0abc123dev0001', '{\"env\":\"dev\",\"team\":\"backend\"}',
   22.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 0.3, 'Percent',
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonRDS', 'primary', 'eu-west-1',
   'db-dev-abandoned', '{\"env\":\"dev\",\"team\":\"data\"}',
   89.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count',
   'Zero connections — likely abandoned', 'data', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AWSLambda', '', 'eu-west-1',
   'dev-unused-email-sender', '{\"env\":\"dev\",\"team\":\"backend\"}',
   2.30, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count',
   'Zero invocations — likely unused', 'backend', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonVPC', 'eip', 'eu-west-1',
   'eipalloc-dev00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count',
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- ── CE Anomaly Detection monitor ghosts ───────────────────────────────────

  -- Account 1: idle paid CE anomaly monitor (zero anomalies in 30 days)
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AWSCostExplorer', '', 'us-east-1',
   'arn:aws:ce::123456789012:anomalymonitor/prod-service-monitor', '{}',
   3.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'AnomalyCount', 0, 'Count',
   'Cost Anomaly Detection monitor "prod-service-monitor" detected zero anomalies in the last 30 days — paying ~\$3/mo for no signal', 'unknown', '$NOW'),

  -- Account 2: idle paid CE anomaly monitor
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AWSCostExplorer', '', 'us-east-1',
   'arn:aws:ce::987654321098:anomalymonitor/stg-cost-monitor', '{}',
   3.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'AnomalyCount', 0, 'Count',
   'Cost Anomaly Detection monitor "stg-cost-monitor" detected zero anomalies in the last 30 days — paying ~\$3/mo for no signal', 'unknown', '$NOW'),

  -- ── EKS ghosts ────────────────────────────────────────────────────────────

  -- Account 1: empty EKS cluster (control plane billed, zero nodes)
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEKS', '', 'eu-central-1',
   'prod-analytics-cluster', '{\"env\":\"prod\",\"team\":\"data\"}',
   73.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NodeCount', 0, 'Count',
   'EKS cluster has zero nodes — control plane (\$73/mo) billing with no workload', 'data', '$NOW'),

  -- Account 2: empty EKS cluster
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEKS', '', 'us-east-1',
   'stg-ml-pipeline', '{\"env\":\"staging\",\"team\":\"platform\"}',
   73.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NodeCount', 0, 'Count',
   'EKS cluster has zero nodes — control plane (\$73/mo) billing with no workload', 'platform', '$NOW'),

  -- ── Tier 1 API-only ghosts ─────────────────────────────────────────────────

  -- Account 1: unattached EBS volume (100 GB gp3, $0.08/GB-month)
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEC2', 'volume', 'eu-central-1',
   'vol-0prod00000001', '{\"env\":\"prod\",\"team\":\"platform\"}',
   8.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'VolumeState', 0, 'State',
   'EBS volume (100 GB gp3) is unattached — not mounted to any instance but still incurring storage charges', 'platform', '$NOW'),

  -- Account 1: orphaned snapshot (source volume deleted, not backing any AMI)
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEC2', 'snapshot', 'eu-central-1',
   'snap-0prod00000001', '{\"env\":\"prod\",\"team\":\"data\"}',
   10.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'SourceVolumeExists', 0, 'Boolean',
   'EBS snapshot (200 GB) source volume vol-0prod-deleted-001 no longer exists — orphaned storage accumulating charges', 'data', '$NOW'),

  -- Account 2: long-stopped EC2 instance (45 days, 80 GB attached EBS)
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'stopped_instance', 'us-east-1',
   'i-0stopped-stg0001', '{\"env\":\"staging\",\"team\":\"backend\"}',
   6.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysStopped', 45, 'Days',
   'EC2 instance stopped for 45 days — attached EBS storage (80 GB) continues to bill at no compute benefit', 'backend', '$NOW'),

  -- Account 2: old AMI (120 days old, 80 GB backing snapshots, $0.05/GB-month)
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'ami', 'us-east-1',
   'ami-0stg00000001', '{\"env\":\"staging\",\"team\":\"platform\"}',
   4.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysSinceCreation', 120, 'Days',
   'AMI is 120 days old and not referenced by any instance — backing snapshots (80 GB) accumulate storage charges', 'platform', '$NOW'),

  -- Account 3: unattached EBS volume (50 GB gp3)
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'volume', 'eu-west-1',
   'vol-0dev00000001', '{\"env\":\"dev\",\"team\":\"backend\"}',
   4.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'VolumeState', 0, 'State',
   'EBS volume (50 GB gp3) is unattached — not mounted to any instance but still incurring storage charges', 'backend', '$NOW'),

  -- Account 3: orphaned snapshot (150 GB)
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'snapshot', 'eu-west-1',
   'snap-0dev00000001', '{\"env\":\"dev\",\"team\":\"data\"}',
   7.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'SourceVolumeExists', 0, 'Boolean',
   'EBS snapshot (150 GB) source volume vol-0dev-deleted-001 no longer exists — orphaned storage accumulating charges', 'data', '$NOW'),

  -- Account 3: long-stopped EC2 instance (60 days, 40 GB attached EBS)
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'stopped_instance', 'eu-west-1',
   'i-0stopped-dev0001', '{\"env\":\"dev\",\"team\":\"backend\"}',
   3.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysStopped', 60, 'Days',
   'EC2 instance stopped for 60 days — attached EBS storage (40 GB) continues to bill at no compute benefit', 'backend', '$NOW'),

  -- Account 3: old AMI (180 days old, 60 GB backing snapshots)
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'ami', 'eu-west-1',
   'ami-0dev00000001', '{\"env\":\"dev\",\"team\":\"platform\"}',
   3.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysSinceCreation', 180, 'Days',
   'AMI is 180 days old and not referenced by any instance — backing snapshots (60 GB) accumulate storage charges', 'platform', '$NOW'),

  -- ── CloudWatch Log Group ghosts ───────────────────────────────────────────

  -- Account 1: log group with no retention policy (2.5 GB stored indefinitely)
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonCloudWatch', 'log_group', 'eu-central-1',
   '/aws/lambda/prod-legacy-processor', '{}',
   0.08, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RetentionDays', 0, 'Days',
   'CloudWatch log group has no retention policy — 2.5 GB stored indefinitely accumulating charges', 'unknown', '$NOW'),

  -- Account 2: log group with no retention policy (5.0 GB stored indefinitely)
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonCloudWatch', 'log_group', 'us-east-1',
   '/ecs/stg-api-service', '{}',
   0.15, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RetentionDays', 0, 'Days',
   'CloudWatch log group has no retention policy — 5.0 GB stored indefinitely accumulating charges', 'unknown', '$NOW'),

  -- Account 3: log group with no retention policy (1.2 GB stored indefinitely)
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonCloudWatch', 'log_group', 'eu-west-1',
   '/aws/rds/dev-abandoned-db', '{}',
   0.04, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RetentionDays', 0, 'Days',
   'CloudWatch log group has no retention policy — 1.2 GB stored indefinitely accumulating charges', 'unknown', '$NOW'),

  -- ── Orphaned RDS snapshot ghosts ──────────────────────────────────────────

  -- Account 1: orphaned manual RDS snapshot (100 GB, source DB deleted, 45 days old)
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonRDS', 'snapshot', 'eu-central-1',
   'rds:prod-legacy-reporting-final-2026-02', '{}',
   9.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'SourceDBExists', 45, 'Days',
   'Manual RDS snapshot (100 GB, 45 days old) is orphaned — source DB "prod-legacy-reporting" no longer exists, accumulating \$9.50/month in storage charges', 'unknown', '$NOW'),

  -- Account 2: orphaned manual RDS snapshot (200 GB, source DB deleted, 90 days old)
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonRDS', 'snapshot', 'us-east-1',
   'rds:stg-analytics-db-pre-migration', '{}',
   19.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'SourceDBExists', 90, 'Days',
   'Manual RDS snapshot (200 GB, 90 days old) is orphaned — source DB "stg-analytics-db" no longer exists, accumulating \$19.00/month in storage charges', 'unknown', '$NOW'),

  -- Account 3: orphaned manual RDS snapshot (50 GB, source DB deleted, 60 days old)
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonRDS', 'snapshot', 'eu-west-1',
   'rds:dev-test-db-backup-2026-01', '{}',
   4.75, 'USD', '$PERIOD_START', '$PERIOD_END',
   'SourceDBExists', 60, 'Days',
   'Manual RDS snapshot (50 GB, 60 days old) is orphaned — source DB "dev-test-db" no longer exists, accumulating \$4.75/month in storage charges', 'unknown', '$NOW'),

  -- ── Stale ECR image ghosts ────────────────────────────────────────────────

  -- Account 1: ECR repo with stale images (12 stale, 8.5 GB)
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonECR', 'repository', 'eu-central-1',
   'prod-api-service', '{}',
   0.85, 'USD', '$PERIOD_START', '$PERIOD_END',
   'StaleImageCount', 12, 'Count',
   'ECR repository has 12 untagged/stale images totaling 8.5 GB — accumulating \$0.85/month in storage', 'unknown', '$NOW'),

  -- Account 2: ECR repo with stale images (25 stale, 15.0 GB)
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonECR', 'repository', 'us-east-1',
   'stg-worker', '{}',
   1.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'StaleImageCount', 25, 'Count',
   'ECR repository has 25 untagged/stale images totaling 15.0 GB — accumulating \$1.50/month in storage', 'unknown', '$NOW'),

  -- Account 3: ECR repo with stale images (8 stale, 3.2 GB)
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonECR', 'repository', 'eu-west-1',
   'dev-frontend', '{}',
   0.32, 'USD', '$PERIOD_START', '$PERIOD_END',
   'StaleImageCount', 8, 'Count',
   'ECR repository has 8 untagged/stale images totaling 3.2 GB — accumulating \$0.32/month in storage', 'unknown', '$NOW'),

  -- ── Unused Secrets Manager ghosts ─────────────────────────────────────────

  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AWSSecretsManager', '', 'eu-central-1',
   'prod/legacy-api/db-password', '{}',
   0.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysSinceAccess', 180, 'Days',
   'Secret not accessed for 180 days — still billing \$0.40/month', 'unknown', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AWSSecretsManager', '', 'us-east-1',
   'stg/old-service/api-key', '{}',
   0.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysSinceAccess', 120, 'Days',
   'Secret not accessed for 120 days — still billing \$0.40/month', 'unknown', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AWSSecretsManager', '', 'eu-west-1',
   'dev/abandoned-project/token', '{}',
   0.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysSinceAccess', 95, 'Days',
   'Secret not accessed for 95 days — still billing \$0.40/month', 'unknown', '$NOW'),

  -- ── CloudFront distribution ghosts (zero requests) ─────────────────────────

  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonCloudFront', 'global', 'us-east-1',
   'E1PROD0ABANDONED', '{}',
   18.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Requests', 0, 'Count',
   'CloudFront distribution has zero requests — likely abandoned', 'unknown', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonCloudFront', 'global', 'us-east-1',
   'E2STG0OLDSITE', '{}',
   8.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Requests', 0, 'Count',
   'CloudFront distribution has zero requests — likely abandoned', 'unknown', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonCloudFront', 'global', 'us-east-1',
   'E3UAT0PREVIEW', '{}',
   6.25, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Requests', 0, 'Count',
   'CloudFront distribution has zero requests — likely abandoned', 'unknown', '$NOW'),

  -- ── Kinesis data streams (zero incoming records, provisioned mode) ────────

  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonKinesis', '', 'eu-central-1',
   'prod-event-ingestion-v1', '{}',
   32.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'IncomingRecords', 0, 'Count',
   'Kinesis data stream has zero incoming records — likely unused', 'unknown', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonKinesis', '', 'us-east-1',
   'stg-monitoring-stream', '{}',
   10.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'IncomingRecords', 0, 'Count',
   'Kinesis data stream has zero incoming records — likely unused', 'unknown', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonKinesis', '', 'eu-west-1',
   'dev-clickstream', '{}',
   5.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'IncomingRecords', 0, 'Count',
   'Kinesis data stream has zero incoming records — likely unused', 'unknown', '$NOW'),

  -- ── S3 buckets (zero requests, requires request metrics enabled) ──────────

  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonS3', '', 'eu-central-1',
   'prod-old-data-export-2024', '{}',
   25.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'AllRequests', 0, 'Count',
   'S3 bucket has zero requests — likely abandoned (requires request metrics enabled)', 'unknown', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonS3', '', 'us-east-1',
   'stg-terraform-state-backup', '{}',
   8.75, 'USD', '$PERIOD_START', '$PERIOD_END',
   'AllRequests', 0, 'Count',
   'S3 bucket has zero requests — likely abandoned (requires request metrics enabled)', 'unknown', '$NOW'),

  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonS3', '', 'eu-west-1',
   'dev-test-uploads-2023', '{}',
   4.25, 'USD', '$PERIOD_START', '$PERIOD_END',
   'AllRequests', 0, 'Count',
   'S3 bucket has zero requests — likely abandoned (requires request metrics enabled)', 'unknown', '$NOW')
;"
echo "  Inserted ghost records including:"
echo "    - 3 CloudFront distributions (zero requests each)"
echo "    - 3 Kinesis streams (zero incoming records each, provisioned mode)"
echo "    - 3 S3 buckets (zero requests each, requires metrics enabled)"
echo "    - Plus all other resource types (EC2, RDS, Lambda, ELB, VPC, EBS, EKS, etc.)"
echo ""

# ── Resource records ──────────────────────────────────────────────────────────
# 36 records: the same 24 ghosts above plus 12 active (healthy) resources.
# Active resources provide contrast in the dashboard — they show up in the
# "all resources" view but NOT in the ghosts view.

echo "Inserting resource records..."
psql_exec "DELETE FROM resource_records WHERE tenant_id = '${TENANT_ID}' AND internal_account_id IN ('seed-account-001','seed-account-002','seed-account-003');"

psql_exec "INSERT INTO resource_records
  (tenant_id, provider, account_id, internal_account_id, service, resource_type, region, resource_id, tags, monthly_cost, currency,
   period_start, period_end, usage_metric, usage_avg, usage_unit, is_ghost, reason, owner, detected_at)
VALUES
  -- ── Production (${ACCT1}) ──────────────────────────────────────────
  -- Ghost: idle EC2
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEC2', 'instance', 'eu-central-1',
   'i-0abc123prod0001', '{\"env\":\"prod\",\"team\":\"backend\"}',
   45.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 1.2, 'Percent', true,
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  -- Ghost: abandoned RDS
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonRDS', '', 'eu-central-1',
   'db-prod-legacy-reporting', '{\"env\":\"prod\",\"team\":\"data\"}',
   210.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count', true,
   'Zero connections — likely abandoned', 'data', '$NOW'),

  -- Ghost: unused ELB
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonElasticLoadBalancing', '', 'eu-central-1',
   'app/legacy-api/abc123prod', '{\"env\":\"prod\",\"team\":\"platform\"}',
   18.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RequestCount', 0, 'Count', true,
   'Zero requests — likely abandoned', 'platform', '$NOW'),

  -- Ghost: unattached EIP
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonVPC', 'eip', 'eu-central-1',
   'eipalloc-prod00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count', true,
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Active: healthy EC2
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEC2', 'instance', 'eu-central-1',
   'i-0abc123prod0099', '{\"env\":\"prod\",\"team\":\"backend\"}',
   182.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 71.5, 'Percent', false, '', 'backend', '$NOW'),

  -- Active: healthy RDS
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonRDS', 'primary', 'eu-central-1',
   'db-production-main', '{\"env\":\"prod\",\"team\":\"data\"}',
   312.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 284, 'Count', false, '', 'data', '$NOW'),

  -- Active: healthy ELB
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonElasticLoadBalancing', 'alb', 'eu-central-1',
   'app/prod-api/xyz789prod', '{\"env\":\"prod\",\"team\":\"platform\"}',
   24.30, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RequestCount', 94200, 'Count', false, '', 'platform', '$NOW'),

  -- ── Staging (${ACCT2}) ─────────────────────────────────────────────
  -- Ghost: idle EC2 #1
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'instance', 'us-east-1',
   'i-0abc123stg0001', '{\"env\":\"staging\",\"team\":\"backend\"}',
   38.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 0.8, 'Percent', true,
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  -- Ghost: idle EC2 #2
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'instance', 'us-east-1',
   'i-0abc123stg0002', '{\"env\":\"staging\",\"team\":\"platform\"}',
   38.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 2.1, 'Percent', true,
   'Instance CPU below 5% — likely idle', 'platform', '$NOW'),

  -- Ghost: unused Lambda
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AWSLambda', '', 'us-east-1',
   'stg-image-resizer', '{\"env\":\"staging\",\"team\":\"backend\"}',
   4.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count', true,
   'Zero invocations — likely unused', 'backend', '$NOW'),

  -- Ghost: unattached EIP
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonVPC', 'eip', 'us-east-1',
   'eipalloc-stg00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count', true,
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Active: healthy EC2
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'instance', 'us-east-1',
   'i-0abc123stg0099', '{\"env\":\"staging\",\"team\":\"backend\"}',
   76.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 48.2, 'Percent', false, '', 'backend', '$NOW'),

  -- Active: healthy RDS
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonRDS', 'read_replica', 'us-east-1',
   'db-staging-main', '{\"env\":\"staging\",\"team\":\"data\"}',
   98.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 37, 'Count', false, '', 'data', '$NOW'),

  -- ── Development (${ACCT3}) ────────────────────────────────────────
  -- Ghost: idle EC2
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'instance', 'eu-west-1',
   'i-0abc123dev0001', '{\"env\":\"dev\",\"team\":\"backend\"}',
   22.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 0.3, 'Percent', true,
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  -- Ghost: abandoned RDS
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonRDS', '', 'eu-west-1',
   'db-dev-abandoned', '{\"env\":\"dev\",\"team\":\"data\"}',
   89.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count', true,
   'Zero connections — likely abandoned', 'data', '$NOW'),

  -- Ghost: unused Lambda
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AWSLambda', '', 'eu-west-1',
   'dev-unused-email-sender', '{\"env\":\"dev\",\"team\":\"backend\"}',
   2.30, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count', true,
   'Zero invocations — likely unused', 'backend', '$NOW'),

  -- Ghost: unattached EIP
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonVPC', 'eip', 'eu-west-1',
   'eipalloc-dev00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count', true,
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Active: healthy EC2
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'instance', 'eu-west-1',
   'i-0abc123dev0099', '{\"env\":\"dev\",\"team\":\"backend\"}',
   22.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 34.7, 'Percent', false, '', 'backend', '$NOW'),

  -- Active: healthy Lambda
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AWSLambda', '', 'eu-west-1',
   'dev-auth-handler', '{\"env\":\"dev\",\"team\":\"backend\"}',
   1.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 1840, 'Count', false, '', 'backend', '$NOW'),

  -- ── CE Anomaly Detection monitor ghost resources ───────────────────────────

  -- Account 1: idle paid CE anomaly monitor (ghost)
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AWSCostExplorer', '', 'us-east-1',
   'arn:aws:ce::123456789012:anomalymonitor/prod-service-monitor', '{}',
   3.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'AnomalyCount', 0, 'Count', true,
   'Cost Anomaly Detection monitor "prod-service-monitor" detected zero anomalies in the last 30 days — paying ~\$3/mo for no signal', 'unknown', '$NOW'),

  -- Account 2: idle paid CE anomaly monitor (ghost)
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AWSCostExplorer', '', 'us-east-1',
   'arn:aws:ce::987654321098:anomalymonitor/stg-cost-monitor', '{}',
   3.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'AnomalyCount', 0, 'Count', true,
   'Cost Anomaly Detection monitor "stg-cost-monitor" detected zero anomalies in the last 30 days — paying ~\$3/mo for no signal', 'unknown', '$NOW'),

  -- Account 3: active CE anomaly monitor (for contrast — has anomalies)
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AWSCostExplorer', '', 'us-east-1',
   'arn:aws:ce::111222333444:anomalymonitor/dev-cost-monitor', '{}',
   3.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'AnomalyCount', 4, 'Count', false, '', 'unknown', '$NOW'),

  -- ── EKS ghost resources ────────────────────────────────────────────────────

  -- Account 1: empty EKS cluster (ghost)
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEKS', '', 'eu-central-1',
   'prod-analytics-cluster', '{\"env\":\"prod\",\"team\":\"data\"}',
   73.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NodeCount', 0, 'Count', true,
   'EKS cluster has zero nodes — control plane (\$73/mo) billing with no workload', 'data', '$NOW'),

  -- Account 2: empty EKS cluster (ghost)
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEKS', '', 'us-east-1',
   'stg-ml-pipeline', '{\"env\":\"staging\",\"team\":\"platform\"}',
   73.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NodeCount', 0, 'Count', true,
   'EKS cluster has zero nodes — control plane (\$73/mo) billing with no workload', 'platform', '$NOW'),

  -- Account 3: active EKS cluster (for contrast)
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEKS', '', 'eu-west-1',
   'dev-app-cluster', '{\"env\":\"dev\",\"team\":\"backend\"}',
   73.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NodeCount', 3, 'Count', false, '', 'backend', '$NOW'),

  -- ── Tier 1 API-only ghost resources ───────────────────────────────────────

  -- Account 1: unattached EBS volume (ghost)
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEC2', 'volume', 'eu-central-1',
   'vol-0prod00000001', '{\"env\":\"prod\",\"team\":\"platform\"}',
   8.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'VolumeState', 0, 'State', true,
   'EBS volume (100 GB gp3) is unattached — not mounted to any instance but still incurring storage charges', 'platform', '$NOW'),

  -- Account 1: active EBS volume (in use — for dashboard contrast)
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEC2', 'volume', 'eu-central-1',
   'vol-0prod00000099', '{\"env\":\"prod\",\"team\":\"backend\"}',
   24.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'VolumeState', 1, 'State', false, '', 'backend', '$NOW'),

  -- Account 1: orphaned snapshot (ghost)
  ('${TENANT_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEC2', 'snapshot', 'eu-central-1',
   'snap-0prod00000001', '{\"env\":\"prod\",\"team\":\"data\"}',
   10.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'SourceVolumeExists', 0, 'Boolean', true,
   'EBS snapshot (200 GB) source volume vol-0prod-deleted-001 no longer exists — orphaned storage accumulating charges', 'data', '$NOW'),

  -- Account 2: long-stopped EC2 instance (ghost)
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'stopped_instance', 'us-east-1',
   'i-0stopped-stg0001', '{\"env\":\"staging\",\"team\":\"backend\"}',
   6.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysStopped', 45, 'Days', true,
   'EC2 instance stopped for 45 days — attached EBS storage (80 GB) continues to bill at no compute benefit', 'backend', '$NOW'),

  -- Account 2: old AMI (ghost)
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'ami', 'us-east-1',
   'ami-0stg00000001', '{\"env\":\"staging\",\"team\":\"platform\"}',
   4.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysSinceCreation', 120, 'Days', true,
   'AMI is 120 days old and not referenced by any instance — backing snapshots (80 GB) accumulate storage charges', 'platform', '$NOW'),

  -- Account 2: active recent AMI (in use — for dashboard contrast)
  ('${TENANT_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'ami', 'us-east-1',
   'ami-0stg00000099', '{\"env\":\"staging\",\"team\":\"platform\"}',
   2.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysSinceCreation', 14, 'Days', false, '', 'platform', '$NOW'),

  -- Account 3: unattached EBS volume (ghost)
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'volume', 'eu-west-1',
   'vol-0dev00000001', '{\"env\":\"dev\",\"team\":\"backend\"}',
   4.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'VolumeState', 0, 'State', true,
   'EBS volume (50 GB gp3) is unattached — not mounted to any instance but still incurring storage charges', 'backend', '$NOW'),

  -- Account 3: orphaned snapshot (ghost)
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'snapshot', 'eu-west-1',
   'snap-0dev00000001', '{\"env\":\"dev\",\"team\":\"data\"}',
   7.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'SourceVolumeExists', 0, 'Boolean', true,
   'EBS snapshot (150 GB) source volume vol-0dev-deleted-001 no longer exists — orphaned storage accumulating charges', 'data', '$NOW'),

  -- Account 3: long-stopped EC2 instance (ghost)
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'stopped_instance', 'eu-west-1',
   'i-0stopped-dev0001', '{\"env\":\"dev\",\"team\":\"backend\"}',
   3.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysStopped', 60, 'Days', true,
   'EC2 instance stopped for 60 days — attached EBS storage (40 GB) continues to bill at no compute benefit', 'backend', '$NOW'),

  -- Account 3: old AMI (ghost)
  ('${TENANT_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'ami', 'eu-west-1',
   'ami-0dev00000001', '{\"env\":\"dev\",\"team\":\"platform\"}',
   3.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysSinceCreation', 180, 'Days', true,
   'AMI is 180 days old and not referenced by any instance — backing snapshots (60 GB) accumulate storage charges', 'platform', '$NOW')
;"
echo "  Inserted 36 resource records (24 ghosts, 12 active) across 3 accounts."
echo ""

# ── Ghost snapshots — historical trend data per account ───────────────────────
# Creates 90 days of snapshots per account simulating daily scans with realistic
# variation (upward trend + weekly sine wave + noise).
# Also creates per-service breakdown rows in ghost_snapshot_services.

# Service cost distribution per account (fractions must sum to ~1.0):
# Format: "service|resource_type:fraction" — pipe separates service from sub-type.
# Plain "service:fraction" means resource_type is empty.
#   ACCT1 (prod):    EC2|instance=0.16, RDS=0.42, ELB=0.04, VPC=0.01, EC2|volume=0.22, EC2|snapshot=0.15
#   ACCT2 (staging): EC2|instance=0.46, Lambda=0.02, VPC=0.03, EC2|stopped_instance=0.33, EC2|ami=0.16
#   ACCT3 (dev):     EC2|instance=0.22, RDS=0.30, Lambda=0.08, VPC=0.06, EC2|volume=0.20, EC2|ami=0.14

generate_snapshots() {
  local acct_id=$1       # e.g. "seed-account-001"
  local base_ghosts=$2   # baseline ghost count (e.g. 14)
  local base_savings=$3  # baseline monthly savings in USD (e.g. 498.0)
  local days=$4          # number of daily snapshots to generate
  local services=$5      # service cost distribution: "Service|type:fraction ..."

  # Uses a single awk invocation to build two INSERT statements (snapshots + services).
  # This avoids spawning thousands of subshells for date math.
  local sql
  sql=$(awk -v acct="$acct_id" -v tenant="$TENANT_ID" \
    -v base_g="$base_ghosts" -v base_s="$base_savings" \
    -v days="$days" -v svcs="$services" \
    'BEGIN {
      srand()
      pi = 3.14159265

      # Parse services — format: "service|resource_type:fraction" or "service:fraction"
      n_svc = split(svcs, svc_arr, " ")
      for (s = 1; s <= n_svc; s++) {
        split(svc_arr[s], parts, ":")
        svc_fracs[s] = parts[2] + 0
        # Split service|resource_type on pipe
        n_pipe = split(parts[1], pipe_parts, "|")
        svc_names[s] = pipe_parts[1]
        svc_rtypes[s] = (n_pipe > 1) ? pipe_parts[2] : ""
      }

      # Get current epoch (approximate — good enough for seed data)
      "date -u +%s" | getline epoch; close("date -u +%s")

      # Snapshot INSERT
      printf "INSERT INTO ghost_snapshots (id, tenant_id, account_id, snapshot_at, ghost_count, total_monthly_cost, currency) VALUES\n"
      for (i = days; i >= 1; i--) {
        snap_epoch = epoch - (i * 86400)
        # Format as ISO date (use shell date for portability)
        cmd = "date -u -r " snap_epoch " +\"%Y-%m-%dT12:00:00Z\" 2>/dev/null || TZ=UTC date -u -d @" snap_epoch " +\"%Y-%m-%dT12:00:00Z\""
        cmd | getline snap_date; close(cmd)

        # Realistic variation: upward trend + weekly sine wave + random noise
        tf = 1.0 + ((days - i) / days) * 0.3
        wf = 1.0 + 0.1 * sin((i / 7) * pi)
        noise = 0.9 + rand() * 0.2
        ghosts = int(base_g * tf * wf * noise)
        cost = base_s * tf * wf * noise
        if (ghosts < 0) ghosts = 0

        snap_id = "snap-" acct "-" i
        comma = (i > 1) ? "," : ""
        printf "  ('\''%s'\'', '\''%s'\'', '\''%s'\'', '\''%s'\'', %d, %.2f, '\''USD'\'')%s\n", snap_id, tenant, acct, snap_date, ghosts, cost, comma

        # Store for service rows
        snap_ids[i] = snap_id
        snap_ghosts[i] = ghosts
        snap_costs[i] = cost
      }
      printf "ON CONFLICT DO NOTHING;\n\n"

      # Service INSERT
      printf "INSERT INTO ghost_snapshot_services (id, snapshot_id, tenant_id, service, resource_type, ghost_count, monthly_cost, currency) VALUES\n"
      first = 1
      for (i = days; i >= 1; i--) {
        for (s = 1; s <= n_svc; s++) {
          svc_noise = 0.85 + rand() * 0.3
          svc_cost = snap_costs[i] * svc_fracs[s] * svc_noise
          svc_g = int(snap_ghosts[i] * svc_fracs[s] * svc_noise)
          if (svc_g < 1) svc_g = 1
          row_id = "svc-" acct "-" i "-" s

          if (!first) printf ",\n"
          first = 0
          printf "  ('\''%s'\'', '\''%s'\'', '\''%s'\'', '\''%s'\'', '\''%s'\'', %d, %.2f, '\''USD'\'')", row_id, snap_ids[i], tenant, svc_names[s], svc_rtypes[s], svc_g, svc_cost
        }
      }
      printf "\nON CONFLICT DO NOTHING;\n"
    }')

  # Pipe both INSERT statements via stdin (psql_exec uses -c which only takes one statement).
  echo "$sql" | psql_pipe || echo "  Warning: snapshot insert failed for $acct_id"

  local svc_count=$((days * $(echo "$services" | wc -w | tr -d ' ')))
  echo "  Inserted $days snapshots + $svc_count service rows for $acct_id."
}

# Clean old seed snapshot data (deterministic IDs).
# ghost_snapshot_services may not exist on older schemas — ignore errors.
psql_exec "DELETE FROM ghost_snapshot_services WHERE snapshot_id LIKE 'snap-seed-account-%';" 2>/dev/null || true
psql_exec "DELETE FROM ghost_snapshots WHERE id LIKE 'snap-seed-account-%';"

DAYS=90
echo "Inserting ghost snapshots with realistic trends (90 days × 3 accounts)..."

generate_snapshots "$ACCT1" 14 498.0 $DAYS \
  "AmazonEC2|instance:0.16 AmazonRDS|primary:0.42 AmazonElasticLoadBalancing|alb:0.04 AmazonVPC|nat_gateway:0.005 AmazonVPC|eip:0.005 AmazonEC2|volume:0.22 AmazonEC2|snapshot:0.15"
generate_snapshots "$ACCT2" 10 330.4 $DAYS \
  "AmazonEC2|instance:0.46 AWSLambda:0.02 AmazonRDS|read_replica:0.20 AmazonVPC|nat_gateway:0.015 AmazonVPC|eip:0.015 AmazonEC2|stopped_instance:0.33 AmazonEC2|ami:0.16"
generate_snapshots "$ACCT3" 9  217.7 $DAYS \
  "AmazonEC2|instance:0.22 AmazonRDS|primary:0.30 AWSLambda:0.08 AmazonElasticLoadBalancing|nlb:0.05 AmazonVPC|nat_gateway:0.03 AmazonVPC|eip:0.03 AmazonEC2|volume:0.20 AmazonEC2|ami:0.14"
echo ""

# ── Cost records ──────────────────────────────────────────────────────────────
# Seed raw cost data (Cost Explorer API records) for testing cost filtering.
# All accounts use the same AWS account ID (111111111111) matching seed script.
# 23 realistic daily cost records across multiple services from the last 30 days.

echo "Inserting cost records for all accounts (last 30 days of records)..."

psql_exec "DELETE FROM cost_records WHERE tenant_id = '${TENANT_ID}' AND account_id = '${AWS_ACCT_ID}';"

psql_pipe << EOF
INSERT INTO cost_records
  (tenant_id, provider, account_id, internal_account_id, service, region, resource_id, amount, currency, period_start, period_end, tags, fetched_at)
VALUES
  -- Daily EC2 costs (3 samples from last 30 days)
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT1}', 'AmazonEC2', 'eu-central-1', 'i-0abc123prod0001', 45.50, 'USD', NOW() - interval '3 days', NOW() - interval '2 days', '{}', '$NOW'),
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT1}', 'AmazonEC2', 'eu-central-1', 'i-0abc123prod0001', 47.20, 'USD', NOW() - interval '2 days', NOW() - interval '1 day', '{}', '$NOW'),
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT1}', 'AmazonEC2', 'eu-central-1', 'i-0abc123prod0001', 46.80, 'USD', NOW() - interval '1 day', NOW(), '{}', '$NOW'),
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT2}', 'AmazonEC2', 'us-east-1', 'i-0abc123stg0001', 38.20, 'USD', NOW() - interval '3 days', NOW() - interval '2 days', '{}', '$NOW'),
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT2}', 'AmazonEC2', 'us-east-1', 'i-0abc123stg0001', 39.10, 'USD', NOW() - interval '2 days', NOW() - interval '1 day', '{}', '$NOW'),
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT3}', 'AmazonEC2', 'eu-west-1', 'i-0abc123dev0001', 22.80, 'USD', NOW() - interval '3 days', NOW() - interval '2 days', '{}', '$NOW'),

  -- Daily RDS costs
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT1}', 'AmazonRDS', 'eu-central-1', 'db-prod-legacy-reporting', 210.40, 'USD', NOW() - interval '3 days', NOW() - interval '2 days', '{}', '$NOW'),
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT1}', 'AmazonRDS', 'eu-central-1', 'db-prod-legacy-reporting', 212.10, 'USD', NOW() - interval '2 days', NOW() - interval '1 day', '{}', '$NOW'),
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT3}', 'AmazonRDS', 'eu-west-1', 'db-dev-abandoned', 89.10, 'USD', NOW() - interval '3 days', NOW() - interval '2 days', '{}', '$NOW'),

  -- S3 costs
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT1}', 'AmazonS3', 'eu-central-1', 'prod-data-lake-bucket', 23.75, 'USD', NOW() - interval '3 days', NOW() - interval '2 days', '{}', '$NOW'),
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT1}', 'AmazonS3', 'eu-central-1', 'prod-data-lake-bucket', 24.20, 'USD', NOW() - interval '2 days', NOW() - interval '1 day', '{}', '$NOW'),
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT2}', 'AmazonS3', 'us-east-1', 'staging-backups', 15.50, 'USD', NOW() - interval '3 days', NOW() - interval '2 days', '{}', '$NOW'),

  -- CloudFront costs
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT1}', 'AmazonCloudFront', 'us-east-1', 'E1PROD0ABANDONED', 18.50, 'USD', NOW() - interval '3 days', NOW() - interval '2 days', '{}', '$NOW'),
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT1}', 'AmazonCloudFront', 'us-east-1', 'E1PROD0ABANDONED', 19.20, 'USD', NOW() - interval '2 days', NOW() - interval '1 day', '{}', '$NOW'),
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT2}', 'AmazonCloudFront', 'us-east-1', 'E2STG0OLDSITE', 8.50, 'USD', NOW() - interval '3 days', NOW() - interval '2 days', '{}', '$NOW'),

  -- Lambda costs
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT2}', 'AWSLambda', 'us-east-1', 'stg-image-resizer', 4.10, 'USD', NOW() - interval '3 days', NOW() - interval '2 days', '{}', '$NOW'),
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT2}', 'AWSLambda', 'us-east-1', 'stg-image-resizer', 4.35, 'USD', NOW() - interval '2 days', NOW() - interval '1 day', '{}', '$NOW'),
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT3}', 'AWSLambda', 'eu-west-1', 'dev-unused-email-sender', 2.30, 'USD', NOW() - interval '3 days', NOW() - interval '2 days', '{}', '$NOW'),

  -- ELB costs
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT1}', 'AmazonElasticLoadBalancing', 'eu-central-1', 'app/legacy-api/abc123prod', 18.50, 'USD', NOW() - interval '3 days', NOW() - interval '2 days', '{}', '$NOW'),
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT1}', 'AmazonElasticLoadBalancing', 'eu-central-1', 'app/legacy-api/abc123prod', 18.75, 'USD', NOW() - interval '2 days', NOW() - interval '1 day', '{}', '$NOW'),

  -- VPC NAT Gateway costs
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT1}', 'AmazonVPC', 'eu-central-1', 'nat-abc123prod', 32.40, 'USD', NOW() - interval '3 days', NOW() - interval '2 days', '{}', '$NOW'),
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT1}', 'AmazonVPC', 'eu-central-1', 'nat-abc123prod', 31.80, 'USD', NOW() - interval '2 days', NOW() - interval '1 day', '{}', '$NOW'),

  -- Data Transfer costs
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT2}', 'AWSDataTransfer', 'us-east-1', 'data-transfer-out', 12.50, 'USD', NOW() - interval '3 days', NOW() - interval '2 days', '{}', '$NOW'),

  -- Tax (simulated)
  ('${TENANT_ID}', 'aws', '${AWS_ACCT_ID}', '${ACCT1}', 'Tax', 'NoRegion', 'vat', 1.42, 'USD', NOW() - interval '3 days', NOW() - interval '2 days', '{}', '$NOW')
ON CONFLICT DO NOTHING;
EOF

echo "  Inserted cost records (EC2, RDS, S3, CloudFront, Lambda, ELB, VPC, Data Transfer, Tax)"
echo ""

# ── Verify seeded data ────────────────────────────────────────────────────────
# Quick sanity check: count rows per table for the dev tenant.

echo "=== Verifying dev tenant data ==="
GHOST_COUNT=$(psql_query "SELECT COUNT(*) FROM ghost_records WHERE tenant_id = '${TENANT_ID}';")
RESOURCE_COUNT=$(psql_query "SELECT COUNT(*) FROM resource_records WHERE tenant_id = '${TENANT_ID}';")
SNAPSHOT_COUNT=$(psql_query "SELECT COUNT(*) FROM ghost_snapshots WHERE tenant_id = '${TENANT_ID}';")
SVC_COUNT=$(psql_query "SELECT COUNT(*) FROM ghost_snapshot_services WHERE tenant_id = '${TENANT_ID}';" 2>/dev/null || echo "n/a")
COST_COUNT=$(psql_query "SELECT COUNT(*) FROM cost_records WHERE tenant_id = '${TENANT_ID}';" 2>/dev/null || echo "n/a")
echo "Dev tenant ghost records:       $GHOST_COUNT  (expected 43)"
echo "Dev tenant resource records:    $RESOURCE_COUNT  (expected 36)"
echo "Dev tenant ghost snapshots:     $SNAPSHOT_COUNT  (expected 270)"
echo "Dev tenant snapshot services:   $SVC_COUNT"
echo "Dev tenant cost records:        $COST_COUNT  (expected 21)"
echo ""

echo "=== Done ==="
echo "Dev tenant ID: ${TENANT_ID}"
echo "DEV_TENANT_ID=${TENANT_ID} is set automatically by dev.sh"
echo ""
echo "Workflow:"
echo "  make start-dev   — start all services (dev mode, no auth)"
echo "  make seed        — (re-)populate dummy data"
echo "  open http://localhost:3000"
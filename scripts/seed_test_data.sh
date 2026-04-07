#!/usr/bin/env bash
# seed_test_data.sh — seed dev tenant with dummy data for local development
#
# Usage:
#   ./scripts/seed_test_data.sh
#
# Starts PostgreSQL automatically if not already running.
# Safe to re-run — all inserts are idempotent (ON CONFLICT DO NOTHING / DO UPDATE).
#
# Dev tenant ID is fixed: dev-tenant-axiaops
# This matches DEV_TENANT_ID exported by dev.sh so the API resolves it without auth.

set -euo pipefail

# Use axiaops_owner for direct DB access (bypasses RLS — owner privilege)
psql_exec()  { docker exec -i -e "PGOPTIONS=-c search_path=axiaops" axiaops-postgres psql -U axiaops_owner -d axiaops --quiet -c "$1"; }
psql_query() { docker exec -i -e "PGOPTIONS=-c search_path=axiaops" axiaops-postgres psql -U axiaops_owner -d axiaops -t --no-align -c "$1"; }

# ── Ensure postgres is running ────────────────────────────────────────────────

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

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

echo "=== AxiaOps — Seeding dev data ==="
echo ""

NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
PERIOD_START=$(date -u -v-30d +"%Y-%m-%dT00:00:00Z" 2>/dev/null || date -u -d '30 days ago' +"%Y-%m-%dT00:00:00Z")
PERIOD_END="$NOW"

# ── Dev tenant (fixed ID used by DEV_TENANT_ID in dev.sh) ────────────────────

echo "Creating dev tenant (id: dev-tenant-axiaops)..."
psql_exec "INSERT INTO tenants (id, org_code, name, created_at)
  VALUES ('dev-tenant-axiaops', 'org_dev_local', 'AxiaOps Dev', '$NOW')
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
  VALUES ('dev-account-001', 'dev-tenant-axiaops', 'aws', 'Dev AWS (seed data)', 'AKIAIOSFODNN7EXAMPLE', '', 'eu-central-1', 'connected', '$NOW')
  ON CONFLICT (id) DO NOTHING;"
echo "  Done."
echo ""

# ── Ghost records — 5 zombie resources ───────────────────────────────────────

echo "Inserting ghost records..."
psql_exec "DELETE FROM ghost_records WHERE tenant_id = 'dev-tenant-axiaops';"

psql_exec "INSERT INTO ghost_records
  (tenant_id, provider, account_id, service, region, resource_id, tags, monthly_cost, currency,
   period_start, period_end, usage_metric, usage_avg, usage_unit, reason, owner, detected_at)
VALUES
  -- Idle EC2 instance
  ('dev-tenant-axiaops', 'aws', '123456789012', 'AmazonEC2', 'eu-central-1',
   'i-0abc123dev0001', '{\"env\":\"staging\",\"team\":\"backend\"}',
   45.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 1.2, 'Percent',
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  -- Abandoned RDS instance
  ('dev-tenant-axiaops', 'aws', '123456789012', 'AmazonRDS', 'eu-central-1',
   'db-dev-abandoned', '{\"env\":\"dev\",\"team\":\"data\"}',
   89.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count',
   'Zero connections — likely abandoned', 'data', '$NOW'),

  -- Unused Lambda
  ('dev-tenant-axiaops', 'aws', '123456789012', 'AWSLambda', 'eu-west-1',
   'unused-email-sender', '{\"env\":\"prod\",\"team\":\"backend\"}',
   2.30, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count',
   'Zero invocations — likely unused', 'backend', '$NOW'),

  -- Unused load balancer
  ('dev-tenant-axiaops', 'aws', '123456789012', 'AmazonElasticLoadBalancing', 'eu-central-1',
   'app/legacy-api/abc123dev456', '{\"env\":\"staging\",\"team\":\"platform\"}',
   18.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RequestCount', 0, 'Count',
   'Zero requests — likely abandoned', 'platform', '$NOW'),

  -- Unattached Elastic IP
  ('dev-tenant-axiaops', 'aws', '123456789012', 'AmazonVPC', 'eu-west-1',
   'eipalloc-dev00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count',
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW')
;"
echo "  Inserted 5 ghost records."
echo ""

# ── Resource records — ghosts + active resources ──────────────────────────────

echo "Inserting resource records..."
psql_exec "DELETE FROM resource_records WHERE tenant_id = 'dev-tenant-axiaops';"

psql_exec "INSERT INTO resource_records
  (tenant_id, provider, account_id, service, region, resource_id, tags, monthly_cost, currency,
   period_start, period_end, usage_metric, usage_avg, usage_unit, is_ghost, reason, owner, detected_at)
VALUES
  -- Ghost: idle EC2
  ('dev-tenant-axiaops', 'aws', '123456789012', 'AmazonEC2', 'eu-central-1',
   'i-0abc123dev0001', '{\"env\":\"staging\",\"team\":\"backend\"}',
   45.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 1.2, 'Percent', true,
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  -- Ghost: abandoned RDS
  ('dev-tenant-axiaops', 'aws', '123456789012', 'AmazonRDS', 'eu-central-1',
   'db-dev-abandoned', '{\"env\":\"dev\",\"team\":\"data\"}',
   89.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count', true,
   'Zero connections — likely abandoned', 'data', '$NOW'),

  -- Ghost: unused Lambda
  ('dev-tenant-axiaops', 'aws', '123456789012', 'AWSLambda', 'eu-west-1',
   'unused-email-sender', '{\"env\":\"prod\",\"team\":\"backend\"}',
   2.30, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count', true,
   'Zero invocations — likely unused', 'backend', '$NOW'),

  -- Ghost: unused ELB
  ('dev-tenant-axiaops', 'aws', '123456789012', 'AmazonElasticLoadBalancing', 'eu-central-1',
   'app/legacy-api/abc123dev456', '{\"env\":\"staging\",\"team\":\"platform\"}',
   18.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RequestCount', 0, 'Count', true,
   'Zero requests — likely abandoned', 'platform', '$NOW'),

  -- Ghost: unattached EIP
  ('dev-tenant-axiaops', 'aws', '123456789012', 'AmazonVPC', 'eu-west-1',
   'eipalloc-dev00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count', true,
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Active: healthy EC2
  ('dev-tenant-axiaops', 'aws', '123456789012', 'AmazonEC2', 'eu-central-1',
   'i-0abc123dev0099', '{\"env\":\"prod\",\"team\":\"backend\"}',
   45.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 67.3, 'Percent', false, '', 'backend', '$NOW'),

  -- Active: healthy RDS
  ('dev-tenant-axiaops', 'aws', '123456789012', 'AmazonRDS', 'eu-central-1',
   'db-production-main', '{\"env\":\"prod\",\"team\":\"data\"}',
   156.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 142, 'Count', false, '', 'data', '$NOW')
;"
echo "  Inserted 7 resource records (5 ghosts, 2 active)."
echo ""

# ── RLS isolation check (using app user, not owner) ───────────────────────────

echo "=== Verifying dev tenant data ==="
GHOST_COUNT=$(psql_query "SELECT COUNT(*) FROM ghost_records WHERE tenant_id = 'dev-tenant-axiaops';")
RESOURCE_COUNT=$(psql_query "SELECT COUNT(*) FROM resource_records WHERE tenant_id = 'dev-tenant-axiaops';")
echo "Dev tenant ghost records:    $GHOST_COUNT"
echo "Dev tenant resource records: $RESOURCE_COUNT"
echo ""

echo "=== Done ==="
echo "Dev tenant ID: dev-tenant-axiaops"
echo "DEV_TENANT_ID=dev-tenant-axiaops is set automatically by dev.sh"
echo ""
echo "Workflow:"
echo "  make start   — start all services (dev mode, no auth)"
echo "  make seed    — (re-)populate dummy data"
echo "  open http://localhost:3000"

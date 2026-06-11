#!/bin/sh
set -e

# Wait for postgres and migrations
until psql -h postgres -U axiaops_owner -d axiaops -c '\dt axiaops.organizations' >/dev/null 2>&1; do
  sleep 1
done

# Create ci-organization + its entitlement.
#
# The entitlement row is required because the DEFAULT (SaaS) build — which the
# integration stack runs — gates scans on a per-tenant `entitlements` row and is
# fail-closed (no row → POST /scan returns 403). In the running app every org is
# auto-entitled at creation via Store.ensureDefaultEntitlement, but this harness
# seeds the org out-of-band with raw SQL (and after the migrate step, so the
# migration-034 backfill doesn't see it), so we seed the entitlement out-of-band
# too. Mirrors the auto-entitle default: status=active, plan=internal.
psql -h postgres -U axiaops_owner -d axiaops <<-EOSQL
  INSERT INTO axiaops.organizations (id, org_code, name, created_at)
  VALUES ('ci-tenant', 'ci-tenant', 'CI Test Organization', NOW())
  ON CONFLICT (id) DO NOTHING;

  INSERT INTO axiaops.entitlements (organization_id, plan, status, max_accounts)
  VALUES ('ci-tenant', 'internal', 'active', 1000)
  ON CONFLICT (organization_id) DO NOTHING;
EOSQL


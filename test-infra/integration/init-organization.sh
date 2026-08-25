#!/bin/sh
set -e

# Wait for postgres and migrations
until psql -h postgres -U axiaops_owner -d axiaops -c '\dt axiaops.organizations' >/dev/null 2>&1; do
  sleep 1
done

# Create ci-organization. No entitlement/license row to seed — scans run
# unconditionally for any org with a connected account.
psql -h postgres -U axiaops_owner -d axiaops <<-EOSQL
  INSERT INTO axiaops.organizations (id, org_code, name, created_at)
  VALUES ('ci-tenant', 'ci-tenant', 'CI Test Organization', NOW())
  ON CONFLICT (id) DO NOTHING;
EOSQL


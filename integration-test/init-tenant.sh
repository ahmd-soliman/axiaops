#!/bin/sh
set -e

# Wait for postgres and migrations
until psql -h postgres -U axiaops_owner -d axiaops -c '\dt axiaops.tenants' >/dev/null 2>&1; do
  sleep 1
done

# Create ci-tenant
psql -h postgres -U axiaops_owner -d axiaops <<-EOSQL
  INSERT INTO axiaops.tenants (id, org_code, name, created_at)
  VALUES ('ci-tenant', 'CI', 'CI Test Tenant', NOW())
  ON CONFLICT (id) DO NOTHING;
EOSQL


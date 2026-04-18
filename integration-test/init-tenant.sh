#!/bin/sh
set -e

# Wait for postgres to be ready
until pg_isready -h postgres -U axiaops_owner -d axiaops; do
  echo "Waiting for postgres..."
  sleep 1
done

# Create ci-tenant if it doesn't exist
psql -h postgres -U axiaops_owner -d axiaops <<-EOSQL
  INSERT INTO axiaops.tenants (id, org_code, name, created_at)
  VALUES ('ci-tenant', 'CI', 'CI Test Tenant', NOW())
  ON CONFLICT (id) DO NOTHING;
EOSQL

echo "ci-tenant ready"

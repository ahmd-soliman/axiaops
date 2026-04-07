#!/usr/bin/env bash
# db_init.sh — idempotent infrastructure setup for PostgreSQL.
#
# Creates the axiaops app user, schema, and default privileges.
# Safe to run multiple times. Must be run as axiaops_owner (DDL access).
#
# Usage:
#   ./scripts/db_init.sh                         # local dev (defaults)
#   DB_HOST=postgres ./scripts/db_init.sh        # CI / remote host
#
# Environment variables (all optional, defaults match docker-compose):
#   DB_HOST      — PostgreSQL host        (default: localhost)
#   DB_PORT      — PostgreSQL port        (default: 5432)
#   DB_NAME      — Database name          (default: axiaops)
#   DB_OWNER     — Owner/admin user       (default: axiaops_owner)
#   PGPASSWORD   — Password for DB_OWNER  (default: axiaops_owner)

set -euo pipefail

DB_HOST="${DB_HOST:-localhost}"
DB_PORT="${DB_PORT:-5432}"
DB_NAME="${DB_NAME:-axiaops}"
DB_OWNER="${DB_OWNER:-axiaops_owner}"
export PGPASSWORD="${PGPASSWORD:-axiaops_owner}"

INIT_SQL="$(cd "$(dirname "$0")/.." && pwd)/services/shared/storage/postgres/init.sql"

echo "db-init: applying $INIT_SQL to $DB_HOST:$DB_PORT/$DB_NAME as $DB_OWNER"
psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_OWNER" -d "$DB_NAME" -f "$INIT_SQL"
echo "db-init: done."

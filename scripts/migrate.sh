#!/usr/bin/env bash
# migrate.sh — run database migrations locally
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

DATABASE_URL="${DATABASE_URL:-postgres://axiaops:axiaops@localhost:5432/axiaops?sslmode=disable}"
MIGRATION_DATABASE_URL="${MIGRATION_DATABASE_URL:-postgres://axiaops_owner:axiaops_owner@localhost:5432/axiaops?sslmode=disable}"

cd "$ROOT/services/migrate"
DATABASE_URL="$DATABASE_URL" MIGRATION_DATABASE_URL="$MIGRATION_DATABASE_URL" go run .

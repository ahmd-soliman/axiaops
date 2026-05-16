#!/usr/bin/env bash
# migrate.sh — run database migrations locally
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

DATABASE_URL="${DATABASE_URL:-postgres://axiaops:axiaops@localhost:5432/axiaops?sslmode=disable}"
MIGRATION_DATABASE_URL="${MIGRATION_DATABASE_URL:-postgres://axiaops_owner:axiaops_owner@localhost:5432/axiaops?sslmode=disable}"

# Inject build identifiers so axiaops.migration_history.applied_by_image
# records something more useful than "unknown@unknown" on local runs. CI
# sets these via Dockerfile --build-arg; on a developer laptop we derive
# them from the working tree. Honours pre-set values so an operator can
# override (e.g. simulating a CI build locally).
APP_VERSION="${APP_VERSION:-dev}"
APP_COMMIT_SHA="${APP_COMMIT_SHA:-$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo local)}"

cd "$ROOT/services/migrate"
DATABASE_URL="$DATABASE_URL" \
MIGRATION_DATABASE_URL="$MIGRATION_DATABASE_URL" \
APP_VERSION="$APP_VERSION" \
APP_COMMIT_SHA="$APP_COMMIT_SHA" \
go run .

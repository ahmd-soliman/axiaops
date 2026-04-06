#!/usr/bin/env bash
# clean_db.sh — wipe all data from the local PostgreSQL database
#
# Usage:
#   ./scripts/clean_db.sh          truncate all tables (keeps schema)
#   ./scripts/clean_db.sh --hard   drop and recreate the entire volume

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

if ! docker ps --format '{{.Names}}' 2>/dev/null | grep -q "^axiaops-postgres$"; then
  echo "PostgreSQL is not running. Start it with: ./scripts/dev.sh"
  exit 1
fi

if [[ "${1:-}" == "--hard" ]]; then
  echo "Hard reset: removing PostgreSQL volume..."
  docker compose -f "$ROOT/docker-compose.yml" down -v
  echo "Done. Run ./scripts/dev.sh to start fresh."
  exit 0
fi

echo "Truncating all tables..."
docker exec -i axiaops-postgres psql -U axiaops_admin -d axiaops --quiet \
  -c "SET search_path TO axiaops" \
  -c "TRUNCATE ghost_records, cost_records, users, tenants RESTART IDENTITY CASCADE;"
echo "Done. All data removed, schema intact."

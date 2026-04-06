#!/usr/bin/env bash
# dev.sh — start all AxiaOps services for local development
#
# Usage:
#   ./scripts/dev.sh           start with fixture data + PostgreSQL (default)
#   ./scripts/dev.sh --aws     start with real AWS + PostgreSQL
#   ./scripts/dev.sh --sqlite  use SQLite instead of PostgreSQL (no Docker needed)
#   ./scripts/dev.sh stop      kill all running services

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
API_DIR="$ROOT/services/api"
INGESTION_DIR="$ROOT/services/ingestion"
DASHBOARD_DIR="$ROOT/services/dashboard"
PID_FILE="$ROOT/.dev-pids"
LOG_FILE="$ROOT/.dev.log"
DB_PATH="$ROOT/axiaops.db"

stop() {
  if [[ -f "$PID_FILE" ]]; then
    echo "Stopping services..."
    while IFS= read -r pid; do
      # Kill children first (compiled binary, node), then the parent (go run, npx)
      pkill -P "$pid" 2>/dev/null || true
      kill "$pid" 2>/dev/null || true
      echo "  stopped $pid"
    done < "$PID_FILE"
    rm -f "$PID_FILE"
  fi
  if docker ps --format '{{.Names}}' 2>/dev/null | grep -q "^axiaops-postgres$"; then
    echo "Stopping PostgreSQL..."
    docker compose -f "$ROOT/docker-compose.yml" stop postgres
  fi
  echo "Done."
}

if [[ "${1:-}" == "stop" ]]; then
  stop
  exit 0
fi

# Parse flags
DEV_MODE=true
USE_SQLITE=false
for arg in "$@"; do
  case "$arg" in
    --aws)    DEV_MODE=false ;;
    --sqlite) USE_SQLITE=true ;;
  esac
done

# Kill any previous session cleanly
[[ -f "$PID_FILE" ]] && stop

# Storage — PostgreSQL by default, SQLite if --sqlite is passed
DATABASE_URL=""
if [[ "$USE_SQLITE" == "true" ]]; then
  echo "Storage: SQLite ($DB_PATH)"
else
  echo "Starting PostgreSQL          →  localhost:5432"
  docker compose -f "$ROOT/docker-compose.yml" up -d postgres
  echo "Waiting for PostgreSQL to be ready..."
  until docker exec axiaops-postgres pg_isready -U axiaops_admin &>/dev/null; do sleep 1; done
  DATABASE_URL="postgres://axiaops:axiaops@localhost:5432/axiaops"
  echo "PostgreSQL ready."
fi
echo ""

# Fresh log file for this run
: > "$LOG_FILE"
echo "Logging to $LOG_FILE"
echo ""

echo "Starting ingestion job       (one-shot, DEV_MODE=$DEV_MODE)"
cd "$INGESTION_DIR"
set -a; [ -f .env ] && source .env; set +a
DEV_MODE=$DEV_MODE DB_PATH="$DB_PATH" DATABASE_URL="$DATABASE_URL" go run ./cmd/main.go >> "$LOG_FILE" 2>&1
echo ""

echo "Starting API service        →  http://localhost:8080"
cd "$API_DIR"
set -a; [ -f "$ROOT/services/ingestion/.env" ] && source "$ROOT/services/ingestion/.env"; set +a
DB_PATH="$DB_PATH" DATABASE_URL="$DATABASE_URL" go run ./cmd/main.go >> "$LOG_FILE" 2>&1 &
API_PID=$!
echo $API_PID >> "$PID_FILE"
disown $API_PID

echo "Starting dashboard          →  http://localhost:8081"
cd "$DASHBOARD_DIR"
npx expo start --web --non-interactive >> "$LOG_FILE" 2>&1 &
DASH_PID=$!
echo $DASH_PID >> "$PID_FILE"
disown $DASH_PID

echo ""
echo "Services running."
echo "  Logs:  tail -f $ROOT/.dev.log"
echo "  Stop:  ./scripts/dev.sh stop"

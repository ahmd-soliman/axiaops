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

# Ensure we are in the root for docker-compose commands
cd "$ROOT"

stop() {
  echo "Cleaning up..."
  if [[ -f "$PID_FILE" ]]; then
    while IFS= read -r pid; do
      if kill -0 "$pid" 2>/dev/null; then
        # Kill process group to catch sub-processes
        kill -TERM -"$pid" 2>/dev/null || kill "$pid" 2>/dev/null
        echo "  Stopped process $pid"
      fi
    done < "$PID_FILE"
    rm -f "$PID_FILE"
  fi

  # Check if postgres container exists and is running
  if docker compose ps postgres --status running | grep -q "postgres"; then
    echo "Stopping PostgreSQL..."
    docker compose stop postgres
  fi
  echo "Done."
}

# Trap unexpected exits (like Ctrl+C during startup)
trap stop ERR SIGINT SIGTERM

if [[ "${1:-}" == "stop" ]]; then
  stop
  exit 0
fi

# Flags
DEV_MODE=true
USE_SQLITE=false
for arg in "$@"; do
  case "$arg" in
    --aws)    DEV_MODE=false ;;
    --sqlite) USE_SQLITE=true ;;
  esac
done

# Clear logs
: > "$LOG_FILE"

# Database Setup
if [[ "$USE_SQLITE" == "true" ]]; then
  export DATABASE_URL="sqlite://$DB_PATH"
  echo "Storage: SQLite ($DB_PATH)"
else
  docker compose up -d postgres
  echo -n "Waiting for PostgreSQL..."
  until docker exec axiaops-postgres pg_isready -U axiaops_owner &>/dev/null; do
    echo -n "."
    sleep 1
  done
  export DATABASE_URL="postgres://axiaops:axiaops@localhost:5432/axiaops"
  echo " Ready."
fi

# Run Ingestion (One-shot)
echo "Running ingestion job..."
cd "$INGESTION_DIR"
if [[ "$DEV_MODE" == "false" && -f .env ]]; then
    set -a; source .env; set +a
fi
# Run in background but wait for it or run foreground if it's fast
DEV_MODE=$DEV_MODE DB_PATH="$DB_PATH" go run ./cmd/main.go >> "$LOG_FILE" 2>&1

# Start API
echo "Starting API service (8080)..."
cd "$API_DIR"
# Use a subshell to run and capture PID without disowning immediately
(
  export DB_PATH="$DB_PATH"
  export DATABASE_URL="$DATABASE_URL"
  exec go run ./cmd/main.go >> "$LOG_FILE" 2>&1
) &
echo $! >> "$PID_FILE"

until curl -sf http://localhost:8080/health &>/dev/null; do sleep 1; done

# Start Dashboard
echo "Starting Dashboard (8081)..."
cd "$DASHBOARD_DIR"
npx expo start --web --non-interactive >> "$LOG_FILE" 2>&1 &
echo $! >> "$PID_FILE"

echo "---------------------------------------"
echo "All systems go."
echo "Logs: tail -f .dev.log"
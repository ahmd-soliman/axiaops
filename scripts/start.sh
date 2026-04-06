#!/usr/bin/env bash
# start.sh — start all AxiaOps services locally
#
# Usage:
#   ./scripts/start.sh                    start in dev mode (no auth, fixed tenant)
#   ./scripts/start.sh --sqlite           use SQLite instead of PostgreSQL (no Docker needed)
#   ./scripts/start.sh stop               kill all running services
#   DEV_MODE=false ./scripts/start.sh     start in staging mode (real Kinde auth + real AWS)

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

  # Kill any leftover processes still bound to the service ports
  for port in 8080 8081 3000; do
    local pids
    pids=$(lsof -ti :"$port" 2>/dev/null || true)
    if [[ -n "$pids" ]]; then
      echo "  Killing stale process(es) on port $port: $pids"
      echo "$pids" | xargs kill -9 2>/dev/null || true
    fi
  done

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
USE_SQLITE=false
for arg in "$@"; do
  case "$arg" in
    --sqlite) USE_SQLITE=true ;;
  esac
done

# Clear logs
: > "$LOG_FILE"

# Database Setup
if [[ "$USE_SQLITE" == "true" ]]; then
  unset DATABASE_URL
  echo "Storage: SQLite ($DB_PATH)"
else
  docker compose up -d postgres
  echo -n "Waiting for PostgreSQL..."
  until docker exec axiaops-postgres pg_isready -U axiaops_owner -d axiaops &>/dev/null; do
    echo -n "."
    sleep 1
  done
  export DATABASE_URL="postgres://axiaops:axiaops@localhost:5432/axiaops"
  echo " Ready."
fi

# Start Ingestion service (long-running HTTP server on :8081)
echo "Starting ingestion service (8081)..."
cd "$INGESTION_DIR"
if [[ -f .env ]]; then
    set -a; source .env; set +a
fi
(
  export DB_PATH="$DB_PATH"
  export DATABASE_URL="${DATABASE_URL:-}"
  exec go run ./cmd/main.go >> "$LOG_FILE" 2>&1
) &
echo $! >> "$PID_FILE"

until curl -sf http://localhost:8081/health &>/dev/null; do sleep 1; done

# Start API — respect DEV_MODE from the caller (default true for local dev).
echo "Starting API service (8080)..."
cd "$API_DIR"
(
  export DB_PATH="$DB_PATH"
  export DATABASE_URL="${DATABASE_URL:-}"
  export DEV_MODE="${DEV_MODE:-true}"
  if [[ "$DEV_MODE" == "true" ]]; then
    export DEV_TENANT_ID="dev-tenant-axiaops"
  else
    if [[ -f .env ]]; then
      set -a; source .env; set +a
    fi
  fi
  exec go run ./cmd/main.go >> "$LOG_FILE" 2>&1
) &
echo $! >> "$PID_FILE"

until curl -sf http://localhost:8080/health &>/dev/null; do sleep 1; done

# Start Dashboard
echo "Starting Dashboard (3000)..."
cd "$DASHBOARD_DIR"
export EXPO_PUBLIC_KINDE_ISSUER="${KINDE_ISSUER:-}"
export EXPO_PUBLIC_KINDE_CLIENT_ID="${KINDE_CLIENT_ID:-}"
export EXPO_PUBLIC_DEV_MODE="${DEV_MODE:-true}"
npx expo start --web --port 3000 --non-interactive --clear >> "$LOG_FILE" 2>&1 &
echo $! >> "$PID_FILE"

echo "---------------------------------------"
echo "All systems go."
echo "Logs: tail -f .dev.log"
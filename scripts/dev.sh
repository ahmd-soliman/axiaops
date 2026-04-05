#!/usr/bin/env bash
# dev.sh — start all AxiaOps services for local development
#
# Usage:
#   ./scripts/dev.sh          start with fixture data (DEV_MODE=true)
#   ./scripts/dev.sh --aws    start with real AWS (DEV_MODE=false)
#   ./scripts/dev.sh stop     kill all running services

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
API_DIR="$ROOT/services/api"
INGESTION_DIR="$ROOT/services/ingestion"
DASHBOARD_DIR="$ROOT/services/dashboard"
PID_FILE="$ROOT/.dev-pids"
DB_PATH="$ROOT/axiaops.db"

stop() {
  if [[ ! -f "$PID_FILE" ]]; then
    echo "No running dev session found."
    exit 0
  fi
  echo "Stopping services..."
  while IFS= read -r pid; do
    kill "$pid" 2>/dev/null && echo "  killed $pid" || true
  done < "$PID_FILE"
  rm -f "$PID_FILE"
  echo "Done."
}

if [[ "${1:-}" == "stop" ]]; then
  stop
  exit 0
fi

# Parse flags
DEV_MODE=true
if [[ "${1:-}" == "--aws" ]]; then
  DEV_MODE=false
fi

# Kill any previous session cleanly
[[ -f "$PID_FILE" ]] && stop

echo "Starting ingestion job       (one-shot, DEV_MODE=$DEV_MODE)"
cd "$INGESTION_DIR"
set -a; [ -f .env ] && source .env; set +a
DEV_MODE=$DEV_MODE DB_PATH="$DB_PATH" go run ./cmd/main.go
echo ""

echo "Starting API service        →  http://localhost:8080"
cd "$API_DIR"
set -a; [ -f "$ROOT/services/ingestion/.env" ] && source "$ROOT/services/ingestion/.env"; set +a
DB_PATH="$DB_PATH" go run ./cmd/main.go &
echo $! >> "$PID_FILE"

echo "Starting dashboard          →  http://localhost:8081"
cd "$DASHBOARD_DIR"
npm run web &
echo $! >> "$PID_FILE"

echo ""
echo "Both services running. Press Ctrl+C to stop."

trap stop INT TERM
wait

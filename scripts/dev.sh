#!/usr/bin/env bash
# dev.sh — start all AxiaOps services for local development
#
# Usage:
#   ./dev.sh          start both services
#   ./dev.sh stop     kill both services
#
# Services:
#   ingestion API  →  http://localhost:8080
#   dashboard      →  http://localhost:8081

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
INGESTION_DIR="$ROOT/services/ingestion"
DASHBOARD_DIR="$ROOT/services/dashboard"
PID_FILE="$ROOT/.dev-pids"

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

# Kill any previous session cleanly
[[ -f "$PID_FILE" ]] && stop

echo "Starting ingestion service  →  http://localhost:8080"
cd "$INGESTION_DIR"
set -a; [ -f .env ] && source .env; set +a
DEV_MODE=true go run ./cmd/main.go &
echo $! >> "$PID_FILE"

echo "Starting dashboard          →  http://localhost:8081"
cd "$DASHBOARD_DIR"
npm run web &
echo $! >> "$PID_FILE"

echo ""
echo "Both services running. Press Ctrl+C to stop."

trap stop INT TERM
wait

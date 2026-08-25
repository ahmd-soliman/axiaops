#!/usr/bin/env bash
# start.sh — start all AxiaOps services locally
#
# Usage:
#   ./scripts/start.sh                    start in dev mode (bypass auth with fixed organization)
#   ./scripts/start.sh stop               kill all running services
#   DEV_MODE=false ./scripts/start.sh     start in staging mode (native cookie-session auth + OIDC SSO)

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
API_DIR="$ROOT/services/api"
INGESTION_DIR="$ROOT/services/ingestion"
DASHBOARD_DIR="$ROOT/services/dashboard"
PID_FILE="$ROOT/.dev-pids"
LOG_FILE="$ROOT/.dev.log"

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
  for port in 8080 8081 5173; do
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

if [[ "${1:-}" == "stop" ]]; then
  stop
  exit 0
fi

# Clear logs
: > "$LOG_FILE"

# Database Setup
docker compose up -d postgres
echo -n "Waiting for PostgreSQL..."
until docker exec axiaops-postgres pg_isready -U axiaops_owner -d axiaops &>/dev/null; do
  echo -n "."
  sleep 1
done
echo " Ready."
export DATABASE_URL="postgres://axiaops:axiaops@localhost:5432/axiaops?sslmode=disable"
export MIGRATION_DATABASE_URL="postgres://axiaops_owner:axiaops_owner@localhost:5432/axiaops?sslmode=disable"
# Runtime RLS-bypass role — api/ingestion connect as this least-privilege role
# (axiaops_runtime) instead of the schema owner; migrate.sh (run just below)
# bootstraps it. See docs/runtime-admin-db-role.md.
export RUNTIME_ADMIN_DATABASE_URL="postgres://axiaops_runtime:axiaops_runtime@localhost:5432/axiaops?sslmode=disable"

# Run migrations
echo "Running migrations..."
"$ROOT/scripts/migrate.sh"

# Capture DEV_MODE before subshells source .env (which may override it)
CALLER_DEV_MODE="${DEV_MODE:-true}"

# Default LOG_OUTPUT=text in local dev for human-readable logs. Logging
# previously consulted DEV_MODE directly, but B1.7 layer 3 (plan §4.10.2)
# moved that read out of services/shared/logging/logging.go to keep the
# build-tag-gated devModeEnabled() seam in services/{api,ingestion}/cmd/
# the single source of truth. Local dev sets LOG_OUTPUT=text here; CI /
# staging / prod leave LOG_OUTPUT unset so logs stay JSON for ingestion.
if [[ "$CALLER_DEV_MODE" == "true" && -z "${LOG_OUTPUT:-}" ]]; then
  export LOG_OUTPUT=text
fi

# Start Ingestion service (long-running HTTP server on :8081)
echo "Starting ingestion service (8081)..."
cd "$INGESTION_DIR"
(
  export DATABASE_URL="$DATABASE_URL"
  export MIGRATION_DATABASE_URL="$MIGRATION_DATABASE_URL"
  export RUNTIME_ADMIN_DATABASE_URL="$RUNTIME_ADMIN_DATABASE_URL"
  if [[ -f .env ]]; then
    set -a; source .env; set +a
  fi
  exec go run ./cmd/
) >> "$LOG_FILE" 2>&1 &
INGESTION_PID=$!
echo $INGESTION_PID >> "$PID_FILE"
disown $INGESTION_PID

until curl -sf http://localhost:8081/health &>/dev/null; do sleep 1; done

# Start API — respect DEV_MODE from the caller (default true for local dev).
echo "Starting API service (8080)..."
cd "$API_DIR"
(
  export DATABASE_URL="$DATABASE_URL"
  export MIGRATION_DATABASE_URL="$MIGRATION_DATABASE_URL"
  export RUNTIME_ADMIN_DATABASE_URL="$RUNTIME_ADMIN_DATABASE_URL"
  if [[ -f .env ]]; then
    set -a; source .env; set +a
  fi
  export DEV_MODE="$CALLER_DEV_MODE"
  if [[ "$DEV_MODE" == "true" ]]; then
    export DEV_ORGANIZATION_ID="dev-organization-axiaops"
  fi
  exec go run ./cmd/
) >> "$LOG_FILE" 2>&1 &
API_PID=$!
echo $API_PID >> "$PID_FILE"
disown $API_PID

until curl -sf http://localhost:8080/health &>/dev/null; do sleep 1; done

# Start Dashboard
echo "Starting Dashboard (5173)..."
cd "$DASHBOARD_DIR"
echo "Installing dashboard dependencies..."
npm install --silent --no-audit
export VITE_DEV_MODE="$CALLER_DEV_MODE"
export VITE_DEV_ORG_NAME="${DEV_ORG_NAME:-AxiaOps Dev}"
npm run dev >> "$LOG_FILE" 2>&1 &
DASHBOARD_PID=$!
echo $DASHBOARD_PID >> "$PID_FILE"
disown $DASHBOARD_PID

echo "---------------------------------------"
echo "All systems go."
echo "  API           http://localhost:8080"
echo "  Ingestion     http://localhost:8081"
echo "  Dashboard     http://localhost:5173"
echo "Logs: tail -f .dev.log"
echo ""
echo "Services are running in the background. Use 'make stop' to shut them down."

# Keep the script alive so the process group stays intact.
# `|| true` prevents set -e from exiting if a background job exits non-zero.
wait || true
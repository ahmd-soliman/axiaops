#!/usr/bin/env bash
# clean_db.sh — clean AxiaOps database (local docker or remote)
#
# Usage:
#   ./scripts/clean_db.sh                                  # Local docker (truncate)
#   ./scripts/clean_db.sh --drop-schema                    # Local docker (drop schema)
#   ./scripts/clean_db.sh --remote dev-1                   # Remote dev-1   (192.0.2.121:5432)
#   ./scripts/clean_db.sh --remote dev-2                   # Remote dev-2   (192.0.2.123:5432)
#   ./scripts/clean_db.sh --remote staging --drop-schema   # Remote staging (192.0.2.122:5432)
#   ./scripts/clean_db.sh --remote preview                 # Remote preview (192.0.2.124:5432)
#   ./scripts/clean_db.sh --remote demo                    # Remote demo    (192.0.2.126:5432)
#   ./scripts/clean_db.sh --remote integration             # Remote integ.  (192.0.2.130:5432)
#
# Each env runs on its own self-hosted container — postgres listens on the
# standard 5432 since per-host means no port collision. Static IPs
# come from self-hosted-infra/stacks/*/variables.tf; using IPs (not the
# axiaops-<env>.local mDNS hostnames) keeps the script working over
# Tailscale subnet routing where mDNS doesn't traverse.

set -euo pipefail

# ── psql discovery ────────────────────────────────────────────────────────────
# Homebrew's libpq formula is keg-only — /opt/homebrew/opt/libpq/bin/psql exists
# but isn't on PATH unless the user added the export to ~/.zshrc. Probe known
# locations so the remote mode works regardless of shell setup. Mirrors the
# helper in seed_test_data.sh — keep them in sync.

resolve_psql() {
  if command -v psql >/dev/null 2>&1; then
    command -v psql
    return
  fi
  local p
  for p in /opt/homebrew/opt/libpq/bin/psql \
           /opt/homebrew/opt/postgresql@16/bin/psql \
           /usr/local/opt/libpq/bin/psql \
           /opt/homebrew/bin/psql \
           /usr/local/bin/psql; do
    if [ -x "$p" ]; then
      echo "$p"
      return
    fi
  done
}

# ── Parse arguments ───────────────────────────────────────────────────────────

REMOTE_ENV=""
DROP_SCHEMA=false
AUTO_YES=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --remote)
      shift
      REMOTE_ENV="${1:-}"
      case "$REMOTE_ENV" in
        dev-1|dev-2|staging|preview|demo|integration) ;;
        *)
          echo "Error: --remote requires 'dev-1', 'dev-2', 'staging', 'preview', 'demo', or 'integration', got '$REMOTE_ENV'"
          exit 1
          ;;
      esac
      ;;
    --drop-schema) DROP_SCHEMA=true ;;
    --yes|-y) AUTO_YES=true ;;
    *) echo "Error: Unknown flag '$1'"; exit 1 ;;
  esac
  shift
done

# ── Connection setup ──────────────────────────────────────────────────────────

if [[ -n "$REMOTE_ENV" ]]; then
  PSQL=$(resolve_psql)
  if [ -z "${PSQL:-}" ]; then
    echo "Error: psql not found on PATH or known libpq locations." >&2
    echo "  Install:  brew install libpq" >&2
    echo "  Then add to PATH (or rely on this script's auto-discovery):" >&2
    echo "    echo 'export PATH=\"/opt/homebrew/opt/libpq/bin:\$PATH\"' >> ~/.zshrc" >&2
    exit 1
  fi

  # Remote mode
  MODE="remote"
  # Per-env static IPs (from self-hosted-infra/stacks/*/variables.tf). Using
  # IPs not hostnames so the script keeps working over Tailscale subnet
  # routing where mDNS doesn't traverse. See seed_test_data.sh for the
  # full rationale.
  case "$REMOTE_ENV" in
    dev-1)       HOST_IP="192.0.2.121" ;;
    dev-2)       HOST_IP="192.0.2.123" ;;
    staging)     HOST_IP="192.0.2.122" ;;
    preview)     HOST_IP="192.0.2.124" ;;
    demo)        HOST_IP="192.0.2.126" ;;
    integration) HOST_IP="192.0.2.130" ;;
  esac
  DB_PORT=5432

  SUPERUSER_URL="postgres://axiaops_owner:axiaops_owner@$HOST_IP:$DB_PORT/axiaops?sslmode=disable"

  psql_exec()  { PGOPTIONS="-c search_path=axiaops" "$PSQL" "$SUPERUSER_URL" --quiet -c "$1"; }
  psql_query() { PGOPTIONS="-c search_path=axiaops" "$PSQL" "$SUPERUSER_URL" -t --no-align -c "$1"; }
  psql_super() { "$PSQL" "$SUPERUSER_URL" --quiet -c "$1"; }
  
  if [[ "$DROP_SCHEMA" == "true" ]]; then
    echo "=== DROPPING AxiaOps $REMOTE_ENV schema ==="
    echo "Target:    $HOST_IP:$DB_PORT"
    echo ""
    echo "⚠️  ⚠️  ⚠️  DESTRUCTIVE OPERATION ⚠️  ⚠️  ⚠️"
    echo "This will DROP the entire 'axiaops' schema and user."
    echo "All data will be permanently deleted."
    echo "You will need to re-run migrations to recreate the schema."
  else
    echo "=== Cleaning AxiaOps $REMOTE_ENV database ==="
    echo "Target:    $HOST_IP:$DB_PORT"
    echo ""
    echo "WARNING: This will DELETE ALL rows from all tables."
  fi
else
  # Local mode
  MODE="local"
  
  psql_exec()  { docker-compose exec -T postgres psql -U axiaops_owner -d axiaops -c "$1"; }
  psql_super() { docker-compose exec -T postgres psql -U axiaops_owner -d axiaops -c "$1"; }
  
  if [[ "$DROP_SCHEMA" == "true" ]]; then
    echo "=== DROPPING local AxiaOps schema ==="
    echo ""
    echo "⚠️  ⚠️  ⚠️  DESTRUCTIVE OPERATION ⚠️  ⚠️  ⚠️"
    echo "This will DROP the entire 'axiaops' schema and user."
    echo "All data will be permanently deleted."
    echo "You will need to re-run migrations to recreate the schema."
  else
    echo "=== Cleaning local AxiaOps database ==="
    echo ""
    echo "WARNING: This will DELETE ALL rows from all tables."
  fi
fi

echo ""

# ── Confirmation ──────────────────────────────────────────────────────────────

if [[ "$AUTO_YES" != "true" ]]; then
  if [[ -t 0 ]]; then
    read -r -p "Are you sure? [y/N] " CONFIRM
    if [[ "$CONFIRM" != "y" && "$CONFIRM" != "Y" ]]; then
      echo "Aborted."
      exit 0
    fi
  else
    echo "Error: stdin is not a terminal. Use --yes to confirm non-interactively."
    exit 1
  fi
fi
echo ""

# ── Verify connection (remote only) ───────────────────────────────────────────

if [[ "$MODE" == "remote" ]]; then
  echo -n "Checking connection to $HOST_IP..."
  if err=$("$PSQL" "$SUPERUSER_URL" -c 'SELECT 1' 2>&1 >/dev/null); then
    echo " Connected."
  else
    echo " Failed."
    echo "Error: connection check at $HOST_IP:$DB_PORT failed."
    if [ -n "${err:-}" ]; then
      echo "psql output:"
      printf '  %s\n' "$err"
    fi
    exit 1
  fi
  echo ""
fi

# ── Drop schema (destructive) ─────────────────────────────────────────────────

if [[ "$DROP_SCHEMA" == "true" ]]; then
  echo "Dropping schema 'axiaops'..."
  # schema_migrations lives inside axiaops now, so CASCADE handles it.
  psql_super "DROP SCHEMA IF EXISTS axiaops CASCADE;"
  echo "Revoking privileges from user 'axiaops'..."
  psql_super "REVOKE ALL PRIVILEGES ON DATABASE axiaops FROM axiaops;" 2>/dev/null || true
  echo "Dropping user 'axiaops'..."
  psql_super "DROP USER IF EXISTS axiaops;"
  echo ""
  echo "=== Schema dropped ==="
  echo "Run migrations to recreate the schema."
  exit 0
fi

# ── Truncate all data ─────────────────────────────────────────────────────────

echo "Truncating all tables..."
# schema_migrations is intentionally NOT truncated — that's what --drop-schema is for.
# Truncating it would leave data tables intact but make golang-migrate think no
# migrations have been applied, causing re-run failures on next service startup.
psql_exec "TRUNCATE TABLE axiaops.zombie_snapshot_services, axiaops.zombie_snapshots, axiaops.dismissed_zombies, axiaops.resource_records, axiaops.zombie_records, axiaops.cost_records, axiaops.accounts, axiaops.users, axiaops.organizations RESTART IDENTITY CASCADE;" 2>/dev/null || true
echo "  Done."
echo ""

echo "=== Clean complete ==="

#!/usr/bin/env bash
# seed_test_data.sh — seed dev organization with dummy data for local development or remote servers
#
# Repo-root helper: resolves the path to the AxiaOps repo regardless of how
# the script is invoked (from the repo root via Makefile, or from elsewhere
# via an absolute path). Needed for the `go run ./services/api/cmd/hash-password`
# invocation in the --demo block that mints argon2id hashes for the
# alice/bob/carol personas — see docs/demo-setup.md for the password flow.
#
# Prerequisites:
#   Requires psql. If not installed:
#     brew install libpq
#   The libpq formula is keg-only on Homebrew so /opt/homebrew/opt/libpq/bin
#   isn't on PATH by default — this script auto-discovers it (see resolve_psql
#   below). To use the binary outside this script too, add the export:
#     echo 'export PATH="/opt/homebrew/opt/libpq/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc
#
# Usage:
#   ./scripts/seed_test_data.sh                                    # Local docker
#   ./scripts/seed_test_data.sh --remote dev-1                     # Remote dev-1   (192.168.1.121:5432)
#   ./scripts/seed_test_data.sh --remote dev-2                     # Remote dev-2   (192.168.1.123:5432)
#   ./scripts/seed_test_data.sh --remote staging                   # Remote staging (192.168.1.122:5432)
#   ./scripts/seed_test_data.sh --remote preview                   # Remote preview (192.168.1.124:5432)
#   ./scripts/seed_test_data.sh --remote demo                      # Remote demo    (192.168.1.126:5432)
#   MIGRATION_DATABASE_URL="postgres://..." ./scripts/seed_test_data.sh      # Custom connection (owner user, bypasses RLS)
#
# Each env runs on its own self-hosted container with hostname axiaops-<env>; the
# .local addresses resolve via mDNS (same mechanism that resolved the old
# axiaops.local). All postgres instances now listen on the standard 5432 —
# no per-env port mapping since each lives on its own host.
#
# Supports both local (docker) and remote database connections.
# Safe to re-run — all inserts are idempotent (ON CONFLICT DO NOTHING / DO UPDATE).

set -euo pipefail

# ── psql discovery ────────────────────────────────────────────────────────────
# Homebrew's libpq formula is keg-only — /opt/homebrew/opt/libpq/bin/psql exists
# but isn't on PATH unless the user added the export shown in the docstring above.
# Probe known locations so the remote mode works regardless of shell setup.
# Note: only used when --remote is passed; local mode uses `docker exec` instead.

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

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

REMOTE_ENV=""
AUTO_YES=false
DEMO_MODE=false
BOOTSTRAP_FIRST=false

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
    --yes|-y) AUTO_YES=true ;;
    --bootstrap-first)
      # Skip the dashboard bootstrap ceremony on auth-on remote envs by
      # creating the first organization + first owner (alice) + sealing
      # bootstrap_state directly via SQL. ONLY for ephemeral demo envs.
      # See docs/demo-setup.md for the rationale and constraints.
      BOOTSTRAP_FIRST=true
      ;;
    --demo)
      # Tier-1 slice of #93. Populates Acme + Globex orgs with copies of the
      # dev seed data under acme-*/globex-* account IDs so the dashboard can
      # demonstrate cross-org RLS isolation + the B1.5 org switcher. The
      # currently-logged-in user (dev user locally, bootstrap user on preview)
      # gets owner memberships in Acme + Globex so they can actually see the
      # new data via /v1/auth/switch-org. Full multi-user + persona
      # differentiation + auth-on staging/demo env support is tracked in #93.
      DEMO_MODE=true
      ;;
    *) echo "Error: Unknown flag '$1'"; exit 1 ;;
  esac
  shift
done

# --demo is allowed for local docker + --remote preview + --remote demo.
# Staging / integration / dev-* are intentionally blocked: dev-* are auth-bypass
# envs where the persona logins wouldn't exercise anything, and staging /
# integration are reference envs whose org list shouldn't be polluted with
# demo fixtures. The full multi-user demo posture is tracked in #93.
DEMO_ALLOWED_REMOTES_REGEX='^(preview|demo)$'
if [[ "$DEMO_MODE" == "true" && -n "$REMOTE_ENV" && ! "$REMOTE_ENV" =~ $DEMO_ALLOWED_REMOTES_REGEX ]]; then
  echo "Error: --demo only supports local docker, --remote preview, or --remote demo." >&2
  echo "       For --remote $REMOTE_ENV, the full multi-org demo posture is tracked in #93." >&2
  exit 1
fi

# --bootstrap-first preconditions: only meaningful on a fresh auth-on remote
# env, only useful with --demo (which mints alice as the first owner), only
# usable when DEMO_USERS_PASSWORD is set (so alice has a real hashed login).
# Refuse loudly otherwise — silent miscombination here would either no-op or
# create an orphan org with no owner.
if [[ "$BOOTSTRAP_FIRST" == "true" ]]; then
  if [[ -z "$REMOTE_ENV" ]]; then
    echo "Error: --bootstrap-first only applies to --remote envs (local docker auto-creates the dev org)." >&2
    exit 1
  fi
  if [[ "$DEMO_MODE" != "true" ]]; then
    echo "Error: --bootstrap-first requires --demo (the alice/bob/carol personas are demo-mode only)." >&2
    exit 1
  fi
  if [[ -z "${DEMO_USERS_PASSWORD:-}" ]]; then
    echo "Error: --bootstrap-first requires DEMO_USERS_PASSWORD env var to be set so alice has a hashed login." >&2
    echo "       See docs/demo-setup.md for the recommended workflow." >&2
    exit 1
  fi
fi

# ── Remote connection setup ───────────────────────────────────────────────────
# When --remote is passed, build a MIGRATION_DATABASE_URL pointing to the remote host.
# For staging, look up the real organization ID from the DB (created by the native bootstrap flow).
# Prompts for confirmation unless --yes/-y is passed.

if [[ -n "$REMOTE_ENV" ]]; then
  PSQL=$(resolve_psql)
  if [ -z "${PSQL:-}" ]; then
    echo "Error: psql not found on PATH or known libpq locations." >&2
    echo "  Install:  brew install libpq" >&2
    echo "  Then add to PATH (or rely on this script's auto-discovery):" >&2
    echo "    echo 'export PATH=\"/opt/homebrew/opt/libpq/bin:\$PATH\"' >> ~/.zshrc" >&2
    exit 1
  fi

  # Per-env static IPs. Sourced from self-hosted-infra/stacks/*/variables.tf —
  # treat that file as the source of truth and update both sides if any
  # IP migrates. Using IPs (not the axiaops-<env>.local mDNS hostnames)
  # because:
  #   • mDNS (.local) doesn't traverse Tailscale's overlay, so the
  #     hostname path breaks the moment you seed from outside the LAN
  #   • .gitlab-ci.yml's DEPLOY_HOST_IP variables already use IPs for
  #     the same reason (Alpine docker image has no mDNS resolver)
  #   • IPs are static (terraform-managed, not DHCP) — equally durable
  # Hostnames still work for humans typing at a prompt (browser, ssh
  # from a LAN-attached laptop). The script itself sticks to IPs to
  # avoid the resolver-dependency surface.
  case "$REMOTE_ENV" in
    dev-1)       HOST_IP="192.168.1.121" ;;
    dev-2)       HOST_IP="192.168.1.123" ;;
    staging)     HOST_IP="192.168.1.122" ;;
    preview)     HOST_IP="192.168.1.124" ;;
    demo)        HOST_IP="192.168.1.126" ;;
    integration) HOST_IP="192.168.1.130" ;;
  esac
  DB_PORT=5432

  export MIGRATION_DATABASE_URL="postgres://axiaops_owner:axiaops_owner@$HOST_IP:$DB_PORT/axiaops?sslmode=disable"

  echo "=== Seeding AxiaOps $REMOTE_ENV database ==="
  echo "Target:    $HOST_IP:$DB_PORT  (axiaops-$REMOTE_ENV)"
  echo "URL:       $MIGRATION_DATABASE_URL"
  echo ""

  if [[ "$AUTO_YES" != "true" ]]; then
    read -r -p "Seed the $REMOTE_ENV database at $HOST_IP:$DB_PORT? This will insert data. [y/N] " confirm
    if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
      echo "Aborted."
      exit 0
    fi
    echo ""
  fi

  # Verify connection — capture stderr+stdout so a real error reaches the user
  # instead of the misleading "Cannot reach" line. Common failure modes the
  # captured output disambiguates: bad password (`FATAL: password authentication`),
  # network unreachable (`could not connect to server`), wrong DB/role missing.
  echo -n "Checking connection to $HOST_IP..."
  if err=$("$PSQL" "$MIGRATION_DATABASE_URL" -c 'SELECT 1' 2>&1 >/dev/null); then
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

# Dev organization id — must match DEV_ORGANIZATION_ID env var used by the API's DevBypass
# middleware. Stable string id (not a UUID), seeded once at API startup via
# Store.EnsureOrganization.
DEV_ORGANIZATION_ID_VAL="${DEV_ORGANIZATION_ID:-dev-organization-axiaops}"

# ── Determine connection mode (local docker or remote) ────────────────────────
# If MIGRATION_DATABASE_URL is set (by --remote or the caller), connect directly as schema owner.
# Otherwise, shell into the local docker container.

if [ -z "${MIGRATION_DATABASE_URL:-}" ]; then
  # Local mode: use docker container
  MODE="docker"
  echo "MIGRATION_DATABASE_URL not set — using local docker container (axiaops-postgres)"
else
  # Remote mode: use direct psql connection
  MODE="remote"
  echo "MIGRATION_DATABASE_URL set — connecting to remote postgres"
fi

# ── psql helpers ──────────────────────────────────────────────────────────────
# All DB access goes through psql_base, which handles connection details and
# sets search_path=axiaops so unqualified table names resolve correctly.
# We connect as axiaops_owner (bypasses RLS) since this is a seed script.

if [ "$MODE" = "docker" ]; then
  psql_base() { docker exec -i -e "PGOPTIONS=-c search_path=axiaops" axiaops-postgres psql -U axiaops_owner -d axiaops "$@"; }
else
  psql_base() { PGOPTIONS="-c search_path=axiaops" "$PSQL" "$MIGRATION_DATABASE_URL" "$@"; }
fi

psql_exec()  { psql_base --quiet -c "$1"; }           # Run single statement, no output (writes)
psql_query() { psql_base -t --no-align -c "$1"; }    # Run single statement, return raw value (reads)
psql_pipe()  { psql_base --quiet; }                   # Read multi-statement SQL from stdin (bulk inserts)

# ── Ensure postgres is running ────────────────────────────────────────────────

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

if [ "$MODE" = "docker" ]; then
  if ! docker ps --format '{{.Names}}' 2>/dev/null | grep -q "^axiaops-postgres$"; then
    echo "PostgreSQL not running — starting..."
    cd "$ROOT"
    docker compose up -d postgres
    echo -n "Waiting for PostgreSQL..."
    until docker exec axiaops-postgres pg_isready -U axiaops_owner -d axiaops &>/dev/null; do
      echo -n "."
      sleep 1
    done
    echo " Ready."
  fi
else
  echo -n "Waiting for remote PostgreSQL to be ready..."
  for i in {1..30}; do
    if "$PSQL" "$MIGRATION_DATABASE_URL" -c "SELECT 1" &>/dev/null; then
      echo " Ready."
      break
    fi
    echo -n "."
    sleep 2
  done
fi

echo "=== AxiaOps — Seeding dev data ==="
echo ""

NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
PERIOD_START=$(date -u -v-30d +"%Y-%m-%dT00:00:00Z" 2>/dev/null || date -u -d '30 days ago' +"%Y-%m-%dT00:00:00Z")
PERIOD_END="$NOW"

# ── Resolve organization ID ─────────────────────────────────────────────────────────
# Must match whichever organization the API is serving data for:
#   - Auth-on envs (staging / preview / demo, native cookie auth + OIDC SSO):
#     one real organization created by the native bootstrap flow. Pick the
#     oldest organizations row — first one wins on a multi-org install, which
#     is the only one we'd want to seed.
#   - Auth-bypass envs (dev-1 / dev-2 / local docker, DEV_MODE=true):
#     organization id == DEV_ORGANIZATION_ID (ensured by the API at startup
#     via Store.EnsureOrganization). Seed mirrors that — pin the same id.

# AUTH_ON_ENVS = the set where the API requires real auth and creates real
# org rows via the native bootstrap flow. Keep in sync with DEV_MODE settings
# in deploy/{env}.yml — if you flip an env's DEV_MODE here, mirror it there.
AUTH_ON_ENVS_REGEX="^(staging|preview|demo|integration)$"

if [[ "$REMOTE_ENV" =~ $AUTH_ON_ENVS_REGEX ]]; then
  ORGANIZATION_ID=$(psql_query "SELECT id FROM organizations ORDER BY created_at LIMIT 1;" 2>/dev/null | tr -d '[:space:]')
  if [ -z "$ORGANIZATION_ID" ]; then
    if [[ "$BOOTSTRAP_FIRST" == "true" ]]; then
      # Mint the first org + first owner (alice) directly via SQL, skipping
      # the install-token-gated /auth/bootstrap endpoint. See docs/demo-setup.md
      # for the security trade-off — only safe on ephemeral demo envs.
      echo "=== --bootstrap-first: creating first org + owner (alice) directly ==="
      ORGANIZATION_ID="org_axiaops_demo"
      psql_exec "INSERT INTO organizations (id, org_code, name, created_at)
        VALUES ('${ORGANIZATION_ID}', '${ORGANIZATION_ID}', 'AxiaOps Demo', NOW())
        ON CONFLICT (org_code) DO NOTHING;"

      echo "Hashing DEMO_USERS_PASSWORD for alice (first owner)..."
      DEMO_USERS_HASH=$(printf '%s' "${DEMO_USERS_PASSWORD}" | (cd "$REPO_ROOT" && go run ./services/api/cmd/hash-password)) || {
        echo "Error: failed to hash DEMO_USERS_PASSWORD." >&2
        exit 1
      }

      psql_exec "INSERT INTO users (id, organization_id, external_id, email, name, password_hash, password_set_at, created_at, last_seen)
        VALUES ('demo-user-alice', '${ORGANIZATION_ID}', 'demo:alice', 'alice@axiaops.io', 'Alice (FinOps Lead)', '${DEMO_USERS_HASH}', NOW(), NOW(), NOW())
        ON CONFLICT (id) DO UPDATE SET password_hash = EXCLUDED.password_hash, password_set_at = EXCLUDED.password_set_at;"

      psql_exec "INSERT INTO memberships (id, organization_id, user_id, role, created_at, updated_at)
        VALUES (gen_random_uuid()::text, '${ORGANIZATION_ID}', 'demo-user-alice', 'owner', NOW(), NOW())
        ON CONFLICT (organization_id, user_id) DO NOTHING;"

      # Seal /auth/bootstrap — DELETE on the singleton row makes the endpoint
      # return 409 'already bootstrapped' for any subsequent install-token POST.
      psql_exec "DELETE FROM bootstrap_state;"

      echo "Bootstrap completed via --bootstrap-first:"
      echo "  org      = ${ORGANIZATION_ID} (AxiaOps Demo)"
      echo "  owner    = alice@axiaops.io (id=demo-user-alice)"
      echo "  password = DEMO_USERS_PASSWORD"
      echo "  /auth/bootstrap = sealed (409 on subsequent POSTs)"
    else
      echo "Error: no organization found in $REMOTE_ENV DB — bootstrap the first owner via the dashboard at https://axiaops-$REMOTE_ENV.local first so an organization row exists, then re-run."
      echo "       Or, for ephemeral demo envs, pass --bootstrap-first --demo with DEMO_USERS_PASSWORD set."
      exit 1
    fi
  else
    echo "Using $REMOTE_ENV organization: ${ORGANIZATION_ID}"
    if [[ "$BOOTSTRAP_FIRST" == "true" ]]; then
      echo "Warning: --bootstrap-first specified but $REMOTE_ENV is already bootstrapped (org=${ORGANIZATION_ID}); skipping bootstrap step."
    fi
  fi
else
  ORGANIZATION_ID="$DEV_ORGANIZATION_ID_VAL"
  psql_exec "INSERT INTO organizations (id, org_code, name, created_at)
    VALUES ('${ORGANIZATION_ID}', '${ORGANIZATION_ID}', '${ORGANIZATION_ID}', NOW())
    ON CONFLICT (id) DO NOTHING;"
  echo "Using dev organization: ${ORGANIZATION_ID}"

  # Dev user — must match DEV_USER_ID env var used by the API's DevBypass
  # middleware. Pinned id so audit_log / dismissed_by FK references resolve
  # without going through the native sign-up path.
  DEV_USER_ID_VAL="${DEV_USER_ID:-dev-user-axiaops}"
  DEV_USER_EMAIL_VAL="${DEV_USER_EMAIL:-dev@axiaops.local}"
  # Values are interpolated into SQL strings below. Reject anything that could
  # close a string literal or inject additional statements. Dev env-var values
  # should always match these allowlists; failing loudly is better than
  # producing malformed SQL.
  if ! [[ "$DEV_USER_ID_VAL" =~ ^[A-Za-z0-9._-]+$ ]]; then
    echo "Error: DEV_USER_ID must match ^[A-Za-z0-9._-]+$ (got: ${DEV_USER_ID_VAL})" >&2
    exit 1
  fi
  if ! [[ "$DEV_USER_EMAIL_VAL" =~ ^[A-Za-z0-9@._+-]+$ ]]; then
    echo "Error: DEV_USER_EMAIL must match ^[A-Za-z0-9@._+-]+$ (got: ${DEV_USER_EMAIL_VAL})" >&2
    exit 1
  fi
  psql_exec "INSERT INTO users (id, organization_id, external_id, email, name, created_at, last_seen)
    VALUES ('${DEV_USER_ID_VAL}', '${ORGANIZATION_ID}', 'dev:${DEV_USER_ID_VAL}',
            '${DEV_USER_EMAIL_VAL}', 'Dev User', NOW(), NOW())
    ON CONFLICT (id) DO NOTHING;"
  echo "Using dev user:   ${DEV_USER_ID_VAL} <${DEV_USER_EMAIL_VAL}>"

  # Owner membership for the dev user. The API also calls EnsureDevMembership
  # at startup, but seeding it here makes the seed self-contained — useful
  # after testing the GDPR right-to-erasure flow (DELETE /v1/organizations/me),
  # which wipes everything including the membership row. Without this, you'd
  # have to restart the API after re-seeding to recover access.
  psql_exec "INSERT INTO memberships (id, organization_id, user_id, role, created_at, updated_at)
    VALUES (gen_random_uuid()::text, '${ORGANIZATION_ID}', '${DEV_USER_ID_VAL}', 'owner', NOW(), NOW())
    ON CONFLICT (organization_id, user_id) DO NOTHING;"
fi

# ── Additional organizations for RLS isolation testing + demo seeding ─────────
# Always create Acme + Globex rows (id = org_code so re-runs are deterministic
# and downstream SQL can reference them without a lookup). On local docker mode
# this exercises RLS isolation; with --demo it gets populated with seed data.
# ON CONFLICT (org_code) DO NOTHING preserves the row's id across re-runs even
# if a prior run wrote a gen_random_uuid()-based id (historical seed shape).

echo "Creating organization: Acme Corp..."
psql_exec "INSERT INTO organizations (id, org_code, name, created_at)
  VALUES ('org_acme', 'org_acme', 'Acme Corp', '$NOW')
  ON CONFLICT (org_code) DO NOTHING;"

echo "Creating organization: Globex Inc..."
psql_exec "INSERT INTO organizations (id, org_code, name, created_at)
  VALUES ('org_globex', 'org_globex', 'Globex Inc', '$NOW')
  ON CONFLICT (org_code) DO NOTHING;"
echo ""

# Resolve the actual ids — for fresh rows this is 'org_acme'/'org_globex', for
# rows from older seed runs (gen_random_uuid()) it's whatever UUID is there.
ACME_ORG_ID=$(psql_query "SELECT id FROM organizations WHERE org_code='org_acme' LIMIT 1;" | tr -d '[:space:]')
GLOBEX_ORG_ID=$(psql_query "SELECT id FROM organizations WHERE org_code='org_globex' LIMIT 1;" | tr -d '[:space:]')

# ── Seed AWS accounts ────────────────────────────────────────────────────────
# Three dummy accounts (prod/staging/dev) with empty credentials.
# ON CONFLICT DO NOTHING so re-runs are safe and real accounts aren't overwritten.

echo "Creating seed accounts for organization ${ORGANIZATION_ID}..."
ACCT1="seed-account-001"
ACCT2="seed-account-002"
ACCT3="seed-account-003"
AWS_ACCT_ID="111111111111"
# Wipe seed accounts from any organization (cleans up orphans left by older seed runs
# that wrote under the wrong organization). Safe because seed-account-* IDs are seed-only.
psql_exec "DELETE FROM accounts WHERE id IN ('${ACCT1}','${ACCT2}','${ACCT3}');"
psql_exec "INSERT INTO accounts (id, organization_id, provider, label, account_id, access_key_id, secret_encrypted, region, status, created_at)
  VALUES
    ('${ACCT1}', '${ORGANIZATION_ID}', 'aws', 'Seed Production AWS', '${AWS_ACCT_ID}', '', '', 'eu-central-1', 'connected', '$NOW'),
    ('${ACCT2}', '${ORGANIZATION_ID}', 'aws', 'Seed Staging AWS',    '${AWS_ACCT_ID}', '', '', 'us-east-1',    'connected', '$NOW'),
    ('${ACCT3}', '${ORGANIZATION_ID}', 'aws', 'Seed Dev AWS',        '${AWS_ACCT_ID}', '', '', 'eu-west-1',    'connected', '$NOW');"
echo "  Done."
echo ""

# ── Zombie records ────────────────────────────────────────────────────────────
# 24 zombie resources across all 3 accounts (8 each):
#   - Tier 2 (CloudWatch): idle EC2, abandoned RDS, unused Lambda/ELB, unattached EIP
#   - Tier 1 (API-only):   unattached EBS, orphaned snapshots, long-stopped EC2, old AMIs
#   - Other:               empty EKS clusters
# Deletes existing seed data first, then re-inserts (idempotent on re-run).

echo "Inserting zombie records..."
psql_exec "DELETE FROM zombie_records WHERE internal_account_id IN ('seed-account-001','seed-account-002','seed-account-003');"

psql_exec "INSERT INTO zombie_records
  (organization_id, provider, account_id, internal_account_id, service, resource_type, region, resource_id, tags, monthly_cost, currency,
   period_start, period_end, usage_metric, usage_avg, usage_unit, reason, owner, detected_at)
VALUES
  -- Account 1 zombies
  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEC2', 'instance', 'eu-central-1',
   'i-0abc123prod0001', '{\"env\":\"prod\",\"team\":\"backend\"}',
   45.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 1.2, 'Percent',
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonRDS', 'db_instance', 'eu-central-1',
   'db-prod-legacy-reporting', '{\"env\":\"prod\",\"team\":\"data\"}',
   210.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count',
   'Zero connections — likely abandoned', 'data', '$NOW'),

  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonElasticLoadBalancing', 'load_balancer', 'eu-central-1',
   'app/legacy-api/abc123prod', '{\"env\":\"prod\",\"team\":\"platform\"}',
   18.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RequestCount', 0, 'Count',
   'Zero requests — likely abandoned', 'platform', '$NOW'),

  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonVPC', 'eip', 'eu-central-1',
   'eipalloc-prod00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count',
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Account 2 zombies
  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'instance', 'us-east-1',
   'i-0abc123stg0001', '{\"env\":\"staging\",\"team\":\"backend\"}',
   38.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 0.8, 'Percent',
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'instance', 'us-east-1',
   'i-0abc123stg0002', '{\"env\":\"staging\",\"team\":\"platform\"}',
   38.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 2.1, 'Percent',
   'Instance CPU below 5% — likely idle', 'platform', '$NOW'),

  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AWSLambda', 'function', 'us-east-1',
   'stg-image-resizer', '{\"env\":\"staging\",\"team\":\"backend\"}',
   4.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count',
   'Zero invocations — likely unused', 'backend', '$NOW'),

  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonVPC', 'eip', 'us-east-1',
   'eipalloc-stg00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count',
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Account 3 zombies
  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'instance', 'eu-west-1',
   'i-0abc123dev0001', '{\"env\":\"dev\",\"team\":\"backend\"}',
   22.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 0.3, 'Percent',
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonRDS', 'db_instance', 'eu-west-1',
   'db-dev-abandoned', '{\"env\":\"dev\",\"team\":\"data\"}',
   89.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count',
   'Zero connections — likely abandoned', 'data', '$NOW'),

  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AWSLambda', 'function', 'eu-west-1',
   'dev-unused-email-sender', '{\"env\":\"dev\",\"team\":\"backend\"}',
   2.30, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count',
   'Zero invocations — likely unused', 'backend', '$NOW'),

  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonVPC', 'eip', 'eu-west-1',
   'eipalloc-dev00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count',
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- ── EKS zombies ───────────────────────────────────────────────────────────

  -- Account 1: empty EKS cluster (control plane billed, zero nodes)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEKS', 'cluster', 'eu-central-1',
   'prod-analytics-cluster', '{\"env\":\"prod\",\"team\":\"data\"}',
   73.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'cluster_node_count', 0, 'Count',
   'EKS cluster has zero nodes — control plane (\$73/mo) billing with no workload', 'data', '$NOW'),

  -- Account 2: empty EKS cluster
  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEKS', 'cluster', 'us-east-1',
   'stg-ml-pipeline', '{\"env\":\"staging\",\"team\":\"platform\"}',
   73.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'cluster_node_count', 0, 'Count',
   'EKS cluster has zero nodes — control plane (\$73/mo) billing with no workload', 'platform', '$NOW'),

  -- ── Tier 1 API-only zombies ────────────────────────────────────────────────

  -- Account 1: unattached EBS volume (100 GB gp3, $0.08/GB-month)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEC2', 'volume', 'eu-central-1',
   'vol-0prod00000001', '{\"env\":\"prod\",\"team\":\"platform\"}',
   8.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'VolumeState', 0, 'State',
   'EBS volume (100 GB gp3) is unattached — not mounted to any instance but still incurring storage charges', 'platform', '$NOW'),

  -- Account 1: orphaned snapshot (source volume deleted, not backing any AMI)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEC2', 'snapshot', 'eu-central-1',
   'snap-0prod00000001', '{\"env\":\"prod\",\"team\":\"data\"}',
   10.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'SourceVolumeExists', 0, 'Boolean',
   'EBS snapshot (200 GB) source volume vol-0prod-deleted-001 no longer exists — orphaned storage accumulating charges', 'data', '$NOW'),

  -- Account 2: long-stopped EC2 instance (45 days, 80 GB attached EBS)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'stopped_instance', 'us-east-1',
   'i-0stopped-stg0001', '{\"env\":\"staging\",\"team\":\"backend\"}',
   6.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysStopped', 45, 'Days',
   'EC2 instance stopped for 45 days — attached EBS storage (80 GB) continues to bill at no compute benefit', 'backend', '$NOW'),

  -- Account 2: old AMI (120 days old, 80 GB backing snapshots, $0.05/GB-month)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'ami', 'us-east-1',
   'ami-0stg00000001', '{\"env\":\"staging\",\"team\":\"platform\"}',
   4.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysSinceCreation', 120, 'Days',
   'AMI is 120 days old and not referenced by any instance — backing snapshots (80 GB) accumulate storage charges', 'platform', '$NOW'),

  -- Account 3: unattached EBS volume (50 GB gp3)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'volume', 'eu-west-1',
   'vol-0dev00000001', '{\"env\":\"dev\",\"team\":\"backend\"}',
   4.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'VolumeState', 0, 'State',
   'EBS volume (50 GB gp3) is unattached — not mounted to any instance but still incurring storage charges', 'backend', '$NOW'),

  -- Account 3: orphaned snapshot (150 GB)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'snapshot', 'eu-west-1',
   'snap-0dev00000001', '{\"env\":\"dev\",\"team\":\"data\"}',
   7.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'SourceVolumeExists', 0, 'Boolean',
   'EBS snapshot (150 GB) source volume vol-0dev-deleted-001 no longer exists — orphaned storage accumulating charges', 'data', '$NOW'),

  -- Account 3: long-stopped EC2 instance (60 days, 40 GB attached EBS)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'stopped_instance', 'eu-west-1',
   'i-0stopped-dev0001', '{\"env\":\"dev\",\"team\":\"backend\"}',
   3.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysStopped', 60, 'Days',
   'EC2 instance stopped for 60 days — attached EBS storage (40 GB) continues to bill at no compute benefit', 'backend', '$NOW'),

  -- Account 3: old AMI (180 days old, 60 GB backing snapshots)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'ami', 'eu-west-1',
   'ami-0dev00000001', '{\"env\":\"dev\",\"team\":\"platform\"}',
   3.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysSinceCreation', 180, 'Days',
   'AMI is 180 days old and not referenced by any instance — backing snapshots (60 GB) accumulate storage charges', 'platform', '$NOW'),

  -- ── CloudWatch Log Group zombies ──────────────────────────────────────────

  -- Account 1: log group with no retention policy (2.5 GB stored indefinitely)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonCloudWatch', 'log_group', 'eu-central-1',
   '/aws/lambda/prod-legacy-processor', '{}',
   0.08, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RetentionDays', 0, 'Days',
   'CloudWatch log group has no retention policy — 2.5 GB stored indefinitely accumulating charges', 'unknown', '$NOW'),

  -- Account 2: log group with no retention policy (5.0 GB stored indefinitely)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonCloudWatch', 'log_group', 'us-east-1',
   '/ecs/stg-api-service', '{}',
   0.15, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RetentionDays', 0, 'Days',
   'CloudWatch log group has no retention policy — 5.0 GB stored indefinitely accumulating charges', 'unknown', '$NOW'),

  -- Account 3: log group with no retention policy (1.2 GB stored indefinitely)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonCloudWatch', 'log_group', 'eu-west-1',
   '/aws/rds/dev-abandoned-db', '{}',
   0.04, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RetentionDays', 0, 'Days',
   'CloudWatch log group has no retention policy — 1.2 GB stored indefinitely accumulating charges', 'unknown', '$NOW'),

  -- ── Orphaned RDS snapshot zombies ─────────────────────────────────────────

  -- Account 1: orphaned manual RDS snapshot (100 GB, source DB deleted, 45 days old)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonRDS', 'db_snapshot', 'eu-central-1',
   'rds:prod-legacy-reporting-final-2026-02', '{}',
   9.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'SourceDBExists', 45, 'Days',
   'Manual RDS snapshot (100 GB, 45 days old) is orphaned — source DB "prod-legacy-reporting" no longer exists, accumulating \$9.50/month in storage charges', 'unknown', '$NOW'),

  -- Account 2: orphaned manual RDS snapshot (200 GB, source DB deleted, 90 days old)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonRDS', 'db_snapshot', 'us-east-1',
   'rds:stg-analytics-db-pre-migration', '{}',
   19.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'SourceDBExists', 90, 'Days',
   'Manual RDS snapshot (200 GB, 90 days old) is orphaned — source DB "stg-analytics-db" no longer exists, accumulating \$19.00/month in storage charges', 'unknown', '$NOW'),

  -- Account 3: orphaned manual RDS snapshot (50 GB, source DB deleted, 60 days old)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonRDS', 'db_snapshot', 'eu-west-1',
   'rds:dev-test-db-backup-2026-01', '{}',
   4.75, 'USD', '$PERIOD_START', '$PERIOD_END',
   'SourceDBExists', 60, 'Days',
   'Manual RDS snapshot (50 GB, 60 days old) is orphaned — source DB "dev-test-db" no longer exists, accumulating \$4.75/month in storage charges', 'unknown', '$NOW'),

  -- ── Stale ECR image zombies ───────────────────────────────────────────────

  -- Account 1: ECR repo with stale images (12 stale, 8.5 GB)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonECR', 'ecr_image', 'eu-central-1',
   'prod-api-service', '{}',
   0.85, 'USD', '$PERIOD_START', '$PERIOD_END',
   'StaleImageCount', 12, 'Count',
   'ECR repository has 12 untagged/stale images totaling 8.5 GB — accumulating \$0.85/month in storage', 'unknown', '$NOW'),

  -- Account 2: ECR repo with stale images (25 stale, 15.0 GB)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonECR', 'ecr_image', 'us-east-1',
   'stg-worker', '{}',
   1.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'StaleImageCount', 25, 'Count',
   'ECR repository has 25 untagged/stale images totaling 15.0 GB — accumulating \$1.50/month in storage', 'unknown', '$NOW'),

  -- Account 3: ECR repo with stale images (8 stale, 3.2 GB)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonECR', 'ecr_image', 'eu-west-1',
   'dev-frontend', '{}',
   0.32, 'USD', '$PERIOD_START', '$PERIOD_END',
   'StaleImageCount', 8, 'Count',
   'ECR repository has 8 untagged/stale images totaling 3.2 GB — accumulating \$0.32/month in storage', 'unknown', '$NOW'),

  -- ── Unused Secrets Manager zombies ────────────────────────────────────────

  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AWSSecretsManager', 'secret', 'eu-central-1',
   'prod/legacy-api/db-password', '{}',
   0.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysSinceAccess', 180, 'Days',
   'Secret not accessed for 180 days — still billing \$0.40/month', 'unknown', '$NOW'),

  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AWSSecretsManager', 'secret', 'us-east-1',
   'stg/old-service/api-key', '{}',
   0.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysSinceAccess', 120, 'Days',
   'Secret not accessed for 120 days — still billing \$0.40/month', 'unknown', '$NOW'),

  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AWSSecretsManager', 'secret', 'eu-west-1',
   'dev/abandoned-project/token', '{}',
   0.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysSinceAccess', 95, 'Days',
   'Secret not accessed for 95 days — still billing \$0.40/month', 'unknown', '$NOW'),

  -- ── CloudFront distribution zombies (zero requests) ────────────────────────

  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonCloudFront', 'distribution', 'us-east-1',
   'E1PROD0ABANDONED', '{}',
   18.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Requests', 0, 'Count',
   'CloudFront distribution has zero requests — likely abandoned', 'unknown', '$NOW'),

  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonCloudFront', 'distribution', 'us-east-1',
   'E2STG0OLDSITE', '{}',
   8.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Requests', 0, 'Count',
   'CloudFront distribution has zero requests — likely abandoned', 'unknown', '$NOW'),

  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonCloudFront', 'distribution', 'us-east-1',
   'E3UAT0PREVIEW', '{}',
   6.25, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Requests', 0, 'Count',
   'CloudFront distribution has zero requests — likely abandoned', 'unknown', '$NOW'),

  -- ── Kinesis data streams (zero incoming records, provisioned mode) ────────

  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonKinesis', 'stream', 'eu-central-1',
   'prod-event-ingestion-v1', '{}',
   32.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'IncomingRecords', 0, 'Count',
   'Kinesis data stream has zero incoming records — likely unused', 'unknown', '$NOW'),

  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonKinesis', 'stream', 'us-east-1',
   'stg-monitoring-stream', '{}',
   10.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'IncomingRecords', 0, 'Count',
   'Kinesis data stream has zero incoming records — likely unused', 'unknown', '$NOW'),

  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonKinesis', 'stream', 'eu-west-1',
   'dev-clickstream', '{}',
   5.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'IncomingRecords', 0, 'Count',
   'Kinesis data stream has zero incoming records — likely unused', 'unknown', '$NOW'),

  -- ── S3 buckets (zero requests, requires request metrics enabled) ──────────

  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonS3', 'bucket', 'eu-central-1',
   'prod-old-data-export-2024', '{}',
   25.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'AllRequests', 0, 'Count',
   'S3 bucket has zero requests — likely abandoned (requires request metrics enabled)', 'unknown', '$NOW'),

  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonS3', 'bucket', 'us-east-1',
   'stg-terraform-state-backup', '{}',
   8.75, 'USD', '$PERIOD_START', '$PERIOD_END',
   'AllRequests', 0, 'Count',
   'S3 bucket has zero requests — likely abandoned (requires request metrics enabled)', 'unknown', '$NOW'),

  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonS3', 'bucket', 'eu-west-1',
   'dev-test-uploads-2023', '{}',
   4.25, 'USD', '$PERIOD_START', '$PERIOD_END',
   'AllRequests', 0, 'Count',
   'S3 bucket has zero requests — likely abandoned (requires request metrics enabled)', 'unknown', '$NOW')
;"
echo "  Inserted zombie records including:"
echo "    - 3 CloudFront distributions (zero requests each)"
echo "    - 3 Kinesis streams (zero incoming records each, provisioned mode)"
echo "    - 3 S3 buckets (zero requests each, requires metrics enabled)"
echo "    - Plus all other resource types (EC2, RDS, Lambda, ELB, VPC, EBS, EKS, etc.)"
echo ""

# ── Resource records ──────────────────────────────────────────────────────────
# 33 records: the same 22 zombies above plus 11 active (healthy) resources.
# Active resources provide contrast in the dashboard — they show up in the
# "all resources" view but NOT in the zombies view.

echo "Inserting resource records..."
psql_exec "DELETE FROM resource_records WHERE internal_account_id IN ('seed-account-001','seed-account-002','seed-account-003');"

psql_exec "INSERT INTO resource_records
  (organization_id, provider, account_id, internal_account_id, service, resource_type, region, resource_id, tags, monthly_cost, currency,
   period_start, period_end, usage_metric, usage_avg, usage_unit, is_zombie, reason, owner, detected_at)
VALUES
  -- ── Production (${ACCT1}) ──────────────────────────────────────────
  -- Zombie: idle EC2
  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEC2', 'instance', 'eu-central-1',
   'i-0abc123prod0001', '{\"env\":\"prod\",\"team\":\"backend\"}',
   45.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 1.2, 'Percent', true,
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  -- Zombie: abandoned RDS
  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonRDS', 'db_instance', 'eu-central-1',
   'db-prod-legacy-reporting', '{\"env\":\"prod\",\"team\":\"data\"}',
   210.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count', true,
   'Zero connections — likely abandoned', 'data', '$NOW'),

  -- Zombie: unused ELB
  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonElasticLoadBalancing', 'load_balancer', 'eu-central-1',
   'app/legacy-api/abc123prod', '{\"env\":\"prod\",\"team\":\"platform\"}',
   18.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RequestCount', 0, 'Count', true,
   'Zero requests — likely abandoned', 'platform', '$NOW'),

  -- Zombie: unattached EIP
  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonVPC', 'eip', 'eu-central-1',
   'eipalloc-prod00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count', true,
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Active: healthy EC2
  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEC2', 'instance', 'eu-central-1',
   'i-0abc123prod0099', '{\"env\":\"prod\",\"team\":\"backend\"}',
   182.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 71.5, 'Percent', false, '', 'backend', '$NOW'),

  -- Active: healthy RDS
  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonRDS', 'db_instance', 'eu-central-1',
   'db-production-main', '{\"env\":\"prod\",\"team\":\"data\"}',
   312.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 284, 'Count', false, '', 'data', '$NOW'),

  -- Active: healthy ELB
  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonElasticLoadBalancing', 'load_balancer', 'eu-central-1',
   'app/prod-api/xyz789prod', '{\"env\":\"prod\",\"team\":\"platform\"}',
   24.30, 'USD', '$PERIOD_START', '$PERIOD_END',
   'RequestCount', 94200, 'Count', false, '', 'platform', '$NOW'),

  -- ── Staging (${ACCT2}) ─────────────────────────────────────────────
  -- Zombie: idle EC2 #1
  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'instance', 'us-east-1',
   'i-0abc123stg0001', '{\"env\":\"staging\",\"team\":\"backend\"}',
   38.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 0.8, 'Percent', true,
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  -- Zombie: idle EC2 #2
  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'instance', 'us-east-1',
   'i-0abc123stg0002', '{\"env\":\"staging\",\"team\":\"platform\"}',
   38.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 2.1, 'Percent', true,
   'Instance CPU below 5% — likely idle', 'platform', '$NOW'),

  -- Zombie: unused Lambda
  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AWSLambda', 'function', 'us-east-1',
   'stg-image-resizer', '{\"env\":\"staging\",\"team\":\"backend\"}',
   4.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count', true,
   'Zero invocations — likely unused', 'backend', '$NOW'),

  -- Zombie: unattached EIP
  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonVPC', 'eip', 'us-east-1',
   'eipalloc-stg00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count', true,
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Active: healthy EC2
  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'instance', 'us-east-1',
   'i-0abc123stg0099', '{\"env\":\"staging\",\"team\":\"backend\"}',
   76.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 48.2, 'Percent', false, '', 'backend', '$NOW'),

  -- Active: healthy RDS
  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonRDS', 'db_instance', 'us-east-1',
   'db-staging-main', '{\"env\":\"staging\",\"team\":\"data\"}',
   98.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 37, 'Count', false, '', 'data', '$NOW'),

  -- ── Development (${ACCT3}) ────────────────────────────────────────
  -- Zombie: idle EC2
  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'instance', 'eu-west-1',
   'i-0abc123dev0001', '{\"env\":\"dev\",\"team\":\"backend\"}',
   22.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 0.3, 'Percent', true,
   'Instance CPU below 5% — likely idle', 'backend', '$NOW'),

  -- Zombie: abandoned RDS
  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonRDS', 'db_instance', 'eu-west-1',
   'db-dev-abandoned', '{\"env\":\"dev\",\"team\":\"data\"}',
   89.10, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DatabaseConnections', 0, 'Count', true,
   'Zero connections — likely abandoned', 'data', '$NOW'),

  -- Zombie: unused Lambda
  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AWSLambda', 'function', 'eu-west-1',
   'dev-unused-email-sender', '{\"env\":\"dev\",\"team\":\"backend\"}',
   2.30, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 0, 'Count', true,
   'Zero invocations — likely unused', 'backend', '$NOW'),

  -- Zombie: unattached EIP
  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonVPC', 'eip', 'eu-west-1',
   'eipalloc-dev00001', '{}',
   3.60, 'USD', '$PERIOD_START', '$PERIOD_END',
   'NetworkInterfaceAttachment', 0, 'Count', true,
   'Elastic IP not attached to any resource — incurring \$0.005/hour idle charge', 'unknown', '$NOW'),

  -- Active: healthy EC2
  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'instance', 'eu-west-1',
   'i-0abc123dev0099', '{\"env\":\"dev\",\"team\":\"backend\"}',
   22.80, 'USD', '$PERIOD_START', '$PERIOD_END',
   'CPUUtilization', 34.7, 'Percent', false, '', 'backend', '$NOW'),

  -- Active: healthy Lambda
  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AWSLambda', 'function', 'eu-west-1',
   'dev-auth-handler', '{\"env\":\"dev\",\"team\":\"backend\"}',
   1.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'Invocations', 1840, 'Count', false, '', 'backend', '$NOW'),

  -- ── EKS zombie resources ───────────────────────────────────────────────────

  -- Account 1: empty EKS cluster (zombie)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEKS', 'cluster', 'eu-central-1',
   'prod-analytics-cluster', '{\"env\":\"prod\",\"team\":\"data\"}',
   73.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'cluster_node_count', 0, 'Count', true,
   'EKS cluster has zero nodes — control plane (\$73/mo) billing with no workload', 'data', '$NOW'),

  -- Account 2: empty EKS cluster (zombie)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEKS', 'cluster', 'us-east-1',
   'stg-ml-pipeline', '{\"env\":\"staging\",\"team\":\"platform\"}',
   73.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'cluster_node_count', 0, 'Count', true,
   'EKS cluster has zero nodes — control plane (\$73/mo) billing with no workload', 'platform', '$NOW'),

  -- Account 3: active EKS cluster (for contrast)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEKS', 'cluster', 'eu-west-1',
   'dev-app-cluster', '{\"env\":\"dev\",\"team\":\"backend\"}',
   73.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'cluster_node_count', 3, 'Count', false, '', 'backend', '$NOW'),

  -- ── Tier 1 API-only zombie resources ──────────────────────────────────────

  -- Account 1: unattached EBS volume (zombie)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEC2', 'volume', 'eu-central-1',
   'vol-0prod00000001', '{\"env\":\"prod\",\"team\":\"platform\"}',
   8.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'VolumeState', 0, 'State', true,
   'EBS volume (100 GB gp3) is unattached — not mounted to any instance but still incurring storage charges', 'platform', '$NOW'),

  -- Account 1: active EBS volume (in use — for dashboard contrast)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEC2', 'volume', 'eu-central-1',
   'vol-0prod00000099', '{\"env\":\"prod\",\"team\":\"backend\"}',
   24.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'VolumeState', 1, 'State', false, '', 'backend', '$NOW'),

  -- Account 1: orphaned snapshot (zombie)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT1}', '${ACCT1}', 'AmazonEC2', 'snapshot', 'eu-central-1',
   'snap-0prod00000001', '{\"env\":\"prod\",\"team\":\"data\"}',
   10.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'SourceVolumeExists', 0, 'Boolean', true,
   'EBS snapshot (200 GB) source volume vol-0prod-deleted-001 no longer exists — orphaned storage accumulating charges', 'data', '$NOW'),

  -- Account 2: long-stopped EC2 instance (zombie)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'stopped_instance', 'us-east-1',
   'i-0stopped-stg0001', '{\"env\":\"staging\",\"team\":\"backend\"}',
   6.40, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysStopped', 45, 'Days', true,
   'EC2 instance stopped for 45 days — attached EBS storage (80 GB) continues to bill at no compute benefit', 'backend', '$NOW'),

  -- Account 2: old AMI (zombie)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'ami', 'us-east-1',
   'ami-0stg00000001', '{\"env\":\"staging\",\"team\":\"platform\"}',
   4.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysSinceCreation', 120, 'Days', true,
   'AMI is 120 days old and not referenced by any instance — backing snapshots (80 GB) accumulate storage charges', 'platform', '$NOW'),

  -- Account 2: active recent AMI (in use — for dashboard contrast)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT2}', '${ACCT2}', 'AmazonEC2', 'ami', 'us-east-1',
   'ami-0stg00000099', '{\"env\":\"staging\",\"team\":\"platform\"}',
   2.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysSinceCreation', 14, 'Days', false, '', 'platform', '$NOW'),

  -- Account 3: unattached EBS volume (zombie)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'volume', 'eu-west-1',
   'vol-0dev00000001', '{\"env\":\"dev\",\"team\":\"backend\"}',
   4.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'VolumeState', 0, 'State', true,
   'EBS volume (50 GB gp3) is unattached — not mounted to any instance but still incurring storage charges', 'backend', '$NOW'),

  -- Account 3: orphaned snapshot (zombie)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'snapshot', 'eu-west-1',
   'snap-0dev00000001', '{\"env\":\"dev\",\"team\":\"data\"}',
   7.50, 'USD', '$PERIOD_START', '$PERIOD_END',
   'SourceVolumeExists', 0, 'Boolean', true,
   'EBS snapshot (150 GB) source volume vol-0dev-deleted-001 no longer exists — orphaned storage accumulating charges', 'data', '$NOW'),

  -- Account 3: long-stopped EC2 instance (zombie)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'stopped_instance', 'eu-west-1',
   'i-0stopped-dev0001', '{\"env\":\"dev\",\"team\":\"backend\"}',
   3.20, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysStopped', 60, 'Days', true,
   'EC2 instance stopped for 60 days — attached EBS storage (40 GB) continues to bill at no compute benefit', 'backend', '$NOW'),

  -- Account 3: old AMI (zombie)
  ('${ORGANIZATION_ID}', 'aws', '${ACCT3}', '${ACCT3}', 'AmazonEC2', 'ami', 'eu-west-1',
   'ami-0dev00000001', '{\"env\":\"dev\",\"team\":\"platform\"}',
   3.00, 'USD', '$PERIOD_START', '$PERIOD_END',
   'DaysSinceCreation', 180, 'Days', true,
   'AMI is 180 days old and not referenced by any instance — backing snapshots (60 GB) accumulate storage charges', 'platform', '$NOW')
;"
echo "  Inserted 33 resource records (22 zombies, 11 active) across 3 accounts."

# ── Backfill: every zombie must also be a resource record ─────────────────────
# Invariant from the real scan path: ingestion runs SaveResources for ALL
# discovered resources and SaveZombies for the idle subset, so resource_records
# is always a superset of zombie_records. The hand-written resource block above
# drifted from the zombie block — it omits whole services (CloudFront,
# CloudWatch, ECR, SecretsManager, S3, Kinesis), so the dashboard listed those
# zombies on "/" yet Detail.jsx (which looks the resource up in /v1/resources)
# rendered NotFound — a 404 on a link the UI itself produced. Rather than keep
# two hand-lists in sync, copy any zombie that lacks a matching resource row
# into resource_records (is_zombie=true). Idempotent: resource_records is
# DELETEd + rebuilt on every seed run, and the NOT EXISTS guard skips zombies
# the explicit block already covers.
psql_exec "INSERT INTO resource_records
  (organization_id, provider, account_id, internal_account_id, service, resource_type, region, resource_id, tags, monthly_cost, currency,
   period_start, period_end, usage_metric, usage_avg, usage_unit, is_zombie, reason, owner, detected_at)
SELECT
   zr.organization_id, zr.provider, zr.account_id, zr.internal_account_id, zr.service, zr.resource_type, zr.region, zr.resource_id, zr.tags, zr.monthly_cost, zr.currency,
   zr.period_start, zr.period_end, zr.usage_metric, zr.usage_avg, zr.usage_unit, true, zr.reason, zr.owner, zr.detected_at
FROM zombie_records zr
WHERE zr.internal_account_id IN ('seed-account-001','seed-account-002','seed-account-003')
  AND NOT EXISTS (
    SELECT 1 FROM resource_records rr
    WHERE rr.internal_account_id = zr.internal_account_id
      AND rr.service     = zr.service
      AND rr.region      = zr.region
      AND rr.resource_id = zr.resource_id
  );"
echo "  Backfilled resource_records from any zombie missing a matching resource row."
echo ""

# ── Zombie snapshots — derived from zombie_records ───────────────────────────
# Each day's snapshot is a rollup of a scaled view of the zombie_records inserted above:
#   - Day 1 (most recent) uses scale = 1.0, so SUM(svc.cost) on that day's services
#     equals SUM(zombie_records.monthly_cost) per account — exactly.
#   - Days 2..N (older; N = $DAYS, currently 365) scale per service by an upward
#     trend × weekly sine wobble
#     × per-service noise, so the time series looks plausible without inventing
#     services that aren't in zombie_records.
# Snapshot totals are computed as SUM of the inserted zombie_snapshot_services for
# the same (account, day) — preserving the within-row invariant
# (snapshot.total = SUM of its services) by construction. This mirrors the
# production scan flow where both SaveZombies and SaveSnapshot run off one
# analyzer.Summarize(zombies) call. See issue #91.

DAYS=365
echo "Inserting zombie snapshots derived from zombie_records (${DAYS} days × 3 accounts)..."

# Clean old seed snapshot data first. The FK on zombie_snapshot_services.snapshot_id
# is ON DELETE CASCADE (per migration 004_snapshot_services.up.sql), so deleting
# zombie_snapshots clears the per-service rows for free.
psql_exec "DELETE FROM zombie_snapshots WHERE id LIKE 'snap-seed-account-%';"

psql_pipe <<EOF
-- Deterministic seed so make seed produces identical wobble every run.
-- random() values stay session-local, so this only affects the statement below.
-- Wrapped in DO so the void-return row doesn't print in --quiet mode.
DO \$\$ BEGIN PERFORM setseed(0.42); END \$\$;

WITH
  params AS (
    SELECT
      '${ORGANIZATION_ID}'::text AS org_id,
      ${DAYS}::int               AS days
  ),
  -- Per-(account, service, resource_type) aggregates from the just-inserted records.
  zr_agg AS (
    SELECT
      zr.internal_account_id AS account_id,
      zr.service,
      zr.resource_type,
      SUM(zr.monthly_cost)::numeric AS svc_cost,
      COUNT(*)::int                 AS svc_count
    FROM zombie_records zr, params
    WHERE zr.organization_id = params.org_id
      AND zr.internal_account_id IN ('${ACCT1}','${ACCT2}','${ACCT3}')
    GROUP BY zr.internal_account_id, zr.service, zr.resource_type
  ),
  day_offsets AS (
    SELECT generate_series(1, (SELECT days FROM params))::int AS d
  ),
  -- svc_rows below is consumed twice (in ins_snapshots' SUM and in the outer
  -- INSERT). MATERIALIZED here pins one random() call per logical row so both
  -- consumers see the same scale; without it, PG would inline svc_scaled into
  -- each consumer and the snapshot total would diverge from SUM(its services).
  svc_scaled AS MATERIALIZED (
    SELECT
      zr.account_id,
      d.d,
      zr.service,
      zr.resource_type,
      zr.svc_cost,
      zr.svc_count,
      CASE
        WHEN d.d = 1 THEN 1.0::numeric
        ELSE (
          -- Upward trend: oldest day → 0.70, today → 1.00.
          (0.70 + 0.30 * (1.0 - (d.d - 1)::numeric / GREATEST(1.0, (SELECT days::numeric - 1 FROM params))))
          -- Weekly sine wobble (±10%).
          * (1.0 + 0.10 * SIN(d.d::numeric / 7.0 * PI()))
          -- Per-service uniform noise in [0.85, 1.15] (centered on 1.0).
          * (0.85 + 0.30 * random())
        )::numeric
      END AS scale
    FROM zr_agg zr CROSS JOIN day_offsets d
  ),
  -- Final per-service rows; floor counts (min 1 per service) and round cost to 2dp.
  svc_rows AS (
    SELECT
      'svc-' || account_id || '-' || d || '-'
        || ROW_NUMBER() OVER (PARTITION BY account_id, d ORDER BY service, resource_type) AS id,
      'snap-' || account_id || '-' || d AS snapshot_id,
      account_id,
      d,
      service,
      resource_type,
      GREATEST(1, FLOOR(svc_count * scale)::int) AS zombie_count,
      ROUND((svc_cost * scale)::numeric, 2)      AS monthly_cost
    FROM svc_scaled
  ),
  -- Snapshot rows are aggregates of the per-service rows. By construction:
  --   snapshot.total_monthly_cost = SUM(svc.monthly_cost) for the same (account, day)
  --   snapshot.zombie_count       = SUM(svc.zombie_count)
  -- and for d=1, scale=1.0 across all services so SUM(svc.cost) = SUM(zr.svc_cost)
  --   which equals SUM(zombie_records.monthly_cost) per account.
  ins_snapshots AS (
    INSERT INTO zombie_snapshots
      (id, organization_id, account_id, snapshot_at, zombie_count, total_monthly_cost, currency)
    SELECT
      snapshot_id,
      (SELECT org_id FROM params),
      account_id,
      -- d=1 is today at noon UTC; d=N is today − (N−1) days (N = $DAYS,
      -- currently 365 → a full year of history). Keeping the latest
      -- snapshot dated "today" so the dashboard's trend chart's most recent
      -- point matches dev's wall-clock expectation when eyeballing fresh data.
      date_trunc('day', NOW()) - ((d - 1)::text || ' days')::interval + INTERVAL '12 hours',
      SUM(zombie_count)::int,
      SUM(monthly_cost),
      'USD'
    FROM svc_rows
    GROUP BY snapshot_id, account_id, d
    ON CONFLICT DO NOTHING
    RETURNING id
  )
INSERT INTO zombie_snapshot_services
  (id, snapshot_id, organization_id, service, resource_type, zombie_count, monthly_cost, currency)
SELECT
  id,
  snapshot_id,
  (SELECT org_id FROM params),
  service,
  resource_type,
  zombie_count,
  monthly_cost,
  'USD'
FROM svc_rows
WHERE snapshot_id IN (SELECT id FROM ins_snapshots)
ON CONFLICT DO NOTHING;
EOF

echo "  Done."
echo ""

# ── Cost records ──────────────────────────────────────────────────────────────
# Seed raw cost data (Cost Explorer API records) for testing cost filtering.
# All accounts use the same AWS account ID (111111111111) matching seed script.
# generate_series writes one row per (account × service × resource_id) per
# day for the full DAYS window — at DAYS=365 that's 365 × 14 = 5,110 rows.
# The chart-sampling rules these rows feed into are documented in
# docs/chart-sampling.md (sum across days/services for amounts, never
# average — cost_records.amount is an actual, not a rate).

echo "Inserting cost records for all accounts (last ${DAYS} days of records)..."

psql_exec "DELETE FROM cost_records WHERE account_id = '${AWS_ACCT_ID}' AND internal_account_id IN ('seed-account-001','seed-account-002','seed-account-003');"

psql_pipe << EOF
-- One row per (account × service × resource_id) per day for the full
-- DAYS-day window. Jittered amounts (±15%) around the per-resource base
-- value so chip selections (7d / 30d / 90d / 6m / 1y) produce visibly
-- different totals AND the chart shows a meaningful daily-spend shape
-- end-to-end without the kink that an earlier hand-written-vs-generated
-- split introduced at days 1..3.
--
-- The hand-written specific-value block this replaces had ~22 records
-- tied to particular zombie stories (i-0abc123prod0001 etc.); those
-- demoed resource_ids still live in zombie_records / resource_records
-- (the cost rows were just mirror data). The story-tied values are not
-- consumed by any test.
--
-- setseed makes the jitter deterministic across re-runs. The outer
-- setseed(0.42) at the snapshot psql_pipe doesn't carry over — each
-- psql_pipe call is a new session — so we re-seed here.
DO \$\$ BEGIN PERFORM setseed(0.42); END \$\$;
INSERT INTO cost_records
  (organization_id, provider, account_id, internal_account_id, service, region, resource_id, amount, currency, period_start, period_end, tags, fetched_at)
SELECT
  '${ORGANIZATION_ID}', 'aws', '${AWS_ACCT_ID}',
  src.internal_account_id, src.service, src.region, src.resource_id,
  ROUND((src.base_amount * (0.85 + random() * 0.30))::numeric, 2),
  'USD',
  NOW() - (d.day || ' days')::interval,
  NOW() - ((d.day - 1) || ' days')::interval,
  '{}'::jsonb, '$NOW'
FROM generate_series(1, ${DAYS}) AS d(day)
CROSS JOIN (VALUES
  ('${ACCT1}', 'AmazonEC2',                  'eu-central-1', 'i-0abc123prod0001',          46.50),
  ('${ACCT1}', 'AmazonRDS',                  'eu-central-1', 'db-prod-legacy-reporting',  211.00),
  ('${ACCT1}', 'AmazonS3',                   'eu-central-1', 'prod-data-lake-bucket',      24.00),
  ('${ACCT1}', 'AmazonCloudFront',           'us-east-1',    'E1PROD0ABANDONED',           19.00),
  ('${ACCT1}', 'AmazonElasticLoadBalancing', 'eu-central-1', 'app/legacy-api/abc123prod',  18.50),
  ('${ACCT1}', 'AmazonVPC',                  'eu-central-1', 'nat-abc123prod',             32.00),
  ('${ACCT2}', 'AmazonEC2',                  'us-east-1',    'i-0abc123stg0001',           38.50),
  ('${ACCT2}', 'AmazonS3',                   'us-east-1',    'staging-backups',            15.50),
  ('${ACCT2}', 'AmazonCloudFront',           'us-east-1',    'E2STG0OLDSITE',               8.50),
  ('${ACCT2}', 'AWSLambda',                  'us-east-1',    'stg-image-resizer',           4.20),
  ('${ACCT2}', 'AWSDataTransfer',            'us-east-1',    'data-transfer-out',          12.50),
  ('${ACCT3}', 'AmazonEC2',                  'eu-west-1',    'i-0abc123dev0001',           22.80),
  ('${ACCT3}', 'AmazonRDS',                  'eu-west-1',    'db-dev-abandoned',           89.10),
  ('${ACCT3}', 'AWSLambda',                  'eu-west-1',    'dev-unused-email-sender',     2.30)
) AS src(internal_account_id, service, region, resource_id, base_amount)
ON CONFLICT DO NOTHING;
EOF

echo "  Inserted cost records (EC2, RDS, S3, CloudFront, Lambda, ELB, VPC, Data Transfer, Tax)"
echo ""

# ── Demo-mode: populate Acme + Globex with copies of the dev seed data ────────
# Tier-1 slice of #93. After the regular seed has populated the dev/bootstrap
# org, copy its accounts + zombie_records + resource_records + snapshots +
# services + cost_records into Acme + Globex with translated IDs (seed-* →
# acme-* and globex-*). The currently-logged-in user — dev user locally, or
# the bootstrap user on --remote preview — gets owner memberships in both
# demo orgs so they can switch via the B1.5 picker.

if [[ "$DEMO_MODE" == "true" ]]; then
  echo "=== Demo mode: populating Acme + Globex ==="

  # Resolve which user to attach memberships to. For local docker that's the
  # dev user already created above. For --remote preview it's whichever user
  # was first added to the bootstrap org (typically the first-owner from the
  # bootstrap ceremony).
  if [[ -n "$REMOTE_ENV" ]]; then
    TARGET_USER_ID=$(psql_query "
      SELECT u.id FROM users u
      JOIN memberships m ON m.user_id = u.id
      WHERE m.organization_id = '${ORGANIZATION_ID}'
      ORDER BY u.created_at ASC LIMIT 1;" | tr -d '[:space:]')
    if [[ -z "$TARGET_USER_ID" ]]; then
      echo "Error: --demo --remote preview requires at least one user in the bootstrap org;" >&2
      echo "       complete the dashboard bootstrap flow at https://axiaops-${REMOTE_ENV}.example.com first." >&2
      exit 1
    fi
    echo "Bootstrap user resolved: $TARGET_USER_ID"
  else
    TARGET_USER_ID="$DEV_USER_ID_VAL"
    echo "Target user: $TARGET_USER_ID (dev user)"
  fi

  # Target-user membership in each demo org. Role depends on whether personas
  # are also being created: the schema enforces one owner per organization
  # (memberships_one_owner_per_organization), so giving the bootstrap user
  # 'owner' of Acme + Globex would block bob from owning Globex and carol from
  # owning Acme in the persona block below.
  #
  # When DEMO_USERS_PASSWORD is set → personas are the owners; target user
  # becomes 'viewer' (still able to switch into those orgs, just can't
  # administer them).
  # When unset (no personas) → target user keeps owner of Acme + Globex
  # (the original --demo behaviour, before this MR).
  if [[ -n "${DEMO_USERS_PASSWORD:-}" ]]; then
    TARGET_DEMO_ROLE='viewer'
  else
    TARGET_DEMO_ROLE='owner'
  fi
  for demo_org in "$ACME_ORG_ID" "$GLOBEX_ORG_ID"; do
    psql_exec "INSERT INTO memberships (id, organization_id, user_id, role, created_at, updated_at)
      VALUES (gen_random_uuid()::text, '${demo_org}', '${TARGET_USER_ID}', '${TARGET_DEMO_ROLE}', NOW(), NOW())
      ON CONFLICT (organization_id, user_id) DO NOTHING;"
  done
  echo "Memberships wired for $TARGET_USER_ID in Acme + Globex (${TARGET_DEMO_ROLE} each)."

  # ── Demo personas: alice / bob / carol ────────────────────────────────────
  # Three named users with a known password (sourced from $DEMO_USERS_PASSWORD,
  # NEVER hardcoded in this script) so prospects + walk-through demos always
  # log in as the same identifiable personas across reseeds. Membership shape
  # demonstrates cross-org switching:
  #   alice → owner  in AxiaOps Dev (the bootstrap org), viewer in Acme + Globex
  #   bob   → owner  in Globex,                          viewer in AxiaOps Dev + Acme
  #   carol → owner  in Acme,                            viewer in AxiaOps Dev + Globex
  #
  # If DEMO_USERS_PASSWORD is unset, this block prints a warning and skips
  # user creation — useful for callers that only want the data seed
  # (cost_records, snapshots, etc.) without minting login-capable accounts.
  # See docs/demo-setup.md for the full rationale and operator workflow.
  if [[ -n "${DEMO_USERS_PASSWORD:-}" ]]; then
    echo "Hashing DEMO_USERS_PASSWORD via services/api/cmd/hash-password..."
    DEMO_USERS_HASH=$(printf '%s' "${DEMO_USERS_PASSWORD}" | (cd "$REPO_ROOT" && go run ./services/api/cmd/hash-password)) || {
      echo "Error: failed to hash DEMO_USERS_PASSWORD." >&2
      echo "       Confirm the password is at least 12 characters and Go is on PATH." >&2
      exit 1
    }

    psql_exec "INSERT INTO users (id, organization_id, external_id, email, name, password_hash, password_set_at, created_at, last_seen) VALUES
      ('demo-user-alice', '${ORGANIZATION_ID}', 'demo:alice', 'alice@axiaops.io', 'Alice (FinOps Lead)',     '${DEMO_USERS_HASH}', NOW(), NOW(), NOW()),
      ('demo-user-bob',   '${GLOBEX_ORG_ID}',   'demo:bob',   'bob@globex.io',    'Bob (Cloud Architect)',   '${DEMO_USERS_HASH}', NOW(), NOW(), NOW()),
      ('demo-user-carol', '${ACME_ORG_ID}',     'demo:carol', 'carol@acme.io',    'Carol (Finance Lead)',    '${DEMO_USERS_HASH}', NOW(), NOW(), NOW())
      ON CONFLICT (id) DO UPDATE SET password_hash = EXCLUDED.password_hash, password_set_at = EXCLUDED.password_set_at;"

    psql_exec "INSERT INTO memberships (id, organization_id, user_id, role, created_at, updated_at) VALUES
      -- Alice: admin in the bootstrap org (the dev user / existing bootstrap
      -- user keeps owner — schema is one-owner-per-org). With --bootstrap-first
      -- alice IS the bootstrap user and is already owner from the earlier
      -- block, so this 'admin' INSERT conflicts on (org, user) and DO NOTHING
      -- leaves the owner seat alone.
      (gen_random_uuid()::text, '${ORGANIZATION_ID}', 'demo-user-alice', 'admin',  NOW(), NOW()),
      (gen_random_uuid()::text, '${ACME_ORG_ID}',     'demo-user-alice', 'viewer', NOW(), NOW()),
      (gen_random_uuid()::text, '${GLOBEX_ORG_ID}',   'demo-user-alice', 'viewer', NOW(), NOW()),
      -- Bob: owner in Globex, viewer in AxiaOps Dev + Acme
      (gen_random_uuid()::text, '${GLOBEX_ORG_ID}',   'demo-user-bob',   'owner',  NOW(), NOW()),
      (gen_random_uuid()::text, '${ORGANIZATION_ID}', 'demo-user-bob',   'viewer', NOW(), NOW()),
      (gen_random_uuid()::text, '${ACME_ORG_ID}',     'demo-user-bob',   'viewer', NOW(), NOW()),
      -- Carol: owner in Acme, viewer in AxiaOps Dev + Globex
      (gen_random_uuid()::text, '${ACME_ORG_ID}',     'demo-user-carol', 'owner',  NOW(), NOW()),
      (gen_random_uuid()::text, '${ORGANIZATION_ID}', 'demo-user-carol', 'viewer', NOW(), NOW()),
      (gen_random_uuid()::text, '${GLOBEX_ORG_ID}',   'demo-user-carol', 'viewer', NOW(), NOW())
      ON CONFLICT (organization_id, user_id) DO NOTHING;"

    echo "Created demo personas: alice@axiaops.io / bob@globex.io / carol@acme.io"
    echo "  Password sourced from DEMO_USERS_PASSWORD (not echoed)."
  else
    echo "Skipping demo personas (alice/bob/carol) — DEMO_USERS_PASSWORD not set."
    echo "  Set DEMO_USERS_PASSWORD before re-running to mint login-capable demo users."
    echo "  See docs/demo-setup.md for the recommended workflow."
  fi

  # Copy the dev seed data into Acme + Globex. INSERT...SELECT with
  # REPLACE() on the seed-account-* prefix gives us acme-account-001 /
  # globex-account-001 etc. We DELETE first by the translated prefix so
  # re-running is idempotent.
  for target in "acme:${ACME_ORG_ID}" "globex:${GLOBEX_ORG_ID}"; do
    prefix="${target%%:*}"
    target_org="${target#*:}"
    echo "Copying seed data → $prefix (org: $target_org)"

    # Clean prior copies (idempotent re-run). zombie_records and resource_records
    # have no unique constraint covering (organization_id, resource_id,
    # period_start), so the downstream INSERT...SELECT ... ON CONFLICT DO NOTHING
    # would otherwise accumulate duplicates on every re-run.
    psql_exec "DELETE FROM zombie_snapshots WHERE id LIKE 'snap-${prefix}-account-%';"
    psql_exec "DELETE FROM accounts WHERE id LIKE '${prefix}-account-%';"
    psql_exec "DELETE FROM cost_records WHERE internal_account_id LIKE '${prefix}-account-%';"
    psql_exec "DELETE FROM zombie_records WHERE internal_account_id LIKE '${prefix}-account-%';"
    psql_exec "DELETE FROM resource_records WHERE internal_account_id LIKE '${prefix}-account-%';"

    # Translate seed-account-* IDs → ${prefix}-account-* on every per-account
    # table. snapshot_id rewrite happens via the snap-seed-account- pattern.
    psql_pipe <<EOF_DEMO
INSERT INTO accounts (id, organization_id, provider, label, account_id, access_key_id, secret_encrypted, region, status, created_at)
SELECT
  REPLACE(id, 'seed-', '${prefix}-'),
  '${target_org}',
  provider,
  REPLACE(label, 'Seed ', INITCAP('${prefix}') || ' '),
  account_id, access_key_id, secret_encrypted, region, status, created_at
FROM accounts
WHERE id LIKE 'seed-account-%' AND organization_id = '${ORGANIZATION_ID}'
ON CONFLICT DO NOTHING;

INSERT INTO zombie_records (organization_id, provider, account_id, internal_account_id, service, resource_type, region, resource_id, tags, monthly_cost, currency,
   period_start, period_end, usage_metric, usage_avg, usage_unit, reason, owner, detected_at)
SELECT
  '${target_org}',
  provider, account_id,
  REPLACE(internal_account_id, 'seed-', '${prefix}-'),
  service, resource_type, region, resource_id, tags, monthly_cost, currency,
  period_start, period_end, usage_metric, usage_avg, usage_unit, reason, owner, detected_at
FROM zombie_records
WHERE internal_account_id LIKE 'seed-account-%' AND organization_id = '${ORGANIZATION_ID}'
ON CONFLICT DO NOTHING;

INSERT INTO resource_records (organization_id, provider, account_id, internal_account_id, service, resource_type, region, resource_id, tags, monthly_cost, currency,
   period_start, period_end, usage_metric, usage_avg, usage_unit, is_zombie, reason, owner, detected_at)
SELECT
  '${target_org}',
  provider, account_id,
  REPLACE(internal_account_id, 'seed-', '${prefix}-'),
  service, resource_type, region, resource_id, tags, monthly_cost, currency,
  period_start, period_end, usage_metric, usage_avg, usage_unit, is_zombie, reason, owner, detected_at
FROM resource_records
WHERE internal_account_id LIKE 'seed-account-%' AND organization_id = '${ORGANIZATION_ID}'
ON CONFLICT DO NOTHING;

INSERT INTO zombie_snapshots (id, organization_id, account_id, snapshot_at, zombie_count, total_monthly_cost, currency)
SELECT
  REPLACE(id, 'snap-seed-', 'snap-${prefix}-'),
  '${target_org}',
  REPLACE(account_id, 'seed-', '${prefix}-'),
  snapshot_at, zombie_count, total_monthly_cost, currency
FROM zombie_snapshots
WHERE id LIKE 'snap-seed-account-%' AND organization_id = '${ORGANIZATION_ID}'
ON CONFLICT DO NOTHING;

INSERT INTO zombie_snapshot_services (id, snapshot_id, organization_id, service, resource_type, zombie_count, monthly_cost, currency)
SELECT
  REPLACE(id, 'svc-seed-', 'svc-${prefix}-'),
  REPLACE(snapshot_id, 'snap-seed-', 'snap-${prefix}-'),
  '${target_org}',
  service, resource_type, zombie_count, monthly_cost, currency
FROM zombie_snapshot_services
WHERE snapshot_id LIKE 'snap-seed-account-%' AND organization_id = '${ORGANIZATION_ID}'
ON CONFLICT DO NOTHING;

INSERT INTO cost_records (organization_id, provider, account_id, internal_account_id, service, region, resource_id, amount, currency, period_start, period_end, tags, fetched_at)
SELECT
  '${target_org}',
  provider, account_id,
  REPLACE(internal_account_id, 'seed-', '${prefix}-'),
  service, region, resource_id, amount, currency, period_start, period_end, tags, fetched_at
FROM cost_records
WHERE internal_account_id LIKE 'seed-account-%' AND organization_id = '${ORGANIZATION_ID}'
ON CONFLICT DO NOTHING;
EOF_DEMO
  done

  # Per-org cost multipliers so each demo org tells a distinct story when the
  # B1.5 switcher flips between them. Baseline (dev org) stays 1×; Acme ×10
  # (enterprise persona), Globex ×3 (mid-size persona). Applied across every
  # money-bearing table the demo INSERTs populated, narrowed by the demo
  # prefix per table so any non-demo data co-residing in the same org row
  # (real scans on a future preview/demo deployment, etc.) never gets
  # accidentally scaled. Wrapped in BEGIN/COMMIT so a connection drop
  # mid-block can't leave the org with some tables scaled and others not —
  # that would break the #91 invariant until the next re-run. Idempotent:
  # re-running rebuilds from the dev baseline (DELETE-first guards above)
  # and re-applies the multiplier. CASE has an explicit ELSE 1 so adding a
  # third demo org to the IN list without a matching WHEN can't produce a
  # NULL factor (which would coerce monthly_cost to NULL and fail the NOT
  # NULL constraint at UPDATE time).
  psql_pipe <<EOF_DEMO_SCALE
BEGIN;
CREATE TEMP TABLE _demo_factors ON COMMIT DROP AS
  SELECT id AS organization_id,
         CASE org_code WHEN 'org_acme' THEN 'acme'::text WHEN 'org_globex' THEN 'globex'::text END AS prefix,
         CASE org_code WHEN 'org_acme' THEN 10::numeric WHEN 'org_globex' THEN 3::numeric ELSE 1::numeric END AS factor
  FROM organizations
  WHERE org_code IN ('org_acme','org_globex');

-- Both CASE expressions must enumerate every org_code in the IN clause above.
-- The factor CASE has ELSE 1 (prevents NULL coercion → NOT NULL UPDATE failure),
-- but the prefix CASE has no safe default — an unknown prefix becomes NULL, every
-- LIKE f.prefix || '...' predicate evaluates to NULL (falsy), and the five UPDATEs
-- silently match zero rows. Same vacuous-pass class of bug the demo-org invariant
-- check below was added to prevent; assert here so the transaction rolls back
-- with a loud diagnostic instead of leaving the maintainer hunting a no-op.
DO \$\$ BEGIN
  IF EXISTS (SELECT 1 FROM _demo_factors WHERE prefix IS NULL) THEN
    RAISE EXCEPTION 'demo factors: org_code in IN clause has no matching prefix WHEN — extend the CASE before re-running';
  END IF;
END \$\$;

UPDATE zombie_records           z SET monthly_cost       = z.monthly_cost       * f.factor FROM _demo_factors f WHERE z.organization_id = f.organization_id AND z.internal_account_id LIKE f.prefix || '-account-%';
UPDATE resource_records         r SET monthly_cost       = r.monthly_cost       * f.factor FROM _demo_factors f WHERE r.organization_id = f.organization_id AND r.internal_account_id LIKE f.prefix || '-account-%';
UPDATE zombie_snapshot_services s SET monthly_cost       = s.monthly_cost       * f.factor FROM _demo_factors f WHERE s.organization_id = f.organization_id AND s.snapshot_id LIKE 'snap-' || f.prefix || '-account-%';
UPDATE zombie_snapshots         z SET total_monthly_cost = z.total_monthly_cost * f.factor FROM _demo_factors f WHERE z.organization_id = f.organization_id AND z.id LIKE 'snap-' || f.prefix || '-account-%';
UPDATE cost_records             c SET amount             = c.amount             * f.factor FROM _demo_factors f WHERE c.organization_id = f.organization_id AND c.internal_account_id LIKE f.prefix || '-account-%';
COMMIT;
EOF_DEMO_SCALE

  # Demo-org #91 invariant gap check. The dev-org check at the end of the
  # script filters on `snap-seed-account-%` only — it doesn't cover the
  # demo-translated rows, so without these per-org assertions the claim
  # "every related row scales by the same factor → invariant preserved"
  # would never be exercised at run time. Any future edit to the multiplier
  # block that misses a table or introduces a rounding drift will fail
  # loudly here instead of being discovered weeks later via /v1/summary
  # vs /v1/trend reconciliation drift.
  for demo_pair in "acme:${ACME_ORG_ID}" "globex:${GLOBEX_ORG_ID}"; do
    inv_prefix="${demo_pair%%:*}"
    inv_org="${demo_pair#*:}"
    # Guard against the earlier psql_query for {ACME,GLOBEX}_ORG_ID returning
    # empty stdout (transient connection drop that didn't trip set -e because
    # psql exited 0). Without this, the SQL below would WHERE on '' and both
    # gap queries would COALESCE to 0 → awk threshold passes → vacuous "✓"
    # for an org the script never actually checked.
    if [[ -z "$inv_org" ]]; then
      echo "  ${inv_prefix} #91 invariants ✗  (${inv_prefix}_ORG_ID is empty — earlier org-creation step failed silently)" >&2
      exit 1
    fi
    DEMO_CROSS_GAP=$(psql_query "
      SELECT COALESCE(SUM(ABS(snap_total - rec_sum))::numeric(12,2), 0)
      FROM (
        SELECT latest.account_id, latest.total_monthly_cost AS snap_total, COALESCE(zr.rec_sum, 0) AS rec_sum
        FROM (
          SELECT DISTINCT ON (account_id) account_id, total_monthly_cost, snapshot_at
          FROM zombie_snapshots
          WHERE organization_id = '${inv_org}' AND id LIKE 'snap-${inv_prefix}-account-%'
          ORDER BY account_id, snapshot_at DESC
        ) latest
        LEFT JOIN (
          SELECT internal_account_id AS account_id, SUM(monthly_cost) AS rec_sum
          FROM zombie_records
          WHERE organization_id = '${inv_org}' AND internal_account_id LIKE '${inv_prefix}-account-%'
          GROUP BY internal_account_id
        ) zr ON zr.account_id = latest.account_id
      ) joined;" | tr -d '[:space:]')
    DEMO_WITHIN_GAP=$(psql_query "
      SELECT COALESCE(SUM(ABS(s.total_monthly_cost - svc.cost_sum))::numeric(12,2), 0)
      FROM zombie_snapshots s
      JOIN (
        SELECT snapshot_id, SUM(monthly_cost) AS cost_sum
        FROM zombie_snapshot_services
        WHERE organization_id = '${inv_org}' AND snapshot_id LIKE 'snap-${inv_prefix}-account-%'
        GROUP BY snapshot_id
      ) svc ON svc.snapshot_id = s.id
      WHERE s.organization_id = '${inv_org}' AND s.id LIKE 'snap-${inv_prefix}-account-%';" | tr -d '[:space:]')
    if awk -v c="$DEMO_CROSS_GAP" -v w="$DEMO_WITHIN_GAP" 'BEGIN { exit !((c+0 <= 0.01) && (w+0 <= 0.01)) }'; then
      echo "  ${inv_prefix} #91 invariants ✓  (cross gap: \$${DEMO_CROSS_GAP}, within gap: \$${DEMO_WITHIN_GAP})"
    else
      echo "  ${inv_prefix} #91 invariants ✗  (cross gap: \$${DEMO_CROSS_GAP}, within gap: \$${DEMO_WITHIN_GAP})" >&2
      echo "                    Multiplier block must scale every related row by the same factor — investigate." >&2
      exit 1
    fi
  done

  echo "  Demo orgs populated (Acme ×10, Globex ×3)."
  echo ""
fi

# ── Verify seeded data ────────────────────────────────────────────────────────
# Quick sanity check: count rows per table for the dev organization.

echo "=== Verifying dev organization data ==="
ZOMBIE_COUNT=$(psql_query "SELECT COUNT(*) FROM zombie_records WHERE organization_id = '${ORGANIZATION_ID}';")
RESOURCE_COUNT=$(psql_query "SELECT COUNT(*) FROM resource_records WHERE organization_id = '${ORGANIZATION_ID}';")
SNAPSHOT_COUNT=$(psql_query "SELECT COUNT(*) FROM zombie_snapshots WHERE organization_id = '${ORGANIZATION_ID}';")
SVC_COUNT=$(psql_query "SELECT COUNT(*) FROM zombie_snapshot_services WHERE organization_id = '${ORGANIZATION_ID}';" 2>/dev/null || echo "n/a")
COST_COUNT=$(psql_query "SELECT COUNT(*) FROM cost_records WHERE organization_id = '${ORGANIZATION_ID}';" 2>/dev/null || echo "n/a")
echo "Dev organization zombie records:      $ZOMBIE_COUNT  (expected 41)"
echo "Dev organization resource records:    $RESOURCE_COUNT  (expected 33)"
echo "Dev organization zombie snapshots:    $SNAPSHOT_COUNT  (expected $((DAYS * 3)))"
echo "Dev organization snapshot services:   $SVC_COUNT"
echo "Dev organization cost records:        $COST_COUNT  (expected $((DAYS * 14)))"
echo ""

# Hard row-count gate before the gap-math invariants below. Without this, an
# empty result set in either invariant query coalesces to gap=\$0.00 and the
# awk gates pass vacuously. The invariants below assume there ARE snapshots
# to compare; assert that here so a partial-state DB fails loud instead of
# silent-passing through a gap-of-\$0.00 false positive.
SEED_SNAPSHOT_COUNT=$(psql_query "SELECT COUNT(*) FROM zombie_snapshots WHERE id LIKE 'snap-seed-account-%';" | tr -d '[:space:]')
SEED_SVC_COUNT=$(psql_query "SELECT COUNT(*) FROM zombie_snapshot_services WHERE snapshot_id LIKE 'snap-seed-account-%';" | tr -d '[:space:]')
EXPECTED_SNAPSHOTS=$((DAYS * 3))   # 3 dev accounts × DAYS days
if [[ "$SEED_SNAPSHOT_COUNT" -ne "$EXPECTED_SNAPSHOTS" ]]; then
  echo "  snapshot count: FAIL ($SEED_SNAPSHOT_COUNT seed rows, expected $EXPECTED_SNAPSHOTS)" >&2
  echo "                  Issue #91 invariant verification would silently pass with no rows; aborting." >&2
  exit 1
fi
if [[ "$SEED_SVC_COUNT" -eq 0 ]]; then
  echo "  service-row count: FAIL ($SEED_SVC_COUNT seed rows, expected >0)" >&2
  exit 1
fi

# Invariant assertions — issue #91. The seed fixture must satisfy the same
# invariants as the production scan path, otherwise dev debugging of /v1/summary
# vs /v1/trend reconciliation (issue #90) chases ghosts that don't exist in prod.
#   (1) Cross-table: SUM(zombie_records.monthly_cost) per account
#                  == latest zombie_snapshots.total_monthly_cost per account.
#   (2) Within-row: SUM(zombie_snapshot_services.monthly_cost where snapshot_id=X)
#                  == zombie_snapshots.total_monthly_cost where id=X, for every snapshot.
# Tolerance is 0.01 USD per account / per snapshot to absorb any incidental
# rounding (the SQL above is exact NUMERIC arithmetic, so this should report 0).

echo "=== Invariant checks (issue #91) ==="
CROSS_TABLE_GAP=$(psql_query "
SELECT COALESCE(SUM(ABS(snap_total - rec_sum))::numeric(12,2), 0)
FROM (
  SELECT
    latest.account_id,
    latest.total_monthly_cost AS snap_total,
    COALESCE(zr.rec_sum, 0)   AS rec_sum
  FROM (
    SELECT DISTINCT ON (account_id) account_id, total_monthly_cost, snapshot_at
    FROM zombie_snapshots
    WHERE organization_id = '${ORGANIZATION_ID}'
      AND id LIKE 'snap-seed-account-%'
    ORDER BY account_id, snapshot_at DESC
  ) latest
  LEFT JOIN (
    SELECT internal_account_id AS account_id, SUM(monthly_cost) AS rec_sum
    FROM zombie_records
    WHERE organization_id = '${ORGANIZATION_ID}'
      AND internal_account_id IN ('${ACCT1}','${ACCT2}','${ACCT3}')
    GROUP BY internal_account_id
  ) zr ON zr.account_id = latest.account_id
) joined;" | tr -d '[:space:]')

WITHIN_ROW_GAP=$(psql_query "
SELECT COALESCE(SUM(ABS(s.total_monthly_cost - svc.cost_sum))::numeric(12,2), 0)
FROM zombie_snapshots s
JOIN (
  SELECT snapshot_id, SUM(monthly_cost) AS cost_sum
  FROM zombie_snapshot_services
  WHERE organization_id = '${ORGANIZATION_ID}'
    AND snapshot_id LIKE 'snap-seed-account-%'
  GROUP BY snapshot_id
) svc ON svc.snapshot_id = s.id
WHERE s.organization_id = '${ORGANIZATION_ID}'
  AND s.id LIKE 'snap-seed-account-%';" | tr -d '[:space:]')

# awk arithmetic — bash's [[ -lt ]] doesn't do floats. Threshold 0.01 USD total
# (i.e. across all accounts/snapshots) absorbs any incidental rounding noise.
if awk -v g="$CROSS_TABLE_GAP" 'BEGIN { exit !(g+0 <= 0.01) }'; then
  echo "  cross-table  ✓  SUM(zombie_records) == latest snapshot per account (gap: \$${CROSS_TABLE_GAP})"
else
  echo "  cross-table  ✗  SUM(zombie_records) != latest snapshot per account (gap: \$${CROSS_TABLE_GAP})" >&2
  echo "                  Issue #91 invariant violated — investigate the snapshot SQL block above." >&2
  exit 1
fi
if awk -v g="$WITHIN_ROW_GAP" 'BEGIN { exit !(g+0 <= 0.01) }'; then
  echo "  within-row   ✓  SUM(services) == snapshot.total per snapshot (gap: \$${WITHIN_ROW_GAP})"
else
  echo "  within-row   ✗  SUM(services) != snapshot.total per snapshot (gap: \$${WITHIN_ROW_GAP})" >&2
  echo "                  Issue #91 invariant violated — investigate the snapshot SQL block above." >&2
  exit 1
fi
echo ""

echo "=== Done ==="
echo "Dev organization ID: ${ORGANIZATION_ID}"
echo "DEV_ORGANIZATION_ID=${ORGANIZATION_ID} is set automatically by dev.sh"
echo ""
echo "Workflow:"
echo "  make start-dev   — start all services (dev mode, no auth)"
echo "  make seed        — (re-)populate dummy data"
echo "  open http://localhost:5173"
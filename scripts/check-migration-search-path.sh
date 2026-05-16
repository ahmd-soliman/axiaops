#!/usr/bin/env bash
# check-migration-search-path.sh
#
# Fails (exit 1) if any migration file in
# services/shared/storage/postgres/migrations/ is missing the line
#
#     SET search_path TO axiaops;
#
# The axiaops schema is non-default; without the SET line, unqualified
# identifiers (e.g. `ALTER TABLE sessions`) resolve against `public`
# and fail with "relation does not exist". Migration 027 shipped this
# bug to preview before the check was added — see docs/migrations.md.
#
# An allowlist excludes historical migrations that intentionally use
# fully-qualified `axiaops.<name>` identifiers instead of relying on
# search_path. New migrations should prefer SET search_path for
# consistency with the dominant convention (21 of 27 files).
#
# Exit codes:
#   0 — clean
#   1 — found a migration missing the SET search_path line

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

MIGRATIONS_DIR="services/shared/storage/postgres/migrations"

# Files that legitimately omit `SET search_path TO axiaops;` because they
# use fully-qualified `axiaops.<name>` identifiers throughout.
# Keep in sync with whatever convention the migration actually follows.
ALLOWED_FILES=(
  "000_init.up.sql"
  "000_init.down.sql"
  "008_normalize_service_names.up.sql"
  "008_normalize_service_names.down.sql"
  "009_add_account_id_to_accounts.up.sql"
  "009_add_account_id_to_accounts.down.sql"
  "011_add_rls_with_check.up.sql"
  "011_add_rls_with_check.down.sql"
  "025_migration_history_revoke_dml.up.sql"
  "025_migration_history_revoke_dml.down.sql"
  "026_rename_and_harden_migration_state.up.sql"
  "026_rename_and_harden_migration_state.down.sql"
)

is_allowed() {
  local base="$1"
  for allowed in "${ALLOWED_FILES[@]}"; do
    if [[ "$base" == "$allowed" ]]; then
      return 0
    fi
  done
  return 1
}

violations=()

for f in "$MIGRATIONS_DIR"/*.up.sql "$MIGRATIONS_DIR"/*.down.sql; do
  [[ -f "$f" ]] || continue
  base="$(basename "$f")"
  if is_allowed "$base"; then
    continue
  fi
  if ! grep -qE '^[[:space:]]*SET[[:space:]]+search_path[[:space:]]+TO[[:space:]]+axiaops[[:space:]]*;' "$f"; then
    violations+=("$f")
  fi
done

if [[ ${#violations[@]} -eq 0 ]]; then
  echo "✓ All migrations declare SET search_path TO axiaops (or are explicitly allowlisted)."
  exit 0
fi

echo "✗ Migrations missing 'SET search_path TO axiaops;':"
echo
for v in "${violations[@]}"; do
  echo "  - $v"
done
echo
echo "Add the line near the top of each file:"
echo
echo "    SET search_path TO axiaops;"
echo
echo "Without it, unqualified identifiers resolve against 'public' and fail"
echo "with 'relation does not exist'. Alternatively, use fully-qualified"
echo "axiaops.<name> identifiers throughout AND add the file to ALLOWED_FILES"
echo "in $0."
exit 1

#!/usr/bin/env sh
# Reap orphaned integration-test compose stacks left behind by CI jobs whose
# teardown never ran — specifically CANCELLED / TIMED-OUT jobs (e.g. GitLab
# auto-cancelling a redundant pipeline when you push again to an MR). Those jobs
# exit with CI_JOB_STATUS=canceled, so the job's `after_script` teardown (which
# only fires on "failed") is skipped, and on a hard cancel the after_script may
# not run at all. The stack then runs forever on the shared runner host.
#
# This is the backstop: every integration job runs it in `before_script`, so the
# next integration job reaps the previous job's orphan even if nothing else did.
# It is AGE-GATED so it can never tear down a live, concurrently-running job's
# stack (api + ingestion run in the same pipeline; other MRs' pipelines may run
# at the same time). Defaults to 2h, well above the integration job timeout.
#
# Reaps by the compose project label (containers + networks + volumes) rather
# than `docker compose down`, so it works without the project's compose file in
# the working directory. Safe to run anywhere with access to the runner's docker
# daemon (CI, or ad-hoc over `self-hosted exec <runner> -- ...`).
#
# Env knobs:
#   STALE_HOURS  age threshold in hours (default 2)
#   NAME_PREFIX  container-name prefix to match (default axiaops-test-infra-)
#   DRY_RUN      when "1", report what would be reaped but change nothing
set -u

STALE_HOURS="${STALE_HOURS:-2}"
NAME_PREFIX="${NAME_PREFIX:-axiaops-test-infra-}"
DRY_RUN="${DRY_RUN:-0}"

now=$(date +%s)
threshold=$((now - STALE_HOURS * 3600))

# Distinct compose projects among currently-running test-infra containers.
projects=$(docker ps --filter "name=${NAME_PREFIX}" \
  --format '{{ .Label "com.docker.compose.project" }}' 2>/dev/null \
  | grep -v '^$' | sort -u)

if [ -z "${projects}" ]; then
  echo "reap: no ${NAME_PREFIX}* stacks running — nothing to do"
  exit 0
fi

for proj in ${projects}; do
  cid=$(docker ps -q --filter "label=com.docker.compose.project=${proj}" | head -n1)
  [ -n "${cid}" ] || continue

  started_iso=$(docker inspect -f '{{.State.StartedAt}}' "${cid}" 2>/dev/null)
  started=$(date -d "${started_iso}" +%s 2>/dev/null || echo 0)
  if [ "${started}" -eq 0 ]; then
    # Couldn't determine age — fail safe by KEEPING it (never nuke blind).
    echo "reap: SKIP ${proj} — could not parse start time '${started_iso}'"
    continue
  fi

  age_h=$(((now - started) / 3600))
  if [ "${started}" -ge "${threshold}" ]; then
    echo "reap: keep ${proj} (age ${age_h}h < ${STALE_HOURS}h — likely a live job)"
    continue
  fi

  if [ "${DRY_RUN}" = "1" ]; then
    echo "reap: [dry-run] would remove stale stack ${proj} (age ${age_h}h >= ${STALE_HOURS}h)"
    continue
  fi

  echo "reap: removing stale stack ${proj} (age ${age_h}h >= ${STALE_HOURS}h)"
  # Containers, then the project's networks + volumes. label filters make this
  # independent of having the compose file present.
  ids=$(docker ps -aq --filter "label=com.docker.compose.project=${proj}")
  [ -n "${ids}" ] && docker rm -f ${ids} >/dev/null 2>&1 || true
  docker network ls -q --filter "label=com.docker.compose.project=${proj}" \
    | xargs -r docker network rm >/dev/null 2>&1 || true
  docker volume ls -q --filter "label=com.docker.compose.project=${proj}" \
    | xargs -r docker volume rm >/dev/null 2>&1 || true
done

echo "reap: done"

#!/usr/bin/env bash
# smoke-metrics-locked.sh — assert /api/metrics is blocked at the public
# ingress (nginx in front of the dashboard) while internal probes still
# work. Pairs with `services/dashboard/nginx.conf`.
#
# Usage:
#   ./scripts/smoke-metrics-locked.sh                 # localhost staging
#   DASHBOARD_URL=https://app.example.com smoke...    # any deployed env
#
# Exit codes:
#   0  — public path correctly returns 404, livez (sanity) still 200
#   1  — public path served metrics or returned anything other than 404
#   2  — sanity probe failed (stack not reachable; not a regression)

set -euo pipefail

DASHBOARD_URL="${DASHBOARD_URL:-http://localhost:8082}"

probe_status() {
    # On transport error (DNS failure, connection refused, timeout) curl
    # itself prints `000` to stdout via %{http_code} and exits non-zero;
    # `|| true` lets set -e through, and ${out:-000} backstops the
    # sentinel if a future curl ever omits its own emission.
    local out
    out="$(curl --silent --output /dev/null \
                --write-out '%{http_code}' \
                --max-time 5 \
                "$1" 2>/dev/null)" || true
    echo "${out:-000}"
}

# Sanity: stack is up and the api is reachable through the dashboard.
livez_status="$(probe_status "${DASHBOARD_URL}/api/livez")"
if [[ "${livez_status}" != "200" ]]; then
    echo "smoke-metrics-locked: ${DASHBOARD_URL}/api/livez returned ${livez_status} (expected 200)" >&2
    echo "smoke-metrics-locked: sanity probe failed — is the stack running?" >&2
    exit 2
fi

# Regression: /api/metrics MUST be blocked at the public ingress.
metrics_status="$(probe_status "${DASHBOARD_URL}/api/metrics")"
if [[ "${metrics_status}" != "404" ]]; then
    echo "smoke-metrics-locked: ${DASHBOARD_URL}/api/metrics returned ${metrics_status} (expected 404)" >&2
    echo "smoke-metrics-locked: REGRESSION — metrics are publicly scrapable" >&2
    exit 1
fi

echo "smoke-metrics-locked: ${DASHBOARD_URL}/api/metrics → 404 (locked); /api/livez → 200 (sanity)"

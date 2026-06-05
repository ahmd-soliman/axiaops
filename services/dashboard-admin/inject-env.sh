#!/bin/sh
# Substitute ADMIN_API_HOST in the nginx config for per-environment routing to
# the api-admin backend. Runs automatically in the nginx docker-entrypoint.d/
# chain. Unlike the tenant dashboard there is no runtime-env.js to write — the
# admin SPA hard-defaults its API base to "/admin" (same-origin), so only the
# nginx upstream host needs templating.
#
# envsubst is restricted to '${ADMIN_API_HOST}' so it does NOT touch nginx's own
# $remote_addr / $host / $uri variables in the config.
set -e

ADMIN_API_HOST="${ADMIN_API_HOST:-api-admin}" \
  envsubst '${ADMIN_API_HOST}' < /etc/nginx/conf.d/default.conf > /tmp/default.conf
cp /tmp/default.conf /etc/nginx/conf.d/default.conf

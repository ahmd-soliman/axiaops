#!/bin/sh
# Writes runtime env vars into /usr/share/nginx/html/runtime-env.js as
# `window.__ENV__ = {...}` before nginx starts. Also substitutes API_HOST
# in nginx config for per-environment API routing. Runs automatically as
# part of the nginx docker-entrypoint.d/ chain.
#
# Why an external file (not an inline <script> in index.html): the strict
# `script-src 'self'` CSP in nginx.conf forbids inline scripts. Serving
# the env as a same-origin .js file keeps CSP strict with no nonce/hash
# plumbing. The default file shipped in the bundle (`public/runtime-env.js`)
# sets `window.__ENV__ = {}`; this script overwrites it with real values.
#
# Values are read via awk's ENVIRON and JS-escaped (\, ', <) before being
# embedded in the single-quoted JS string literals.
set -e

ENV_JS=/usr/share/nginx/html/runtime-env.js

awk 'BEGIN {
  n = split("DEV_MODE DEV_ORG_NAME FEATURE_ROLE_AUTH AXIAOPS_AWS_ACCOUNT_ID", keys, " ")
  printf "window.__ENV__ = {"
  for (i = 1; i <= n; i++) {
    v = ENVIRON[keys[i]]
    gsub(/\\/, "\\\\", v)
    gsub(/'\''/, "\\'\''", v)
    gsub(/</, "\\u003c", v)
    if (i > 1) printf ","
    printf "%s:%c%s%c", keys[i], 39, v, 39
  }
  printf "};\n"
}' > "$ENV_JS.tmp" && mv "$ENV_JS.tmp" "$ENV_JS"

# Substitute API_HOST in nginx config (defaults to "api" for local docker-compose)
API_HOST="${API_HOST:-api}" envsubst '${API_HOST}' < /etc/nginx/conf.d/default.conf > /tmp/default.conf
cp /tmp/default.conf /etc/nginx/conf.d/default.conf

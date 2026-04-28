#!/bin/sh
# Injects runtime env vars into index.html as window.__ENV__ before nginx starts.
# Also substitutes API_HOST in nginx config for per-environment API routing.
# Runs automatically as part of the nginx docker-entrypoint.d/ chain.
#
# Values are read via awk's ENVIRON and JS-escaped (\, ', <) before being
# embedded in the single-quoted JS string literals, and the snippet is
# inserted with awk rather than sed so that & \ | in any value can never
# mangle the substitution.
set -e

INDEX=/usr/share/nginx/html/index.html

SNIPPET=$(awk 'BEGIN {
  n = split("DEV_MODE KINDE_ISSUER KINDE_CLIENT_ID DEV_ORG_NAME FEATURE_ROLE_AUTH AXIAOPS_AWS_ACCOUNT_ID", keys, " ")
  printf "<script>window.__ENV__={"
  for (i = 1; i <= n; i++) {
    v = ENVIRON[keys[i]]
    gsub(/\\/, "\\\\", v)
    gsub(/'\''/, "\\'\''", v)
    gsub(/</, "\\u003c", v)
    if (i > 1) printf ","
    printf "%s:%c%s%c", keys[i], 39, v, 39
  }
  printf "};</script>"
}')

# Insert snippet just before </head>. awk avoids sed's special handling of
# & and \ in the replacement expression.
SNIPPET="$SNIPPET" awk '
  {
    idx = index($0, "</head>")
    if (idx > 0) {
      print substr($0, 1, idx - 1) ENVIRON["SNIPPET"] substr($0, idx)
    } else {
      print
    }
  }
' "$INDEX" > "$INDEX.tmp" && mv "$INDEX.tmp" "$INDEX"

# Substitute API_HOST in nginx config (defaults to "api" for local docker-compose)
API_HOST="${API_HOST:-api}" envsubst '${API_HOST}' < /etc/nginx/conf.d/default.conf > /tmp/default.conf
cp /tmp/default.conf /etc/nginx/conf.d/default.conf

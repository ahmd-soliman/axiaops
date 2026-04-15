#!/bin/sh
# Injects runtime env vars into index.html as window.__ENV__ before nginx starts.
# Also substitutes API_HOST in nginx config for per-environment API routing.
# Runs automatically as part of the nginx docker-entrypoint.d/ chain.
set -e

INDEX=/usr/share/nginx/html/index.html

SNIPPET="<script>window.__ENV__={DEV_MODE:'${DEV_MODE}',KINDE_ISSUER:'${KINDE_ISSUER}',KINDE_CLIENT_ID:'${KINDE_CLIENT_ID}',DEV_ORG_NAME:'${DEV_ORG_NAME}'};</script>"

# Insert snippet just before </head>
sed -i "s|</head>|${SNIPPET}</head>|" "$INDEX"

# Substitute API_HOST in nginx config (defaults to "api" for local docker-compose)
API_HOST="${API_HOST:-api}" envsubst '${API_HOST}' < /etc/nginx/conf.d/default.conf > /tmp/default.conf
cp /tmp/default.conf /etc/nginx/conf.d/default.conf

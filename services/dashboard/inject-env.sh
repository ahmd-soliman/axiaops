#!/bin/sh
# Injects runtime env vars into index.html as window.__ENV__ before nginx starts.
# Runs automatically as part of the nginx docker-entrypoint.d/ chain.
set -e

INDEX=/usr/share/nginx/html/index.html

SNIPPET="<script>window.__ENV__={DEV_MODE:'${DEV_MODE}',KINDE_ISSUER:'${KINDE_ISSUER}',KINDE_CLIENT_ID:'${KINDE_CLIENT_ID}',DEV_ORG_NAME:'${DEV_ORG_NAME}'};</script>"

# Insert snippet just before </head>
sed -i "s|</head>|${SNIPPET}</head>|" "$INDEX"

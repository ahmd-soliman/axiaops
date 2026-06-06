# Admin-plane staff console — Vite build → nginx static + /admin proxy.
# Mirrors services/dashboard/Dockerfile. No VITE_* build args: the admin SPA
# resolves its API base to "/admin" same-origin at runtime (src/api/admin.js),
# so there is nothing to inject into the bundle — only nginx upstream templating
# (ADMIN_API_HOST) at container start, via inject-env.sh.
FROM node:22-alpine AS builder
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=builder /app/dist /usr/share/nginx/html
# Normalise docroot perms so the non-root nginx worker (uid 101) can read
# everything regardless of the build host's umask. See services/dashboard for
# the full rationale (Vite public-dir static copies inheriting 0770 → 403).
RUN chmod -R u=rwX,go=rX /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY inject-env.sh /docker-entrypoint.d/40-inject-env.sh
RUN chmod +x /docker-entrypoint.d/40-inject-env.sh
EXPOSE 80

FROM node:22-alpine AS builder
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .

ARG VITE_API_URL=/api
ARG VITE_DEV_MODE=false
# Version + commit SHA baked into the bundle so the footer can display them.
# Defaults to "dev" / "local" so a plain `docker build` without CI context
# still produces a usable image — production builds override via --build-arg.
ARG VITE_APP_VERSION=dev
ARG VITE_APP_COMMIT_SHA=local
# Feature flag + AxiaOps AWS account ID for cross-account-role onboarding.
# Both are also overridable at container start via inject-env.sh, so this
# is a baked-in default for the case where window.__ENV__ is missing.
ARG VITE_FEATURE_ROLE_AUTH=false
ARG VITE_AXIAOPS_AWS_ACCOUNT_ID=
ENV VITE_API_URL=$VITE_API_URL
ENV VITE_DEV_MODE=$VITE_DEV_MODE
ENV VITE_APP_VERSION=$VITE_APP_VERSION
ENV VITE_APP_COMMIT_SHA=$VITE_APP_COMMIT_SHA
ENV VITE_FEATURE_ROLE_AUTH=$VITE_FEATURE_ROLE_AUTH
ENV VITE_AXIAOPS_AWS_ACCOUNT_ID=$VITE_AXIAOPS_AWS_ACCOUNT_ID

RUN npm run build

FROM nginx:1.27-alpine
COPY --from=builder /app/dist /usr/share/nginx/html
# Normalise perms so the nginx worker (uid 101, non-root) can read everything
# under the docroot regardless of whatever umask the build host happened to
# carry into the BuildKit context. Without this, files routed through Vite's
# public-dir static-copy (favicon.svg, axiaops-logo*.svg) inherit the build
# host's mode — observed as 0770 root:root on GitLab runners — and 403 at
# request time. Generated assets/* are fine because Vite writes them fresh
# under the build process's umask. u=rwX,go=rX = 0644 for files, 0755 for dirs.
RUN chmod -R u=rwX,go=rX /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY inject-env.sh /docker-entrypoint.d/40-inject-env.sh
RUN chmod +x /docker-entrypoint.d/40-inject-env.sh
EXPOSE 80 443

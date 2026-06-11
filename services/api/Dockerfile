FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY services/shared/ /shared/
COPY services/api/go.mod services/api/go.sum ./
RUN go mod download
COPY services/api/ .
# BUILD_TAGS selects the license + DEV_MODE shape. Default empty = the DEFAULT
# build: SaaS (license bypassed at boot, scans gate on per-tenant entitlement)
# with DEV_MODE honoured — what every deployed env's image (build:images) uses.
#   BUILD_TAGS=production            → SaaS, DEV_MODE stripped (staging/prod).
#   BUILD_TAGS="production selfhosted" → the licensed CUSTOMER image: `selfhosted`
#       re-enables the license JWT gate (entitlement no-op), `production` strips
#       DEV_MODE. Built by the manual build:images-selfhosted job and shipped
#       pre-built to self-hosted customers. (`production` hard-wires
#       devModeEnabled() to false; `selfhosted` makes saasmode_selfhosted.go the
#       compiled seam, so the license bypass is absent.)
ARG BUILD_TAGS=
# Build the package (`./cmd/`) not the single file — without this, sibling
# build-tag-gated files (devmode_dev.go / devmode_production.go) aren't
# compiled in and the build fails with "undefined: devModeEnabled" under
# any -tags invocation.
RUN go build -tags "$BUILD_TAGS" -o api ./cmd/

FROM alpine:3.20
# ca-certificates: Go's TLS verification needs the Mozilla CA bundle for
# outbound HTTPS to AWS APIs (STS, SSM, etc.). Alpine bundles a minimal set
# by default; installing explicitly tracks the dependency and picks up
# bundle updates. ~1MB.
RUN apk add --no-cache ca-certificates
# AWS RDS CA bundle. The production DATABASE_URL connects with
# sslmode=verify-full&sslrootcert=/etc/ssl/certs/rds-ca-eu-central-1.pem;
# RDS presents an Amazon-issued cert that is NOT in the Mozilla bundle
# above, so pgx's verify-full needs this explicit trust anchor. We vendor
# the rotation-proof global bundle (all regional roots + future-dated CAs)
# at the region-named path the aws-infra secret references.
# See deploy/certs/README.md.
COPY deploy/certs/rds-global-bundle.pem /etc/ssl/certs/rds-ca-eu-central-1.pem
WORKDIR /app
COPY --from=builder /app/api .
# Build identifiers — populated by CI via --build-arg and surfaced via
# /v1/version and the slog `version` attribute. Defaults so a vanilla
# `docker build` without CI context still produces a usable image.
ARG APP_VERSION=dev
ARG APP_COMMIT_SHA=local
ENV APP_VERSION=$APP_VERSION
ENV APP_COMMIT_SHA=$APP_COMMIT_SHA
EXPOSE 8080
# Liveness probe — `/livez` is the lightweight "process is up" endpoint
# (does NOT touch DB). `/health` and `/readyz` are deeper checks; pointing
# the in-image HEALTHCHECK at /livez means a transient DB blip doesn't
# trigger a restart. ECS Express Mode + ALB target groups use their own
# health check via --health-check-path on the create-service call (also
# /livez); this in-image HEALTHCHECK is for `docker run` + dev/staging.
HEALTHCHECK --interval=30s --timeout=3s --start-period=15s --retries=3 \
    CMD wget -qO- http://localhost:8080/livez || exit 1
# Non-root runtime is intentionally NOT set. Earlier attempt (USER app)
# broke DNS on the GitLab shared runner: Go's pure-Go resolver couldn't
# open /etc/resolv.conf as the non-root user (the runner's Docker daemon
# bind-mounts the file with restrictive perms), fell back to defaults
# 127.0.0.1:53 + [::1]:53, and lookups failed with connection-refused.
# Fix deferred to a follow-up: either setcap CAP_DAC_READ_SEARCH on the
# binary, or migrate to a distroless `nonroot` base where the resolver
# path is known-compatible with UID 65532.
CMD ["./api"]

FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY services/shared/ /shared/
COPY services/api/go.mod services/api/go.sum ./
RUN go mod download
COPY services/api/ .
# BUILD_TAGS controls B1.7 layer 3 stripping. Default empty (dev image:
# DEV_MODE honoured, full feature set for internal envs). CI sets
# BUILD_TAGS=production for the customer-shipping image — that build's
# devmode_production.go hard-wires devModeEnabled() to false, so DEV_MODE
# is read but ignored regardless of how the operator sets the env.
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
RUN apk add --no-cache ca-certificates && \
    addgroup -S app && adduser -S app -G app
WORKDIR /app
COPY --from=builder /app/api .
# Build identifiers — populated by CI via --build-arg and surfaced via
# /v1/version and the slog `version` attribute. Defaults so a vanilla
# `docker build` without CI context still produces a usable image.
ARG APP_VERSION=dev
ARG APP_COMMIT_SHA=local
ENV APP_VERSION=$APP_VERSION
ENV APP_COMMIT_SHA=$APP_COMMIT_SHA
# Non-root runtime. Binary doesn't write to disk, doesn't bind privileged
# ports, doesn't need root capabilities. Defence-in-depth.
USER app
EXPOSE 8080
# Liveness probe — `/livez` is the lightweight "process is up" endpoint
# (does NOT touch DB). `/health` and `/readyz` are deeper checks; pointing
# the in-image HEALTHCHECK at /livez means a transient DB blip doesn't
# trigger a restart. ECS Express Mode + ALB target groups use their own
# health check via --health-check-path on the create-service call (also
# /livez); this in-image HEALTHCHECK is for `docker run` + dev/staging.
HEALTHCHECK --interval=30s --timeout=3s --start-period=15s --retries=3 \
    CMD wget -qO- http://localhost:8080/livez || exit 1
CMD ["./api"]

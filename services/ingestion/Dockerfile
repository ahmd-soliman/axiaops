FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY services/shared/ /shared/
COPY services/ingestion/go.mod services/ingestion/go.sum ./
RUN go mod download
COPY services/ingestion/ .
# BUILD_TAGS — see services/api/Dockerfile for the layer-3 rationale.
# Mirroring api's stripping for parity: an api binary that ignores
# DEV_MODE paired with an ingestion binary that honours it would still
# leak the bypass at the ingestion-side scan-gate.
ARG BUILD_TAGS=
RUN go build -tags "$BUILD_TAGS" -o ingestion ./cmd/

FROM alpine:3.20
# ca-certificates for Go TLS to AWS APIs (Cost Explorer, CloudWatch, STS,
# CloudWatch Logs, ECR). Alpine ships a minimal CA bundle by default;
# explicit install tracks the dependency. ~1MB.
RUN apk add --no-cache ca-certificates && \
    addgroup -S app && adduser -S app -G app
WORKDIR /app
COPY --from=builder /app/ingestion .
# Build identifiers — populated by CI via --build-arg and surfaced via the
# slog `version` attribute on every log line. Defaults so a vanilla
# `docker build` without CI context still produces a usable image.
ARG APP_VERSION=dev
ARG APP_COMMIT_SHA=local
ENV APP_VERSION=$APP_VERSION
ENV APP_COMMIT_SHA=$APP_COMMIT_SHA
# Non-root runtime. Binary doesn't write to disk, doesn't bind privileged
# ports, doesn't need root capabilities.
USER app
EXPOSE 8081
# Ingestion only exposes /health (not /livez — see cmd/main.go:149).
# /health is a flat "status: ok" response, doesn't touch DB.
HEALTHCHECK --interval=30s --timeout=3s --start-period=15s --retries=3 \
    CMD wget -qO- http://localhost:8081/health || exit 1
CMD ["./ingestion"]

FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY services/shared/ /shared/
COPY services/ingestion/go.mod services/ingestion/go.sum ./
RUN go mod download
COPY services/ingestion/ .
# BUILD_TAGS — see services/api/Dockerfile for the full matrix. Default empty =
# SaaS (license bypassed, entitlement-gated); `production selfhosted` = the
# licensed customer image. Mirrors api so the license + DEV_MODE shape is
# symmetric across the two binaries: an api that bypasses the license paired with
# an ingestion that enforced it (or vice versa) would gate scans inconsistently.
ARG BUILD_TAGS=
RUN go build -tags "$BUILD_TAGS" -o ingestion ./cmd/

FROM alpine:3.20
# ca-certificates for Go TLS to AWS APIs (Cost Explorer, CloudWatch, STS,
# CloudWatch Logs, ECR). Alpine ships a minimal CA bundle by default;
# explicit install tracks the dependency. ~1MB.
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
COPY --from=builder /app/ingestion .
# Build identifiers — populated by CI via --build-arg and surfaced via the
# slog `version` attribute on every log line. Defaults so a vanilla
# `docker build` without CI context still produces a usable image.
ARG APP_VERSION=dev
ARG APP_COMMIT_SHA=local
ENV APP_VERSION=$APP_VERSION
ENV APP_COMMIT_SHA=$APP_COMMIT_SHA
EXPOSE 8081
# Ingestion only exposes /health (not /livez — see cmd/main.go:149).
# /health is a flat "status: ok" response, doesn't touch DB.
HEALTHCHECK --interval=30s --timeout=3s --start-period=15s --retries=3 \
    CMD wget -qO- http://localhost:8081/health || exit 1
# Non-root runtime intentionally NOT set — see services/api/Dockerfile
# for the resolv.conf-permission issue that blocks USER app on the
# GitLab shared runner. Same deferral applies here.
CMD ["./ingestion"]

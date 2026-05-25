FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY services/shared/ /shared/
COPY services/migrate/go.mod services/migrate/go.sum ./
RUN go mod download
COPY services/migrate/ .
RUN go build -o migrate .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
# AWS RDS CA bundle. The production DATABASE_URL connects with
# sslmode=verify-full&sslrootcert=/etc/ssl/certs/rds-ca-eu-central-1.pem;
# RDS presents an Amazon-issued cert that is NOT in the Mozilla bundle
# installed above, so pgx's verify-full needs this explicit trust anchor.
# pgxpool.ParseConfig loads sslrootcert at parse time — a missing file fails
# Bootstrap before any connection attempt. We vendor the rotation-proof
# global bundle (all regional roots + future-dated CAs) at the region-named
# path the aws-infra secret references. See deploy/certs/README.md.
COPY deploy/certs/rds-global-bundle.pem /etc/ssl/certs/rds-ca-eu-central-1.pem
WORKDIR /app
COPY --from=builder /app/migrate .
# Build identifiers — populated by CI via --build-arg and surfaced as
# applied_by_image on every axiaops.migration_history row. Without these,
# the wrapper records "unknown@unknown" and slog.Warns at boot that drift
# forensics are degraded. Defaults so a vanilla `docker build` without CI
# context still produces a usable image.
ARG APP_VERSION=dev
ARG APP_COMMIT_SHA=local
ENV APP_VERSION=$APP_VERSION
ENV APP_COMMIT_SHA=$APP_COMMIT_SHA
# Non-root runtime intentionally NOT set — see services/api/Dockerfile
# for the resolv.conf-permission issue that blocks USER app on the
# GitLab shared runner. Same deferral applies here.
CMD ["./migrate"]

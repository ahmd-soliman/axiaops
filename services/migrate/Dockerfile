FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY services/shared/ /shared/
COPY services/migrate/go.mod services/migrate/go.sum ./
RUN go mod download
COPY services/migrate/ .
RUN go build -o migrate .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && \
    addgroup -S app && adduser -S app -G app
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
# Non-root runtime. Migrate is a one-shot binary that connects to RDS,
# applies SQL, exits. No HEALTHCHECK (it's not a service), no EXPOSE
# (no port). Non-root is defence-in-depth — the binary doesn't need
# any privileged ops; running as root just enlarges blast radius.
USER app
CMD ["./migrate"]

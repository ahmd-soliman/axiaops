FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY services/shared/ /shared/
COPY services/ingestion/go.mod services/ingestion/go.sum ./
RUN go mod download
COPY services/ingestion/ .
RUN go build -o ingestion ./cmd/

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /app/ingestion .
# Build identifiers — populated by CI via --build-arg and surfaced via the
# slog `version` attribute on every log line. Defaults so a vanilla
# `docker build` without CI context still produces a usable image.
ARG APP_VERSION=dev
ARG APP_COMMIT_SHA=local
ENV APP_VERSION=$APP_VERSION
ENV APP_COMMIT_SHA=$APP_COMMIT_SHA
CMD ["./ingestion"]

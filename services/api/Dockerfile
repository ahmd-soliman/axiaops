FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY services/shared/ /shared/
COPY services/api/go.mod services/api/go.sum ./
RUN go mod download
COPY services/api/ .
RUN go build -o api ./cmd/main.go

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /app/api .
# Build identifiers — populated by CI via --build-arg and surfaced via
# /v1/version and the slog `version` attribute. Defaults so a vanilla
# `docker build` without CI context still produces a usable image.
ARG APP_VERSION=dev
ARG APP_COMMIT_SHA=local
ENV APP_VERSION=$APP_VERSION
ENV APP_COMMIT_SHA=$APP_COMMIT_SHA
CMD ["./api"]

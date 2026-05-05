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

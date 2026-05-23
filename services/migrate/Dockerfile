FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY services/shared/ /shared/
COPY services/migrate/go.mod services/migrate/go.sum ./
RUN go mod download
COPY services/migrate/ .
RUN go build -o migrate .

FROM alpine:3.20
# Debug-build: keep the addgroup/adduser so the user exists, but do NOT
# switch via USER. Earlier attempt with `USER app` produced a DNS-lookup
# failure on the GitLab shared runner (`lookup postgres on [::1]:53: read
# udp [::1]:46684->[::1]:53: read: connection refused`) — local tests
# passed but CI didn't. This commit isolates whether the user switch is
# the cause (printing /etc/resolv.conf at startup to confirm).
RUN apk add --no-cache ca-certificates && \
    addgroup -S app && adduser -S app -G app
WORKDIR /app
COPY --from=builder /app/migrate .
ARG APP_VERSION=dev
ARG APP_COMMIT_SHA=local
ENV APP_VERSION=$APP_VERSION
ENV APP_COMMIT_SHA=$APP_COMMIT_SHA
# Debug step — print /etc/resolv.conf at container start so the next CI
# trace shows whether the resolver is being given a sane nameserver list.
CMD ["sh", "-c", "echo '--- /etc/resolv.conf ---'; cat /etc/resolv.conf; echo '--- id ---'; id; echo '--- start migrate ---'; exec ./migrate"]

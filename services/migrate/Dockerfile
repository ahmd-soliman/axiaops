FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY services/shared/ /shared/
COPY services/migrate/go.mod services/migrate/go.sum ./
RUN go mod download
COPY services/migrate/ .
RUN go build -o migrate .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/migrate .
CMD ["./migrate"]

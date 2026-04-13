FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY services/shared/ /shared/
COPY services/ingestion/go.mod services/ingestion/go.sum ./
RUN go mod download
COPY services/ingestion/ .
RUN go build -o ingestion ./cmd/main.go

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /app/ingestion .
CMD ["./ingestion"]

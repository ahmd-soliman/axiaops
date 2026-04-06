FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY shared/ /shared/
COPY ingestion/go.mod ingestion/go.sum ./
RUN go mod download
COPY ingestion/ .
RUN go build -o ingestion ./cmd/main.go

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /app/ingestion .
CMD ["./ingestion"]

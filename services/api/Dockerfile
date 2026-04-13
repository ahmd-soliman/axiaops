FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY shared/ /shared/
COPY api/go.mod api/go.sum ./
RUN go mod download
COPY api/ .
RUN go build -o api ./cmd/main.go

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /app/api .
CMD ["./api"]

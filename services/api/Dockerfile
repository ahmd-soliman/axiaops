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
CMD ["./api"]

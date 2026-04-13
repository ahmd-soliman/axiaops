FROM golang:1.25-alpine
WORKDIR /smoke
COPY go.mod .
COPY api_test.go .
ENV GOWORK=off
RUN go mod download
CMD ["go", "test", "-v", "./...", "-count=1"]

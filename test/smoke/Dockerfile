FROM golang:1.25-alpine
WORKDIR /smoke
COPY go.mod go.sum ./
COPY api_test.go redis_test.go ./
ENV GOWORK=off
RUN go mod download
CMD ["sh", "-c", "go test -v ./... -count=1 ${SMOKE_TEST_RUN:+-run \"$SMOKE_TEST_RUN\"}"]

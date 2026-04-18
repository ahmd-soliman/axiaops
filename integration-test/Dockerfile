FROM golang:1.25-alpine

WORKDIR /test

COPY test/integration/go.mod test/integration/go.sum ./
RUN go mod download

COPY test/integration/*.go ./

CMD ["go", "test", "-v", ".", "-count=1", "-tags=integration"]

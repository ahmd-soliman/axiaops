FROM golang:1.25-alpine

WORKDIR /test

COPY test/integration/go.mod test/integration/go.sum ./
RUN go mod download

COPY test/integration/*.go ./

ARG TEST_FILTER=""
ENV TEST_FILTER=${TEST_FILTER}

CMD if [ -n "$TEST_FILTER" ]; then \
      go test -v . -count=1 -tags=integration -run="$TEST_FILTER"; \
    else \
      go test -v . -count=1 -tags=integration; \
    fi

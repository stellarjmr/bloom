BIN := bloom
VERSION ?= dev
LDFLAGS := -s -w -X github.com/stellarjmr/bloom/internal/bloom.Version=$(VERSION)

.PHONY: build test clean

build:
	go build -ldflags="$(LDFLAGS)" -o bin/$(BIN) ./cmd/bloom

test:
	go test ./...

clean:
	rm -rf bin

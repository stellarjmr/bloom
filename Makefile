VERSION ?= dev
LDFLAGS := -s -w -X github.com/stellarjmr/bloom/internal/bloom.Version=$(VERSION)

.PHONY: build test clean

build:
	mkdir -p bin
	go build -ldflags="$(LDFLAGS)" -o bin/bloom-core ./cmd/bloom
	cp bloom bm bin/
	chmod +x bin/bloom bin/bm

test:
	go test ./...

clean:
	rm -rf bin

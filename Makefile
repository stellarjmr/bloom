VERSION ?= dev
LDFLAGS := -s -w -X github.com/stellarjmr/bloom/internal/bloom.Version=$(VERSION)

.PHONY: build test clean

build:
	mkdir -p bin
	rm -f bin/bloom bin/bloom-core
	go build -ldflags="$(LDFLAGS)" -o bin/bm-core ./cmd/bloom
	cp bm bin/
	chmod +x bin/bm

test:
	go test ./...

clean:
	rm -rf bin

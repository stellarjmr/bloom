VERSION ?= dev
LDFLAGS := -s -w -X github.com/stellarjmr/bloom/internal/bloom.Version=$(VERSION)
DIST_DIR := dist
RELEASE_TMP := .release

.PHONY: build test release clean

build:
	mkdir -p bin
	rm -f bin/bloom bin/bloom-core
	go build -ldflags="$(LDFLAGS)" -o bin/bm-core ./cmd/bloom
	cp bm bin/
	chmod +x bin/bm

test:
	go test ./...

release:
	rm -rf $(DIST_DIR) $(RELEASE_TMP)
	mkdir -p $(DIST_DIR) $(RELEASE_TMP)/bm-darwin-arm64 $(RELEASE_TMP)/bm-darwin-amd64
	GOOS=darwin GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(RELEASE_TMP)/bm-darwin-arm64/bm-core ./cmd/bloom
	GOOS=darwin GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(RELEASE_TMP)/bm-darwin-amd64/bm-core ./cmd/bloom
	cp bm $(RELEASE_TMP)/bm-darwin-arm64/bm
	cp bm $(RELEASE_TMP)/bm-darwin-amd64/bm
	chmod +x $(RELEASE_TMP)/bm-darwin-arm64/bm $(RELEASE_TMP)/bm-darwin-arm64/bm-core
	chmod +x $(RELEASE_TMP)/bm-darwin-amd64/bm $(RELEASE_TMP)/bm-darwin-amd64/bm-core
	tar -C $(RELEASE_TMP)/bm-darwin-arm64 -czf $(DIST_DIR)/bm-darwin-arm64.tar.gz bm bm-core
	tar -C $(RELEASE_TMP)/bm-darwin-amd64 -czf $(DIST_DIR)/bm-darwin-amd64.tar.gz bm bm-core
	shasum -a 256 $(DIST_DIR)/*.tar.gz

clean:
	rm -rf bin $(DIST_DIR) $(RELEASE_TMP)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  = -s -w -X github.com/volodymyrsmirnov/choix/internal/cli.Version=$(VERSION)

BIN := choix
PKG := ./...

WEB_DIR  := internal/ui/web
WEB_DIST := $(WEB_DIR)/dist

.PHONY: build webui test fmt fmt-check lint vet vulncheck release clean help

webui:
	cd $(WEB_DIR) && bun install --frozen-lockfile && bun run build

build: webui
	go build -ldflags="$(LDFLAGS)" -o $(BIN) ./cmd/choix

test:
	go test -race -count=1 -coverprofile=coverage.out -covermode=atomic $(PKG)

fmt:
	gofmt -s -w .

fmt-check:
	@gofmt -s -l . | tee /dev/stderr | (! read)

lint: vet
	golangci-lint run

vet:
	go vet ./...

vulncheck:
	govulncheck ./...

# Local universal-binary build (darwin amd64 + arm64 → choix-osx-universal).
# Requires running on macOS; uses Apple's clang with -arch for cross-compile.
release: webui
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 CC="clang -arch x86_64" \
		go build -ldflags="$(LDFLAGS)" -o $(BIN)-darwin-amd64 ./cmd/choix
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 CC="clang -arch arm64" \
		go build -ldflags="$(LDFLAGS)" -o $(BIN)-darwin-arm64 ./cmd/choix
	lipo -create -output $(BIN)-osx-universal $(BIN)-darwin-amd64 $(BIN)-darwin-arm64

clean:
	rm -f $(BIN) $(BIN)-darwin-amd64 $(BIN)-darwin-arm64 $(BIN)-osx-universal coverage.out
	rm -rf $(WEB_DIST)

help:
	@echo "Available targets:"
	@echo "  build      - Build the choix binary (rebuilds the embedded SPA)"
	@echo "  webui      - Rebuild only the embedded SPA bundle"
	@echo "  test       - Run tests with race detector and coverage"
	@echo "  fmt        - Format Go source files"
	@echo "  fmt-check  - Check formatting without modifying files"
	@echo "  lint       - Run go vet and golangci-lint"
	@echo "  vet        - Run go vet"
	@echo "  vulncheck  - Run vulnerability scanner"
	@echo "  release    - Build a universal macOS binary (choix-osx-universal)"
	@echo "  clean      - Remove build outputs and the SPA bundle"
	@echo "  help       - Show this help message"

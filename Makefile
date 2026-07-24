BINARY        := eitri
GO            := go
TEMPL         := templ
BUILD_DIR     := dist

VERSION       := $(shell cat VERSION 2>/dev/null || echo dev)
GOFLAGS       := -ldflags="-s -w -X main.Version=$(VERSION)"

.PHONY: all build clean test test-race help run templ-generate release release-check \
        release-all release-linux-amd64 release-linux-arm64 release-darwin-amd64 release-darwin-arm64

all: build

## build — compile binary (generate templ, then go build, embed version)
build: templ-generate
	$(GO) build $(GOFLAGS) -o $(BINARY) ./cmd/eitri

## clean — remove build artifacts (binary + dist/)
clean:
	rm -f $(BINARY)
	rm -rf $(BUILD_DIR)

## test — run all tests (fast, no race detector)
test:
	$(GO) test ./...

## test-race — run all tests with race detector
test-race:
	$(GO) test -race ./...

## release — build linux/amd64 release tarball + checksums (default platform)
release: _clean-checksums release-linux-amd64

## release-all — build release tarballs for all supported platforms
release-all: _clean-checksums release-linux-amd64 release-linux-arm64 release-darwin-amd64 release-darwin-arm64

# Internal: start fresh checksums file for a clean release build.
_clean-checksums:
	rm -f $(BUILD_DIR)/checksums.txt

## release-linux-amd64 — build linux/amd64 tarball + checksums
release-linux-amd64: RELEASE_OS   = linux
release-linux-amd64: RELEASE_ARCH = amd64
release-linux-amd64: release-tarball

## release-linux-arm64 — build linux/arm64 tarball + checksums
release-linux-arm64: RELEASE_OS   = linux
release-linux-arm64: RELEASE_ARCH = arm64
release-linux-arm64: release-tarball

## release-darwin-amd64 — build darwin/amd64 tarball + checksums
release-darwin-amd64: RELEASE_OS   = darwin
release-darwin-amd64: RELEASE_ARCH = amd64
release-darwin-amd64: release-tarball

## release-darwin-arm64 — build darwin/arm64 tarball + checksums
release-darwin-arm64: RELEASE_OS   = darwin
release-darwin-arm64: RELEASE_ARCH = arm64
release-darwin-arm64: release-tarball

# Internal: parameterised tarball builder. RELEASE_OS and RELEASE_ARCH must be set.
release-tarball: templ-generate
	$(eval RELEASE_NAME := eitri-$(RELEASE_OS)-$(RELEASE_ARCH))
	$(eval TARBALL       := $(BUILD_DIR)/$(RELEASE_NAME).tar.gz)
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=$(RELEASE_OS) GOARCH=$(RELEASE_ARCH) $(GO) build -trimpath $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/eitri
	tar -C $(BUILD_DIR) -czf $(TARBALL) $(BINARY)
	cd $(BUILD_DIR) && sha256sum $(notdir $(TARBALL)) >> checksums.txt
	rm -f $(BUILD_DIR)/$(BINARY)

## release-check — release readiness test gates (includes race detector)
release-check:
	$(GO) test -race ./...
	$(GO) test -race -tags=browser ./internal/api/

## run — build and start server
run: build
	./$(BINARY)

## templ-generate — recompile .templ files to Go (skip if templ not installed)
templ-generate:
	@if command -v $(TEMPL) >/dev/null 2>&1; then \
		$(TEMPL) generate; \
	else \
		echo "warning: 'templ' not found, skipping templ generate"; \
	fi

## help — print this help
help:
	@echo "Usage:"
	@echo "  make build              Compile the eitri binary (with embedded version)"
	@echo "  make clean              Remove build artifacts (binary + dist/)"
	@echo "  make test               Run all tests (fast, no race detector)"
	@echo "  make test-race          Run all tests with race detector"
	@echo "  make release            Build linux/amd64 tarball + checksums"
	@echo "  make release-all        Build tarballs for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64"
	@echo "  make release-check      Run release readiness tests (includes race detector)"
	@echo "  make run                Build and run the server"
	@echo "  make help               Show this help"
	@echo ""
	@echo "Env vars:"
	@echo "  EITRI_ADDR          Listen address (default 127.0.0.1:8080)"
	@echo "  EITRI_CONFIG        Config file path"
	@echo "  EITRI_OPEN_BROWSER  Browser auto-open: 1 force, 0 disable, unset auto-detect"
	@echo "  EITRI_WORKSPACE     Workspace directory (default CWD)"
	@echo "  EITRI_GITHUB_CLIENT_ID Optional override for built-in Copilot device-flow client ID"

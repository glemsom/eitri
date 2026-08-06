BINARY        := eitri
GO            := go
TEMPL         := templ
BUILD_DIR     := dist

VERSION       := $(shell cat VERSION 2>/dev/null || echo dev)
GOFLAGS       := -ldflags="-s -w -X main.Version=$(VERSION)"

.PHONY: all build clean test test-race test-flaky help run templ-generate css-generate release release-check \
        release-all release-linux-amd64

all: build

## build — compile binary (generate templ + css aggregate, then go build, embed version)
build: templ-generate css-generate
	$(GO) build $(GOFLAGS) -o $(BINARY) ./cmd/eitri

## clean — remove build artifacts (binary + dist/)
clean:
	rm -f $(BINARY)
	rm -rf $(BUILD_DIR)

## test — run all tests (fast, no race detector), compact verdict, full log in dist/
test:
	./scripts/test.sh

## test-race — run all tests with race detector, compact verdict, full log in dist/
test-race:
	./scripts/test.sh --race

## test-flaky — reproduce CI flakes: cache-cleared, -cpu 1,2, -p 1 (compact verdict)
test-flaky:
	./scripts/test.sh --flaky

## release — build linux/amd64 release tarball + checksums (default platform)
release: _clean-checksums release-linux-amd64

## release-all — build release tarball for linux/amd64
release-all: _clean-checksums release-linux-amd64

# Internal: start fresh checksums file for a clean release build.
_clean-checksums:
	rm -f $(BUILD_DIR)/checksums.txt

## release-linux-amd64 — build linux/amd64 tarball + checksums
release-linux-amd64: RELEASE_OS   = linux
release-linux-amd64: RELEASE_ARCH = amd64
release-linux-amd64: release-tarball



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

## run — build and start server
run: build
	./$(BINARY)

## css-generate — regenerate the embedded eitri.css aggregate from partials/
css-generate:
	cd internal/api/assets && $(GO) run gen_eitri_css.go

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
	@echo "  make test               Run all tests, compact verdict (full log in dist/test-output.log)"
	@echo "  make test-race          Run all tests with race detector, compact verdict (log in dist/test-race-output.log)"
	@echo "  make test-flaky         Reproduce CI flakes: cache-cleared, -cpu 1,2, -p 1 (log in dist/test-flaky-output.log)"
	@echo "  make lint               Run golangci-lint with strict config"
	@echo "  make release            Build linux/amd64 tarball + checksums"
	@echo "  make release-all        Build tarball for linux/amd64"
	@echo "  make release-check      Run release readiness tests (includes race detector)"
	@echo "  make run                Build and run the server"
	@echo "  make help               Show this help"
	@echo ""
	@echo "Env vars:"
	@echo "  EITRI_ADDR          Listen address (default 127.0.0.1:8080)"
	@echo "  EITRI_CONFIG        Config file path (default ~/.eitri/config.json)"
	@echo "  EITRI_DIR           Root directory for persisted data (default ~/.eitri/)"
	@echo "  EITRI_OPEN_BROWSER  Browser auto-open: 1 force, 0 disable, unset auto-detect"
	@echo "  EITRI_GITHUB_CLIENT_ID Optional override for built-in Copilot device-flow client ID"

## lint — run golangci-lint with strict config
lint:
	golangci-lint run

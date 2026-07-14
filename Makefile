BINARY        := eitri
GO            := go
TEMPL         := templ
BUILD_DIR     := dist
DIST_BINARY   := $(BUILD_DIR)/$(BINARY)
DIST_TARBALL  := $(BUILD_DIR)/eitri-linux-amd64.tar.gz
DIST_CHECKSUM := $(BUILD_DIR)/checksums.txt
GOFLAGS       := -ldflags="-s -w"

.PHONY: all build clean test help run templ-generate release release-check

all: build

## build — compile binary (generate templ, then go build)
build: templ-generate
	$(GO) build $(GOFLAGS) -o $(BINARY) ./cmd/eitri

## clean — remove build artifacts (binary + dist/)
clean:
	rm -f $(BINARY)
	rm -rf $(BUILD_DIR)

## test — run all tests
test:
	$(GO) test ./...

## release — build linux/amd64 release tarball + checksums.txt
release: templ-generate
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath $(GOFLAGS) -o $(DIST_BINARY) ./cmd/eitri
	tar -C $(BUILD_DIR) -czf $(DIST_TARBALL) $(BINARY)
	cd $(BUILD_DIR) && sha256sum $(notdir $(DIST_TARBALL)) > $(notdir $(DIST_CHECKSUM))

## release-check — release readiness test gates
release-check:
	$(GO) test ./...
	$(GO) test -tags=browser ./internal/api/

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
	@echo "  make build          Compile the eitri binary"
	@echo "  make clean          Remove build artifacts (binary + dist/)"
	@echo "  make test           Run all tests"
	@echo "  make release        Build linux/amd64 tarball + checksums"
	@echo "  make release-check  Run release readiness tests"
	@echo "  make run            Build and run the server"
	@echo "  make help           Show this help"
	@echo ""
	@echo "Env vars:"
	@echo "  EITRI_ADDR          Listen address (default 127.0.0.1:8080)"
	@echo "  EITRI_CONFIG        Config file path"
	@echo "  EITRI_OPEN_BROWSER  Browser auto-open: 1 force, 0 disable, unset auto-detect"
	@echo "  EITRI_WORKSPACE     Workspace directory (default CWD)"
	@echo "  EITRI_GITHUB_CLIENT_ID Optional override for built-in Copilot device-flow client ID"

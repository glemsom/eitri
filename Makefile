BINARY   := eitri
GO       := go
TEMPL    := templ
BUILD_DIR:= dist
GOFLAGS  := -ldflags="-s -w"

.PHONY: all build clean test help run templ-generate

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
	@echo "  make run            Build and run the server"
	@echo "  make help           Show this help"
	@echo ""
	@echo "Env vars:"
	@echo "  EITRI_ADDR          Listen address (default 127.0.0.1:8080)"
	@echo "  EITRI_CONFIG        Config file path"
	@echo "  EITRI_WORKSPACE     Workspace directory (default CWD)"

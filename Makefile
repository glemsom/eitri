.PHONY: build install debug debug-install test clean

BINARY := eitri
BIN_DIR := bin
INSTALL_DIR := $(HOME)/.local/bin
DEBUG_BINARY := $(BINARY)-debug
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# LDFLAGS minus -s -w (symtab/dwarf). Debug builds strip nothing: they need
# symbols so pprof/delve can map samples and stack frames to source. Set
# STRIP=0 to keep symbols in the normal build too.
LDFLAGS := $(if $(filter 0,$(STRIP)),,-s -w) -X github.com/glemsom/eitri/internal/app.Version=$(VERSION)

build:
	mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) .

install: build
	install -d $(INSTALL_DIR)
	install $(BIN_DIR)/$(BINARY) $(INSTALL_DIR)/$(BINARY)

# debug builds a binary with full debug info (no -s -w), for pprof CPU/heap
# profiling and delve-backed debugging. Typically run under --pprof, e.g.:
#	make debug
#	$(BIN_DIR)/$(DEBUG_BINARY) --pprof 127.0.0.1:6060
#	go tool pprof -seconds 30 http://127.0.0.1:6060/debug/pprof/profile
debug:
	mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "-X github.com/glemsom/eitri/internal/app.Version=$(VERSION)" -o $(BIN_DIR)/$(DEBUG_BINARY) .

# debug-install copies the debug binary over the installed one (for replacing a
# currently-running instance you must restart it yourself).
debug-install: debug
	install -d $(INSTALL_DIR)
	install $(BIN_DIR)/$(DEBUG_BINARY) $(INSTALL_DIR)/$(BINARY)

test:
	go test ./...

clean:
	rm -rf $(BIN_DIR)

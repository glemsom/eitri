.PHONY: build install debug debug-install package test clean

BINARY := eitri
BIN_DIR := bin
INSTALL_DIR := $(HOME)/.local/bin
DEBUG_BINARY := $(BINARY)-debug
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# LDFLAGS minus -s -w (symtab/dwarf). Debug builds strip nothing: they need
# symbols so pprof/delve can map samples and stack frames to source. Set
# STRIP=0 to keep symbols in the normal build too.
LDFLAGS := $(if $(filter 0,$(STRIP)),,-s -w) -X github.com/glemsom/eitri/internal/app.Version=$(VERSION)
PACKAGE_VERSION := $(patsubst v%,%,$(VERSION))
PACKAGE_NAME := $(BINARY)_$(PACKAGE_VERSION)_linux_amd64
PACKAGE_DIR := dist/$(PACKAGE_NAME)
PACKAGE_ARCHIVE := dist/$(PACKAGE_NAME).tar.gz
PACKAGE_LDFLAGS := $(if $(filter 0,$(STRIP)),,-s -w) -X github.com/glemsom/eitri/internal/app.Version=$(PACKAGE_VERSION)

build:
	mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) .

package:
	rm -rf $(PACKAGE_DIR) $(PACKAGE_ARCHIVE)
	mkdir -p $(PACKAGE_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags "$(PACKAGE_LDFLAGS)" -o $(PACKAGE_DIR)/$(BINARY) .
	cp LICENSE $(PACKAGE_DIR)/LICENSE
	tar -C $(PACKAGE_DIR) --mtime=@0 --owner=0 --group=0 --numeric-owner -czf $(PACKAGE_ARCHIVE) $(BINARY) LICENSE
	rm -rf $(PACKAGE_DIR)

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
	rm -rf $(BIN_DIR) dist

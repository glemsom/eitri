.PHONY: build test clean

BINARY := eitri
BIN_DIR := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/glemsom/eitri/internal/app.Version=$(VERSION)

build:
	mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) .

test:
	go test ./...

clean:
	rm -rf $(BIN_DIR)

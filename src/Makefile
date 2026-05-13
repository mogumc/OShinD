# OShinD Build System
# Usage:
#   make build          - Build CLI + FFI (version from git tag)
#   make build-cli      - Build CLI only
#   make build-ffi      - Build FFI DLL only
#   make version        - Show current version
#   make tag VERSION=1.4.0 - Create git tag
#   make clean          - Remove build artifacts

# Version: from git tag, fallback to "dev"
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -X main.version=$(VERSION)

# Build output directory
DIST := dist

# Go build flags
GOFLAGS := -trimpath
CGO_ENABLED_CLI := 0
CGO_ENABLED_FFI := 1

.PHONY: build build-cli build-ffi version tag clean

## Build both CLI and FFI
build: build-cli build-ffi

## Build CLI binary (static, no CGO)
build-cli:
	@echo "Building CLI v$(VERSION)..."
	@mkdir -p $(DIST)
	CGO_ENABLED=$(CGO_ENABLED_CLI) go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/oshind.exe ./cmd/cli/
	@echo "  -> $(DIST)/oshind.exe"

## Build FFI DLL (CGO shared library)
build-ffi:
	@echo "Building FFI DLL v$(VERSION)..."
	@mkdir -p $(DIST)
	CGO_ENABLED=$(CGO_ENABLED_FFI) go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -buildmode=c-shared -o $(DIST)/oshind.dll ./cmd/ffi/
	@echo "  -> $(DIST)/oshind.dll"
	@echo "  -> $(DIST)/oshind.h"

## Show current version
version:
	@echo $(VERSION)

## Create git tag
tag:
	@if [ -z "$(VERSION)" ] || [ "$(VERSION)" = "dev" ]; then \
		echo "Usage: make tag VERSION=1.4.0"; \
		exit 1; \
	fi
	git tag -a v$(VERSION) -m "Release v$(VERSION)"
	@echo "Created tag v$(VERSION)"

## Remove build artifacts
clean:
	rm -rf $(DIST)

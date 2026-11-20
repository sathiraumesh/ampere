# ampere: estimate energy per process (and later per container)

BINARY  := ampere
BIN_DIR := bin
PKG     := ./cmd

# Build info, injected into internal/cli with -ldflags -X.
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION_PKG := sathiraumesh/ampere/internal/cli
LDFLAGS := -X $(VERSION_PKG).Version=$(VERSION) \
           -X $(VERSION_PKG).Commit=$(COMMIT) \
           -X $(VERSION_PKG).BuildDate=$(BUILD_DATE)

.DEFAULT_GOAL := help

.PHONY: help build run version read-root status status-root fmt vet test tidy check clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*## "}; {printf "  %-12s %s\n", $$1, $$2}'

build: ## Build the binary into bin/ampere
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(PKG)

run: build ## Run ampere with ARGS, e.g. make run ARGS="pid 1234 -i 2s"
	./$(BIN_DIR)/$(BINARY) $(ARGS)

version: build ## Print version and build info
	./$(BIN_DIR)/$(BINARY) --version

read-root: build ## Read CPU energy and power as root, e.g. make read-root ARGS="-i 5s"
	sudo ./$(BIN_DIR)/$(BINARY) read $(ARGS)

status: build ## Check available energy/CPU sources (as normal user)
	./$(BIN_DIR)/$(BINARY) status

status-root: build ## Same as status, but as root (RAPL energy_uj is root-only)
	sudo ./$(BIN_DIR)/$(BINARY) status

fmt: ## Format all Go code
	go fmt ./...

vet: ## Report suspicious code
	go vet ./...

test: ## Run tests
	go test ./...

tidy: ## Clean up go.mod / go.sum
	go mod tidy

check: fmt vet test ## fmt + vet + test

clean: ## Remove build output
	rm -rf $(BIN_DIR)

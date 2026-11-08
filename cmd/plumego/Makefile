# Plumego CLI Makefile

# Version information
VERSION ?= dev
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Build flags
LDFLAGS = -X github.com/birdordev/plumego/cmd/plumego/commands.Version=$(VERSION) \
          -X github.com/birdordev/plumego/cmd/plumego/commands.GitCommit=$(GIT_COMMIT) \
          -X github.com/birdordev/plumego/cmd/plumego/commands.BuildDate=$(BUILD_DATE)

# Output binary
BINARY = plumego
OUTPUT_DIR = ../../bin
OUTPUT = $(OUTPUT_DIR)/$(BINARY)

# Go parameters
GOCMD = go
GOBUILD = $(GOCMD) build
GOTEST = $(GOCMD) test
GOMOD = $(GOCMD) mod

.PHONY: all build clean test install help

all: build

## build: Build the CLI binary
build:
	@echo "Building $(BINARY)..."
	@mkdir -p $(OUTPUT_DIR)
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o $(OUTPUT) .
	@echo "✓ Build complete: $(OUTPUT)"

## build-release: Build optimized release binary
build-release:
	@echo "Building release $(BINARY)..."
	@mkdir -p $(OUTPUT_DIR)
	$(GOBUILD) -ldflags "$(LDFLAGS) -s -w" -trimpath -o $(OUTPUT) .
	@echo "✓ Release build complete: $(OUTPUT)"

## clean: Remove build artifacts
clean:
	@echo "Cleaning..."
	@rm -f $(OUTPUT)
	@echo "✓ Clean complete"

## test: Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

## test-coverage: Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	$(GOTEST) -cover -coverprofile=coverage.out ./...
	@$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "✓ Coverage report: coverage.html"

## install: Install to GOPATH/bin
install:
	@echo "Installing $(BINARY)..."
	$(GOCMD) install -ldflags "$(LDFLAGS)" .
	@echo "✓ Installed to $(shell go env GOPATH)/bin/$(BINARY)"

## deps: Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy
	@echo "✓ Dependencies updated"

## fmt: Format code
fmt:
	@echo "Formatting code..."
	@$(GOCMD) fmt ./...
	@echo "✓ Code formatted"

## vet: Run go vet
vet:
	@echo "Running go vet..."
	@$(GOCMD) vet ./...
	@echo "✓ Vet complete"

## version: Show version information
version:
	@echo "Version: $(VERSION)"
	@echo "Git Commit: $(GIT_COMMIT)"
	@echo "Build Date: $(BUILD_DATE)"

## help: Show this help
help:
	@echo "Plumego CLI Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'

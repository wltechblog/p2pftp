# P2PFTP Makefile
# Builds the server component

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get

# Binary name
SERVER_BINARY_NAME=p2pftp-server

# Build directory
SERVER_DIR=.

# Output directory
BIN_DIR=./bin

# Default target
all: clean setup build

# Setup output directories
setup:
	mkdir -p $(BIN_DIR)

# Build server
build: build-server

# Build server
build-server:
	cd $(SERVER_DIR) && $(GOBUILD) -o $(BIN_DIR)/$(SERVER_BINARY_NAME) -v

# Run tests
test:
	$(GOTEST) -v ./...

# Clean build artifacts
clean:
	$(GOCLEAN)
	rm -rf $(BIN_DIR)

# Run server
run-server:
	$(BIN_DIR)/$(SERVER_BINARY_NAME)

# Install dependencies
deps:
	$(GOGET) -v ./...

# Help target
help:
	@echo "Available targets:"
	@echo "  all          - Clean, setup directories, and build server"
	@echo "  build        - Build server"
	@echo "  build-server - Build only the server"
	@echo "  clean        - Remove build artifacts"
	@echo "  test         - Run tests"
	@echo "  run-server   - Run the server"
	@echo "  deps         - Install dependencies"

.PHONY: all setup build build-server test clean run-server deps help
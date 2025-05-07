# P2PFTP Makefile
# Builds both server and client components

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get

# Binary names
SERVER_BINARY_NAME=p2pftp-server
CLIENT_BINARY_NAME=p2pftp-cli

# Build directories
SERVER_DIR=.
CLIENT_DIR=./client

# Output directories
BIN_DIR=./bin

# Default target
all: clean setup build

# Setup output directories
setup:
	mkdir -p $(BIN_DIR)

# Build both server and client
build: build-server build-client

# Build server
build-server:
	cd $(SERVER_DIR) && $(GOBUILD) -o $(BIN_DIR)/$(SERVER_BINARY_NAME) -v

# Build client
build-client:
	cd $(CLIENT_DIR) && $(GOBUILD) -o ../$(BIN_DIR)/$(CLIENT_BINARY_NAME) -v

# Run tests
test:
	$(GOTEST) -v ./...
	cd $(CLIENT_DIR) && $(GOTEST) -v ./...

# Clean build artifacts
clean:
	$(GOCLEAN)
	cd $(CLIENT_DIR) && $(GOCLEAN)
	rm -rf $(BIN_DIR)

# Run server
run-server:
	$(BIN_DIR)/$(SERVER_BINARY_NAME)

# Run client
run-client:
	$(BIN_DIR)/$(CLIENT_BINARY_NAME)

# Install dependencies
deps:
	$(GOGET) -v ./...
	cd $(CLIENT_DIR) && $(GOGET) -v ./...

# Help target
help:
	@echo "Available targets:"
	@echo "  all          - Clean, setup directories, and build both server and client"
	@echo "  build        - Build both server and client"
	@echo "  build-server - Build only the server"
	@echo "  build-client - Build only the client"
	@echo "  clean        - Remove build artifacts"
	@echo "  test         - Run tests for both server and client"
	@echo "  run-server   - Run the server"
	@echo "  run-client   - Run the client"
	@echo "  deps         - Install dependencies"

.PHONY: all setup build build-server build-client test clean run-server run-client deps help
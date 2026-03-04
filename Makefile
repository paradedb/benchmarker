.PHONY: all build k6 loader viewer clean test fmt lint deps help

GOBIN := $(shell go env GOPATH)/bin
XK6 := $(GOBIN)/xk6

# Default target
all: build

# Build everything
build: k6 loader viewer

# Build k6 with the search extension
k6: $(XK6)
	@echo "Building k6 with xk6-search extension..."
	$(XK6) build --with github.com/paradedb/benchmarks=.
	@echo "Done: ./k6"

$(XK6):
	@echo "Installing xk6..."
	go install go.k6.io/xk6/cmd/xk6@latest

# Build the loader CLI
loader:
	@echo "Building loader..."
	@mkdir -p bin
	go build -o bin/loader ./cmd/loader
	@echo "Done: ./bin/loader"

# Build the dashboard-viewer CLI
viewer:
	@echo "Building dashboard-viewer..."
	@mkdir -p bin
	go build -o bin/dashboard-viewer ./cmd/dashboard-viewer
	@echo "Done: ./bin/dashboard-viewer"

# Run tests
test:
	go test -v ./...

# Format code
fmt:
	go fmt ./...

# Lint code
lint:
	golangci-lint run

# Clean build artifacts
clean:
	rm -f k6
	rm -rf bin/

# Install dependencies
deps:
	go mod download
	go install go.k6.io/xk6/cmd/xk6@latest

# Help
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  all      Build everything (default)"
	@echo "  build    Build k6 and loader"
	@echo "  k6       Build k6 with xk6-search extension"
	@echo "  loader   Build the loader CLI to bin/"
	@echo "  viewer   Build the dashboard-viewer CLI to bin/"
	@echo "  test     Run tests"
	@echo "  fmt      Format code"
	@echo "  lint     Run golangci-lint"
	@echo "  clean    Remove build artifacts"
	@echo "  deps     Install dependencies (go modules + xk6)"
	@echo "  help     Show this help"

BINARY := dtrack
PKG := ./cmd/dtrack
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build install test vet fmt fmt-check tidy clean

all: build

build: ## Build the dtrack binary into ./dtrack
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

install: ## Install dtrack into $GOBIN
	go install -ldflags "$(LDFLAGS)" $(PKG)

test: ## Run the test suite
	go test ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format all Go sources
	gofmt -w .

fmt-check: ## Fail if any file is not gofmt-clean
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "unformatted files:"; echo "$$out"; exit 1; fi

tidy: ## Sync go.mod / go.sum
	go mod tidy

clean: ## Remove build artifacts
	rm -f $(BINARY)
	rm -rf dist

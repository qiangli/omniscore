# OmniScore — single-binary build.
# Runtime is pure Go. The React/TS frontend is built with Vite and embedded
# at compile time via go:embed.

GO ?= go
NPM ?= npm
BIN ?= bin/omniscore
PKG ?= ./cmd/omniscore
LDFLAGS ?= -s -w
BUILD_TAGS ?= netgo,osusergo

.PHONY: help all tidy fmt vet build frontend backend test go-test fe-lint install clean dev release

.DEFAULT_GOAL := help

help: ## Show this help message
	@printf "OmniScore — Makefile targets\n\n"
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ { printf "  \033[1m%-10s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

all: build ## Alias for `build`

tidy: ## go mod tidy + go fmt + go vet
	$(GO) mod tidy
	$(GO) fmt ./...
	$(GO) vet ./...

fmt: ## go fmt ./...
	$(GO) fmt ./...

vet: ## go vet ./...
	$(GO) vet ./...

test: go-test fe-lint ## Run Go tests + vet + frontend tsc typecheck

go-test:
	$(GO) test ./...
	$(GO) vet ./...

fe-lint:
	cd frontend && $(NPM) run lint

build: frontend backend ## Frontend (vite) + backend (go build with embedded dist) → bin/omniscore

frontend:
	cd frontend && $(NPM) install
	cd frontend && $(NPM) run build

backend:
	mkdir -p bin
	$(GO) build -tags $(BUILD_TAGS) -ldflags '$(LDFLAGS)' -o $(BIN) $(PKG)

install: frontend ## Install the omniscore binary into $$GOBIN (or $$GOPATH/bin)
	$(GO) install -tags $(BUILD_TAGS) -ldflags '$(LDFLAGS)' $(PKG)

ap-import: ## Build the AP PDF→JSON importer (separate binary; needs pdftoppm + Ollama at run time)
	mkdir -p bin
	$(GO) build -o bin/ap-import ./cmd/ap-import

dev: frontend backend ## Build then run the server locally
	./$(BIN)

clean: ## Remove bin/, frontend/dist, node_modules, omniscore.db, omniscore.key
	rm -rf bin frontend/dist frontend/node_modules omniscore.db omniscore.key

release: frontend ## Cross-compile darwin/{arm64,amd64}, linux/amd64, windows/amd64
	mkdir -p bin
	GOOS=darwin GOARCH=arm64 $(GO) build -tags $(BUILD_TAGS) -ldflags '$(LDFLAGS)' -o bin/omniscore-darwin-arm64 $(PKG)
	GOOS=darwin GOARCH=amd64 $(GO) build -tags $(BUILD_TAGS) -ldflags '$(LDFLAGS)' -o bin/omniscore-darwin-amd64 $(PKG)
	GOOS=linux  GOARCH=amd64 $(GO) build -tags $(BUILD_TAGS) -ldflags '$(LDFLAGS)' -o bin/omniscore-linux-amd64  $(PKG)
	GOOS=windows GOARCH=amd64 $(GO) build -tags $(BUILD_TAGS) -ldflags '$(LDFLAGS)' -o bin/omniscore-windows-amd64.exe $(PKG)

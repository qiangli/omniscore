# OmniScore — single-binary build.
# Runtime is pure Go. The React/TS frontend is built with Vite and embedded
# at compile time via go:embed.

GO ?= go
NPM ?= npm
BIN ?= bin/omniscore
PKG ?= ./cmd/omniscore
LDFLAGS ?= -s -w
BUILD_TAGS ?= netgo,osusergo

.PHONY: help all tidy fmt vet build frontend backend test go-test fe-lint install clean dev start stop status release ap-import sat-import sat-pdf-eval

# Background-server tracking. `start` writes the pid + log here so `stop` and
# `status` can find a running instance without polling for ports.
RUN_DIR ?= .run
PID_FILE ?= $(RUN_DIR)/omniscore.pid
LOG_FILE ?= $(RUN_DIR)/omniscore.log
CONTENT_DIR ?= data/omni-data
BIND ?= 0.0.0.0:28080

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

ap-import: ## Build the AP PDF→JSON importer (separate binary; needs pdftoppm + a vision LLM at run time)
	mkdir -p bin
	$(GO) build -o bin/ap-import ./cmd/ap-import

sat-import: ## Build the SAT PDF→JSON importer (separate binary; needs pdftoppm + a vision LLM at run time)
	mkdir -p bin
	$(GO) build -o bin/sat-import ./cmd/sat-import

sat-pdf-eval: ## Build the PDF-extraction eval tool (compares pure-Go / shell-out extractors against the LLM-extracted JSON)
	mkdir -p bin
	$(GO) build -o bin/sat-pdf-eval ./cmd/sat-pdf-eval

dev: frontend backend ## Build then run the server in the foreground (Ctrl-C to stop)
	./$(BIN)

start: build ## Build then launch the server in the background against $(CONTENT_DIR)
	@mkdir -p $(RUN_DIR)
	@if [ -f $(PID_FILE) ] && kill -0 `cat $(PID_FILE)` 2>/dev/null; then \
		echo "omniscore already running (pid $$(cat $(PID_FILE))); use 'make stop' first"; exit 1; \
	fi
	@if [ ! -d $(CONTENT_DIR) ]; then \
		echo "content dir $(CONTENT_DIR) does not exist — populate it (see skills/convert-sat-pdf) or override CONTENT_DIR="; exit 1; \
	fi
	@nohup ./$(BIN) -bind $(BIND) -content $(CONTENT_DIR) > $(LOG_FILE) 2>&1 & echo $$! > $(PID_FILE)
	@sleep 1
	@if kill -0 `cat $(PID_FILE)` 2>/dev/null; then \
		echo "omniscore started: pid=$$(cat $(PID_FILE)) bind=$(BIND) content=$(CONTENT_DIR)"; \
		echo "  log:  $(LOG_FILE)"; \
		echo "  stop: make stop"; \
	else \
		echo "omniscore failed to start; see $(LOG_FILE):"; tail -n 20 $(LOG_FILE); rm -f $(PID_FILE); exit 1; \
	fi

stop: ## Stop the background server started by `make start`
	@if [ ! -f $(PID_FILE) ]; then echo "no pid file at $(PID_FILE) — nothing to stop"; exit 0; fi
	@PID=`cat $(PID_FILE)`; \
	if kill -0 $$PID 2>/dev/null; then \
		kill $$PID; \
		for i in 1 2 3 4 5; do sleep 1; kill -0 $$PID 2>/dev/null || break; done; \
		if kill -0 $$PID 2>/dev/null; then echo "graceful stop timed out; sending SIGKILL"; kill -9 $$PID; fi; \
		echo "omniscore stopped (pid $$PID)"; \
	else \
		echo "stale pid $$PID — process not running"; \
	fi; \
	rm -f $(PID_FILE)

status: ## Report whether the background server is running
	@if [ -f $(PID_FILE) ] && kill -0 `cat $(PID_FILE)` 2>/dev/null; then \
		echo "omniscore running: pid=$$(cat $(PID_FILE))"; \
		echo "  log: $(LOG_FILE)"; \
	else \
		echo "omniscore not running"; \
		[ -f $(PID_FILE) ] && echo "  (stale pid file at $(PID_FILE) — remove with 'make stop')"; \
		exit 1; \
	fi

clean: ## Remove bin/, frontend/dist, node_modules, omniscore.db, omniscore.key, .run/
	rm -rf bin frontend/dist frontend/node_modules omniscore.db omniscore.key $(RUN_DIR)

release: frontend ## Cross-compile darwin/{arm64,amd64}, linux/amd64, windows/amd64
	mkdir -p bin
	GOOS=darwin GOARCH=arm64 $(GO) build -tags $(BUILD_TAGS) -ldflags '$(LDFLAGS)' -o bin/omniscore-darwin-arm64 $(PKG)
	GOOS=darwin GOARCH=amd64 $(GO) build -tags $(BUILD_TAGS) -ldflags '$(LDFLAGS)' -o bin/omniscore-darwin-amd64 $(PKG)
	GOOS=linux  GOARCH=amd64 $(GO) build -tags $(BUILD_TAGS) -ldflags '$(LDFLAGS)' -o bin/omniscore-linux-amd64  $(PKG)
	GOOS=windows GOARCH=amd64 $(GO) build -tags $(BUILD_TAGS) -ldflags '$(LDFLAGS)' -o bin/omniscore-windows-amd64.exe $(PKG)

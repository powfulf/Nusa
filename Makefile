SHELL := /bin/sh
.DEFAULT_GOAL := help

COMPOSE ?= docker compose
WEB     := web
BIN     := $(CURDIR)/bin
EXE     := $(if $(findstring Windows,$(OS)),.exe,)

# Development tools are pinned in tools/go.mod and built into bin/, so CI and
# every contributor run identical versions without a separate install step, and
# without the linter dictating the application's minimum Go version.
GOLANGCI := $(BIN)/golangci-lint$(EXE)
SQLC     := $(BIN)/sqlc$(EXE)

# Local overrides live in .env, which is gitignored. Loading it here means
# `make migrate-up` and `make dev` see the same connection string that Compose
# does, without anyone exporting variables by hand.
ifneq (,$(wildcard .env))
include .env
export
endif

.PHONY: help
help: ## Show this help
	@awk 'BEGIN{FS=":.*?## "} /^[a-zA-Z0-9_-]+:.*?## /{printf "  \033[36m%-14s\033[0m %s\n",$$1,$$2}' $(MAKEFILE_LIST)

# ---------------------------------------------------------------------------
# Development
# ---------------------------------------------------------------------------

.PHONY: dev
dev: db-up migrate-up $(WEB)/node_modules ## Run the API and the web dev server against a local Postgres
	@echo "api → http://localhost:8080   web → http://localhost:5173"
	@trap 'kill 0' EXIT INT TERM; \
		go run ./cmd/nusa serve & \
		$(MAKE) --no-print-directory web-dev & \
		wait

.PHONY: web-dev
web-dev:
	cd $(WEB) && npm run dev

.PHONY: db-up
db-up: ## Start Postgres and wait for it to accept connections
	$(COMPOSE) up -d --wait postgres

.PHONY: db-down
db-down: ## Stop Postgres, keeping its data
	$(COMPOSE) stop postgres

.PHONY: up
up: ## Build and run everything in containers
	$(COMPOSE) up --build

.PHONY: down
down: ## Stop everything. Add ARGS=-v to also delete the database volume
	$(COMPOSE) down $(ARGS)

# The target must be the real directory, not a bare name, or make never sees it
# and reinstalls on every invocation. npm ci wipes node_modules first, so that
# mistake costs a full reinstall before every test run.
$(WEB)/node_modules: $(WEB)/package-lock.json
	cd $(WEB) && npm ci
	@touch $@

# ---------------------------------------------------------------------------
# Quality
# ---------------------------------------------------------------------------

.PHONY: test
test: test-go test-web ## Run every test

.PHONY: test-go
test-go: ## Run Go tests with the race detector
	go test -race ./...

.PHONY: test-web
test-web: $(WEB)/node_modules ## Run frontend tests
	cd $(WEB) && npm test

.PHONY: lint
lint: lint-go lint-web ## Lint everything

.PHONY: lint-go
lint-go: $(GOLANGCI) ## Run golangci-lint
	$(GOLANGCI) run

.PHONY: lint-web
lint-web: $(WEB)/node_modules ## Type-check the frontend and verify design tokens
	cd $(WEB) && npm run typecheck
	cd $(WEB) && npm run lint:tokens
	cd $(WEB) && npm run lint:floor
	cd $(WEB) && npm run lint:bytes
	cd $(WEB) && npm run check:gallery
	cd $(WEB) && npm run test:contrast

.PHONY: fmt
fmt: $(GOLANGCI) ## Format Go sources
	$(GOLANGCI) fmt

.PHONY: tools
tools: $(GOLANGCI) $(SQLC) ## Build the pinned development tools into bin/

$(GOLANGCI):
	go -C tools build -o $@ github.com/golangci/golangci-lint/v2/cmd/golangci-lint

$(SQLC):
	go -C tools build -o $@ github.com/sqlc-dev/sqlc/cmd/sqlc

# ---------------------------------------------------------------------------
# Build
# ---------------------------------------------------------------------------

.PHONY: build
build: build-web build-go ## Build the binary and the frontend

.PHONY: build-go
build-go:
	go build -trimpath -o bin/nusa ./cmd/nusa

.PHONY: build-web
build-web: $(WEB)/node_modules
	cd $(WEB) && npm run build

# ---------------------------------------------------------------------------
# Database
# ---------------------------------------------------------------------------

.PHONY: generate
generate: $(SQLC) ## Regenerate sqlc query code from db/queries and db/migrations
	$(SQLC) generate

.PHONY: migrate-up
migrate-up: ## Apply every pending migration
	go run ./cmd/nusa migrate up

.PHONY: migrate-down
migrate-down: ## Roll back one migration. Use STEPS=all to roll back everything
	go run ./cmd/nusa migrate down $(or $(STEPS),1)

.PHONY: migrate-version
migrate-version: ## Report the applied schema version
	go run ./cmd/nusa migrate version

.PHONY: clean
clean: ## Remove build output
	rm -rf bin $(WEB)/dist $(WEB)/coverage

SHELL := /usr/bin/env bash

BIN := commitbrief-ui
PKG := github.com/CommitBrief/ui

GO ?= go

# Explicit package patterns: a bare ./... would also walk Go files that npm
# packages ship inside frontend/node_modules.
GO_PKGS := . ./cmd/... ./internal/...

# Tool pins. Moving WAILS3_VERSION is a deliberate change with its own review
# (UI-0001 section 1); it must stay in step with the github.com/wailsapp/wails/v3
# version in go.mod and the @wailsio/runtime version in frontend/package.json.
WAILS3_VERSION      := v3.1.0
GO_LICENSES_VERSION := v1.6.0

FRONTEND_SRC := $(shell find frontend/src frontend/index.html frontend/vite.config.ts frontend/package.json frontend/package-lock.json frontend/bindings -type f 2>/dev/null)

.PHONY: help tools wails3-check build dev test vet lint fmt fmt-check tidy clean \
	license-check spdx-check i18n-check bindings-check \
	frontend-deps frontend-build frontend-check check

help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

tools: ## Install the pinned wails3 CLI and go-licenses into GOBIN
	$(GO) install github.com/wailsapp/wails/v3/cmd/wails3@$(WAILS3_VERSION)
	$(GO) install github.com/google/go-licenses@$(GO_LICENSES_VERSION)

wails3-check:
	@command -v wails3 >/dev/null 2>&1 || { echo "wails3 not found; run 'make tools' (and put \$$(go env GOPATH)/bin on PATH)"; exit 1; }
	@got=$$(NO_COLOR=1 wails3 version 2>&1 | grep -Eo 'v[0-9]+\.[0-9]+\.[0-9]+[^ ]*' | tail -n 1); \
	if [ "$$got" != "$(WAILS3_VERSION)" ]; then \
		echo "wails3 $$got installed, $(WAILS3_VERSION) required; run 'make tools'"; exit 1; \
	fi

# The Go packages embed frontend/dist (embed.go), so everything that compiles
# Go code needs a frontend build first.
frontend/node_modules: frontend/package-lock.json
	cd frontend && npm ci
	@touch frontend/node_modules

frontend/dist/index.html: frontend/node_modules $(FRONTEND_SRC)
	cd frontend && npm run build

frontend-deps: frontend/node_modules ## Install frontend dependencies (npm ci)

frontend-build: frontend/dist/index.html ## Build the frontend into frontend/dist

build: wails3-check ## Build the desktop binary into bin/ (wails3 build)
	wails3 build

dev: wails3-check ## Run the app in development mode (wails3 dev)
	wails3 dev -config ./build/config.yml

test: frontend-build ## Run Go tests
	$(GO) test $(GO_PKGS)

vet: frontend-build ## Run go vet
	$(GO) vet $(GO_PKGS)

lint: frontend-build ## Run golangci-lint (incl. depguard layering rules)
	golangci-lint run $(GO_PKGS)

fmt: ## Format Go source with gofmt (+ goimports if available)
	gofmt -s -w .
	@if command -v goimports >/dev/null 2>&1; then \
		goimports -w -local $(PKG) . ; \
	else \
		echo "goimports not installed (go install golang.org/x/tools/cmd/goimports@latest)"; \
	fi

fmt-check:
	@drift=$$(gofmt -l -s . | grep -v '^frontend/' || true); \
	if [ -n "$$drift" ]; then \
		echo "gofmt: needs '-s -w' on:"; echo "$$drift"; exit 1; \
	fi

tidy: ## Sync go.mod / go.sum
	$(GO) mod tidy

clean: ## Remove build artifacts
	rm -rf bin frontend/dist

license-check: ## Audit Go dependency licenses for GPL-3.0 compatibility
	bash scripts/license-check.sh

spdx-check: ## Fail if any Go source lacks the SPDX header
	bash scripts/spdx-check.sh

i18n-check: ## Fail if frontend/src/i18n/en.json and tr.json differ in keys
	node scripts/i18n-check.mjs

bindings-check: wails3-check frontend-deps ## Fail if committed Wails bindings drift from the Go code
	@tmp=$$(mktemp -d); \
	wails3 generate bindings -d "$$tmp" -ts -i $(GO_PKGS) >/dev/null 2>&1 || { echo "bindings generation failed"; rm -rf "$$tmp"; exit 1; }; \
	if diff -r "$$tmp" frontend/bindings >/dev/null 2>&1; then \
		rm -rf "$$tmp"; echo "bindings-check: ok"; \
	else \
		diff -r "$$tmp" frontend/bindings | head -n 40; rm -rf "$$tmp"; \
		echo "bindings drift: run 'wails3 generate bindings -clean=true -ts -i $(GO_PKGS)' and commit"; exit 1; \
	fi

frontend-check: frontend-deps ## Frontend lint + typecheck
	cd frontend && npm run lint
	cd frontend && npm run typecheck

check: ## Run every guard CI runs
	@echo "==> gofmt drift"
	@$(MAKE) -s fmt-check
	@echo "==> frontend build"
	@$(MAKE) -s frontend-build
	@echo "==> go vet"
	@$(MAKE) -s vet
	@echo "==> golangci-lint"
	@$(MAKE) -s lint
	@echo "==> go test"
	@$(MAKE) -s test
	@echo "==> license-check"
	@$(MAKE) -s license-check
	@echo "==> spdx-check"
	@$(MAKE) -s spdx-check
	@echo "==> i18n-check"
	@$(MAKE) -s i18n-check
	@echo "==> frontend-check"
	@$(MAKE) -s frontend-check
	@echo "==> bindings-check"
	@$(MAKE) -s bindings-check
	@echo "==> all checks passed"

##
## h2 Makefile
##
## Usage: make <target>
##

# ── Variables ────────────────────────────────────────────────────────
GO ?= go
VERSION_PKG := h2/internal/version
GIT_REF ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
RELEASE ?= false
LDFLAGS := -X '$(VERSION_PKG).GitRef=$(GIT_REF)' -X '$(VERSION_PKG).ReleaseBuild=$(RELEASE)'

# ── Build ────────────────────────────────────────────────────────────

.PHONY: build
build: ## Build the h2 binary
	$(GO) build -ldflags "$(LDFLAGS)" -o h2 ./cmd/h2

.PHONY: build-release
build-release: ## Build the h2 binary with release flag
	$(MAKE) build RELEASE=true

# ── Quality ──────────────────────────────────────────────────────────

.PHONY: fmt
fmt: ## Format all Go source files
	@echo "==> gofmt"
	gofmt -w .

.PHONY: fmt-nofix
fmt-nofix: ## Check formatting without modifying files
	@echo "==> gofmt (check)"
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "above files are not formatted" && exit 1)

.PHONY: fmt-check
fmt-check: fmt-nofix ## Alias for fmt-nofix

.PHONY: check
check: fmt ## Run vet + staticcheck (auto-formats first)
	@echo "==> go vet"
	$(GO) vet ./...
	@echo "==> staticcheck"
	$(GO) run honnef.co/go/tools/cmd/staticcheck@latest ./...

.PHONY: check-nofix
check-nofix: fmt-nofix ## Run vet + staticcheck without auto-formatting  [CI: 1/3]
	@echo "==> go vet"
	$(GO) vet ./...
	@echo "==> staticcheck"
	$(GO) run honnef.co/go/tools/cmd/staticcheck@latest ./...

# ── Tests ────────────────────────────────────────────────────────────

.PHONY: test
test: ## Run fast unit tests (skips slow + e2e)  [CI: 2/3]
	$(GO) test -short $$($(GO) list ./... | grep -v '^h2/e2etests$$')

.PHONY: test-all
test-all: ## Run all unit/integration tests (includes slow, skips e2e)  [CI: 3/3]
	$(GO) test $$($(GO) list ./... | grep -v '^h2/e2etests$$')

.PHONY: test-e2e
test-e2e: ## Run end-to-end tests
	$(GO) test ./e2etests

.PHONY: test-coverage
test-coverage: ## Run tests with coverage report (HTML + func summary)
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	$(GO) tool cover -func=coverage.out

# ── Tools ────────────────────────────────────────────────────────────

.PHONY: deps
deps: ## Install development tool dependencies
	$(GO) install honnef.co/go/tools/cmd/staticcheck@latest

.PHONY: loc
loc: ## Count lines of code (requires scc)
	scc --no-gen --exclude-dir .git,.beads,.claude,.github,docs,qa .

# ── Help ─────────────────────────────────────────────────────────────

.PHONY: help
help: ## Show this help
	@echo ""
	@echo "h2 Makefile targets:"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "CI jobs (single workflow, 3 steps):"
	@echo "  1/3  check-nofix    Lint + format check"
	@echo "  2/3  test           Fast unit tests"
	@echo "  3/3  test-all       All tests (including slow)"
	@echo ""

.DEFAULT_GOAL := help

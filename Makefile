# Maktaba developer + CI command surface (Story 22.1).
#
# CI runs the same `make` targets developers run locally so behavior
# stays in lock-step (Story 22.8 parity requirement). Each target is
# the smallest possible shell wrapper around the underlying tool — no
# extra logic that CI and developers have to keep in sync.

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c

GO_MODULES := api streaming
PIPELINE_DIR := pipeline
WEB_DIR := web

ARTIFACT_DIR ?= artifacts
GOOS ?= $(shell uname -s | tr '[:upper:]' '[:lower:]')
GOARCH ?= $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')

# Story 22.2 owns the reproducibility flags; we just consume them.
GO_LDFLAGS ?= -s -w
GO_BUILD_FLAGS ?= -trimpath

.PHONY: help
help:  ## Show this help.
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------------------
# Lint (gate 1)
# ---------------------------------------------------------------------------

.PHONY: lint
lint: lint-go lint-py lint-web  ## Run every linter (CI gate 1).

.PHONY: lint-go
lint-go:  ## gofmt + go vet + golangci-lint over every Go module.
	@for mod in $(GO_MODULES); do \
		echo "==> gofmt $$mod"; \
		test -z "$$(gofmt -l $$mod)" || { gofmt -d $$mod; exit 1; }; \
		echo "==> go vet $$mod"; \
		(cd $$mod && go vet ./...); \
		echo "==> golangci-lint $$mod"; \
		(cd $$mod && golangci-lint run ./...); \
	done

.PHONY: lint-py
lint-py:  ## ruff + mypy --strict over the pipeline package.
	cd $(PIPELINE_DIR) && uv run ruff check .
	cd $(PIPELINE_DIR) && uv run ruff format --check .
	cd $(PIPELINE_DIR) && uv run mypy

.PHONY: lint-web
lint-web:  ## eslint + tsc --noEmit + prettier --check.
	pnpm -C $(WEB_DIR) run lint
	pnpm -C $(WEB_DIR) run typecheck
	pnpm -C $(WEB_DIR) run format:check

# ---------------------------------------------------------------------------
# Unit tests (gate 2)
# ---------------------------------------------------------------------------

.PHONY: test-unit
test-unit: test-unit-go test-unit-py test-unit-web  ## Unit tier (Epic 20.1).

.PHONY: test-unit-go
test-unit-go:
	@for mod in $(GO_MODULES); do \
		echo "==> go test -short ./... ($$mod)"; \
		(cd $$mod && go test -short -race -count=1 ./...); \
	done

.PHONY: test-unit-py
test-unit-py:
	cd $(PIPELINE_DIR) && uv run pytest -m unit

.PHONY: test-unit-web
test-unit-web:
	pnpm -C $(WEB_DIR) run test:unit

# ---------------------------------------------------------------------------
# Integration tests (gate 3) — needs Postgres + Chroma reachable.
# ---------------------------------------------------------------------------

.PHONY: test-integration
test-integration:  ## Integration tier (Epic 20.4).
	@for mod in $(GO_MODULES); do \
		echo "==> go test -tags=integration ./... ($$mod)"; \
		(cd $$mod && go test -tags=integration -count=1 ./...); \
	done
	cd $(PIPELINE_DIR) && uv run pytest -m integration

.PHONY: migrate
migrate:  ## Apply database migrations against $$DATABASE_URL.
	@if [ -z "$${DATABASE_URL:-}" ]; then \
		echo "DATABASE_URL is required"; exit 1; \
	fi
	@echo "Story 24.x owns the migration runner; this is a stub."
	@echo "DATABASE_URL=$$DATABASE_URL"

# ---------------------------------------------------------------------------
# E2E tests (gate 4) — drives the compose stack.
# ---------------------------------------------------------------------------

.PHONY: test-e2e
test-e2e:  ## E2E tier (Epic 20.5). Assumes the compose stack is up.
	cd $(PIPELINE_DIR) && uv run pytest -m e2e

# ---------------------------------------------------------------------------
# Perf-CI (gate 5) — reduced perf suite.
# ---------------------------------------------------------------------------

.PHONY: perf-ci
perf-ci:  ## Reduced perf suite (Epic 20.7).
	@echo "perf-ci stub: Epic 20.7 will replace this with the real reduced perf suite."

# ---------------------------------------------------------------------------
# Build (gate 6)
# ---------------------------------------------------------------------------

.PHONY: build
build: build-go build-web  ## Cross-compile every artifact for $$GOOS/$$GOARCH.

.PHONY: build-go
build-go:
	@mkdir -p api/bin streaming/bin
	@for mod in $(GO_MODULES); do \
		echo "==> building $$mod for $(GOOS)/$(GOARCH)"; \
		(cd $$mod && \
			GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
			go build $(GO_BUILD_FLAGS) -ldflags='$(GO_LDFLAGS)' \
				-o bin/maktaba-$$mod .); \
	done

.PHONY: build-web
build-web:
	pnpm -C $(WEB_DIR) run build

.PHONY: clean
clean:
	rm -rf api/bin streaming/bin $(WEB_DIR)/dist $(ARTIFACT_DIR)

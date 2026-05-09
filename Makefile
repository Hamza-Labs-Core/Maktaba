# Maktaba developer + CI command surface (Story 22.1 + 22.2).
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

# ---------------------------------------------------------------------------
# Reproducibility envelope (Story 22.2)
# ---------------------------------------------------------------------------
#
# Two builds of the same source tree on the same OS/arch with the same Go
# toolchain must produce byte-identical binaries (AC1, TC1). The pieces:
#
#   -trimpath            strip absolute paths from the binary so $HOME and
#                        $PWD don't leak into the build.
#   -ldflags '-buildid=' clear Go's link-time random build ID; without this
#                        every build hashes differently.
#   -ldflags '-s -w'     strip debug + symbol tables (smaller + stable).
#   -X version.Version=… stamp release metadata so binaries can introspect
#                        themselves (`--version`, /healthz, log fields).
#   SOURCE_DATE_EPOCH    pinned to the source commit's timestamp; consumed
#                        by Go (mod cache mtime), uv, and tar so timestamps
#                        embedded in artifacts are deterministic.
#
# Cross-platform `git describe` and `git rev-parse` work the same way on
# every CI runner; the only host-derived value we rely on is the Go
# version, which CI pins via setup-go.

VERSION       ?= $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
COMMIT        ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
SOURCE_DATE_EPOCH ?= $(shell git log -1 --pretty=%ct 2>/dev/null || echo 0)
BUILD_DATE    := $(SOURCE_DATE_EPOCH)

# Each Go module gets its own -X path because the version package lives
# under the module's own import path.
API_VERSION_PKG       := github.com/Hamza-Labs-Core/Maktaba/api/internal/version
STREAMING_VERSION_PKG := github.com/Hamza-Labs-Core/Maktaba/streaming/internal/version

GO_LDFLAGS_BASE := -buildid= -s -w
GO_BUILD_FLAGS  ?= -trimpath

api_ldflags = $(GO_LDFLAGS_BASE) \
	-X $(API_VERSION_PKG).Version=$(VERSION) \
	-X $(API_VERSION_PKG).Commit=$(COMMIT) \
	-X $(API_VERSION_PKG).BuildDate=$(BUILD_DATE)

streaming_ldflags = $(GO_LDFLAGS_BASE) \
	-X $(STREAMING_VERSION_PKG).Version=$(VERSION) \
	-X $(STREAMING_VERSION_PKG).Commit=$(COMMIT) \
	-X $(STREAMING_VERSION_PKG).BuildDate=$(BUILD_DATE)

# Cross-compile matrix for `build-all`. Linux for servers + containers,
# darwin/arm64 for maintainer laptops. Windows + darwin/amd64 land with
# Story 22.7 (multi-platform packaging).
CROSS_PLATFORMS := linux/amd64 linux/arm64 darwin/arm64

# Common env that every Go build invocation needs. Exported via shell so
# child `go` invocations inherit it.
export TZ := UTC
export LANG := C.UTF-8
export SOURCE_DATE_EPOCH

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
# Build (gate 6) — Story 22.2 reproducibility envelope
# ---------------------------------------------------------------------------

.PHONY: build
build: build-go build-web  ## Build every artifact for $$GOOS/$$GOARCH (reproducible).

.PHONY: build-go
build-go: build-go-api build-go-streaming  ## Build both Go binaries.

# Output layout:
#   single-platform `make build`     -> {api,streaming}/bin/maktaba-{api,streaming}
#   cross-platform  `make build-all` -> {api,streaming}/bin/<os>-<arch>/maktaba-{api,streaming}
# `BIN_SUBDIR` is overridden by build-all so cross-compile outputs don't
# collide with each other.
BIN_SUBDIR ?=

.PHONY: build-go-api
build-go-api:
	@mkdir -p api/bin/$(BIN_SUBDIR)
	@echo "==> building api for $(GOOS)/$(GOARCH) (version=$(VERSION))"
	@cd api && \
		GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		go build $(GO_BUILD_FLAGS) -ldflags='$(api_ldflags)' \
			-o bin/$(BIN_SUBDIR)maktaba-api .

.PHONY: build-go-streaming
build-go-streaming:
	@mkdir -p streaming/bin/$(BIN_SUBDIR)
	@echo "==> building streaming for $(GOOS)/$(GOARCH) (version=$(VERSION))"
	@cd streaming && \
		GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		go build $(GO_BUILD_FLAGS) -ldflags='$(streaming_ldflags)' \
			-o bin/$(BIN_SUBDIR)maktaba-streaming .

.PHONY: build-web
build-web:
	pnpm -C $(WEB_DIR) run build

.PHONY: build-all
build-all:  ## Cross-compile Go binaries for every supported $(CROSS_PLATFORMS).
	@for platform in $(CROSS_PLATFORMS); do \
		os=$$(echo $$platform | cut -d/ -f1); \
		arch=$$(echo $$platform | cut -d/ -f2); \
		echo "==> $$os/$$arch"; \
		$(MAKE) --no-print-directory build-go \
			GOOS=$$os GOARCH=$$arch BIN_SUBDIR=$$os-$$arch/; \
	done
	@$(MAKE) --no-print-directory build-web

.PHONY: checksums
checksums:  ## Emit sha256 manifest of build outputs into $(ARTIFACT_DIR).
	@mkdir -p $(ARTIFACT_DIR)
	@echo "==> sha256 manifest -> $(ARTIFACT_DIR)/checksums.txt"
	@find api/bin streaming/bin $(WEB_DIR)/dist -type f 2>/dev/null \
		| sort \
		| LC_ALL=C xargs shasum -a 256 \
		> $(ARTIFACT_DIR)/checksums.txt || true
	@cat $(ARTIFACT_DIR)/checksums.txt

# ---------------------------------------------------------------------------
# Reproducibility self-check (Story 22.2 TC1)
# ---------------------------------------------------------------------------

.PHONY: verify-reproducibility
verify-reproducibility:  ## Build twice, diff sha256 sums (TC1).
	@bash tools/verify-reproducibility.sh

# ---------------------------------------------------------------------------
# SBOM (Story 22.2 stub; Story 23.7 wires CVE gating)
# ---------------------------------------------------------------------------

.PHONY: sbom
sbom:  ## Generate CycloneDX SBOMs for every component (stub).
	@bash tools/sbom.sh

# ---------------------------------------------------------------------------
# Lockfile drift gates (Story 22.2 TC3)
# ---------------------------------------------------------------------------

.PHONY: lockfile-check
lockfile-check:  ## Fail if uv.lock / pnpm-lock.yaml / go.mod drift from sources.
	@echo "==> uv lock --check"
	cd $(PIPELINE_DIR) && uv lock --check
	@echo "==> pnpm install --frozen-lockfile (lockfile-only)"
	pnpm -C $(WEB_DIR) install --frozen-lockfile --lockfile-only --reporter=silent
	@git diff --exit-code $(WEB_DIR)/pnpm-lock.yaml
	@for mod in $(GO_MODULES); do \
		echo "==> go mod verify ($$mod)"; \
		(cd $$mod && go mod verify); \
	done

.PHONY: clean
clean:
	rm -rf api/bin streaming/bin $(WEB_DIR)/dist $(ARTIFACT_DIR)

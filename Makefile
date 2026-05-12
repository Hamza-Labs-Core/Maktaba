# Maktaba developer + CI command surface (Story 22.1 + 22.2).
#
# CI runs the same `make` targets developers run locally so behavior
# stays in lock-step (Story 22.8 parity requirement). Each target is
# the smallest possible shell wrapper around the underlying tool — no
# extra logic that CI and developers have to keep in sync.

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c

GO_MODULES := api streaming shared/log/go shared/health/go shared/metrics/go shared/tracing/go shared/testtier/go tools/test-budget
PIPELINE_DIR := pipeline
WEB_DIR := web
MIGRATIONS_DIR := shared/db/migrations
MIGRATION_LINT_DIR := tools/migration-lint
MIGRATION_LINT_BASE_REF ?= origin/main

# Story 20.1 budgets — single source of truth, mirrored across
# shared/testtier/{go,py} so the soft caps + tier totals stay in
# sync. The integration / e2e / perf-ci numbers are wall-clock
# bounds for the whole tier; the unit tier is per-Go-package
# (enforced inside tools/test-budget.sh via `go test -json`).
UNIT_PACKAGE_BUDGET     ?= 30s
# 100ms was the AC4 target, but several api/internal/auth/* tests call
# RSA-2048 keygen multiple times (e.g. TestSet_JWKS_IncludesActiveAndPrevious
# generates two keys for rotation), and CI hardware pushes those to
# ~1.3 s. Bump to 500ms soft (hard = 1500ms via the 3x rule) so the
# crypto-heavy tests in the keys/middleware packages pass while the
# budget still flags tests that genuinely run 15x slower than intended.
UNIT_PER_TEST_SOFT_CAP  ?= 500ms
INTEGRATION_TIER_BUDGET ?= 2m
E2E_TIER_BUDGET         ?= 5m
PERF_CI_TIER_BUDGET     ?= 2m

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
	@awk 'BEGIN{FS = ":[^=]*## "} \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5); next } \
		/^[a-zA-Z][a-zA-Z0-9_-]*:[^=]*## / { printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2 }' \
		$(MAKEFILE_LIST)

# ---------------------------------------------------------------------------
# Local dev workflow (Story 22.8) — same `make` targets CI runs.
# ---------------------------------------------------------------------------

##@ Local development

DEV_COMPOSE := docker compose \
	-f deploy/compose/docker-compose.yml \
	-f deploy/compose/docker-compose.dev.yml

.PHONY: prereqs
prereqs:  ## Verify host has docker, go, uv, pnpm, git (and report versions).
	@bash tools/check-prereqs.sh

.PHONY: dev
dev:  ## Bring up the live-reload stack (postgres, chroma, api, streaming, pipeline, web).
	$(DEV_COMPOSE) up --build -d
	@echo ""
	@echo "Maktaba dev stack is up:"
	@echo "  api      : http://localhost:8080"
	@echo "  streaming: http://localhost:8081"
	@echo "  web      : http://localhost:5173"
	@echo "  postgres : postgres://maktaba:maktaba@localhost:5432/maktaba"
	@echo "  chroma   : http://localhost:8000"
	@echo ""
	@echo "Tail logs with: make dev-logs"

.PHONY: dev-down
dev-down:  ## Stop the dev stack but keep volumes (caches, postgres data).
	$(DEV_COMPOSE) down

.PHONY: dev-clean
dev-clean:  ## Stop the dev stack and remove volumes (resets caches + db).
	$(DEV_COMPOSE) down -v

.PHONY: dev-logs
dev-logs:  ## Tail logs from every dev service.
	$(DEV_COMPOSE) logs -f --tail=50

.PHONY: dev-build
dev-build:  ## Rebuild dev images (after changing a Dockerfile.dev).
	$(DEV_COMPOSE) build

.PHONY: dev-ps
dev-ps:  ## Show status of dev stack services.
	$(DEV_COMPOSE) ps

##@ Quality gates

# ---------------------------------------------------------------------------
# Lint (gate 1)
# ---------------------------------------------------------------------------

.PHONY: lint
lint: lint-go lint-py lint-web lint-migrations  ## Run every linter (CI gate 1).

.PHONY: lint-migrations
lint-migrations:  ## Migration conventions: append-only + idempotency + SQLite parity.
	@echo "==> migration-lint vet/test"
	cd $(MIGRATION_LINT_DIR) && go vet ./...
	cd $(MIGRATION_LINT_DIR) && go test ./...
	@echo "==> migration-lint $(MIGRATIONS_DIR) (base=$(MIGRATION_LINT_BASE_REF))"
	cd $(MIGRATION_LINT_DIR) && go run . \
		--dir $(CURDIR)/$(MIGRATIONS_DIR) \
		--base-ref $(MIGRATION_LINT_BASE_REF)

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
# Test pyramid (Story 20.1)
# ---------------------------------------------------------------------------
#
# Three runtime tiers (unit / integration / e2e) plus the dedicated
# perf-ci tier. Tier separation is by:
#
#   Go     — `go test -short` for unit; `//go:build integration` for
#            integration. The shared helper `WithSoftCap` from
#            shared/testtier/go enforces AC4 per-test soft caps.
#   Python — `@pytest.mark.unit` / `@pytest.mark.integration` /
#            `@pytest.mark.e2e`. The `maktaba_testtier` plugin loaded
#            via pipeline/tests/conftest.py enforces AC1 (no sockets
#            in unit tests) and AC4 (per-test soft cap).
#   TS     — `*.unit.spec.ts`, `*.int.spec.ts`, `*.e2e.spec.ts`.
#            Configs live at shared/testtier/ts; activated by Epic 11.
#
# Per-tier wall-clock budgets are enforced by tools/test-budget.sh
# and surface as `::error::` annotations in CI on breach.

.PHONY: test
test: test-unit test-integration test-e2e  ## Run every tier (unit + integration + e2e).

# ---------------------------------------------------------------------------
# Unit tests (gate 2; Story 20.1 unit tier)
# ---------------------------------------------------------------------------

##@ Tests

.PHONY: test
test: test-unit  ## Default test target: the unit tier (no network, no sudo).

.PHONY: test-unit
test-unit: test-unit-go test-unit-py test-unit-web  ## Unit tier (Story 20.1).

.PHONY: test-unit-go
test-unit-go:
	@GO_MODULES="$(GO_MODULES)" \
		UNIT_PACKAGE_BUDGET="$(UNIT_PACKAGE_BUDGET)" \
		UNIT_PER_TEST_SOFT_CAP="$(UNIT_PER_TEST_SOFT_CAP)" \
		bash tools/test-budget.sh unit

.PHONY: test-unit-py
test-unit-py:
	cd $(PIPELINE_DIR) && uv run pytest -m unit

.PHONY: test-unit-web
test-unit-web:
	pnpm -C $(WEB_DIR) run test:unit

# ---------------------------------------------------------------------------
# Integration tests (gate 3; Story 20.1 integration tier)
# Needs Postgres + Chroma reachable.
# ---------------------------------------------------------------------------

.PHONY: test-integration
test-integration:  ## Integration tier (Story 20.1, Epic 20.4).
	@bash tools/test-budget.sh wall integration $(INTEGRATION_TIER_BUDGET) -- \
		$(MAKE) --no-print-directory test-integration-inner

.PHONY: test-integration-inner
test-integration-inner:
	@for mod in $(GO_MODULES); do \
		echo "==> go test -tags=integration ./... ($$mod)"; \
		(cd $$mod && go test -tags=integration -count=1 ./...); \
	done
	@# pytest exits 5 when no tests match the marker — that's the
	@# normal state until Story 20.4 lands real integration tests.
	@# The `|| { ... }` form is required because .SHELLFLAGS has
	@# `-e`, which would otherwise kill the recipe on pytest's
	@# non-zero exit before the rc check runs.
	@cd $(PIPELINE_DIR) && uv run pytest -m integration || { \
		rc=$$?; [ $$rc -eq 5 ] || exit $$rc; \
	}

.PHONY: migrate
migrate:  ## Apply database migrations against $$DATABASE_URL.
	@if [ -z "$${DATABASE_URL:-}" ]; then \
		echo "DATABASE_URL is required"; exit 1; \
	fi
	cd api && go run . migrate up --dir $(CURDIR)/$(MIGRATIONS_DIR)

.PHONY: migrate-status
migrate-status:  ## Show applied vs. pending migrations against $$DATABASE_URL.
	@if [ -z "$${DATABASE_URL:-}" ]; then \
		echo "DATABASE_URL is required"; exit 1; \
	fi
	cd api && go run . migrate status --dir $(CURDIR)/$(MIGRATIONS_DIR)

# ---------------------------------------------------------------------------
# E2E tests (gate 4) — drives the compose stack.
# ---------------------------------------------------------------------------

.PHONY: test-e2e
test-e2e:  ## E2E tier (Story 20.1, Epic 20.5). Assumes the compose stack is up.
	@bash tools/test-budget.sh wall e2e $(E2E_TIER_BUDGET) -- \
		$(MAKE) --no-print-directory test-e2e-inner

.PHONY: test-e2e-inner
test-e2e-inner:
	@# pytest exits 5 when no tests match the marker — that's the
	@# normal state until Story 20.5 lands real e2e tests.
	@cd $(PIPELINE_DIR) && uv run pytest -m e2e; rc=$$?; \
		[ $$rc -eq 0 ] || [ $$rc -eq 5 ] || exit $$rc

# ---------------------------------------------------------------------------
# Perf-CI (gate 5; Story 20.1 perf-ci tier) — reduced perf suite.
# ---------------------------------------------------------------------------

.PHONY: perf-ci
perf-ci:  ## Reduced perf suite (Story 20.1, Epic 20.7).
	@bash tools/test-budget.sh wall perf-ci $(PERF_CI_TIER_BUDGET) -- \
		$(MAKE) --no-print-directory perf-ci-inner

.PHONY: perf-ci-inner
perf-ci-inner:
	@echo "perf-ci stub: Epic 20.7 will replace this with the real reduced perf suite."

# ---------------------------------------------------------------------------
# Build (gate 6) — Story 22.2 reproducibility envelope
# ---------------------------------------------------------------------------

##@ Build

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
build-web: build-tokens
	pnpm -C $(WEB_DIR) run build

.PHONY: build-tokens
build-tokens:  ## Story 17.1 — generate CSS/TS/Swift/Kotlin/JSON outputs from web/design-system/tokens/tokens.json.
	@echo "==> building design tokens"
	@node $(WEB_DIR)/design-system/build/build-tokens.mjs

.PHONY: test-tokens
test-tokens:  ## Story 17.1 — assert the design-tokens build pipeline is green.
	@node $(WEB_DIR)/design-system/build/verify-tokens.mjs

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

##@ Formatting

.PHONY: format
format:  ## Auto-format every language (in-place).
	@for mod in $(GO_MODULES); do \
		echo "==> gofmt -w $$mod"; \
		gofmt -w $$mod; \
	done
	cd $(PIPELINE_DIR) && uv run ruff format .
	pnpm -C $(WEB_DIR) run format:write

# ---------------------------------------------------------------------------
# Lockfile drift gates (Story 22.2 TC3)
# ---------------------------------------------------------------------------

##@ Misc

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

# ---------------------------------------------------------------------------
# Compose stack (Story 22.3)
# ---------------------------------------------------------------------------
#
# `make compose-up` brings up the canonical stack; `make compose-mac`
# layers the Mac overlay on top (FFmpeg bind + MLX). Build args mirror
# the reproducibility envelope so an image built from `make compose-up`
# matches the one CI publishes for the same commit.

COMPOSE_FILE := deploy/compose/docker-compose.yml
COMPOSE_MAC_FILE := deploy/compose/docker-compose.mac.yml
COMPOSE_IMAGES := api streaming pipeline web
COMPOSE_REGISTRY ?= ghcr.io/maktaba
COMPOSE_VERSION ?= $(VERSION)

# Compose reads these from the environment via the file's
# ${MAKTABA_VERSION}/${MAKTABA_COMMIT}/${SOURCE_DATE_EPOCH} interpolation.
export MAKTABA_VERSION ?= $(VERSION)
export MAKTABA_COMMIT  ?= $(COMMIT)

.PHONY: compose-up
compose-up:  ## Bring up the full compose stack and wait for healthy.
	docker compose -f $(COMPOSE_FILE) up -d --wait

.PHONY: compose-down
compose-down:  ## Tear the compose stack down (preserves volumes).
	docker compose -f $(COMPOSE_FILE) down

.PHONY: compose-down-volumes
compose-down-volumes:  ## Tear down + drop named volumes (destructive).
	docker compose -f $(COMPOSE_FILE) down -v

.PHONY: compose-logs
compose-logs:  ## Tail logs from the compose stack.
	docker compose -f $(COMPOSE_FILE) logs -f

.PHONY: compose-build
compose-build:  ## Build the four service images locally via compose.
	docker compose -f $(COMPOSE_FILE) build

.PHONY: compose-mac
compose-mac:  ## Bring up compose with the Mac (FFmpeg + MLX) overlay.
	docker compose -f $(COMPOSE_FILE) -f $(COMPOSE_MAC_FILE) up -d --wait

.PHONY: compose-mac-down
compose-mac-down:  ## Tear down the Mac-overlay compose stack.
	docker compose -f $(COMPOSE_FILE) -f $(COMPOSE_MAC_FILE) down

.PHONY: image-size-guard
image-size-guard:  ## Fail if any image exceeds the AC4 size cap.
	bash tools/image-size-guard.sh

.PHONY: clean
clean:
	rm -rf api/bin streaming/bin $(WEB_DIR)/dist $(ARTIFACT_DIR)

# Maktaba developer + CI command surface (Story 22.1 + 22.2).
#
# CI runs the same `make` targets developers run locally so behavior
# stays in lock-step (Story 22.8 parity requirement). Each target is
# the smallest possible shell wrapper around the underlying tool — no
# extra logic that CI and developers have to keep in sync.

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c

GO_MODULES := api streaming cmd/maktaba-server cmd/maktaba-launcher shared/go/layout shared/go/pagetheme shared/log/go shared/health/go shared/metrics/go shared/tracing/go shared/errrpt/go shared/go/releaseverify shared/testtier/go tools/release tools/test-budget tools/log-lint
PIPELINE_DIR := pipeline
WEB_DIR := web
MIGRATIONS_DIR := shared/db/migrations
MIGRATION_LINT_DIR := tools/migration-lint
MIGRATION_LINT_BASE_REF ?= origin/main
LOG_LINT_DIR := tools/log-lint

# Story 20.1 budgets — single source of truth, mirrored across
# shared/testtier/{go,py} so the soft caps + tier totals stay in
# sync. The integration / e2e / perf-ci numbers are wall-clock
# bounds for the whole tier; the unit tier is per-Go-package
# (enforced inside tools/test-budget.sh via `go test -json`).
UNIT_PACKAGE_BUDGET     ?= 30s
# 100ms was the AC4 target. The auth/keys tests now share two
# pre-generated RSA-2048 keypairs across the package via
# sync.OnceValues, which drops the per-test keygen cost from
# ~250ms to amortized ~0 in that package. Real-world tests still
# hit slow paths under `-race` though: `api/internal/auth/jwt`
# signs/verifies tokens (RSA-PKCS1-v1.5 is not cheap), and the
# `streaming/internal/auth` signed-URL tests do HMAC + base64
# round-trips. Hold at 400ms soft (hard = 1200ms via the 3x rule)
# to cover CI race-detector overhead; tightening further requires
# either sharing keypairs in jwt/streaming-auth too (parallel to
# what we did for auth/keys) or pulling the slow ops out of the
# hot path. See HLB-251 follow-up notes.
# The module's packages run in parallel under -race on a 4-vCPU
# runner, so a test's time is partly the others' load: tests whose
# own work is a fraction of the cap (7 SQLite migrations and a few
# queries) took 1.21-1.24s, a different one each failing run. So
# tools/test-budget.sh re-runs a test past the hard cap once, alone,
# and fails only if it is past the hard cap alone too (or fails); a
# breach that does not repeat is a ::warning:: naming both timings.
# More than 10 such tests in a module fail without re-runs.
UNIT_PER_TEST_SOFT_CAP  ?= 400ms
# Integration wall budget: 2m held at ~1m45 on main; story 35.1 links
# sigstore-go into api and cmd/maktaba-server (in-process release
# verification), which adds ~20s of compile/link to the tier's test
# binaries, so 2m30s. Since then Epics 37-39 added real integration
# tests (adopt, the Windows app, relisten on Postgres): main's last ten
# runs (2026-10-22) took 1m42s-2m27s, two merges minutes apart differing
# by ~45s with the runner (the tier is mostly compiling and linking the Go
# modules one after another, then pytest). 2m30s failed PRs whose every
# test passed (2m30.5s, 2m31.9s). 3m30s is the slowest of those runs plus
# ~40%, for runner noise and the next tests; a test that hangs, or a
# slowdown that takes the tier past it, still fails. By 2026-10-25 every
# test passed in runs of 3m35s-3m50s (Epic 36's index worker, the error
# logging, the claim loop's tests) and the tier failed main on time alone,
# holding releases: 5m is the slowest of those plus ~30%.
INTEGRATION_TIER_BUDGET ?= 5m
E2E_TIER_BUDGET         ?= 5m
MEDIA_E2E_TIER_BUDGET   ?= 9m
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
#   -ldflags '-buildid=' clear Go's link-time build ID (not for macOS: Go
#                        makes the LC_UUID from it, and macOS matches a
#                        Local Network or other privacy grant to a program
#                        by that; the ID is a content hash, so two builds
#                        still match byte for byte).
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
SERVER_VERSION_PKG    := github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version

# macOS builds keep the build ID (above): GOOS from the command line, else this host's
GO_LDFLAGS_BASE := $(if $(filter darwin,$(or $(GOOS),$(shell go env GOOS))),,-buildid= )-s -w
# The streaming binary ships with TV staging and hot swap (Epic 33 stories
# 33.5-33.9) compiled in; `make build-go-streaming STREAMING_TAGS=` builds
# without them (rules.tv_staging.enabled then has no effect).
STREAMING_TAGS ?= tvstaging
GO_BUILD_FLAGS  ?= -trimpath

api_ldflags = $(GO_LDFLAGS_BASE) \
	-X $(API_VERSION_PKG).Version=$(VERSION) \
	-X $(API_VERSION_PKG).Commit=$(COMMIT) \
	-X $(API_VERSION_PKG).BuildDate=$(BUILD_DATE)

streaming_ldflags = $(GO_LDFLAGS_BASE) \
	-X $(STREAMING_VERSION_PKG).Version=$(VERSION) \
	-X $(STREAMING_VERSION_PKG).Commit=$(COMMIT) \
	-X $(STREAMING_VERSION_PKG).BuildDate=$(BUILD_DATE)

server_ldflags = $(GO_LDFLAGS_BASE) \
	-X $(SERVER_VERSION_PKG).Version=$(VERSION) \
	-X $(SERVER_VERSION_PKG).Commit=$(COMMIT) \
	-X $(SERVER_VERSION_PKG).BuildDate=$(BUILD_DATE)

# Cross-compile matrix for `build-all`. Linux for servers + containers,
# darwin/arm64 for maintainer laptops. Windows + darwin/amd64 land with
# Story 22.7 (multi-platform packaging).
CROSS_PLATFORMS := linux/amd64 linux/arm64 darwin/arm64

# Common env that every Go build invocation needs. Exported via shell so
# child `go` invocations inherit it.
export TZ := UTC
export LANG := C.UTF-8
export SOURCE_DATE_EPOCH
# Export the build stamp so the web build (vite.config.ts) can emit
# dist/version.json + bake VITE_APP_VERSION (Epic 28, Story 28.1). The Go
# builds pass these via -ldflags; the web build reads them from the env.
export VERSION
export COMMIT

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
lint: lint-go lint-py lint-web lint-migrations lint-log  ## Run every linter (CI gate 1).

.PHONY: lint-log
lint-log:  ## Story 21.1 AC-4: no runtime data concatenated into log msg.
	@echo "==> log-lint vet/test"
	cd $(LOG_LINT_DIR) && go vet ./...
	cd $(LOG_LINT_DIR) && go test ./...
	@echo "==> log-lint (api/streaming/shared log sites)"
	cd $(LOG_LINT_DIR) && go run . \
		--dirs "$(CURDIR)/api,$(CURDIR)/streaming,$(CURDIR)/shared/log/go,$(CURDIR)/shared/health/go,$(CURDIR)/shared/metrics/go,$(CURDIR)/shared/tracing/go,$(CURDIR)/shared/errrpt/go"

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
	@echo "==> go test -tags $(STREAMING_TAGS) ./internal/frontdoor/ (streaming, as shipped)"
	@cd streaming && go test -tags '$(STREAMING_TAGS)' -count=1 ./internal/frontdoor/

.PHONY: test-unit-py
test-unit-py:
	cd $(PIPELINE_DIR) && uv run pytest -m unit

.PHONY: test-unit-web
test-unit-web:
	pnpm -C $(WEB_DIR) run test:unit

# ---------------------------------------------------------------------------
# Integration tests (gate 3; Story 20.1 integration tier)
# Needs Postgres + Chroma reachable.
#
# INTEGRATION_GO_PG_URL names a Postgres server (its user may create
# databases and roles) for the Go tests gated on MAKTABA_TEST_PG_URL:
# each makes, migrates and drops its own scratch database there. It
# is handed to the Go tests only (the pipeline's pytest reads the same
# variable); empty leaves MAKTABA_TEST_PG_URL as the environment has
# it. CI's integration job points it at its Postgres service.
# ---------------------------------------------------------------------------

INTEGRATION_GO_PG_URL ?=

# The release tooling has no integration-tagged tests; its unit run
# covers it.
INTEGRATION_GO_MODULES := $(filter-out shared/go/releaseverify tools/release,$(GO_MODULES))

.PHONY: test-integration
test-integration:  ## Integration tier (Story 20.1, Epic 20.4).
	@bash tools/test-budget.sh wall integration $(INTEGRATION_TIER_BUDGET) -- \
		$(MAKE) --no-print-directory test-integration-inner

# The Go modules run four at a time (tools/ci/integration-go.sh: their output in order,
# the tier failing when any fails), with the pipeline's integration tests beside them.
# pytest exits 5 when no tests match the marker: that is no failure (the `|| ...` forms:
# .SHELLFLAGS has -e).
.PHONY: test-integration-inner
test-integration-inner:
	@py_log="$$(mktemp)"; \
	( cd $(PIPELINE_DIR) && uv run pytest -m integration ) >"$$py_log" 2>&1 & py=$$!; \
	go_rc=0; $(if $(INTEGRATION_GO_PG_URL),MAKTABA_TEST_PG_URL='$(INTEGRATION_GO_PG_URL)') \
		bash tools/ci/integration-go.sh $(INTEGRATION_GO_MODULES) || go_rc=$$?; \
	py_rc=0; wait $$py || py_rc=$$?; \
	echo "==> pytest -m integration ($(PIPELINE_DIR))"; cat "$$py_log"; rm -f "$$py_log"; \
	if [ $$py_rc -ne 0 ] && [ $$py_rc -ne 5 ]; then exit $$py_rc; fi; \
	exit $$go_rc

# Epic 35 story 35.5 AC6: the previous stable release against this build's database (its
# repository tests on these migrations, and the nodes' schema guard both ways); a failure
# without a "Breaking for the previous release" MANIFEST row fails. Needs
# MAKTABA_TEST_PG_URL, psql and the git tags.
.PHONY: compat-prev
compat-prev:  ## The previous release on this build's database (needs MAKTABA_TEST_PG_URL).
	@bash tools/compat-prev/run.sh

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
# Media e2e tests — the Go tests that need a real ffmpeg/ffprobe (remote
# end to end, simulcast, two hosts, the channel engine and strip). Needs
# ffmpeg + ffprobe, fonts-noto-core and MAKTABA_TEST_PG_URL; fails on any
# skip. See tools/media-e2e.sh.
# ---------------------------------------------------------------------------

.PHONY: test-media-e2e
test-media-e2e:  ## Media e2e tier (real ffmpeg + Postgres; CI job media-e2e).
	@bash tools/test-budget.sh wall media-e2e $(MEDIA_E2E_TIER_BUDGET) -- \
		bash tools/media-e2e.sh

# ---------------------------------------------------------------------------
# E2E tests (gate 4) — drives the compose stack.
# ---------------------------------------------------------------------------

.PHONY: test-e2e
test-e2e:  ## E2E tier (Story 20.1, Epic 20.5). Assumes the compose stack is up.
	@bash tools/test-budget.sh wall e2e $(E2E_TIER_BUDGET) -- \
		$(MAKE) --no-print-directory test-e2e-inner

.PHONY: test-e2e-inner
test-e2e-inner:
	@# Track V: real gate. NO exit-5 swallow — an empty suite (pytest
	@# exit 5) must FAIL, so a missing/uncollected e2e suite can never
	@# be a false green. .SHELLFLAGS has `-e`, so any non-zero pytest
	@# exit kills the recipe, which is exactly the desired behaviour.
	@#
	@# Run from the pipeline uv env (the only env with pytest — CI
	@# does `cd pipeline && uv sync`), but point it at the repo-root
	@# tests/e2e suite. $(CURDIR) is the repo root (this Makefile).
	@cd $(PIPELINE_DIR) && uv run pytest -m e2e $(CURDIR)/tests/e2e -q

# ---------------------------------------------------------------------------
# Perf-CI (gate 5; Story 20.1 perf-ci tier) — reduced perf suite.
# ---------------------------------------------------------------------------

.PHONY: perf-ci
perf-ci:  ## Reduced perf suite (Story 20.1, Epic 20.7).
	@bash tools/test-budget.sh wall perf-ci $(PERF_CI_TIER_BUDGET) -- \
		$(MAKE) --no-print-directory perf-ci-inner

.PHONY: perf-ci-inner
perf-ci-inner:
	@# Track V: real gate. Asserts the ci_pr subset of
	@# shared/perf_budgets.yaml is present, non-empty, and well-formed
	@# (removing the budgets file makes this fail). Run from the
	@# pipeline uv env (the only env with pytest) against the
	@# repo-root tests/perf suite. $(CURDIR) is the repo root.
	@cd $(PIPELINE_DIR) && uv run pytest $(CURDIR)/tests/perf/test_perf_ci.py -q
	@# Epic 35 story 35.16 TC11's latency half: the cluster link's control channel under 20
	@# TV pulls and a release download (its numbers in the log; the integration tier keeps
	@# the functional half).
	@echo "==> go test -tags perf TestControlLatencySLO (api/internal/cluster/link)"
	@cd api && go test -tags perf -count=1 -v -run '^TestControlLatencySLO$$' ./internal/cluster/link/

# ---------------------------------------------------------------------------
# Coverage floor (Story 20.3 AC1/TC1) — real gate with teeth.
#
# Collects `go test -short -coverprofile` per Go module and a
# pytest-cov total for the pipeline, then fails if any module's
# statement coverage is below its documented floor in
# tools/coverage-floor/floors.yaml. The floors are a NON-BREAKING
# RATCHET set at the coverage measured on the integrated branch, so
# enabling the gate cannot redden an in-flight PR; raise floors as
# coverage improves, never lower them to pass a red PR. Wired into the
# CI Lint gate (.github/workflows/_lint.yml) so a coverage regression
# blocks merge — mirrors V's real-gate pattern (test-e2e / perf-ci).
# ---------------------------------------------------------------------------

.PHONY: test-coverage
test-coverage:  ## Coverage-floor gate (Story 20.3): fail if a module regresses below its floor.
	@GO_MODULES="$(GO_MODULES)" PIPELINE_DIR="$(PIPELINE_DIR)" \
		bash tools/coverage-floor.sh

# ---------------------------------------------------------------------------
# Build (gate 6) — Story 22.2 reproducibility envelope
# ---------------------------------------------------------------------------

##@ Build

.PHONY: build
build: build-go build-web  ## Build every artifact for $$GOOS/$$GOARCH (reproducible).

.PHONY: build-go
build-go: build-go-api build-go-streaming build-go-cloudlink  ## Build the Go binaries.

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
		go build $(GO_BUILD_FLAGS) -tags '$(STREAMING_TAGS)' -ldflags='$(streaming_ldflags)' \
			-o bin/$(BIN_SUBDIR)maktaba-streaming .

# The cloud relay agent (Epic 25): the home's supervisor runs it while [cloud] is
# enabled (the cloud module, outside the api/streaming graph).
.PHONY: build-go-cloudlink
build-go-cloudlink:
	@mkdir -p cloud/bin/$(BIN_SUBDIR)
	@echo "==> building cloudlink for $(GOOS)/$(GOARCH) (version=$(VERSION))"
	@cd cloud && \
		GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		go build $(GO_BUILD_FLAGS) -ldflags='-X main.Version=$(VERSION)' \
			-o bin/$(BIN_SUBDIR)maktaba-cloudlink ./cmd/maktaba-cloudlink

.PHONY: server
server: build-web  ## Build the unified maktaba-server binary with the embedded web UI.
	@mkdir -p cmd/maktaba-server/bin/$(BIN_SUBDIR)
	@echo "==> embedding web UI into maktaba-server"
	@rm -rf cmd/maktaba-server/internal/webui/dist
	@mkdir -p cmd/maktaba-server/internal/webui/dist
	@cp -R $(WEB_DIR)/dist/. cmd/maktaba-server/internal/webui/dist/
	@touch cmd/maktaba-server/internal/webui/dist/.gitkeep
	@echo "==> building maktaba-server for $(GOOS)/$(GOARCH) (version=$(VERSION))"
	@cd cmd/maktaba-server && \
		GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		go build $(GO_BUILD_FLAGS) -tags embed_web -ldflags='$(server_ldflags)' \
			-o bin/$(BIN_SUBDIR)maktaba-server .
	@echo "==> built cmd/maktaba-server/bin/$(BIN_SUBDIR)maktaba-server"

.PHONY: server-dev
server-dev:  ## Build maktaba-server WITHOUT the embedded web UI (fast dev compile).
	@mkdir -p cmd/maktaba-server/bin/$(BIN_SUBDIR)
	@cd cmd/maktaba-server && \
		CGO_ENABLED=0 go build $(GO_BUILD_FLAGS) -ldflags='$(server_ldflags)' \
			-o bin/$(BIN_SUBDIR)maktaba-server .
	@echo "==> built (no embed) cmd/maktaba-server/bin/$(BIN_SUBDIR)maktaba-server"

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

# ---------------------------------------------------------------------------
# Native app shells (Epics 12/13) — Capacitor (iOS/Android) + Tauri (desktop)
# ---------------------------------------------------------------------------
#
# All three wrap the SAME web/dist bundle. The shells' own build scripts
# own the web build + platform sync so the Make targets stay thin wrappers
# (the Story 22.8 "no logic CI and devs must keep in sync" rule). Native
# toolchains (Xcode+CocoaPods, Android SDK+JDK, Rust) are NOT prerequisites
# for the rest of the repo, so these targets are intentionally out of the
# default `build` graph.

##@ Native apps

MOBILE_DIR  := apps/mobile
DESKTOP_DIR := apps/desktop
TVOS_DIR    := apps/tv/tvos
ANDROID_TV_DIR := apps/tv/android

.PHONY: mobile-build
mobile-build:  ## Build the Capacitor iOS+Android apps (web build + cap sync). Needs Xcode/Android SDK.
	@bash $(MOBILE_DIR)/scripts/build.sh

.PHONY: desktop-build
desktop-build:  ## Build the Tauri desktop app for the host OS. Needs the Rust toolchain.
	@bash $(DESKTOP_DIR)/scripts/build.sh

# The TV apps are NATIVE shells (not web wrappers): SwiftUI on tvOS,
# Compose on Android TV. Like the other native targets they are out of
# the default `build` graph — they need Xcode / the Android SDK.
.PHONY: tv-build-ios
tv-build-ios:  ## Build the Apple TV app (scheme "Maktaba TV" in apps/apple/Maktaba.xcodeproj) for the tvOS simulator. Needs Xcode.
	xcodebuild -project apps/apple/Maktaba.xcodeproj -scheme "Maktaba TV" -destination 'generic/platform=tvOS Simulator' CODE_SIGNING_ALLOWED=NO build

.PHONY: tv-build-android
tv-build-android:  ## Assemble the Android TV debug APK via the Gradle wrapper. Needs the Android SDK.
	cd $(ANDROID_TV_DIR) && ./gradlew :app:assembleDebug

.PHONY: native-sync
native-sync: build-web  ## Rebuild web/dist and sync it into the native platforms (Capacitor cap sync).
	@echo "==> syncing web/dist into Capacitor platforms"
	@pnpm -C $(MOBILE_DIR) exec cap sync
	@echo "==> Tauri reads web/dist at build time (run 'make desktop-build')"

# Packaging targets — local mirrors of the `*-release.yml` CI workflows.
# They produce the SAME installable artifacts CI ships, on this host, for
# smoke-testing a release before tagging. Native toolchains required (Rust
# / Android SDK+JDK17 / Xcode+CocoaPods); these stay out of `build`.

.PHONY: package-desktop
package-desktop:  ## Build the Tauri desktop installers for the host OS (.dmg / .msi+.exe / .deb+.AppImage). Needs Rust.
	@bash $(DESKTOP_DIR)/scripts/build.sh

# Maktaba.app with the whole server inside (Epic 36, story 36.7): Go binaries, Postgres, Ollama,
# ffmpeg, Python and the pipeline wheel in Contents/Resources/server. Pinned downloads are
# in tools/release/mac-app/pins.env. Signed when APPLE_SIGNING_IDENTITY is set.
.PHONY: mac-app
mac-app:  ## Build Maktaba.app + .dmg for Apple Silicon with the server payload (unsigned unless APPLE_SIGNING_IDENTITY). Needs Rust, Go, uv, pnpm.
	@bash tools/release/mac-app/build-app.sh

.PHONY: mac-app-pins
mac-app-pins:  ## Dry run of the Maktaba.app pins (pins.env complete, checked downloads), offline; ONLINE=1 also asks each URL.
	@bash tools/release/mac-app/pins-check.sh $(if $(ONLINE),--online,)

.PHONY: mac-app-payload
mac-app-payload:  ## Build only the Maktaba.app server payload (build/mac-app/server) and smoke-test it.
	@bash tools/release/mac-app/payload.sh
	@bash tools/release/mac-app/check.sh

# The Windows app with the whole server inside (Epic 38, story 38.11): Maktaba.exe and
# <install>\server (Go binaries, Postgres, ffmpeg, Python, the pipeline wheel) as one NSIS
# installer for the current user. Pinned downloads are in tools/release/win-app/pins.env.
# Unsigned. Run in Git Bash on Windows x64; payload.sh builds the Go binaries with this make
# (Git for Windows has none: MinGW's make, Strawberry Perl's gmake, ...). Through a variable
# of its own: a recipe line naming $(MAKE) itself runs even under `make -n`.
WIN_APP_MAKE := $(MAKE)

.PHONY: win-app
win-app:  ## Build the Windows app's NSIS installer with the server payload (unsigned) and test-install it. Needs Git Bash, Rust, Go, uv, pnpm, Visual Studio's C++ tools.
	@MAKE="$(WIN_APP_MAKE)" bash tools/release/win-app/build-app.sh

.PHONY: win-app-payload
win-app-payload:  ## Build only the Windows app's server payload (build/win-app/server) and smoke-test it.
	@MAKE="$(WIN_APP_MAKE)" bash tools/release/win-app/payload.sh
	@bash tools/release/win-app/check.sh

# The Linux server payload (Epic 39, story 39.2): the same tree for this machine's
# architecture (x86_64 or aarch64), with Postgres's libraries from this machine. Pinned
# downloads are in tools/release/linux-app/pins.env.
.PHONY: linux-app
linux-app:  ## Build the Linux app (AppImage, .deb, .rpm) for this machine's architecture with the server payload, and check them. Needs Rust, Go, uv, pnpm, patchelf, tauri's Linux libraries.
	@bash tools/release/linux-app/build-app.sh

.PHONY: linux-app-payload
linux-app-payload:  ## Build the Linux server payload (build/linux-app/server) for this machine's architecture and smoke-test it. Needs Go, uv, pnpm, patchelf.
	@bash tools/release/linux-app/payload.sh
	@bash tools/release/linux-app/check.sh

.PHONY: package-mobile-android
package-mobile-android: native-sync  ## Build the Android release APK + AAB (web build + cap sync + gradle). Needs Android SDK + JDK17.
	@echo "==> assembling Android release APK + AAB"
	@cd $(MOBILE_DIR)/android && ./gradlew --no-daemon assembleRelease bundleRelease
	@echo "==> outputs under $(MOBILE_DIR)/android/app/build/outputs/{apk,bundle}/release/"

.PHONY: package-mobile-ios
package-mobile-ios: native-sync  ## Archive the iOS app (unsigned — see workflow TODO for signed .ipa). Needs macOS + Xcode + CocoaPods.
	@echo "==> installing CocoaPods + archiving iOS (unsigned)"
	@cd $(MOBILE_DIR)/ios/App && pod install
	@cd $(MOBILE_DIR)/ios/App && xcodebuild -workspace App.xcworkspace -scheme App \
		-configuration Release -sdk iphoneos -archivePath build/Maktaba.xcarchive \
		archive CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO
	@echo "==> archive at $(MOBILE_DIR)/ios/App/build/Maktaba.xcarchive (a signed .ipa needs Apple certs)"

.PHONY: package-tv-android
package-tv-android:  ## Assemble the Android TV release APK. Needs the Android SDK + JDK17.
	@cd $(ANDROID_TV_DIR) && ./gradlew --no-daemon :app:assembleRelease
	@echo "==> output under $(ANDROID_TV_DIR)/app/build/outputs/apk/release/"

.PHONY: package-all
package-all: package-desktop package-mobile-android package-mobile-ios package-tv-android  ## Build every client package (desktop + mobile iOS/Android + Android TV). tvOS .ipa is distributed manually via Xcode/TestFlight.
	@echo "==> all client packages built. tvOS distribution is manual (Xcode ▸ Archive ▸ TestFlight)."

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

# ---------------------------------------------------------------------------
# Cloud Relay CI/CD (deploy/cloud-relay/, .github/workflows/relay.yml)
# ---------------------------------------------------------------------------
#
# `relay-ci` mirrors the relay.yml PR gates (lint + test + build) so a
# developer reproduces CI locally; `relay-deploy` is a thin wrapper over
# the single-source-of-truth deploy script (also called by the CI deploy
# job). Both keep the Story 22.8 "no logic CI and devs must keep in sync"
# rule — the deploy logic lives in deploy.sh, not here.

CLOUD_DIR := cloud
RELAY_DEPLOY_SCRIPT := deploy/cloud-relay/deploy.sh

.PHONY: relay-ci
relay-ci: relay-lint relay-test relay-build  ## Run the relay PR gates locally (lint + test + build), mirroring relay.yml.

.PHONY: relay-lint
relay-lint:  ## golangci-lint over the cloud module.
	@echo "==> golangci-lint (cloud)"
	cd $(CLOUD_DIR) && golangci-lint run --timeout=5m

.PHONY: relay-test
relay-test:  ## Run the cloud module tests (needs a Postgres at MAKTABA_CLOUD_DB_URL for the integration tier).
	@echo "==> go test (cloud, unit)"
	cd $(CLOUD_DIR) && go test -short -count=1 ./...
	@echo "==> go test (cloud, integration — needs MAKTABA_CLOUD_DB_URL)"
	cd $(CLOUD_DIR) && go test -tags=integration -count=1 ./...

.PHONY: relay-build
relay-build:  ## Cross-compile the maktaba-cloud relay binary for linux/{amd64,arm64}.
	@for arch in amd64 arm64; do \
		echo "==> building maktaba-cloud for linux/$$arch"; \
		mkdir -p dist; \
		(cd $(CLOUD_DIR) && \
			GOOS=linux GOARCH=$$arch CGO_ENABLED=0 \
			go build -trimpath \
				-ldflags "-buildid= -s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT)" \
				-o "../dist/maktaba-cloud-linux-$$arch" ./cmd/maktaba-cloud); \
	done

.PHONY: relay-deploy
relay-deploy:  ## Roll a new relay image onto the VPS via deploy.sh (set RELAY_SSH_HOST/USER + MAKTABA_CLOUD_TAG).
	@bash $(RELAY_DEPLOY_SCRIPT)

.PHONY: clean
clean:
	rm -rf api/bin streaming/bin $(WEB_DIR)/dist $(ARTIFACT_DIR)

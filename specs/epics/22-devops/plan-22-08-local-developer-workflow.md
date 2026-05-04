# Implementation Plan — Story 22.8 Local developer workflow

> Companion to [story-22-08-local-developer-workflow.md](story-22-08-local-developer-workflow.md).
> Story states *what* and *why*; this plan states *how*.
> Reuses the make targets defined in
> [Story 22.1](plan-22-01-ci-pipeline.md) (CI runs the same targets a
> developer runs locally — no CI-only scripts).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Dev compose overlay | `deploy/compose/docker-compose.dev.yml`. Bind-mounts source dirs; runs `air` for Go and `vite dev` / `uvicorn --reload` so file save → hot reload in ≤ 5 s. |
| Live reload Go | `air` (`cosmtrek/air`) with per-service config. |
| Live reload Python | `uv run watchmedo auto-restart …` (`maktaba-pipeline serve` re-execs on src change). |
| Live reload web | `vite` dev server proxied through Caddy on the dev compose. |
| Pre-commit | `pre-commit` Python tool with config at `.pre-commit-config.yaml`. |
| CONTRIBUTING.md | Single source of truth for the dev loop; CI documentation links here. |
| Out of scope | Production compose (Story 22.3); CI gates (Story 22.1). |

## 1. Architecture diagram

```
┌──────────────────────────────────────┐
│ make dev                             │
└──────────────┬───────────────────────┘
               │ docker compose -f base.yml -f dev.yml up
               ▼
   ┌──────────────────────────────────┐
   │ caddy (host:443 → web)           │
   │ web    : vite dev (hot module)   │
   │ api    : air → go run            │
   │ stream : air → go run            │
   │ pipe   : watchmedo + uvicorn     │
   │ pg     : standard image          │
   └──────────────────────────────────┘
   bind-mount → /src for each service
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `deploy/compose/docker-compose.dev.yml` | Dev overlay; bind mounts + dev images. |
| `api/.air.toml`, `streaming/.air.toml` | air configs. |
| `pipeline/dev-watch.sh` | Wraps `watchmedo`. |
| `Makefile` (extended) | `dev`, `dev-down`, `lint`, `test`, `test-unit`, `test-integration`, `test-e2e`, `perf-ci`, `build`, `format`, `migrate`, `apps`. (`migrate` and `apps` are listed in arch §12.2; `migrate` shells to `tools/build.sh apps` is incorrect — see §2.7 for actual mapping.) |
| `.pre-commit-config.yaml` | gofmt, ruff, prettier, trailing whitespace, EOF newline. |
| `CONTRIBUTING.md` | The canonical workflow doc. |
| `.editorconfig` | Tabs/spaces, line endings — one source. |
| `.devcontainer/devcontainer.json` | Optional: VS Code dev container. |
| `Makefile.help` | Auto-generated help target. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `README.md` | "Quick start" pointing at CONTRIBUTING. |
| `web/vite.config.ts` | Dev server `host: 0.0.0.0` and `hmr.host` for Docker access. |
| `api/Dockerfile.dev`, `streaming/Dockerfile.dev`, `pipeline/Dockerfile.dev` | Slim dev images that include the compiler/runtime + live-reload tooling. |

### 2.3 Dev compose overlay

`deploy/compose/docker-compose.dev.yml`:

```yaml
services:
  postgres:
    ports: ["5432:5432"]      # Direct access from host for psql.

  api:
    build:
      context: ../..
      dockerfile: api/Dockerfile.dev
    image: maktaba/api:dev
    volumes:
      - ../../api:/src/api
      - ../../shared:/src/shared
      - go-build-cache:/root/.cache/go-build
    environment:
      MAKTABA_LOG_LEVEL: debug
      MAKTABA_AUTO_MIGRATE: "true"
    command: ["air", "-c", "/src/api/.air.toml"]

  streaming:
    build:
      context: ../..
      dockerfile: streaming/Dockerfile.dev
    image: maktaba/streaming:dev
    volumes:
      - ../../streaming:/src/streaming
      - ../../shared:/src/shared
      - go-build-cache:/root/.cache/go-build
    command: ["air", "-c", "/src/streaming/.air.toml"]

  pipeline:
    build:
      context: ../..
      dockerfile: pipeline/Dockerfile.dev
    image: maktaba/pipeline:dev
    volumes:
      - ../../pipeline:/src/pipeline
    environment:
      UV_LINK_MODE: copy
    command: ["bash", "/src/pipeline/dev-watch.sh"]

  web:
    build:
      context: ../..
      dockerfile: web/Dockerfile.dev
    image: maktaba/web:dev
    volumes:
      - ../../web:/src/web
      - pnpm-store:/root/.local/share/pnpm/store
    command: ["pnpm", "-C", "/src/web", "dev", "--host", "0.0.0.0"]
    environment:
      VITE_HMR_HOST: localhost   # Docker-bridge → host port mapping.

volumes:
  go-build-cache:
  pnpm-store:
```

Each service's `Dockerfile.dev` is a thin layer over the base toolchain
image with `air` (Go), `watchmedo` (Python), and the project sources
mounted at runtime — no copy at build time.

### 2.4 air config (Go)

`api/.air.toml`:

```toml
root = "/src/api"
# tmp_dir lives OUTSIDE the bind-mounted source tree so that the
# editor's filesystem watcher (VS Code, JetBrains, etc.) doesn't see
# the rebuilt binary as a "user edit" and trigger another full reload
# (PLAN_REVIEW §22-08). `/tmp/.air-api` is a tmpfs path inside the
# container; it persists across air rebuilds but vanishes on container
# stop. A repo-relative `.air-tmp/` would also work as long as it is
# `.gitignore`d AND added to the editor's ignore list.
tmp_dir = "/tmp/.air-api"

[build]
  cmd = "go build -o /tmp/.air-api/main ./cmd/api"
  bin = "/tmp/.air-api/main"
  full_bin = "/tmp/.air-api/main serve"
  include_ext = ["go", "tpl", "tmpl", "html", "sql"]
  exclude_dir = ["vendor", ".air-tmp"]
  delay = 200
  stop_on_error = true

[log]
  time = true
  main_only = true

[misc]
  clean_on_exit = true
```

A `.go` save triggers a `go build`; the supervisor SIGTERMs the old
binary and starts the new one. Wall clock under 5 s for a small change
on a modern laptop.

### 2.5 watchmedo wrapper (Python)

`pipeline/dev-watch.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

uv sync --frozen --dev
exec uv run watchmedo auto-restart \
  --directory /src/pipeline/src \
  --pattern '*.py' \
  --recursive \
  --signal SIGTERM \
  -- maktaba-pipeline serve
```

`auto-restart --signal SIGTERM` lets the asyncio gRPC server shut down
cleanly so heartbeats finish and claims release.

### 2.6 vite dev (web)

`web/Dockerfile.dev`:

```dockerfile
FROM node:20-alpine
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN corepack enable && pnpm config set store-dir /root/.local/share/pnpm/store
RUN pnpm install --frozen-lockfile
EXPOSE 5173
CMD ["pnpm", "dev", "--host", "0.0.0.0"]
```

Caddy's dev config proxies `/` to `web:5173` instead of the prod
`web:80`. HMR uses the same port; `VITE_HMR_HOST=localhost` ensures
the websocket URL the browser opens points at the host port mapping.

### 2.7 Makefile

`Makefile` (relevant section):

```make
.PHONY: dev dev-down lint test test-unit test-integration test-e2e \
        perf-ci build help migrate apps generate

COMPOSE_DEV = docker compose -f deploy/compose/docker-compose.yml \
                             -f deploy/compose/docker-compose.dev.yml

dev:                 ## Start the live-reload stack
	$(COMPOSE_DEV) up --build -d
	@echo "Maktaba is up. Web: https://localhost  API: https://localhost/api"

dev-down:            ## Stop the dev stack but keep volumes
	$(COMPOSE_DEV) down

lint: lint-go lint-py lint-web ## Run all linters

lint-go:
	cd api && golangci-lint run ./... && gofmt -l . | (! grep .)
	cd streaming && golangci-lint run ./... && gofmt -l . | (! grep .)
	go run tools/migration-lint.go

lint-py:
	cd pipeline && uv run ruff check . && uv run mypy --strict src

lint-web:
	cd web && pnpm tsc --noEmit && pnpm lint && pnpm prettier --check .

test: test-unit ## Default: unit tier (Epic 20.1)

test-unit:
	cd api && go test -short ./...
	cd streaming && go test -short ./...
	cd pipeline && uv run pytest -m unit
	cd web && pnpm test:unit --run

test-integration:    ## Requires Postgres + Chroma; CI provides them.
	cd api && go test -tags=integration ./...
	cd pipeline && uv run pytest -m integration

test-e2e:
	docker compose -f deploy/compose/docker-compose.yml up -d --wait
	cd tests/e2e && pnpm test
	docker compose -f deploy/compose/docker-compose.yml down

perf-ci:
	cd tests/perf && go run ./cmd/perf-ci

build:
	tools/build.sh all

migrate:             ## Run pending DB migrations against $MAKTABA_DATABASE_URL (arch §12.2)
	cd api && go run ./cmd/api migrate up

apps:                ## Build mobile, desktop, and TV apps (arch §12.2)
	tools/build.sh apps

generate:            ## Run sqlc, gqlgen, protoc; CI's generate-drift gate runs the same target (Story 22.1)
	cd api && go generate ./...
	cd streaming && go generate ./...

format:
	cd api && gofmt -w .
	cd streaming && gofmt -w .
	cd pipeline && uv run ruff format .
	cd web && pnpm prettier --write .

help:                ## Print available targets
	@# Pattern is POSIX-portable: BSD awk (default on macOS) and GNU
	@# awk both honor -F as a literal split string. Avoid GNU-only
	@# extensions like `gensub`, lookahead in regex, or `\b`.
	@grep -E '^[a-zA-Z_-]+:[^=]*## ' $(MAKEFILE_LIST) | \
	  awk 'BEGIN { FS = ":[^#]*## " } { printf "%-20s %s\n", $$1, $$2 }'
```

CI's `_lint.yml`, `_unit.yml`, etc. invoke these exact targets — no
divergent CI scripts (AC3).

### 2.8 pre-commit

`.pre-commit-config.yaml`:

```yaml
default_install_hook_types: [pre-commit, commit-msg]
repos:
  - repo: https://github.com/pre-commit/pre-commit-hooks
    rev: v4.6.0
    hooks:
      - id: trailing-whitespace
      - id: end-of-file-fixer
      - id: check-yaml
      - id: check-json
      - id: check-added-large-files
        args: ['--maxkb=512']
  # `dnephin/pre-commit-golang` has been unmaintained since 2021 (last
  # release v0.5.1). PLAN_REVIEW §22-08 — switched to the actively-
  # maintained `tekwizely/pre-commit-golang` fork, which tracks Go 1.22+
  # and modern golangci-lint. Local hooks below are the fallback if the
  # fork is ever unavailable.
  - repo: https://github.com/tekwizely/pre-commit-golang
    rev: v1.0.0-rc.1
    hooks:
      - id: go-fmt-repo
      - id: go-vet-repo-mod
  - repo: https://github.com/astral-sh/ruff-pre-commit
    rev: v0.6.0
    hooks:
      - id: ruff
      - id: ruff-format
  - repo: https://github.com/pre-commit/mirrors-prettier
    rev: v3.1.0
    hooks:
      - id: prettier
        files: '^web/'
  - repo: local
    hooks:
      - id: migration-lint
        name: migration-lint
        entry: go run tools/migration-lint.go
        language: system
        files: '^shared/db/migrations/'
        pass_filenames: false
```

`pre-commit install` is part of the `make dev` first-run prompt.

### 2.9 CONTRIBUTING.md

```
# Contributing to Maktaba

Day-1 setup (≤ 30 minutes):

1. Install Docker Desktop or Docker Engine + Compose v2.
2. Clone the repo.
3. `make dev` — first run pulls images and builds, ~5 minutes.
4. Open https://localhost (Caddy serves the web; auto-CA-trusted on Mac).
5. `make test` — unit tier; runs without network, no sudo.

Day-N loop:

- Edit a `.go`, `.py`, or `.tsx` file. The change is live in ≤ 5 s.
- `make lint` before pushing — same as CI.
- `pre-commit install` once; runs the quick checks on every commit.

Troubleshooting:

- `make dev-down && make dev` rebuilds the dev images.
- `docker compose logs -f <service>` for live logs.
- Apple Silicon: MLX requires the Mac compose overlay (Story 22.3).
- Behind a corporate proxy: set `MAKTABA_REGISTRY_MIRROR` (EC2).
```

## 3. Test plan

### 3.1 Cold/warm start (TC1)

| Test | What it pins |
|---|---|
| `TestColdMakeDevUnder5Min` | Fresh clone + cold image cache; `make dev` returns within 5 minutes; `/api/health` 200. |
| `TestWarmMakeDevUnder90s` | Second `make dev` (images cached, volumes warm) returns within 90 s. |
| `TestMakeDevDownIdempotent` | `make dev-down` twice; second is a no-op (no stale containers). |

### 3.2 Live reload (TC2)

| Test | What it pins |
|---|---|
| `TestGoLiveReloadUnder5s` | Edit `api/cmd/api/main.go`; observable behavior change visible at `https://localhost/api/system/version` within 5 s. |
| `TestPythonLiveReloadUnder5s` | Same flow against `pipeline/src/maktaba_pipeline/cli.py`. |
| `TestWebHotModuleUnder1s` | Edit `web/src/App.tsx`; the open browser tab updates without full reload within 1 s. |

### 3.3 Parity (TC3)

| Test | What it pins |
|---|---|
| `TestMakeLintParity` | A fixture branch with three deliberate violations: `make lint` locally and CI's `_lint.yml` produce identical pass/fail per linter. |
| `TestMakeBuildParity` | `make build` produces sha256-identical artifacts as CI's build matrix (per Story 22.2). |
| `TestNoCiOnlyScripts` | A grep over `.github/workflows/` for shell snippets > 5 lines fails CI; everything that big should be a `make` target. |

### 3.4 Pre-commit

| Test | What it pins |
|---|---|
| `TestPreCommitInstallsAndRuns` | `pre-commit install && pre-commit run --all-files` exits 0 on a clean tree. |

`TestNoVerifyBypassedCaughtByCi` (EC3) lives in plan-22-01 (CI
pipeline) — the safety-net assertion belongs with the lint gate that
catches the bypass, not with the local pre-commit setup. This plan
links to it from the EC3 row below.

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| Apple Silicon vs Intel (EC1) | Both arches are tested; `make dev` selects the right base images via `--platform=$BUILDPLATFORM`. MLX features are gated behind a runtime check; on Intel they fall back to whisper.cpp. | `TestMakeDevOnIntelMac` (manual) |
| Slow corporate proxy (EC2) | `MAKTABA_REGISTRY_MIRROR=https://mirror.corp/v2/` env makes Docker pull through the mirror; documented in CONTRIBUTING. | `TestMirrorEnvHonored` |
| Pre-commit bypassed (EC3) | `--no-verify` is allowed locally; CI's lint gate catches it. The merge gate is the safety net. | `TestNoVerifyBypassedCaughtByCi` (lives in plan-22-01) |
| Volume permissions on Linux | The dev images run as `1000:1000`; the bind-mounted source dirs are owned by the user (`stat $UID == 1000`). On non-1000-UID hosts, the `MAKTABA_UID` env overrides. | `TestNon1000UidLinux` |
| Docker Desktop file-system slow (Mac) | The `:cached` mount option is set on every bind in `docker-compose.dev.yml` for Mac users (compose ignores it on Linux). | `TestMacBindCached` |
| Air watching too many files | `.air.toml` excludes `vendor/` and `tmp/`; CPU under 5 % when idle. | `TestAirIdleCpu` |
| Vite HMR over Caddy | Caddy's reverse_proxy preserves the WebSocket upgrade; the `VITE_HMR_HOST=localhost` env wires it correctly. | `TestWebHmrThroughCaddy` |
| Pipeline auto-restart loses claims | `watchmedo --signal SIGTERM` lets asyncio shutdown release claims; tested by inducing a restart while a claim is held. | `TestPipelineRestartReleasesClaim` |
| `make test` requires no network | Asserted by running `make test` with `--network=none` in CI; a network-dependent test fails the gate. | `TestUnitTierNoNetwork` |
| `make test` requires no sudo | All test paths run as user; `sudo` invocations fail the lint check. | `TestNoSudoInTests` |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `docker compose` | v2.27+ | Profiles, secrets, healthchecks. |
| `air` | latest | Go live reload. |
| `watchdog` (`watchmedo`) | latest | Python live reload. |
| `pre-commit` | 3.x | Git hooks. |
| `vite` | 5.x (already in repo) | Web dev server. |

## 6. Acceptance checklist

**Boot times**
- [ ] Cold `make dev` ≤ 5 min on a representative laptop.
- [ ] Warm `make dev` ≤ 90 s.

**Live reload**
- [ ] Go save → live in ≤ 5 s.
- [ ] Python save → live in ≤ 5 s.
- [ ] Web save → HMR in ≤ 1 s.

**Parity**
- [ ] CI runs `make lint`, `make test-*`, `make build`, `make migrate`, `make apps`, `make generate` — no CI-only scripts.
- [ ] `make help` lists every target.
- [ ] `make help` awk pattern is POSIX-portable (works on both BSD-awk default on macOS and GNU-awk on Linux).

**Pre-commit**
- [ ] `.pre-commit-config.yaml` covers gofmt, ruff, prettier, trailing whitespace, EOF, large-file guard, migration lint.
- [ ] CI catches bypassed pre-commit (`--no-verify`).

**Docs**
- [ ] CONTRIBUTING.md documents the dev loop.
- [ ] README.md links to CONTRIBUTING.

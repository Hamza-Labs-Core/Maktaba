# Epic 22 — DevOps & Delivery: Spec-vs-Implementation Gap Analysis

**Verdict (one line):** CI gating (22.1), reproducible-build flags (22.2),
container/compose (22.3), migration discipline (22.4), and local dev
workflow (22.8) are largely **complete**; but the entire **release →
publish → package** half of the epic (22.5 release.yml, 22.6
upgrade/rollback tooling, 22.7 nfpm/mobile/desktop builds) is
**missing or stub** — there is no path to actually ship a signed,
versioned release, so the epic's stated goal ("maintainers ship
releases predictably") is not deliverable.

> Method: every AC was traced to concrete files/lines in
> `.github/workflows/`, `Makefile`, `deploy/`, `tools/`, `api/`,
> `pipeline/`. Spec/audit self-claims were re-verified against code.
> Note: `specs/P0_CERTIFICATION.md` is **stale** — it reports 22.8 as
> "essentially un-implemented," but `make dev`, `.pre-commit-config.yaml`,
> `deploy/compose/docker-compose.dev.yml`, `.air.toml`, `.editorconfig`,
> and `.env.example` all now exist. The pipeline-lint "T201 in
> `__main__.py` stub" claim is also stale: `__main__.py` uses
> `sys.stdout.write`, not `print()`.

---

## Story 22.1 — Continuous integration pipeline

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 — six gates: lint, unit, integration, e2e, perf-ci, build-artifacts | **complete** (perf-ci substantively stub) | `.github/workflows/ci.yml:61-95` fans out all six reusable workflows; `_lint.yml`, `_unit.yml`, `_integration.yml`, `_e2e.yml`, `_perf-ci.yml`, `_build-artifacts.yml` all exist. Lint covers golangci-lint (api/streaming/shared), gofmt, vet, ruff, mypy, eslint, tsc, prettier, migration-lint (`_lint.yml:66-97`). **Gap:** `perf-ci` resolves to `Makefile:316 perf-ci-inner` which only `echo`s a stub — gate runs but proves nothing (AC1.5 behaviorally unmet; spec explicitly defers real suite to Epic 20.7, so acceptable-by-deferral). |
| AC2 — merge requires all six green; force-merge needs recorded override | **complete** | `ci.yml:134-171` `ci-success` rollup; `deploy/github/branch-protection.tf:16-19` pins `ci-success` as the sole required check, `allows_force_pushes=false`; `_pr-body-check.yml:32-52` enforces `force-merge:` reason line (≥10 chars) when the `force-merge` label is present. |
| AC3 — build gate on linux/amd64, linux/arm64, darwin/arm64 | **complete** | `_build-artifacts.yml:19-26` matrix exactly these three; `name: build (${{ matrix.goos }}/${{ matrix.goarch }})` surfaces offending arch (TC2). Test gates run ubuntu-22.04; **partial gap:** no darwin/arm64 spot-check job for darwin-only test paths (AC3 second sentence) — not implemented. |
| AC4 — green PR wall-clock ≤ 20 min | **partial/unverified** | Per-gate `timeout-minutes` set (lint 10, unit 15, integration 20, e2e 25, build 15). Sum of `needs`-parallel critical path is plausibly < 20 min but **no enforcement / `TestWallClock` metric** exists. |
| TC1 gate independence | complete | Separate reusable workflows; each reports independently into rollup. |
| TC2 cross-platform break visible | complete | `fail-fast: false` + arch in job name. |
| TC3 force-merge refused without body | complete | `_pr-body-check.yml`. |
| EC1 flake quarantine | partial | No `nick-fields/retry@v3` service-up retry block as the plan specifies (plan §5); integration gate uses a hand-rolled poll loop (`_integration.yml:71-85`) — acceptable substitute. |
| EC2 fork PR skips e2e/perf-ci + comment | complete | `ci.yml:76-90` fork guard; `ci.yml:101-132` `fork-rerun-comment` bot job. |
| EC3 docs-only skip + label | complete | `ci.yml:31-59` `dorny/paths-filter@v4` `every:` filter; `.github/labeler.yml` `docs-only` rule + `labeler.yml` workflow. |

**Story verdict: complete** (perf-ci stub and missing wall-clock metric
are the only gaps; both are spec-acknowledged deferrals).

---

## Story 22.2 — Reproducible builds and artifacts

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 — Go `-trimpath -ldflags='-buildid='` + vendored deps; stable sha256 | **partial** | `Makefile:77-88` sets `GO_LDFLAGS_BASE := -buildid= -s -w`, `GO_BUILD_FLAGS := -trimpath`, `CGO_ENABLED=0`, `SOURCE_DATE_EPOCH`/`TZ=UTC`/`LANG=C.UTF-8` exported (`Makefile:97-99`). `tools/verify-reproducibility.sh` exists and is wired to `make verify-reproducibility`. **Gap:** spec/plan require **vendored deps** (`api/vendor/`, `streaming/vendor/`, `GOFLAGS=-mod=vendor`) — `api/vendor/` and `streaming/vendor/` **do not exist**; build is module-cache based, so byte-stability depends on an unpinned module cache. `tools/.go-build-flags` single-source file (plan §2.1) **missing** (flags inlined in Makefile instead — functionally OK). |
| AC2 — deterministic image builder + pinned base by digest | **missing** | Plan mandates `ko` for Go images + `docker buildx --provenance` for pipeline, base images pinned **by digest**. Reality: hand-written multi-stage Dockerfiles (`api/Dockerfile`, `streaming/Dockerfile`, `web/Dockerfile`, `pipeline/Dockerfile`). Base images pinned by **tag only** (`golang:1.23-bookworm`, `postgres:16-alpine`, `caddy:2-alpine`, `ghcr.io/chroma-core/chroma:0.5.5`) — **no `@sha256:` digest pins anywhere**. No `.ko.yaml`. No `--provenance`. |
| AC3 — Python pinned `uv` lockfile; `uv lock` drift fails CI | **complete** | `pipeline/uv.lock` present; `Makefile:423-433 lockfile-check` runs `uv lock --check`. **Gap:** `lockfile-check` is **not invoked by any CI workflow** (`_lint.yml` runs `lint-go/py/web/migrations` but not `lockfile-check`) — drift gate exists but is **unwired** (TC3 unmet in CI). |
| AC4 — pinned pnpm lockfile; byte-stable vite build | **partial** | `web/pnpm-lock.yaml` present; `lockfile-check` checks it (but unwired, see AC3). No evidence of deterministic rollup output config / sorted-output plugin verified; `verify-reproducibility.sh` covers web/dist but is **not run in CI** (no `_reproducibility-check.yml`). |
| AC5 — all release artifacts signed (cosign images, minisign binaries) | **missing** | No `tools/sign.sh`. No cosign/minisign invocation anywhere in repo. No `SECURITY.md` maintainer pubkeys for signing. Signing entirely absent. |
| TC1 reproducibility build-twice | partial | `tools/verify-reproducibility.sh` + `make verify-reproducibility` exist; **not wired to CI** (`_reproducibility-check.yml` missing). |
| TC2 signature verification | missing | No signing → nothing to verify. |
| TC3 lockfile drift fails CI | **unwired** | `make lockfile-check` works locally but no workflow calls it. |

**Story verdict: partial** — local reproducibility envelope is solid;
signing, digest-pinned/deterministic images, vendored deps, and CI
wiring of drift+repro checks are all missing.

---

## Story 22.3 — Container images and compose stack

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 — four images api/streaming/pipeline/web | **partial** | Four Dockerfiles exist (`api/`, `streaming/`, `pipeline/`, `web/Dockerfile`) and `deploy/compose/docker-compose.yml` references `ghcr.io/maktaba/{api,streaming,pipeline,web}`. **Gap:** no workflow **builds or publishes** these images. `_build-artifacts.yml` builds Go binaries + web bundle only, **no `docker build`/`docker push`**. "Published per release" unmet (no release.yml — see 22.5). |
| AC2 — compose boots full stack one command | **complete** | `deploy/compose/docker-compose.yml` (postgres, chroma, api, streaming, pipeline, web, caddy) with healthchecks + `depends_on: service_healthy`; `make compose-up` = `up -d --wait`. `_e2e.yml:36` drives it. |
| AC3 — mac overlay binds FFmpeg + ANE; doctor verifies | **partial** | `deploy/compose/docker-compose.mac.yml` exists with FFmpeg bind + `MAKTABA_STT_BACKEND=whisper_mlx`; `make compose-mac`. **Gap:** the MLX-bind doctor probe is a **stub** — `pipeline/src/maktaba_pipeline/__main__.py:48-52 _doctor()` "Always returns 0 until Story 22.3 §2.7 fills in the real DB / Chroma / ffmpeg / MLX checks." No `doctor_mac.py`. The "doctor one-liner verifies the bind" AC is behaviorally unmet (TC2 unmet). |
| AC4 — image sizes ≤ caps; guard fails CI | **partial/unwired** | `tools/image-size-guard.sh` exists with correct byte caps; `make image-size-guard`. **Gap:** **not wired into any CI workflow** — `grep` of `.github/` finds zero references. AC4's "fails CI" (TC3) unmet; also requires images to be built first, which CI never does. |
| TC1 cold boot ≤ 90 s all healthy | partial | Compose has healthchecks; `_e2e.yml` does `up -d --wait` but no 90 s assertion test. |
| Deferred-from-P0: replace ghost healthcheck binary | **resolved differently** | `tools/healthcheck/main.go` is a real Go binary baked into api/streaming images (`api/Dockerfile:68-79`); distroless images keep it (acceptable). |
| Deferred-from-P0: image-size guard CI integration | **NOT done** | Still unwired (see AC4). |

**Story verdict: partial** — compose stack works; image build/publish,
size-guard CI wiring, and the Mac doctor probe are missing/stub.

---

## Story 22.4 — Database migrations

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 — append-only enforced vs main in CI | **complete** | `tools/migration-lint/main.go:167-233 appendOnlyCheck` diffs `origin/main...HEAD`; allows comment-only edits; `_lint.yml:96-97` runs `make lint-migrations`; `_lint.yml:24 fetch-depth: 0` so the base ref resolves. |
| AC2 — `migrate up` at boot behind flag; `--migrate-only` exits | **partial** | Boot-time auto-migrate: `api/main.go:139-145` honors `MAKTABA_AUTO_MIGRATE=true`. `migrate` subcommand exists (`api/migrate.go`) with `up/status/version/validate`. **Gap:** **no `--migrate-only` flag** and **no `--accept-long-migration` flag** (`api/migrate.go:31-42` flag set has only dir/dsn/dialect/timeout/help). AC2 second clause unmet. |
| AC3 — every migration has idempotency guard | **complete** | `migration-lint` idempotencyCheck (`main.go:300-333`) enforces `IF NOT EXISTS`/`IF EXISTS` for CREATE/DROP TABLE/INDEX, ADD COLUMN, VIEW, TRIGGER; integration gate runs `make migrate` twice (`_integration.yml:87-94`) as a live idempotency smoke. |
| AC4 — long-running DDL lint (CREATE INDEX w/o CONCURRENTLY, rewrites) | **partial** | `longRunningCheck` (`main.go:346-370`) flags non-CONCURRENTLY index, ALTER TYPE, SET NOT NULL. **Gap:** the ">10 k row" / "table rewrites without batched plan" nuance (TC3) is not row-aware (static regex only — acceptable approximation); **`lints.json` exemption mechanism is MISSING** — plan §2.6 requires `shared/db/migrations/meta/lints.json` with expiry; `shared/db/migrations/meta/` directory **does not exist** and the linter has no `isExempt`. Any legitimately-safe long DDL cannot be exempted. |
| AC5 — SQLite parity `.sqlite.sql` per migration | **complete** | `parityCheck` (`main.go:376-392`); every `NNNN_*.sql` in `shared/db/migrations/` has a `.sqlite.sql` sibling (verified by listing). |
| TC2 idempotent (run twice no-op) | complete | `_integration.yml:87-94`. |
| EC1 down migrations dev-only | partial | `+goose Down` blocks present (e.g. `0029_users.sql:55`); no `MAKTABA_DISABLE_DOWN` enforcement in code. |

**Story verdict: complete-ish** — linter is robust and CI-wired; gaps
are `--migrate-only`/`--accept-long-migration` flags and the missing
`lints.json` exemption system.

---

## Story 22.5 — Release management and versioning

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 — SemVer; platform version spans api+streaming+pipeline+web; app-store tagged separately | **missing infra** | No `VERSION` file at repo root (plan §2.1 makes it the single source of truth). Version is `git describe` derived at build (`Makefile:67`). `apps/mobile` has no `compatibleApiVersion` (grep finds none). No lockstep mechanism. |
| AC2 — release = git tag on green main; release.yml rebuilds from tag | **MISSING** | **`.github/workflows/release.yml` does not exist.** There is **no release pipeline at all**. `deploy/packaging/release-manifest.json` + `tools/release/manifest.go` validator exist (manifest schema + validation), but nothing produces or consumes a real manifest in a workflow. This is the single largest gap in the epic. |
| AC3 — CHANGELOG Keep-a-Changelog; CI fails PR w/o entry | **partial** | `CHANGELOG.md` exists in Keep-a-Changelog format with `[Unreleased]`. **Gap:** **no `_changelog-gate.yml`**, no `tools/changelog-check.sh` — the gate that "fails a PR without a changelog entry" does not exist. AC3 second clause entirely unmet. |
| AC4 — `maktaba --version` + `GET /api/system/version` consistent across 4 services | **partial** | `api/main.go:51-52 --version` → `version.String()`; `streaming/main.go:43-44` same; `pipeline __main__.py:--version` → `__version__`. `api/internal/system/version.go VersionHandler` serves `GET /api/system/version`. **Gap:** no cross-service consistency check (`tools/check-version-consistency.sh` missing); pipeline version is a hardcoded `__version__`, not lockstepped to a root `VERSION`; streaming exposes version on admin port not via the `/api/system/version` contract. Field names differ (`version.Info` plan vs `VersionInfo{version,build_sha,build_time}` actual). |
| TC1/TC2/TC3 | missing | No release workflow → tag↔artifact lineage, OCI labels, changelog gate all untestable/absent. |

**Story verdict: missing** — only the static manifest schema/validator
exists; the entire tag-driven release workflow, changelog gate, and
version-consistency tooling are absent.

---

## Story 22.6 — Upgrade and rollback

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 — canonical upgrade `git pull; compose pull; compose up -d`; independent rolling | **missing** | No `tools/upgrade.sh`. No `/admin/drain` handler in api or streaming (grep finds none). No rolling-restart mechanism. `deploy/packaging/upgrade.md` exists (doc only). |
| AC2 — rollback within one minor documented + tested | **partial (doc only)** | `deploy/packaging/upgrade.md` documents it; `release-manifest.json` has `rollback_to`/`rollback_schema_rev` fields. No `tools/rollback.sh`, no test. |
| AC3 — pre-upgrade `migrate doctor` against pg_dump temp DB + duration estimate | **MISSING** | `api/migrate.go` has **no `doctor` subcommand** (actions: up/status/version/validate only). No temp-DB simulation, no duration estimate, no JSON report. AC3 entirely unmet. |
| AC4 — long migrations (>30 s) require `--accept-long-migration` | **missing** | No such flag (see 22.4 AC2); no >30 s gating logic. |
| TC1/TC2/TC3 | missing | No upgrade/rollback/drain code → none testable. |
| EC1 two-minor jump refused | missing | No `tools/version-jump-guard.sh`. |

**Story verdict: missing** — documentation stubs only; zero runtime
upgrade/rollback/drain/doctor implementation.

---

## Story 22.7 — Multi-platform packaging

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 — macOS Homebrew tap: 3 binaries + uv venv + 3 launchd plists | **partial (skeleton)** | `deploy/packaging/homebrew/maktaba.rb` exists (hand-written, builds from source via `make build`, placeholder `sha256 0000…`, `version v0.1.0`). `deploy/packaging/launchd/com.maktaba.{api,streaming,pipeline}.plist` exist. **Gap:** not rendered from a template by a release (`tools/render-formula.sh` + `deploy/homebrew/Formulafile.tpl` missing); no tap repo automation; placeholder checksum means it cannot install. |
| AC2 — Debian/RPM: systemd unit per service + maktaba user | **partial** | `deploy/packaging/systemd/maktaba-{api,streaming,pipeline}.service` exist with hardening. **Gap:** **no `deploy/packaging/nfpm.yaml`** (plan's source of truth for deb/rpm); no `postinst.sh`/`postrm.sh` (user creation); no packaging workflow. nfpm referenced nowhere in repo. |
| AC3 — Mobile iOS/Android Capacitor, signed, version-gated | **missing** | `apps/mobile/` exists but no `build-mobile.sh`, no `compatibleApiVersion` in capacitor config, no CI mobile build. |
| AC4 — Desktop Tauri .dmg/.msi/.AppImage signed + opt-in auto-update | **partial** | `apps/desktop/src-tauri/tauri.conf.json:64-67` has an `updater` block with `endpoints`. **Gap:** no `build-desktop.sh`, no signing, no release JSON, no CI build. |
| AC5 — TV apps XCode/Gradle signed, manual publish | **missing** | `apps/tv/` exists but no build scripts/CI. |
| `.github/workflows/_packaging.yml` (plan §2.1) | **MISSING** | The matrix runner for all five package paths does not exist. |
| Deferred-from-P0: Renovate/Dependabot SHA-pin Actions | **NOT done** | Actions pinned to semver tags only (e.g. `actions/checkout@v6.0.2`); no `renovate.json`/`dependabot.yml` SHA-pin config found. |

**Story verdict: missing** — only inert packaging skeletons (formula
with zero-sha, systemd units, plists, tauri updater stanza); no nfpm,
no build scripts, no packaging CI.

---

## Story 22.8 — Local developer workflow

> P0_CERTIFICATION.md marks this **FAIL** but is **STALE**. Re-verified
> against code: substantially implemented.

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 — `make dev` full stack live-reload, save→visible ≤ 5 s | **complete (latency unverified)** | `Makefile:122-133 dev` = `$(DEV_COMPOSE) up --build -d` over base + `docker-compose.dev.yml`. `deploy/compose/docker-compose.dev.yml` swaps prod images for `Dockerfile.dev` with `air` (Go), `dev-watch.sh`/watchmedo (Python), vite (web), source bind-mounts. `api/.air.toml`, `streaming/.air.toml`, all four `Dockerfile.dev` present. **Gap:** ≤ 5 s latency not asserted by any test (TC2). |
| AC2 — `make test` no network, no sudo | **partial** | `Makefile:227 test: test-unit`; unit tier shells via `tools/test-budget.sh`. **Gap:** no `--network=none`/no-sudo enforcement test (`TestUnitTierNoNetwork`/`TestNoSudoInTests` from plan §4 absent). |
| AC3 — CONTRIBUTING.md canonical; CI runs same `make` targets | **complete** | `CONTRIBUTING.md` documents `make prereqs && make dev && make test`, `make lint`, pre-commit. `_lint.yml`/`_unit.yml`/etc. invoke `make lint`/`make test-unit`/`make test-integration`/`make test-e2e`/`make perf-ci`/`make build-go`/`make build-web` — true CI/local parity. |
| AC4 — pre-commit config checked in, covers lint quick checks | **complete** | `.pre-commit-config.yaml` exists; `.editorconfig`, `.env.example` also present. |
| TC3 lint parity | complete | Same `make lint` both sides. |
| EC3 `--no-verify` caught by CI | complete | Lint gate is the merge-gate safety net. |

**Story verdict: complete** — P0_CERT's FAIL is outdated; only missing
the latency/no-network self-tests (spec test cases, not ACs).

---

## Top gaps by impact

1. **[Ship-blocking] No release pipeline (22.5 AC2).**
   `.github/workflows/release.yml` does not exist. There is no
   tag-driven, rebuild-from-tag, sign, publish-images,
   `gh release create` flow anywhere. Combined with #2/#3 this means
   **a self-hoster cannot obtain a versioned, signed Maktaba release**
   — the epic's headline goal is undeliverable. Only the static
   `deploy/packaging/release-manifest.json` schema + `tools/release`
   validator exist (orphaned).

2. **[Ship-blocking] No artifact signing at all (22.2 AC5).**
   No `tools/sign.sh`, no cosign, no minisign, no signing pubkeys in
   `SECURITY.md`. Every "signatures published alongside artifacts"
   claim is unmet; supply-chain trust story is absent.

3. **[High] Images never built or published; size guard + repro +
   lockfile-drift checks all unwired (22.2 AC3/AC4, 22.3 AC1/AC4).**
   `_build-artifacts.yml` builds Go binaries + web bundle only — no
   `docker build`/`push`. `make image-size-guard`,
   `make lockfile-check`, `make verify-reproducibility` exist and work
   but are referenced by **zero** workflows, so their gates (22.2 TC3,
   22.3 TC3) never run in CI. No base-image digest pins anywhere
   (22.2 AC2), no `api/vendor/`/`streaming/vendor/` (22.2 AC1).

4. **[High] Entire upgrade/rollback runtime missing (22.6).**
   No `/admin/drain` (api/streaming), no `migrate doctor` subcommand,
   no `--migrate-only`/`--accept-long-migration` flags, no
   `tools/upgrade.sh`/`rollback.sh`/`version-jump-guard.sh`. Upgrades
   are documentation-only; data-safety guarantees (AC3 pre-upgrade
   simulation, AC4 long-migration ack) are entirely absent.

5. **[Medium] Packaging is inert skeletons (22.7).** Homebrew formula
   carries placeholder `sha256 0000…` (uninstallable); no `nfpm.yaml`
   / postinst (no deb/rpm); no mobile/desktop/TV build scripts; no
   `_packaging.yml`. Renovate/Dependabot SHA-pin (deferred-from-P0)
   not done.

6. **[Low] Migration `lints.json` exemption system missing (22.4 AC4).**
   `shared/db/migrations/meta/` absent; no way to exempt a
   legitimately-safe long-running DDL, so the long-running guard is
   all-or-nothing.

**Stale-audit corrections:** P0_CERTIFICATION's 22.8 FAIL is wrong
(`make dev`, pre-commit, dev overlay, `.air.toml`, `.editorconfig`,
`.env.example` all exist). The "pipeline lint dirty — T201 in
`__main__.py` stub" claim is also wrong: `__main__.py` uses
`sys.stdout.write`, not `print()`, and has no T201 violation.

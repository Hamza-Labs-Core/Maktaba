# Epic 20 — Testing: Spec vs. Implementation Gap Analysis

**Verdict:** Story 20.1 (test pyramid/budgets) is largely real and enforced; everything that 20.1 is supposed to *enable* is missing — no fixtures (20.2), no coverage gates (20.3), no real cross-service integration tests (20.4), zero e2e specs (20.5), no contract harness (20.6), a stubbed perf-ci with no nightly/quarantine (20.7), and no flake registry (20.8). Of ~36 ACs: **8 complete, 5 partial, 21 missing, 2 stub**.

Scope verified: read README, all 8 `story-*.md`, all 8 `plan-*.md`. Code verified directly (worktree copies excluded from all counts).

## Test file census (non-worktree)

| Service | Test files | Notes |
|---|---|---|
| Go (api/streaming/shared/tools) | 86 `_test.go` | Only **1** is `//go:build integration`: `api/migrate_integration_test.go` (migrations only, not cross-service) |
| Python (pipeline) | ~40 test modules, 242 marker uses | unit-heavy; markers wired |
| Web (`web/`) | **0** | `test:unit` script = `vitest run --passWithNoTests`; no `vitest.config`; no `*.unit.spec.ts` |
| e2e (`tests/e2e/`, `web/e2e/`) | **0 / dir absent** | `tests/` contains only an empty `__init__.py` |

---

## Story 20.1 — Test pyramid & runtime budgets

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 three layers in `make test` order | partial | `Makefile:217-218,226-227` declares `test:` **twice** (second overrides → `make test` runs unit only, NOT integration+e2e). `test-unit/integration/e2e` targets exist (`Makefile:229-304`). |
| AC2 runtime budgets (unit≤60s, int≤8m, e2e≤12m, PR≤20m) | partial | Budgets exist but **do not match spec**: `Makefile:23-39` `INTEGRATION_TIER_BUDGET=2m` (spec 8m), `E2E_TIER_BUDGET=5m` (spec 12m), unit is per-package 30s not 60s total. Per-test soft cap raised to `400ms` (`Makefile:36`), spec AC4 says 100ms. CI timeouts (`_unit.yml:14` 15m, `_e2e.yml:18` 25m) exceed spec tiers. |
| AC3 unambiguous tags, CI runs tiers independently | complete | Go `-short`/`//go:build integration`, pytest markers (`pyproject.toml:36-39`), CI matrix `ci.yml:66-90` fans out unit/integration/e2e/perf-ci. (TS tags unused — no web tests.) |
| AC4 per-test soft cap warn / 3× fail | complete | `shared/testtier/go/softcap.go:24-38` (warn at cap, fail at 3×); `tools/test-budget/main.go:53,77` enforces in JSON mode; Python `shared/testtier/py/maktaba_testtier/softcap.py:37-80`. Cap value diverges from spec (400ms vs 100ms) — documented tradeoff in `Makefile:24-36`. |
| TC1 unit net-socket fails | complete | `shared/testtier/go/netguard.go:14,40-55` (`ErrUnitNetGuard`); Python `netguard.py:22-52`; tests `netguard_test.go`. |
| TC2 runtime breach flagged | complete | `softcap_test.go` + `tools/test-budget/main_test.go:54-87` cover hard breach. |
| TC3 CI parallelism, integration slowest | partial | Matrix runs in parallel (`ci.yml`), but integration is **not** the slowest tier in practice (only 1 trivial integration test; e2e is empty). |
| EC1 testcontainers 60s timeout+retry → flake category | missing | No testcontainers usage anywhere; no flake category plumbing (Story 20.8 also missing). |
| EC2 darwin-only tagging, skip count | missing | No `//go:build darwin` test tags or `skipif(platform!=darwin)` for MLX/AVPlayer found. |
| EC3 tmp-dir leak sweep at exit | partial | `shared/testtier/go/tmpdir.go` helper exists; no global `TestMain` leak sweep across services as `plan §9` specifies. |

## Story 20.2 — Fixtures & seed data

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 `shared/fixtures/samples/` (ar/en/mixed/multitrack/4K + hashes/goldens) | **missing** | `shared/fixtures/` **does not exist** (`ls` → No such file or directory). No samples, no `expected/*.probe.json`, no transcript goldens. |
| AC2 ≤50 MiB committed, 4K download-on-demand + checksum | missing | No fixtures, no `scripts/fixtures-make.sh`, no `4k-hdr.manifest.json`. |
| AC3 `seeded_db` 1k videos/10k segments ≤5s | missing | No `seeded_db.sql.zst`, no `scripts/seed-db.sh`/`generate-seeded-db.go`. |
| AC4 `LICENSE` per sample | missing | No `shared/fixtures/LICENSE`. |
| TC1/2/3, EC1 no-audio, EC2 corrupt-moov, EC3 RTL filename | missing | None of the edge-case fixtures or `fixtures-check.sh` size guard exist. |

## Story 20.3 — Unit test coverage & conventions

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 per-package coverage floors (domain≥85, manifest≥90, web/src/lib≥80…) | **missing** | No `tools/coverage/floors.yaml`, no `tools/coverage/` dir at all. `web/src/lib` is uncoverable (0 web tests). No coverage collected in any CI workflow (`grep coverage .github/workflows` → none). |
| AC2 generated-code excludes in `.coveragerc`/`vitest.config.ts` | missing | No `.coveragerc`, no `coverage.go.yml`, no `vitest.config.ts`. |
| AC3 table-driven Go / parametrized pytest, single behavior | partial | Convention followed in many existing Go/py tests, but unenforced and not universal; no lint. |
| AC4 mutation testing (go-mutesting/mutmut/stryker) weekly | missing | No `tools/coverage/mutation/`, no mutation config, no weekly workflow. |
| TC1 coverage gate blocks PR | missing | No gate (`enforce.go` absent). |
| TC2 error-path lint | missing | No `error_path_lint.go` / `make lint:errpath`. |
| TC3 mutation report ≤5 auth / 0 hash | missing | No mutation pipeline. |
| EC3 `init()` ban outside `cmd/` | missing | No `lint_no_init.go` / `make lint:noinit`. |

## Story 20.4 — Integration tests with real backends

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 Go suite spins Postgres via testcontainers, reuse + per-test tx | **missing** | No `testcontainers` import; no `shared/integration/containers/postgres.go`. CI provides a Postgres *service container* (`_integration.yml:19-32`) but the only integration test (`api/migrate_integration_test.go`) just runs migrations — no container lifecycle, no per-test savepoint isolation. |
| AC2 Python suite: Postgres + ChromaDB + real FFmpeg subprocess | partial | CI starts pg+chroma services (`_integration.yml`), but `make test-integration-inner` (`Makefile:268-270`) tolerates pytest exit 5 ("no tests match") — comment at `Makefile:263-264` admits "normal state until Story 20.4 lands real integration tests." No `pipeline/tests/integration/` ChromaDB/FFmpeg suite. |
| AC3 gRPC contract test vs real Pipeline server / bufconn | missing | No `api/tests/integration/transcribe_e2e_test.go`, no bufconn plumbing, no `shared/proto` (proto absent entirely). |
| AC4 no gomock/unittest.mock for our services; replay tapes for SaaS | missing | No `shared/integration/replay/` tape harness; policy unenforced. |
| TC1 spin-up ≤30s | partial | Service containers healthcheck-gated (`_integration.yml:71-85`) but no measured budget assertion. |
| TC2 cross-service enqueue→claim→WS | missing | Not implemented. |
| EC1 pg-embed fallback / EC3 FFmpeg version check | missing | No `pgembed_fallback.go`, no `ffmpeg_check.go`. |

## Story 20.5 — End-to-end smoke flows

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 five Playwright golden flows green | **missing** | No `web/e2e/`, no `playwright.config.ts`, no `flows/*.e2e.spec.ts`. `tests/e2e/` does not exist. `make test-e2e` (`Makefile:296-304`) only runs `pytest -m e2e` and tolerates exit 5 (no tests). |
| AC2 `make e2e` dockerized one command | partial | `make test-e2e` exists but runs nothing; `_e2e.yml:36` brings up `docker-compose.yml` (not the spec'd `deploy/compose/test.yml`, which is absent). |
| AC3 HTML report + video trace on failure, CI artifacts | partial | CI uploads only `compose.log` (`_e2e.yml:45-51`); no Playwright HTML/trace because no Playwright. |
| AC4 no external network beyond compose | n/a | No e2e suite to evaluate. |
| TC1-3, EC1 HLS, EC2 Maestro mobile | missing | None implemented; `apps/mobile/maestro/` absent. |

## Story 20.6 — Contract tests for service boundaries

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 `schema.graphql` + `*.proto` single source, drift fails CI | **missing** | No `shared/graphql/schema.graphql`, **no `.proto` files anywhere**, no `buf.yaml`. No `git diff --exit-code` / `buf generate` step in any workflow (`grep buf/gqlgen/drift .github/workflows` → none). |
| AC2 OpenAPI contract, test every operationId | partial | `shared/api/openapi.yaml` exists (4056 lines, OpenAPI 3.1.0) but is **hand-maintained**, not chi-reflection-extracted (`shared/openapi/extract.go` absent). No `operationid_test.go`; no CI diff gate. Matches FULL_IMPLEMENTATION_AUDIT claim "no OpenAPI CI gate." |
| AC3 typed WS events (TS/Go/pydantic), bad payload fails parser | missing | No `shared/ws/events.ts`/`events_gen.go`/`events_gen.py`; no `ParseEvent`/`WSEvent` types found in code. |
| AC4 backwards-compat lint, deprecation window | missing | No `buf breaking`, no `breaking_lint.go`, no `shared/contract/policy.yaml`. |
| TC1-3, EC1-3 | missing | No contract harness exists. |

## Story 20.7 — Performance regression tests in CI

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 `make perf-ci` reduced suite ≤5m on tagged runner | **stub** | `make perf-ci` → `perf-ci-inner` literally `echo "perf-ci stub: Epic 20.7 will replace this…"` (`Makefile:316-317`). Runs on `ubuntu-22.04` not a tagged perf runner (`_perf-ci.yml:17`). `shared/perf_budgets.yaml` exists (Epic 18) but `ci_pr` filtering (`tests/perf/ci_subset.go`) absent. |
| AC2 nightly full suite → time-series store | missing | No `perf-nightly.yml`, no `perf-history` branch tooling, no `tests/perf/publisher.go`. |
| AC3 PR comment with deltas, >10% p95 blocks merge | missing | No `delta_report.go`, no `post-pr-comment.sh`, no gate. |
| AC4 flap 3×/5d → quarantine + auto-issue | missing | No `quarantine.go`, no `perf-quarantine.yml`. |
| TC1-3, EC1-3 | missing | Entire perf-CI lane is a stub. |

## Story 20.8 — Flaky test policy

| AC | Status | Evidence / Gap |
|---|---|---|
| AC1 flake registry with failure log | **missing** | No `tools/flakes/` dir, no `registry.json`, no `recorder.go`. |
| AC2 ≥3 flakes/7d → auto-skip + P2 issue | missing | No `auto_quarantine.go`, no `quarantined.txt`, no `SkipIfQuarantined` helper. |
| AC3 14-day SLA pages owner | missing | No `sla_check.go`, no `flake-sla.yml`. |
| AC4 retry only at e2e tier; unit/int fail first | partial | Spirit holds: `make test-unit-go` uses `-count=1` (`test-budget.sh:56`), no rerun configured for unit/integration; but **no e2e retry** because no e2e/Playwright config exists. Not enforced as policy. |
| EC2 ban time-zone tests / EC3 randomized order | missing | No `ban_tz_test.go`; no `-shuffle=on` / `pytest-randomly` in runners. |

---

## Top gaps by impact

1. **Story 20.2 fixtures absent entirely** — `shared/fixtures/` does not exist. This is the linchpin dependency for 20.4 (integration), 20.5 (e2e) and many per-epic feature tests. Without committed Arabic/English/multitrack samples, probe/transcript goldens, and `seeded_db`, no integration or e2e flow in the spec can be authored. Highest blast radius.

2. **Story 20.5 e2e is 100% absent** — no Playwright, no `web/e2e/`, no `deploy/compose/test.yml`. `make test-e2e` deliberately no-ops (tolerates pytest exit 5). The CI `e2e` gate is green while testing nothing — a false-confidence merge gate. AC1's five golden flows (first-run, drop-video, Arabic search jump, pause/resume, RTL visual diff) have zero coverage.

3. **Story 20.6 contract harness uninstantiable** — no `.proto` files, no GraphQL schema file, no WS typed-event codegen, and the OpenAPI doc is hand-written with no CI drift gate. AC1's "single source of truth, CI fails on drift" cannot fire; AC2 operationId test impossible.

4. **Story 20.3 zero coverage enforcement + 0 web tests** — no `tools/coverage/`, no floors gate in CI, no mutation testing. `web/` has no test runner config and `test:unit` is `--passWithNoTests`, so the `web/src/lib ≥ 80%` floor is structurally unmeetable.

5. **Story 20.4 "real backends" unused** — CI provisions Postgres+Chroma service containers, but the only `//go:build integration` test runs DB migrations; pytest integration tolerates "no tests." The expensive integration gate exercises almost nothing; no testcontainers, no bufconn cross-service flow, no replay tapes.

6. **Budget/policy divergence from spec** — `make test` is redefined twice (`Makefile:217` then `:226`), so `make test` silently runs unit-only. Tier budgets (int 2m vs spec 8m; e2e 5m vs 12m) and per-test soft cap (400ms vs 100ms) diverge from AC2/AC4; the 400ms divergence is documented, the duplicate-target bug is not.

**Single worst gap:** Story 20.5 — the e2e tier (5 golden flows, the only top-of-pyramid safety net and a branch-protection merge gate) has zero implementation: no Playwright, no `web/e2e/`, no `deploy/compose/test.yml`; `make test-e2e` deliberately no-ops by tolerating pytest's "no tests" exit code, so the CI `e2e` gate reports green while asserting nothing.

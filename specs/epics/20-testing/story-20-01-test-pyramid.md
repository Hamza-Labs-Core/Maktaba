# Story 20.1 — Test pyramid and runtime budgets

Codify the layers and what each layer is and is not allowed to do.

## Acceptance criteria

- AC1. Three layers, in `make test` execution order:
  1. **Unit** — pure, no I/O, no DB, no FFmpeg, no network. Per-package.
  2. **Integration** — real Postgres (testcontainers / `postgres`
     binary), real FFmpeg, real ChromaDB, fixture media. Per-service.
  3. **End-to-end** — full compose stack via Playwright + a headless
     browser; a handful of golden flows.
- AC2. Runtime budgets:
  - Unit: total ≤ 60 s across all services.
  - Integration: total ≤ 8 minutes.
  - E2E: total ≤ 12 minutes.
  - PR CI green-to-merge ≤ 20 minutes wall-clock.
- AC3. Each test is unambiguously tagged (`//go:build unit` /
  `pytest.mark.unit` / `test.unit.spec.ts`); CI runs each tier
  independently.
- AC4. A new test that exceeds its tier's per-test soft cap (unit 100
  ms, integration 5 s, e2e 30 s) emits a warning; > 3× the soft cap
  fails the build.

## Test cases

- TC1. Tier compliance: a unit test that opens a network socket fails
  with a clear "unit tests must not do I/O" assertion.
- TC2. Runtime breach: artificially `sleep(2 * unit_soft_cap_ms)` in a
  unit test; build flags it.
- TC3. CI parallelism: three matrix jobs (unit, integration, e2e) run
  in parallel and the slowest is integration.

## Edge cases

- EC1. `testcontainers` slow-start on a CI runner — the integration
  tier has a 60 s container-up timeout with a retry; further failure is
  surfaced as a flake category, not a test failure.
- EC2. macOS-only paths (MLX, AVPlayer) — tagged `darwin-only` and
  skipped on Linux CI with a recorded skip reason.
- EC3. Ephemeral `/tmp` cleanup — every integration test owns a
  per-test temp dir under `t.TempDir()` (Go) / `tmp_path` (pytest),
  asserted empty at process exit.

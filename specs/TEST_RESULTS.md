# Test Results — main @ 0686b76 (2026-05-10)

Full test suite executed across every service after pulling latest `main`. Results captured below as a snapshot for the current state of the tree.

## Summary table

| Suite | Result | Pass | Fail | Notes |
|---|---|---|---|---|
| `api` Go tests | PASS | 40 pkgs | 0 | 6 packages have no test files (informational) |
| `streaming` Go tests | PASS | 13 pkgs | 0 | 1 package without tests (`internal/config`) |
| `cloud` Go tests | PASS | 12 pkgs | 0 | 16 packages without tests (handlers, stores, push, etc.) |
| `shared/log/go` | PASS | 1 pkg | 0 | |
| `shared/health/go` | PASS | 1 pkg | 0 | |
| `shared/metrics/go` | PASS | 1 pkg | 0 | |
| `shared/tracing/go` | PASS | 1 pkg | 0 | |
| `shared/testtier/go` | PASS | 1 pkg | 0 | |
| `shared/states/go` | PASS | 1 pkg | 0 | |
| `tools/migration-lint` Go tests | PASS | 1 pkg | 0 | |
| `pipeline` pytest | **FAIL** | 810 | **2** | `pyyaml` missing in dev extras |
| `pipeline` ruff | **FAIL** | — | **20** | mostly UP017 (datetime.UTC), UP045 (X \| None), I001 (import order) |
| `pipeline` mypy --strict | **FAIL** | — | **6** | 4 files: `idempotency.py`, `concurrency.py`, `probe.py`, `budgets.py` |
| `web` typecheck (`tsc --noEmit`) | PASS | — | 0 | clean |
| `migration-lint` (against `shared/db/migrations`) | **FAIL** | — | **11** | unguarded DDL + Postgres `CREATE INDEX` not `CONCURRENTLY` |

## Details

### Go services — all green

**`api/` — 40 packages OK** (1.6s–4.8s each). 6 packages report `[no test files]`: `internal/auth/principal`, `internal/grpcclients/streaming`, `internal/handlers/devices`, `internal/handlers/recommendations`, `internal/handlers/speakers`, `internal/idempotency`, `internal/reqid`.

**`streaming/` — 13 packages OK** (0.4s–2.0s each). `internal/config` has no test files.

**`cloud/` — 12 packages OK** (0.2s–1.8s each). Packages without tests: `cmd/maktaba-cloud`, `internal/abuse`, `internal/auth/middleware`, `internal/auth/oauth`, `internal/auth/sessions`, `internal/clock`, `internal/db`, `internal/handlers/account`, `internal/handlers/admin`, `internal/handlers/auth`, `internal/handlers/billing`, `internal/handlers/health`, `internal/handlers/push`, `internal/push`, `internal/server`, `internal/stores`, `migrations`. (Cloud is the newest service — Epic 25 — and many of the handlers ship without unit coverage yet.)

**Shared modules — 6 modules OK.** Every Go module under `shared/*/go` passes its own `go test ./...`:
- `shared/log/go` — 0.219s
- `shared/health/go` — 0.432s
- `shared/metrics/go` — 0.247s
- `shared/tracing/go` — 0.213s
- `shared/testtier/go` — 0.222s
- `shared/states/go` — 0.199s

**`tools/migration-lint` — package OK** (0.197s).

### `pipeline` pytest — 2 failures / 812 tests

```
tests/perf/test_perf.py::test_load_real_budgets_file FAILED
tests/perf/test_perf.py::test_load_rejects_bad_p99 FAILED
```

Both failures share the same root cause: `RuntimeError: pyyaml is not installed`, raised from `src/maktaba_pipeline/perf/budgets.py:54`. The module imports `yaml` defensively, but neither `pyproject.toml` `[project]` nor the `dev` extras declare `pyyaml`, so a fresh `uv sync --extra dev` does not install it. Either:
- add `pyyaml` to `dev` extras (since the perf budget tests need it), or
- skip these tests when `yaml is None`, matching the runtime behaviour.

Other 810 tests pass in ~9s.

### `pipeline` ruff — 20 errors

Distribution by rule:

| Rule | Count | Description |
|---|---|---|
| UP017 | 6 | `datetime.timezone.utc` → `datetime.UTC` |
| UP045 | 4 | `Optional[X]` → `X \| None` |
| I001 | 4 | Unsorted import blocks |
| UP037 | 2 | Quoted annotations no longer needed |
| SIM105 | 2 | `try/except/pass` → `contextlib.suppress` |
| UP035 | 1 | Deprecated import |
| UP012 | 1 | Other pyupgrade |

18 of 20 are `--fix` auto-fixable.

### `pipeline` mypy --strict — 6 errors in 4 files

```
src/maktaba_pipeline/perf/budgets.py:16        unused "type: ignore"            [unused-ignore]
src/maktaba_pipeline/integrity/idempotency.py:58  builtins.callable not valid as type [valid-type]
src/maktaba_pipeline/integrity/idempotency.py:65  callable? not callable          [misc]
src/maktaba_pipeline/integrity/idempotency.py:73  callable? not callable          [misc]
src/maktaba_pipeline/perf/concurrency.py:39     missing return-type annotation   [no-untyped-def]
src/maktaba_pipeline/discovery/probe.py:129     returning Any from str | None    [no-any-return]
```

Three of the six trace back to the same `idempotency.py` site using bare `callable` instead of `typing.Callable`; the rest are isolated annotation gaps.

### `web` typecheck — clean

`pnpm typecheck` (`tsc --noEmit`) completes with no output and exit 0.

### `migration-lint` — 11 violations on `shared/db/migrations`

Tool's own unit tests pass; running it against the live migration tree surfaces:

| File | Violation |
|---|---|
| `0003_videos_content_hash.sqlite.sql` | unguarded `DROP TABLE videos` |
| `0015_subtitle_files.sql` | unguarded `CREATE TRIGGER subtitle_files_notify_trg` |
| `0045_media_features.sqlite.sql` | unguarded `ADD COLUMN content_type` |
| `0048_speakers_voiceprint.sqlite.sql` | 3 × unguarded `ADD COLUMN` (library_id, voiceprint, unknown_index) |
| `0049_chapter_infer_stage.sqlite.sql` | unguarded `DROP TABLE processing_jobs` |
| `0050_transcript_units.sql` | 3 × `CREATE INDEX` not `CONCURRENTLY` (video_idx, segment_idx, time_idx) |
| `0051_transcript_segments_view.sqlite.sql` | unguarded `CREATE VIEW transcript_segments_v` |

The Postgres `CREATE INDEX` violations on `0050` are the most material — those will take ACCESS EXCLUSIVE in production. The SQLite-only files are convention-only fixes.

## Verdict

**Go: green across every module.** All 73 Go test packages pass cleanly (40 api + 13 streaming + 12 cloud + 6 shared + 1 migration-lint + extras). The newest service (`cloud`, Epic 25) ships with thinner handler coverage than the older services but the tests it does have all pass.

**Web: green.** Type checking is clean.

**Python pipeline: red on three checks.**
- 2/812 pytest failures, both triggered by missing `pyyaml` dev dependency — a packaging bug, not a logic bug.
- 20 ruff errors, almost all auto-fixable formatting/upgrade nits (`datetime.UTC` migrations, import sorting).
- 6 mypy strict errors, the meaningful one being `callable` used as a type in `integrity/idempotency.py`.

**Migrations: red.** `migration-lint` flags 11 violations in `shared/db/migrations/`. Three of them — the non-`CONCURRENTLY` Postgres indexes on `0050_transcript_units.sql` — are correctness issues for production deploys; the rest are SQLite-side idempotency hygiene.

**Net:** Go and web are shippable as-is. Python pipeline needs a quick housekeeping pass (add `pyyaml`, run `ruff check --fix`, fix the `idempotency.py` `callable` typing). Migration `0050` should be revised before any prod migration window.

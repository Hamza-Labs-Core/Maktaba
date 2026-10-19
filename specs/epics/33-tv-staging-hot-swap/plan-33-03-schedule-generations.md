# Plan 33.3 — Schedule generations — implementation

> Implementation plan for [story-33-03](story-33-03-schedule-generations.md).
> Design: [design.md](design.md) §4. Migration: slot 0124 (NO TRANSACTION, concurrent indexes).

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | A generation column, not versioned tables. | One table, same indexes and readers; a view keeps reader changes small. |
| D2 | Copy-to-boundary instead of sharing rows across generations. | ≈ 20 channels × 7 days is a few thousand rows; copy keeps readers simple (one `=` filter). |
| D3 | Divergence at the end of the block on air at the planned swap time. | Keeps `pass_.py`'s rule "never rewrite the block on air" (D3 there) across generations. |
| D4 | `channels` keeps the live lineup; staged adds use `lifecycle = staged`. | The FK from `channel_programs` needs a row; live readers stay on one table. |
| D5 | Per-generation schedule state. | Shuffle bags and marathon cursors diverge after `B_c`. |

## 1. Files

**Create**
- `shared/db/migrations/0124_tv_sched_generations.sql` (+ sqlite)
- `pipeline/src/maktaba_pipeline/tvrelease/generation.py` — `build_generation`, `reanchor`
- `pipeline/src/maktaba_pipeline/tvrelease/gc.py`
- tests

**Modify**
- `pipeline/src/maktaba_pipeline/channels/repo.py` — every query takes `gen`; `replace_future_blocks(…, gen)`; `copy_blocks(channel, from_gen, to_gen, before=None)`
- `pipeline/src/maktaba_pipeline/channels/pass_.py` — `run_schedule(…, gen=…, anchor=None)`; `topup_once` iterates open generations; advisory key includes gen
- `pipeline/src/maktaba_pipeline/channels/base.py`, `__init__.py`
- `streaming/internal/channel/repo_pg.go`, `streaming/internal/hdhr/repo_pg.go`, `hdhr/guide.go` — view / generation
- `api/internal/handlers/guide/{repo,guide,snapshot}.go`, `api/internal/handlers/channels/{repo,schedule,builder,dto}.go` — view; `?gen=staging` (admin)
- `api/internal/handlers/queues/history.go`, `pipeline/.../tools/queue_transcription.py` — view
- lineup readers — `lifecycle = 'live'`

## 2. Steps

1. Migration: column with default (metadata-only on PG ≥ 11), new indexes concurrently, drop old,
   state PK swap (new table + copy + rename inside a short transaction), lifecycle column and partial
   indexes, function and view.
2. Repo and pass changes with `gen` defaulting to `tv_live_gen()` so behaviour with staging off is
   identical.
3. Generation builder + re-anchor (uses `airing_until` per channel at T).
4. Top-up across open generations.
5. Readers to the view; admin `?gen=staging`.
6. GC job (catalog stage `tv_gc`, daily, visible run per the control-centre rule).

## 3. Tests

Python: generation builder/reanchor/top-up/GC with the in-memory and SQLite repos; Postgres job for
the migration (online, lock timing). Go: reader filters.

## 4. Rollout

Phase A. Only generation 0 exists until 33.10 stages the first change.

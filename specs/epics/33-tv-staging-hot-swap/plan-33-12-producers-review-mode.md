# Plan 33.12 — Producers in review mode — implementation

> Implementation plan for [story-33-12](story-33-12-producers-review-mode.md). No migration.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Keep `apply_or_review`; swap the `apply` callable. | The engine's compute/apply split was built for this ("a review/Apply flow with notifications is a Maktaba feature to build"). |
| D2 | The Changes page's Approve is the queue's Apply for producer changes. | One approval, one audit trail; the queue keeps its digest semantics. |
| D3 | Editor and settings saves go to a draft. | Every path to the air goes through staging; nothing bypasses it. |
| D4 | Suppress the `write_channels -> plex_livetv` edge when staging is on. | Plex must only see the live lineup. |

## 1. Files

**Modify**
- `pipeline/src/maktaba_pipeline/engine/stages/write_channels.py` — `run`: when `tv_staging.enabled`, `apply = stage_tv_change` (from `tvrelease`); `_verify` on the snapshot
- `pipeline/src/maktaba_pipeline/engine/changes.py` — optional `on_review(ctx, changes, digest)` hook so the stage can create the draft TV change while parking
- `pipeline/src/maktaba_pipeline/engine/graph.py` — edge condition for `plex_livetv`
- `pipeline/src/maktaba_pipeline/channels/pass_.py` — stale handling with staging on
- `api/internal/handlers/channels/*.go` — writes through `tvchange.Draft()` when staging on; reads draft overlay
- filler pools / art override handlers — same
- `api/internal/handlers/rules/` — `tv`/`tv_output` saves to draft when staging on
- `api/internal/handlers/queues/` — approve path callable from `tvchange` (post the digest)

**Create**
- `pipeline/src/maktaba_pipeline/tvrelease/stage.py` — `stage_tv_change(db, changes, source)`
- `api/internal/tvchange/draft.go` — open-draft overlay (pending snapshot) and append
- tests

## 2. Steps

1. `stage_tv_change` + write_channels wiring (auto mode).
2. Review hook + Changes-page approve → queue apply.
3. Editor/filler/art/settings drafts.
4. Stale + plex edge.
5. Regression suite with staging off.

## 3. Tests

Pipeline stage tests (SQLite + Postgres jobs), Go handler tests, regression run.

## 4. Rollout

Phase C, then `tv_staging.enabled` on the home.

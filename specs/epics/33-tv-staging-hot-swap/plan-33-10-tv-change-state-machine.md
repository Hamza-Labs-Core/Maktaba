# Plan 33.10 — TV change objects and state machine — implementation

> Implementation plan for [story-33-10](story-33-10-tv-change-state-machine.md).
> Design: [design.md](design.md) §11. Migration: slot 0127 (`tv_changes`, `tv_change_events`).

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | The controller lives in the API (Go). | Admin actions, auth and audit are there; the front door stays small. |
| D2 | Release building stays in the pipeline. | Python owns the plan → channel logic; the controller enqueues `tv_stage` jobs (visible runs). |
| D3 | One change in flight. | Two staged releases cannot both be "next"; keeps slots at two. |
| D4 | Rules per kind, strictest wins. | A lineup change riding with a schedule change still needs approval. |
| D5 | Transitions in one table-driven function. | Every path tested; `allowed_actions` derived from it. |

## 1. Files

**Create**
- `api/internal/tvchange/` — `store.go`, `machine.go` (table), `controller.go` (loop: verification, ready re-checks, auto-promote, schedule), `diff.go`, `rules.go`
- `api/internal/handlers/tvchange/handlers.go` — endpoints
- `pipeline/src/maktaba_pipeline/engine/stages/tv_stage.py` — stage `tv_stage` (catalog subject): build release + generation for a change id (visible in the control centre)
- `api/internal/router/p33.go`
- tests

**Modify**
- `pipeline/src/maktaba_pipeline/engine/graph.py` — `tv_stage`, `tv_gc` stage defs
- `api/internal/handlers/rules/schema.json` — `tv_staging` section (README settings)

## 2. Steps

1. Store + machine + events + audit.
2. Diff (release vs release; schedule diff from generations).
3. Stage via `tv_stage` job; staged → verifying hand-off to 33.5.
4. Rules + quiet window + scheduling.
5. Handlers + `allowed_actions`.
6. Resume on restart under the advisory lock.

## 3. Tests

Table-driven machine tests; rules tests with a fake clock; Postgres job for store/resume.

## 4. Rollout

Phase B (admin-created changes), Phase C (producers).

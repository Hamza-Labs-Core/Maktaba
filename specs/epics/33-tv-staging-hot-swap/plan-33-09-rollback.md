# Plan 33.9 — Rollback — implementation

> Implementation plan for [story-33-09](story-33-09-rollback.md).
> Design: [design.md](design.md) §10. No migration.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Warm rollback is a swap in reverse through the same code. | One path, tested twice; continuity identical. |
| D2 | Warm rollback switches immediately, not at boundaries. | A rollback is urgent; content before `B_c` is identical anyway. |
| D3 | Cold rollback is a normal change with a fresh generation. | The retained generation's tail is in the past; the timeline must continue from now. |
| D4 | Automatic rollback only inside the grace. | Afterwards the new release is the known-good state; a crash is a restart matter. |

## 1. Files

**Create**
- `api/internal/tvchange/rollback.go` — warm (commit reverse + front door `/internal/tv/routing/rollback`), cold (create change), auto triggers (subscribe to slot state and post-swap health)
- tests

**Modify**
- `streaming/internal/frontdoor/swap.go` — `rollback` mode: all channels `switch_at = now`, warm-first
- `cmd/maktaba-server/internal/supervisor/` — report crash/crash-loop of a slot to the API (event + `tv_slots.last_exit`)
- `pipeline/src/maktaba_pipeline/tvrelease/generation.py` — `clone_generation(from_gen, anchor)` for cold rollback

## 2. Steps

1. Reverse commit (lineup restore from the previous release snapshot).
2. Front door rollback mode.
3. Auto triggers: slot crash events (watchdog), crash-loop counter, post-swap health monitor (first
   `keep_warm_minutes`).
4. Cold rollback change creation.
5. API + Changes page button state (33.11).

## 3. Tests

As in the story; reuse 33.7's integration harness and 33.8's fake-ingest E2E.

## 4. Rollout

Phase B with 33.7.

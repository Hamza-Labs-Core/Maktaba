# Plan 35.6 — TV during updates — implementation

> Implementation plan for [story-35-06-tv-during-updates](story-35-06-tv-during-updates.md). Design: [design.md](design.md) §7.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | The override lives in the controller's rule resolution (`rules.go`), keyed on `source.kind = auto_update` and `kinds == ["binary"]`. | One place decides auto-promotion; the setting `auto_promote_binary` keeps meaning for every other source. |
| D2 | The orchestrator talks to the controller through the API (loopback admin route), not the database. | The controller owns transitions and events; the supervisor must not duplicate the state machine. |
| D3 | The fallback is chosen at run time from `GET /api/tv/slots`. | The same release works before and after Epic 33.1 lands. |
| D4 | `edge_protocol` is a Go constant in `streaming/internal/frontdoor` exported by `maktaba-streaming --version --json`. | The orchestrator can compare without starting the binary as a server. |
| D5 | Mixed-kind changes are never auto-promoted. | The override is for unattended binary upgrades, not a way to sneak a lineup change through. |

## 1. Files

**Create**
- `cmd/maktaba-server/internal/updater/step_tv.go` — step 10: create change, wait, fallback, timeouts.
- `cmd/maktaba-server/internal/updater/step_tv_test.go`
- `api/internal/router/p35_tv.go` — loopback-only `POST /internal/updates/tv-change` (create the
  binary change for a run) and `GET /internal/updates/tv-change/{id}` (state).

**Modify**
- `api/internal/tvchange/rules.go` — `effectiveRule(change)`: `immediate` for auto_update binary-only.
- `api/internal/tvchange/controller.go` — accept `source.kind = auto_update`; record the run id in
  `source`; emit `tv_change_*` events as usual.
- `api/internal/tvchange/draft.go` — build the release from the live one plus `streaming` from
  `bin/releases/<v>/.verified` (path resolved by version, never from the request).
- `streaming/internal/frontdoor` — `EdgeProtocol` constant; `--version --json` prints it.
- `cmd/maktaba-server/internal/supervisor/supervisor.go` — keep a role on its own release dir when
  it was not restarted (front door with equal `edge_protocol`).
- `cmd/maktaba-server/internal/updater/prune.go` — never prune a version referenced by a running
  process or by `tv_slots.release_id`.
- `specs/epics/33-tv-staging-hot-swap/README.md`, `story-33-14-upgrade-via-swap.md` — override note.

## 2. Steps

1. Controller rule + tests (TC1, TC2).
2. Loopback routes; orchestrator step with a fake controller (TC3, TC4).
3. Viewer count query (sessions in `channel_runtime` excluding probe holds) and the fallback loop (TC5).
4. `EdgeProtocol` and the restart gate in step 11; pruning guard (TC6).
5. Rollback hook: `POST /api/tv/rollback` with the run's change (Epic 33.9; 501 before it exists →
   fallback restarts streaming on `from`).
6. Nightly E2E with the 33.14 harness (TC7).

## 3. Tests

Story TCs; plus a controller fuzz over (source kind × kinds × rule settings) asserting that only the
auto_update binary-only case ever resolves to `immediate` while all rules are `approval`.

## 4. Rollout

Ships with phase B. Until Epic 33.1 the fallback path is the one that runs; the seamless path turns
on by itself when `GET /api/tv/slots` reports a staging-capable runtime.

# Plan 33.14 — Upgrade via swap — implementation

> Implementation plan for [story-33-14](story-33-14-upgrade-via-swap.md). No migration.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Side-by-side install per version. | Two slot versions must coexist during staging. |
| D2 | TV first, other roles after. | Viewer-facing work is protected by the swap; the rest tolerates restarts. |
| D3 | `edge_protocol` constant gates front door restarts. | The front door is the one process whose restart is visible; restart only when needed. |
| D4 | Expand/contract migrations required. | The old slot runs against the new schema during staging. |

## 1. Files

**Modify**
- `cmd/maktaba-server/internal/selfupdate/` — install into `bin/releases/<version>/`; `current` pointer; GC of old versions
- `cmd/maktaba-server/cmd_update.go`, `api/internal/handlers/admin/system_update.go` (Epic 28.3) — staging branch: create the binary change via the API controller, wait, then rolling restart
- `cmd/maktaba-server/internal/supervisor/` — rolling restart of named roles; re-adopt children by pid file on Unix
- `streaming/internal/version/edge.go` — `EdgeProtocol` constant
- `shared/db/migrations/README.md` — expand/contract rule for TV tables

## 2. Steps

1. Side-by-side install and pointer.
2. Binary change creation and wait (poll the change).
3. Rolling restart order; front door gate.
4. Abort path.
5. Node per-host changes.

## 3. Tests

As in the story; the E2E runs on a Linux VM in the release pipeline.

## 4. Rollout

Phase D.

# Plan 35.4 — Home update orchestrator — implementation

> Implementation plan for [story-35-04-home-orchestrator](story-35-04-home-orchestrator.md). Design: [design.md](design.md) §5.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Orchestrator in the supervisor process. | Survives every role restart; the launcher covers its own restart. |
| D2 | Journal file is authoritative, database rows mirror it. | The database may be the thing failing; the journal is local and atomic. |
| D3 | Each step is an interface `Step{Name; Run(ctx, *Journal) error; Resume(ctx, *Journal) error}`. | The resume table is testable step by step. |
| D4 | Roles restart one at a time with health gates, supervisor last. | A broken role is caught before anything else moves. |
| D5 | Rollback touches binaries only. | Migrations are additive (35.5); the previous release runs on the new schema. |
| D6 | Steps 9+ cannot be aborted. | After the schema moves, the only safe ends are commit or binary rollback. |

## 1. Files

**Create**
- `cmd/maktaba-server/internal/updater/run.go` — state machine, step list, failure mapping
- `cmd/maktaba-server/internal/updater/journal.go` — atomic journal read/write
- `cmd/maktaba-server/internal/updater/steps_*.go` — preflight, download, verify, stage, smoke, drain,
  migrate, restart_roles, watch, commit (backup/rehearse in 35.5, tv in 35.6, supervisor in 35.7,
  rollout in 35.12)
- `cmd/maktaba-server/internal/updater/health.go` — role health gate (probe, `/readyz`, version, agent)
- `cmd/maktaba-server/internal/updater/rollback.go`
- `cmd/maktaba-server/internal/updater/store.go` — `update_runs` / `update_run_steps` writes
- `cmd/maktaba-server/internal/pyenv/pyenv.go` — venv build (moved from the node installers)
- `cmd/maktaba-server/cmd_pyenv.go` — `maktaba-server pyenv build` for the installers
- `api/selfcheck_cli.go` (beside `migrate.go`, `keys_cli.go`) — `maktaba-api selfcheck [--deep]`

**Modify**
- `cmd/maktaba-server/internal/supervisor/supervisor.go` — per-role restart onto another release dir;
  expose `RestartRole(role, dir)`; `/update/*` handlers on the admin mux (local token)
- `cmd/maktaba-server/cmd_serve.go` — construct the updater; resume a journal at start
- `cmd/maktaba-server/internal/supervisor/eventlog.go` — `update_*` events
- `deploy/packaging/node/install-node.sh`, `windows/install-node.ps1` — call `maktaba-server pyenv build`
  when the server binary is present (shared logic; shell path stays as fallback)
- `api/internal/router/processes.go` — `/api/system/processes` shows the run state

## 2. Steps

1. Journal + store + state machine with no-op steps; resume table tests.
2. preflight, download (range resume), verify, stage (pyenv), smoke (`selfcheck`).
3. drain (agent `cmd drain` through the hub API), migrate (one-shot child).
4. `RestartRole` and health gate; restart_roles; watch; commit; prune.
5. Rollback path and refusal rule.
6. Wire into serve; loopback endpoints; API client (35.3) calls them.

## 3. Tests

Story TCs; `go test -race` on the state machine; a Postgres integration job in `_integration.yml`.

## 4. Rollout

Phase B. Until 35.6 and 35.7 land, the `tv` step uses the fallback restart and `restart_supervisor` is a
plain exit 75 restart (TV interruption allowed only when viewers ≤ `quiet_max_viewers`).

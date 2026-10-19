# Plan 35.8 — Full nodes — implementation

> Implementation plan for [story-35-08-full-nodes](story-35-08-full-nodes.md). Design: [design.md](design.md) §9.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Node mode is derived from `node.json`, as `ChildEnv` already does for streaming. | One source of truth; no flag to forget. |
| D2 | The API checks its own role privileges at start. | A copied superuser URL must never let a node run DDL. |
| D3 | One Go machine agent per node, in the supervisor; the API uses it over loopback. | One channel per machine for Go; survives API restarts. |
| D4 | Go schema guard mirrors the streaming one with shared fixtures. | Same verdicts and texts in Python, streaming and API. |
| D5 | Worker-only nodes are converted automatically on their next update (`update.convert`, verified with sigstore-python). | Owner decision (2026-10-19): no machine is left behind on the old install. |
| D6 | Full nodes always run the streaming role for VOD, reading the NAS directly. | Owner decision: media never goes through the home. |

## 1. Files

**Create**
- `cmd/maktaba-server/internal/machineagent/` — `agent.go` (dial, v2 handshake, reconnect),
  `frames.go` (`keys`, `auth.revoked`, `update.*`), `local.go` (loopback endpoints + token).
- `api/internal/cluster/schemaguard/guard.go` (+ `_test.go`).
- `api/internal/nodemode/nodemode.go` — `Enabled()`, privilege check, component skip list.
- `cmd/maktaba-server/cmd_setup_node.go` — `setup --node`.

**Modify**
- `cmd/maktaba-server/internal/supervisor/env.go` — node-mode env for `api`
  (`MAKTABA_NODE_MODE`, node `DATABASE_URL`, no `MAKTABA_AUTO_MIGRATE`, `MAKTABA_HOME_API_URL`).
- `cmd/maktaba-server/internal/config/nodeconfig.go` — expose `home.api_url`, `cluster_pub`, key.
- `cmd/maktaba-server/cmd_serve.go` — role set in node mode; start the machine agent.
- `api/main.go` — skip home components in node mode; refuse migrations.
- `api/internal/cluster/pgstore.go` — `cluster_node_report` carries web URL and `full_node`.
- `shared/db/migrations/<S3>_cluster_node_versions.sql` (+ `.sqlite.sql`) — `cluster_nodes.version`,
  `platform`, `update_state`, `full_node` (slot S3, next free at implementation time).
- `deploy/packaging/node/install-node.sh`, `windows/install-node.ps1` — `--full` / `-Full` call
  `maktaba-server setup --node`; README.
- `web/src/pages/admin/Machines*` — web URL link, worker-only badge.

## 2. Steps

1. `nodemode` + env + role set (TC1, TC3).
2. Privilege check and migrate refusal (TC2).
3. Schema guard in Go with shared fixtures (TC4).
4. Machine agent: handshake vectors first (TC5), then frames, reconnect, loopback token.
5. Migration S3 and report fields; Machines page link.
6. `setup --node` and installer `--full` (TC6, TC7), removing old units last.

## 3. Tests

Story TCs; an integration test with a home and one node on one CI host (two data dirs, ports offset)
that checks the node's `/api/system/version` and a library read through `any`.

## 4. Rollout

Phase C. Conversion happens on each worker-only node's next update; the Updates page lists the ones that need an installer run.

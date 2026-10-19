# Plan 35.12 — Cluster rollout — implementation

> Implementation plan for [story-35-12-cluster-rollout](story-35-12-cluster-rollout.md). Design: [design.md](design.md) §9.2, §13.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Commands travel to a Go machine agent in the node's supervisor, not to pipeline processes. | The supervisor is what restarts every role; a pipeline agent dies with its role. |
| D2 | The home's rollout driver runs in the home's supervisor (`internal/updater`), continuing the same run after `home_committed`. | One owner of the run, journaled; the API may restart without effect. |
| D3 | Prepare in parallel (cap 3), apply sequentially. | Download time overlaps the home's own update; the risky part is one machine at a time. |
| D4 | The node re-verifies everything; the mirror is a convenience. | The home's word is not a trust anchor (README Security). |
| D5 | `hold_platform` default. | A platform-specific bug should not block other platforms nor brick all machines of that platform. |
| D6 | Late `healthy` after a timeout is accepted and recorded, but does not un-hold a platform by itself. | Avoids flapping; an admin Resume is explicit. |

## 1. Files

**Create**
- `cmd/maktaba-server/internal/machineagent/` — `client.go` (v2 handshake, sealed frames, reconnect),
  `update.go` (prepare/apply/abort/rollback handlers, node journal), `loopback.go` (local API bridge,
  per-boot token), tests.
- `cmd/maktaba-server/internal/updater/rollout.go` — targets, ordering, policy, catch-up, timeouts.
- `api/internal/cluster/updates/mirror.go` — artifacts handler (signed, ranges, rate limit).
- `shared/db/migrations/<S3>_cluster_node_versions.sql` + `.sqlite.sql` — `cluster_nodes.version`,
  `platform`, `update_state`, `full_node`; revoke from `maktaba_worker`.

**Modify**
- `api/internal/cluster/agent/hub.go`, `session.go` — `process: "supervisor"` sessions, `update.*`
  command routing, `update.progress` / `update.state` frames, forwarding progress to the `updates`
  topic and to the supervisor (loopback `POST /update/target-event`).
- `api/internal/cluster/agent/crypto.go` — no change to the handshake; shared vectors extended with a
  supervisor `hello`.
- `api/internal/router/p31.go` — mount the mirror and the run target actions
  (`/runs/{id}/targets/{node}/retry|skip`, `/runs/{id}/resume`).
- `cmd/maktaba-server/cmd_serve.go` — start the machine agent in node mode.
- `docs/cluster/protocol.md` — new frames and commands.

## 2. Steps

1. Migration S3 and store methods (`SetNodeVersion`, `SetUpdateState`).
2. Machine agent client with the v2 handshake; connects, sends `update.state`; hub accepts supervisor
   sessions.
3. Mirror endpoint (the home already holds the per-platform downloads from its step 2, story 35.4).
4. `update.prepare` on the node: download, `releaseverify`, stage, venv (`internal/pyenv`), progress.
5. `update.apply` on the node: drains, pointer flip, exit 75, post-start checks, node journal,
   rollback path.
6. Rollout driver on the home: ordering, concurrency, timeouts, policy, admin actions.
7. Catch-up from `update.state` on connect.
8. Integration test harness with fake node supervisors in `clustertest`.

## 3. Tests

Story TC1–TC10. The fake node supervisor reuses `cmd/maktaba-server/internal/supervisor/fakechild_test.go`
patterns for fake roles that fail `/readyz` on demand.

## 4. Rollout

Phase D. Behind `rules.updates.auto_install` like the home's update; until 35.13's page lands, the run
is visible through `GET /api/cluster/updates/runs/{id}` and the audit log.

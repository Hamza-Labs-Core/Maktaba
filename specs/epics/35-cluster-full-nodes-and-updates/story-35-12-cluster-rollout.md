# Story 35.12 — Cluster rollout

## Description

*As the owner, I want every machine to end up on the home's version by itself, one at a time, each
checked before the next, and a machine that fails to go back to what worked and tell me.*

After the home commits a release (story 35.4, `home_committed`), the home orders each joined machine
over the cluster channel to install the same release. Machines download and stage in parallel from
the home's mirror and verify everything themselves; the switch is sequential. A machine that fails
rolls back locally, stays drained and is reported; `node_failure_policy` decides what happens to the
rest. Machines that were offline are caught up when they come back.

Design: [design.md](design.md) §9.2, §13; README R8, decisions "Order", "Machine failure", "Skew".

## Acceptance criteria

- **AC1** Machine agent: a Go client of the cluster channel in the node's supervisor
  (`kind: "node"`, `process: "supervisor"`, `worker_id: "sup-<node8>"`, v2 handshake with the node key)
  connects at start and after every restart and sends `update.state {version, prepared_version,
  platform, layout}`.
- **AC2** Commands `update.prepare`, `update.apply`, `update.abort`, `update.rollback` are added to
  the fixed list for machine agents only; any other session answers `ok: false`. A node accepts them
  only on its authenticated session to the home in its node config, and only for a `version` equal to
  the `release.json` it verified itself.
- **AC3** Mirror: `GET /api/cluster/updates/artifacts/{version}/{name}` serves the verified files of a
  staged release (every platform present in `cluster_nodes.platform`) and `trusted_root.json`;
  node-signed (story 35.9 delegation key), range requests, per-node rate limit; unknown names 404.
- **AC4** On `home_committed` every online full node gets `update.prepare` (at most 3 downloading at
  once); it downloads (mirror, then GitHub after 3 failures), verifies (story 35.1), stages
  `bin/releases/<v>/` with its venv, and reports `prepared`; progress arrives as `update.progress`.
- **AC5** Apply order: non-streaming machines first, then streaming machines (inside the window plus
  `window_overrun_minutes`), then by name. One machine at a time: `queued → draining → switching →
  verifying → healthy`, recorded in `update_targets` and `update_run_steps`.
- **AC6** Apply on the node: drain the pipeline (≤ `drain_timeout_minutes`), drain streaming (Epic 33
  per-host `binary` change when it runs slots, else Epic 32.4 drain), write `pending`, flip
  `current`/`previous`, exit 75; the new supervisor checks schema guard, api `/readyz`, pipeline
  `/healthz` + agent session with the new version, streaming `/readyz`, machine agent reconnected; then
  removes `pending`, raises its watermark, undrains, reports `healthy`.
- **AC7** Failure (any check, or no `healthy` within `node_health_timeout_minutes`): the node sets
  `current = previous`, exits 75, stays drained, reports `rolled_back` with the failed check; if the
  previous code does not fit the schema it comes up paused (Epic 31 §5.1). The home applies
  `node_failure_policy`: `hold_platform` (default) holds remaining targets of the same `platform`,
  others continue; `halt` holds all; `continue` goes on.
- **AC8** Admin actions on the run: Retry and Skip per target, Resume a held rollout, Roll back a
  healthy node (`update.rollback`); all audited.
- **AC9** Catch-up: a node `offline` during the rollout, or behind the watermark when its agent
  connects, gets a `catch_up` run with that one target — at once (`catch_up = immediate`), or in the
  window for streaming machines and `catch_up = window`. A node paused by the schema guard is caught up
  at once regardless.
- **AC10** Skew: no machine is ever ordered to a version the home has not committed; a node ahead of
  the home is flagged "ahead of the home"; worker-only nodes (no machine agent) are listed
  "worker-only — convert to update automatically" and are not targets.
- **AC11** Each node keeps its own journal (§5.2 shape); a power loss during its apply resumes or
  rolls back without the home.
- **AC12** `cluster_nodes` gains `version`, `platform`, `update_state`, `full_node` (slot S3, next free
  at implementation time), revoked from `maktaba_worker`.

## Test cases

- **TC1** `TestMachineAgentHandshakeVectors` (`cmd/maktaba-server/internal/machineagent`) against
  `shared/testdata/cluster/agent_vectors.json`.
- **TC2** `TestUpdateCmdRejectedForPipelineSession` (`api/internal/cluster/agent`).
- **TC3** `TestNodeRefusesVersionMismatch` — `update.apply` for a version other than the verified one.
- **TC4** `TestMirrorSignedRangeRequests` / `TestMirrorUnknownName404`.
- **TC5** `TestRolloutOrderNonStreamingFirst`, `TestPrepareConcurrencyCap3`.
- **TC6** `TestFailurePolicyHoldPlatform` / `Halt` / `Continue` (table over three nodes, two platforms).
- **TC7** `TestNodeHealthTimeoutCountsAsFailure`; `TestLateHealthyAccepted`.
- **TC8** `TestCatchUpOnReconnect` / `TestCatchUpPausedNodeImmediate` / `TestCatchUpStreamingWaitsWindow`.
- **TC9** `TestNodeJournalResumeAfterCrash` (crash before and after the pointer flip).
- **TC10** Integration (`api/internal/cluster/clustertest`): a fake home plus two fake node supervisors
  run a rollout end to end; one fails `/readyz` and rolls back.

## Edge cases

- **EC1** A node loses the home mid-apply: it finishes what it can; checks needing the home fail after
  `node_health_timeout_minutes` and it rolls back.
- **EC2** The home restarts during a rollout (e.g. a crash): the supervisor's journal and
  `update_targets` resume the rollout at the target that was in flight (its state is re-read from the
  node's `update.state`).
- **EC3** A node without internet cannot build its venv (`pypi_unreachable`): `failed_prepare`, not
  applied; reported with the reason.
- **EC4** A node whose layout is `package` (deb/rpm without the self-managed releases dir): reported,
  skipped, with the conversion command.
- **EC5** A newer release appears mid-rollout: the current rollout finishes first; the next run starts
  from the new home commit.

## Dependencies

35.1 (verification), 35.2 (layout, launcher), 35.4 (home commit), 35.8 (full nodes), 35.9 (delegation
key for the mirror). Epic 31 cluster channel; Epic 32.4 drain; Epic 33.14 AC7.

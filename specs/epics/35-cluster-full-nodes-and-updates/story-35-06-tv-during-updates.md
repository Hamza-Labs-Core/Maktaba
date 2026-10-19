# Story 35.6 — TV during updates

## Description

*As the owner, I want the nightly automatic update to install the new streaming software without
anyone watching TV noticing, and without me approving it at 03:00.*

Step 10 of the home's run (design §5.3) hands TV to Epic 33: the new streaming binary becomes a
`binary` TV change with `source.kind = auto_update`, which the change controller stages, verifies and
swaps at programme boundaries without waiting for an admin (the owner's override of Epic 33 decision
2, for this source and this kind only). Before the two-slot runtime exists, the step restarts the
streaming role inside the window when nobody is watching. The front door restarts only when its
`edge_protocol` changed.

Design: [design.md](design.md) §7; README R5.1, decisions "Approval override", "TV during updates".

## Acceptance criteria

- **AC1** With the two-slot runtime (Epic 33.1/33.7), step 10 creates a `binary` TV change:
  `source = {kind: "auto_update", run_id}`, release = the live release with
  `streaming = {version, sha256}` taken from `bin/releases/<v>/.verified`. The run journal records
  `tv_change_id`.
- **AC2** The controller (`api/internal/tvchange`) applies `auto_rule = immediate` to a change whose
  only kind is `binary` and whose source is `auto_update`; it is staged, verified (33.5) and swapped
  with `swap_wait = boundary` without an approval. A `binary` change from any other source, and every
  `lineup`, `settings`, `schedule`, `art`, `live_slots` change, still follows `auto_promote_<kind>`
  (default `approval`). A change that mixes `binary` with another kind is never auto-promoted.
- **AC3** The orchestrator waits for the change to reach `live` or a terminal state, timeout
  `boundary_max_wait_minutes + 10`; `live` → step 11; `failed` → run state `failed_tv` (no role
  restarted, pipeline undrained, version `blocked`, admin notified with the verify report).
- **AC4** If another TV change is `ready` waiting for approval, the binary change queues behind it
  (Epic 33 R9.4); the run fails with `tv_change_pending` at the window end + `window_overrun_minutes`
  and is retried next window.
- **AC5** Fallback (no staging-capable runtime, `GET /api/tv/slots` reports one process): step 10
  restarts the `streaming` role on the new binary only while viewers (excluding probes) ≤
  `tv_staging.quiet_max_viewers`; otherwise it re-checks every 5 minutes inside the window and fails
  with `tv_busy` at its end (retried next window). Pushed channels reconnect with the same video id.
- **AC6** `edge_protocol` equal between the running front door and `release.json`: the front door is
  not restarted and keeps running its previous release's binary; `keep_versions` pruning never removes
  a version a running process uses. Different: it restarts in step 11 after the TV swap, inside the
  window (story 33.14 AC4), event `tv_front_door_restart`.
- **AC7** Epic 33 docs record the override (README decision 2 and R13, story 33.14 AC2).
- **AC8** A rollback of the run (design §5.6) issues an Epic 33.9 rollback of the binary change
  (warm while the previous slot is warm, else cold).

## Test cases

- **TC1** `TestAutoUpdateBinaryChangeAutoPromotes` (tvchange: source auto_update + kind binary → immediate).
- **TC2** `TestManualBinaryChangeNeedsApproval`, `TestMixedKindsNeverAutoPromote`.
- **TC3** `TestOrchestratorTVStepWaitsForLive` / `...FailedTVBlocksVersion` (fake controller).
- **TC4** `TestTVStepPendingApprovalTimesOut` (`tv_change_pending`).
- **TC5** `TestFallbackRestartWaitsForViewers` (fake `channel_runtime`, clock) and `...TVBusyAtWindowEnd`.
- **TC6** `TestEdgeProtocolGate` (equal: no restart, version kept from pruning; different: restart after swap).
- **TC7** E2E (nightly): update vN → vN+1 with one TS and one HLS client connected through the front
  door; no disconnect, no gap > 1 GOP (reuses 33.14 TC5).

## Edge cases

- **EC1** Power loss while the change is `swapping`: on resume the orchestrator reads the change by
  `tv_change_id` and waits again; it never creates a second change for the same run.
- **EC2** `tv_staging.enabled = false` with the runtime built: the fallback path (AC5) is used.
- **EC3** Staging slot crash during verification: the change fails (Epic 33), AC3 applies.
- **EC4** A node with slots: handled in its own apply (story 35.12, story 33.14 AC7), not here.

## Dependencies

35.4 (orchestrator step 10); Epic 33.10 (built), 33.1/33.5/33.7/33.9 for the seamless path, 33.14.

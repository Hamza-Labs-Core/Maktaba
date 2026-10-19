# Story 33.14 — Upgrade via swap

## Description

*As the owner, I want installing a new Maktaba version to never interrupt TV: the new streaming software
should go through staging and be swapped in like any other change.*

Epic 28.3's self-update replaces the executable and re-execs. With staging on, the update installs side by
side and becomes a `binary` TV change; the TV swap happens first, then the non-viewer roles restart, and the
front door restarts only when its edge protocol changed.

Design: [design.md](design.md) §13.

## Acceptance criteria

- **AC1** `POST /api/admin/system/update` (and `maktaba-server update`) with staging on downloads to
  `bin/releases/<version>/`, verifies checksums (Epic 28), and creates a `binary` TV change (release =
  live release with the new streaming version).
- **AC2** The change is staged, verified and swapped under `auto_promote.binary` (quiet window by default)
  or approval; viewers, Plex, remote viewers and simulcast outputs see no interruption (33.7/33.8 ACs).
- **AC3** After the TV swap: `api`, `pipeline` and `web` restart on the new version (rolling, one at a time,
  each waits for `/readyz`); the supervisor re-execs last.
- **AC4** The front door restarts only when `edge_protocol` differs between versions, and then in the quiet
  window after the TV swap (event `tv_front_door_restart`); pushes reconnect within 30 s (same video id).
- **AC5** Verification failure aborts: the live slot stays on the old version, no other role restarts, the
  admin is notified with the report; the downloaded version stays for retry or is removed by GC.
- **AC6** Rolling back the binary change (33.9) returns TV to the old version; other roles are not
  downgraded automatically (shown as a warning with the command to do it).
- **AC7** Nodes (Epic 32): a node's update creates a per-host `binary` change for its slots; the home's
  TV is unaffected.
- **AC8** With staging off, Epic 28.3's behaviour is unchanged.

## Test cases

- **TC1** `TestUpdateCreatesBinaryChange`.
- **TC2** `TestUpdateOrderTVFirst` — fake supervisor records restarts order.
- **TC3** `TestEdgeProtocolGate`.
- **TC4** `TestUpdateAbortOnVerifyFail`.
- **TC5** E2E on a VM: update from vN to vN+1 with a TS client and an HLS client connected; no disconnect.

## Edge cases

- **EC1** A database migration in the new version: migrations must be backward compatible with the old
  slot (expand/contract), since the old slot runs during staging; a release with a breaking migration
  is refused by the updater (`requires_tv_restart`) and needs a planned restart window.
- **EC2** Windows: the supervisor cannot re-adopt children after re-exec; it restarts non-TV roles and keeps
  slots running only if they are not its job-object children — otherwise the update waits for the quiet
  window and accepts a front door restart.

## Dependencies

Epic 28.3; 33.1–33.10.

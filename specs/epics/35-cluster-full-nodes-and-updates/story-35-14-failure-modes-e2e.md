# Story 35.14 — Failure modes and end-to-end tests

## Description

*As the owner, I want to trust that an update at 03:00 either finishes or puts everything back the way
it was, whatever happens — a power cut, a bad release, a machine that is off, or someone trying to
push an old or forged build.*

The other stories each test their own part. This story proves the whole flow against the failures of
design §14 with chaos tests, attack fixtures and a nightly end-to-end run on three operating systems,
and fixes what those tests find.

Design: [design.md](design.md) §5.5, §5.6, §13.4, §13.5, §14, §15, §17; README Acceptance (epic), R10.

## Acceptance criteria

- **AC1 Power loss on the home** — a VM reset at each of: `download`, `stage`, `backup`, `rehearse`,
  `migrate` (inside a transactional file and inside a NO TRANSACTION file), `tv`, `restart_roles`
  (after `api`, before `pipeline`), right after the `current` flip, during `watch`. After boot, without
  an admin, the home either completes the run or ends `rolled_back`/`failed`, every role serves, the
  database's applied version is consistent with goose, and no `bin/releases/<v>.tmp` remains.
- **AC2 Supervisor killed** (`kill -9`, Windows `TerminateProcess`) at every step: the launcher
  restarts it; the journal resumes; TV slots and the front door survive (adopted, design §8).
- **AC3 Home update fails** — a fixture release whose API fails `/readyz`: binaries roll back to the
  previous release, the schema stays, nodes are never told to prepare, the version is `blocked`, the
  admin gets `update_rolled_back`. A fixture whose migration fails: `failed_migrate`, old binaries keep
  serving. A fixture whose rehearsal fails: the real database is untouched.
- **AC4 Node offline during rollout** — one node powered off at `home_committed`: the rollout finishes
  with that target `offline`; on power-on it is caught up (`catch_up`) and reaches `healthy`.
- **AC5 Node fails** — a fixture failing only on `windows/amd64`: the Windows node rolls back, stays
  drained, `update_node_failed`; with `hold_platform` the Linux node still updates.
- **AC6 Node loses the home mid-apply** — network dropped after the pointer flip: the node rolls back
  after `node_health_timeout_minutes`; when the network returns, Retry succeeds.
- **AC7 Signature attacks** — never installed, `update_verify_failed` (critical), version blocked, for:
  a bundle signed by another workflow identity (other repo; same repo, another workflow file; another
  tag); a valid bundle over a different `checksums.txt`; an archive not matching `checksums.txt`; a
  `release.json` whose version differs from the tag; a constraints file without hashes; a missing
  transparency-log proof.
- **AC8 Downgrade and replay** — refused and notified: an older signed release published as the newest
  GitHub release; the same version re-published with a different `checksums.txt`; a pin below the
  watermark; a channel switch `beta → stable` with a lower stable; a node ordered to a version other
  than the one it verified.
- **AC9 Node trust** — a fake home (wrong cluster key) sending `update.apply` is refused at the
  handshake; a mirror serving a tampered archive is caught by the node's own verification.
- **AC10 Three-OS E2E (nightly CI)** — macOS arm64 home, Ubuntu node, Windows node, joined from a
  locally built release v0.0.1-e2e and updated to v0.0.2-e2e (both signed with a staging identity
  configured through the test-only `MAKTABA_UPDATE_REPO` path): home steps, TV continuity (an HLS and a
  TS client connected through the swap, no disconnect, story 33.14 TC5), rollout of both nodes,
  Updates page shows all three on v0.0.2-e2e.
- **AC11 Auth failures** — the home unreachable from a node: authenticated routes fail closed after
  30 s; a sign-out on the home reaches a node within 1 s; a replayed delegation signature is refused.
- **AC12** Every failure found is fixed or recorded as a known issue in the runbook (story 35.15)
  before phase E closes.

## Test cases

- **TC1** `chaos/update_power_loss_test.go` — a VM-reset harness (QEMU/Lima on Linux CI, or
  `systemd-run --scope` + `kill -9` of the whole tree as the cheap variant) iterating AC1's points.
- **TC2** `TestSupervisorKillAtEveryStep` (`cmd/maktaba-server/internal/updater`, fake children).
- **TC3** `TestHomeRollbackOnReadyzFailure`, `TestFailedMigrateKeepsOldBinaries`,
  `TestRehearsalFailureLeavesDBUntouched` (integration with Postgres).
- **TC4** `TestRolloutOfflineNodeCatchUp`, `TestHoldPlatformWindowsOnly`, `TestNodeLosesHomeMidApply`
  (`api/internal/cluster/clustertest` with fake node supervisors).
- **TC5** `TestVerifyRejects_*` (`shared/go/releaseverify`) — one per AC7 fixture; fixtures in
  `shared/testdata/releases/` produced once by `.github/workflows/release-fixtures.yml` and committed.
- **TC6** `TestRefuseDowngrade`, `TestRefuseReplaySameVersion`, `TestPinBelowWatermark`,
  `TestChannelSwitchNoDowngrade`, `TestNodeRefusesVersionMismatch`.
- **TC7** `TestMachineAgentRejectsWrongHomeKey`, `TestMirrorTamperCaughtByNode`.
- **TC8** Per-OS E2E in CI (`macos-14`, `ubuntu-24.04`, `windows-2022`): each runner updates a
  single-machine home from v0.0.1-e2e to v0.0.2-e2e. The three-OS **cluster** E2E (AC10) runs on a
  self-hosted lab later (follow-up, owner decision); CI is not gated on it.
- **TC9** `TestDelegationFailClosed`, `TestRevocationPushLatency`, `TestDelegationReplay`.

## Edge cases

- **EC1** Disk full during stage: the step fails, `<v>.tmp` removed, old release untouched.
- **EC2** Clock jump (NTP correction) across the window boundary: a run that started stays started;
  none starts twice (`update_runs` partial unique index).
- **EC3** Two homes on a LAN with the same release: each updates only itself and its own nodes.
- **EC4** A corrupt launcher: covered by the runbook (manual restore), and by a test that the launcher
  replacement keeps `.old` until the next good start.

## Dependencies

35.1–35.13. Epic 33.14 TC5 harness (fake TS/HLS clients); `deploy/packaging/node/tests` dry runs.

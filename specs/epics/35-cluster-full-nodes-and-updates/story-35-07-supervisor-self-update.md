# Story 35.7 — Supervisor and launcher self-update

## Description

*As the owner, I want the program that supervises everything to update itself too, without stopping
the TV processes it supervises, and to fall back on its own when the new one cannot start.*

The supervisor (`maktaba-server`) is the last thing a run restarts (design §5.3 step 12). It hands
the surviving children (front door, TV slots) over to its successor through `handover.json`, exits
75, and the launcher starts the new release, which adopts them. On Windows a named Job Object whose
handle the launcher holds keeps those children alive across the restart. The launcher itself is
replaced only when `launcher_protocol` changes, as a full restart in the quiet window.

Design: [design.md](design.md) §8, §2.2; README R5.1, R5.4.

## Acceptance criteria

- **AC1** Step 12 writes `pending`, sets `previous = current`, `current = <v>` atomically, writes
  `updates/handover.json` (per surviving child: role, pid, pgid or job name, admin/probe URLs, start
  time), stops the in-process web server and exits 75 without signalling surviving children.
- **AC2** The new supervisor reads `handover.json` and adopts each child whose pid is alive, whose
  start time matches (no pid reuse) and whose probe answers; it supervises adopted children by probe
  and polling; one that dies or must restart is replaced by a normal child. Unadoptable entries are
  started fresh and logged (`supervisor_adopt_failed`).
- **AC3** POSIX: systemd units use `KillMode=mixed` and `RestartForceExitStatus=75`; launchd plists
  `AbandonProcessGroup`; a supervisor exit 75 kills no child; a service stop still stops every
  adopted group (the launcher signals them from `handover.json`).
- **AC4** Windows: surviving children run in the named job `Local\Maktaba-tv-<boot id>`; the
  supervisor duplicates the job handle into the launcher (`MAKTABA_LAUNCHER_PID`); the new supervisor
  opens the job by name and adopts its pids. If the duplication fails, the run degrades to an ordered
  restart in the window only while viewers ≤ `tv_staging.quiet_max_viewers`, and the run log says so.
- **AC5** The new supervisor resumes the run from the journal (step 13 `watch`); at commit it removes
  `pending`. If it never confirms, the launcher's boot-loop guard (3 non-75 exits in 10 min or
  `pending_timeout` 15 min) restores `previous`, and the old supervisor finishes `rolling_back`.
- **AC6** Launcher replacement runs only when `release.json.launcher_protocol` differs from the
  running launcher's, after `home_committed`, inside the window, with viewers ≤ `quiet_max_viewers`,
  announced on the Updates page: `maktaba-launcher.new --replace-self` renames the running launcher
  to `.old`, itself into place, and the service manager restarts it; `.old` is deleted at the next
  good start.
- **AC7** `maktaba-launcher --status` shows current, previous, pending and the last rollback.

## Test cases

- **TC1** `TestHandoverWritesSurvivorsAndExits75` (fake children).
- **TC2** `TestAdoptRejectsReusedPid`, `TestAdoptedChildReplacedOnDeath`.
- **TC3** `TestLauncherRestartsOn75ReadsNewCurrent`, `TestLauncherBootLoopGuardRestoresPrevious`.
- **TC4** `TestNamedJobSurvivesSupervisorExit` (Windows, `supervisor_windows_test.go`).
- **TC5** `TestServiceStopSignalsAdoptedGroups` (POSIX).
- **TC6** `TestReplaceSelfRenamesRunningLauncher` (Windows and POSIX temp dirs).
- **TC7** Integration: a front door with a connected HLS client keeps serving across a supervisor
  restart on Linux, macOS and Windows runners.

## Edge cases

- **EC1** Power loss between the pointer swap and the handover file: no `handover.json` → the new
  supervisor starts every child fresh (TV interruption accepted for this rare case; logged).
- **EC2** An adopted child hangs: the probe-failure path kills its group/job member and restarts it.
- **EC3** A stale `handover.json` from an earlier boot (boot id differs) is ignored and deleted.
- **EC4** The launcher is package-managed (`.deb`/`.rpm`): AC6 is skipped; the package updates it.

## Dependencies

35.2 (launcher, layout), 35.4 (journal); Epic 22.9 supervisor (process groups, Job Objects).

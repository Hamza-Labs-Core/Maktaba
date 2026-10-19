# Story 35.2 — Side-by-side layout and the launcher

## Description

*As the owner, I want a new version installed next to the old one and started by something small that
never changes, so a bad version or a power cut at the wrong moment always comes back up on a version
that works.*

Every machine gets the same layout: `<install>/bin/releases/<version>/` (the Epic 33 §3.3 directory,
now for every binary and the pipeline's environment), `current` / `previous` / `pending` / `watermark`
pointer files, and `maktaba-launcher`, a tiny stable process that the OS service runs. The launcher
starts `bin/releases/<current>/maktaba-server serve`, restarts it on exit code 75, and falls back to
`previous` when a pending version cannot boot.

Design: [design.md](design.md) §2; README R5.2, R5.5, decisions "Install layout", "Package-managed
installs".

## Acceptance criteria

- **AC1** Layout of design §2.1 on every OS; pointer files written atomically (temp + fsync + rename +
  directory fsync; Windows `MoveFileExW` with `MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH`).
- **AC2** The supervisor starts every role from `MAKTABA_RELEASE_DIR` (`bin/releases/<current>/`), never
  from `PATH` or the launcher's directory; `ResolveReleaseBinary` keeps resolving slot binaries there.
- **AC3** `maktaba-launcher` validates `current` (`.verified` present, `maktaba-server` digest matches)
  before starting it; a mismatch starts `previous` and logs `launcher_refused`.
- **AC4** Exit 75 → immediate restart re-reading `current`; exit 0 after a stop request → launcher exits
  0; any other exit → restart with backoff 1 s doubling to 30 s.
- **AC5** Boot-loop guard: while `pending` exists, 3 non-75 exits within 10 min, or no confirmation within
  15 min (`pending_timeout`), → `current = previous`, `pending` removed, `launcher_rollback` in
  `updates/launcher.log`, previous started. The supervisor confirms by removing `pending`.
- **AC6** `maktaba-launcher --version`, `--status` (current, previous, pending, last rollback),
  `--verify <dir>` (offline re-check through `releaseverify`), `--replace-self <file>` (used by 35.7).
- **AC7** Services per design §2.3: systemd (system and user units, `Restart=always`,
  `RestartForceExitStatus=75`, `KillMode=mixed`), launchd (`KeepAlive`, `AbandonProcessGroup`), Windows
  service (launcher implements `golang.org/x/sys/windows/svc`, no NSSM) or logon task; written by
  `maktaba-server setup --service [--user|--system]`.
- **AC8** `maktaba-server setup --self-managed` converts archive/manual and Homebrew installs (design
  §2.4): copies the running binaries into `bin/releases/<running>/` with `.verified`
  (`identity: "migrated"`), writes `current` and `watermark`, installs the launcher, rewrites the unit.
- **AC9** The next `.deb`/`.rpm` ships `/usr/bin/maktaba-launcher` and puts binaries under
  `/var/lib/maktaba/bin/releases/<v>/`; postinstall writes `current` only when newer than `watermark`.
- **AC10** Homebrew installs show "managed by Homebrew — automatic updates off" with the convert command;
  Docker keeps Epic 28.3's 409.
- **AC11** Releases are pruned to `keep_versions` (3), never `current`, `previous`, `pending`, or a
  version a running process (front door, TV slot) uses.

## Test cases

- **TC1** `TestPointerWriteAtomic` (crash between write and rename leaves the old pointer).
- **TC2** `TestLauncherRestartsOn75`, `TestLauncherBackoff`, `TestLauncherStopExits0` (fake child).
- **TC3** `TestBootLoopGuardFallsBack`, `TestPendingTimeoutFallsBack`, `TestConfirmedPendingStays`.
- **TC4** `TestLauncherRefusesTamperedCurrent`.
- **TC5** `TestSupervisorUsesReleaseDir` (cmd/maktaba-server/internal/supervisor).
- **TC6** `TestSetupSelfManagedMigratesArchive` (temp dir), `TestPruneKeepsInUse`.
- **TC7** `TestWindowsServiceControl` (windows-latest), `TestSystemdUnitRender`, `TestLaunchdPlistRender`.
- **TC8** nfpm dry run: package layout lists the launcher and `/var/lib/maktaba/bin/releases/<v>/`.

## Edge cases

- **EC1** `previous` missing (first install): the guard has no fallback; it keeps retrying with backoff and
  logs `no_previous`.
- **EC2** `current` names a directory that was pruned by hand: start `previous`, notify.
- **EC3** Windows: the running `.exe` cannot be deleted; pruning skips locked directories and retries at
  the next prune.
- **EC4** Read-only `/usr/bin` for system units: only `/var/lib/maktaba` is written (fits
  `ProtectSystem=strict`).
- **EC5** Two launchers (a stale unit plus the new one): the second takes `updates/lock` and exits 1 with
  `already_running`.

## Dependencies

35.1 (`.verified`, `releaseverify`), Epic 22.9 (supervisor), Epic 33.2 (`ResolveReleaseBinary`).

# Story 28.3 — Server self-update (maktaba-server)

> Epic 28 · Auto-Update · Phase 3 (apply)

## Description

An admin-triggered, one-click update of the running `maktaba-server`
binary, sourced from a GitHub Release, verified, atomic, and reversible.

`POST /api/admin/system/update` with body `{"version": "v1.4.2"}` (or
empty to mean "latest on my channel"):

1. **Resolve.** Pick the target release (explicit version, else the
   28.2 latest). 409 if already current.
2. **Pick the asset.** Match `runtime.GOOS`/`runtime.GOARCH` to the
   release asset naming pattern
   (`maktaba-server-<ver>-<os>-<arch>.tar.gz` / `.zip`). 404 if no asset
   for this platform.
3. **Detect install type.**
   - **Docker** (cgroup / `/.dockerenv`): return **409** with
     instructions (`docker compose pull && docker compose up -d`) — a
     container cannot replace its own image.
   - **deb/rpm** (binary under `/usr/bin`, package DB present): invoke
     `apt-get install --only-upgrade` / `dnf upgrade` for the package
     rather than a raw binary swap, so the package manager's state stays
     consistent.
   - **Plain binary** (archive/Homebrew/manual): do the binary swap.
4. **Download + verify.** Download the asset and the release's
   `checksums.txt`; compute SHA-256; compare to the manifest entry for
   the asset. Mismatch ⇒ abort, nothing touched.
5. **Atomic swap.** Extract the new `maktaba-server`, write it beside the
   running binary, `rename` the live binary to `<name>.bak`, `rename` the
   new one into place (rename-within-dir is atomic on every supported
   OS; on Windows the running `.exe` can be renamed but not deleted, so
   move-aside-first is required).
6. **Restart.** Re-exec via `syscall.Exec` (Unix) so the PID/socket
   handoff is seamless; on Windows, signal the supervisor to relaunch.
7. **Rollback.** A boot-time health probe (3 retries, 10 s) checks the
   new process is serving; if it fails, restore `<name>.bak` and
   relaunch the previous binary, surfacing "update failed, rolled back".

Admin-only (`principal.IsAdmin`); requires an explicit confirmation flag
in the request (`{"confirm": true}`) so a stray POST can't swap a binary.

## Acceptance criteria

- **Given** an admin on a plain-binary install of `v1.4.1`,
  **when** they `POST /api/admin/system/update {"confirm":true}`,
  **then** the correct platform asset is downloaded, its SHA-256 matches
  `checksums.txt`, the binary is swapped with a `.bak` kept, and the
  server re-execs into `v1.4.2`.

- **Given** the downloaded asset's checksum does **not** match,
  **when** the update runs,
  **then** it aborts before touching the live binary and returns a 502
  with a checksum-mismatch detail; the running binary is unchanged.

- **Given** the post-restart health check fails 3×,
  **when** rollback fires,
  **then** the `.bak` binary is restored and relaunched, and the audit
  log records `update.rolled_back`.

- **Given** a Docker install,
  **when** the update is requested,
  **then** it returns **409** with the `docker compose pull` instruction
  and changes nothing.

- **Given** a `.deb` install,
  **when** the update is requested,
  **then** the server shells out to the package manager (not a raw swap)
  and reports the package-manager result.

- **Given** a non-admin principal,
  **when** they POST the endpoint,
  **then** **403**.

- **Given** the request omits `confirm`,
  **when** posted,
  **then** **400** ("confirmation required").

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | asset list + GOOS/GOARCH | match | correct asset name picked |
| T02 | unit        | checksums.txt + asset bytes | verify ok | passes |
| T03 | unit        | flip a byte | verify | mismatch error, no write |
| T04 | unit        | temp dir + fake binary | swap | new in place, `.bak` exists |
| T05 | unit        | swap then induced failure | rollback | `.bak` restored |
| T06 | unit        | docker env marker present | detect | `installDocker` |
| T07 | unit        | `/usr/bin` + dpkg marker | detect | `installDeb` |
| T08 | integration | non-admin JWT | POST | 403 |
| T09 | integration | missing confirm | POST | 400 |
| T10 | integration | already on latest | POST | 409 "already current" |
| T11 | integration | docker install | POST | 409 + instructions body |

## Edge cases

- **Disk space.** Refuse if free space < 2× asset size (avoid a
  half-written binary). Pinned by a unit test.
- **Partial download.** Verified by checksum; a truncated download fails
  verification and aborts.
- **Permission denied on swap** (binary owned by root, server runs as a
  user). Detected up front (writability probe on the binary's dir);
  returns a clear "run as the install owner / use the package manager"
  error rather than a half-swap.
- **Re-exec loses CWD/env.** `syscall.Exec` is given the original argv +
  env; CWD preserved.
- **Concurrent update requests.** A process-wide mutex; the second
  request gets 409 "update in progress".
- **`.bak` from a prior update.** Overwritten each time (one previous
  version retained, matching 25.34).
- **Windows running-exe lock.** Move-aside-then-replace; relaunch via the
  supervisor (`cmd/maktaba-server/internal/supervisor`).
- **Symlinked binary** (Homebrew Cellar). Resolve `EvalSymlinks` and swap
  the real file, mirroring the existing CLI `selfupdate`.

## Files / packages

- `api/internal/system/selfupdate.go` (new) — handler + download, verify,
  swap, rollback, install-type detection.
- `api/internal/system/selfupdate_test.go` (new).
- `api/internal/router/p28.go` — mount under the admin group.
- Reuses the swap/verify shape proven in
  `cmd/maktaba-server/internal/selfupdate/selfupdate.go`.

## Open questions

- **Should the server update its sibling api/streaming binaries too?**
  For the unified `maktaba-server` they're forked from one binary, so
  swapping the server binary suffices. Multi-binary installs are a
  documented manual step for v1.

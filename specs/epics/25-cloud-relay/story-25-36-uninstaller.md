# Story 25.36 — Cross-platform uninstaller

> Epic 25 · Cloud relay · Phase 6 (server distribution)

## Description

Removing Maktaba should be as clean as installing it. Each platform
has its native uninstall path; we make them all consistent in what
they leave behind and what they remove. The decision the user gets
to make: **"keep my library data" vs "remove everything"**.

Universal contract:

- Uninstall **always** removes:
  - Binaries / app bundle
  - Auto-start entries (LaunchAgent, systemd, Startup
    folder, package init scripts)
  - Firewall rules added by us
  - Tray icon entries
  - Service registration
- Uninstall **prompts** before removing:
  - Database (`maktaba.db` / Postgres data dir)
  - Cache (transcodes, thumbnails) — not critical, but
    rebuildable
  - Logs
  - Configuration
- Uninstall **never** touches:
  - The user's library files
  - User-created backups outside our managed paths
  - Cloud account at HamzaLabs (separate; via cloud
    `DELETE /api/me` from 25.5)

Per-platform implementations:

- **macOS (25.27).**
  - "Drag to Trash" calls a `Quit` helper that signals
    services and runs a `~/Library/Application
    Support/Maktaba/uninstall.sh` that removes the
    LaunchAgent and prompts for data removal via a
    SwiftUI dialog.
  - Homebrew cask `brew uninstall --cask maktaba` runs
    the same script.
- **Windows (25.28).**
  - "Add or remove programs" → MSI uninstall runs WiX
    custom actions: stop service, remove firewall rules,
    delete service, optionally delete `%PROGRAMDATA%\Maktaba`.
- **Linux deb/rpm (25.29).**
  - `apt remove maktaba` keeps config; `apt purge` also
    removes config and `/var/lib/maktaba`. Document.
  - User account `maktaba` removed only on `purge`.
- **Linux Snap/Flatpak (25.29).**
  - `snap remove maktaba` / `flatpak uninstall
    app.maktaba.Server` — these handle data per their
    runtime; we add a "deep clean" instruction in our
    docs to wipe `~/snap/maktaba/` if desired.
- **AppImage (25.29).**
  - User deletes the file. We document the per-user
    systemd unit cleanup and the `~/.local/share/Maktaba`
    paths.
- **Docker (25.30).**
  - `docker compose down` stops; `docker compose down -v`
    removes volumes. Documented.
- **NAS (25.31).**
  - Vendor's package manager handles removal; we surface
    a "Also delete data?" toggle in the package's wizard.
- **VPS one-click (25.33).**
  - Destroying the droplet/instance is the uninstall;
    nothing further to do.

In-app "uninstall preview":

- Settings → Advanced → Uninstall shows a preview list of
  what's about to be removed and the size of each
  category. Clicking "Open uninstaller" hands off to the
  platform path.

## Acceptance criteria

- **Given** a macOS user drags Maktaba to the Trash,
  **when** the post-uninstall hook runs,
  **then** services stop, the LaunchAgent is unregistered,
  and the user is asked whether to remove
  `~/Library/Application Support/Maktaba`.
- **Given** the user answers "Yes, remove everything",
  **when** the script completes,
  **then** the `Application Support/Maktaba` and
  `Caches/io.maktaba.*` and `Logs/Maktaba` directories
  are gone; library files untouched.
- **Given** a Windows user uninstalls via Programs &
  Features,
  **when** the MSI rollback runs,
  **then** the service is stopped, firewall rules are
  removed, and `%PROGRAMFILES%\Maktaba` is gone;
  `%PROGRAMDATA%\Maktaba` removal is offered with a
  checkbox.
- **Given** a Debian user runs `apt remove maktaba`,
  **when** the package is removed,
  **then** the binaries and unit file are removed but
  `/var/lib/maktaba`, `/etc/maktaba`, and the `maktaba`
  user remain (so a re-install picks up where they left
  off).
- **Given** the same Debian user runs `apt purge maktaba`,
  **when** purge completes,
  **then** all of the above plus the `maktaba` user are
  removed.
- **Given** a Docker user runs `docker compose down -v`,
  **when** the command completes,
  **then** the named volumes (`pgdata`, `maktaba-data`,
  `maktaba-cache`) are deleted; the `media` bind mount is
  untouched.
- **Given** any platform uninstall is interrupted (power
  loss),
  **when** the user retries,
  **then** the second attempt completes idempotently;
  partial removals are tolerated.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | manual      | macOS uninstall keeps data | drag, "no" | data preserved |
| T02 | manual      | macOS uninstall removes data | drag, "yes" | data gone |
| T03 | integration | Windows MSI uninstall | run | service + firewall clean |
| T04 | integration | Debian remove vs purge | run both | clear difference |
| T05 | integration | Docker compose down -v | run | volumes deleted |
| T06 | regression  | re-install after remove | install | reuses preserved data |
| T07 | regression  | re-install after purge | install | starts fresh |
| T08 | smoke       | NAS uninstall (Synology) | run | clean |
| T09 | regression  | uninstall during running scan | run | scan stops cleanly, no zombie process |
| T10 | regression  | leftover files after AppImage delete | inspect | per-user systemd unit + ~/.local/share/Maktaba removed via documented script |

## Edge cases

- **Library files inside the install dir.** Bad practice
  but possible (user dropped `~/Movies` symlink under
  `~/Library/Application Support/Maktaba`). We never
  follow symlinks during uninstall; we treat
  `Application Support/Maktaba` as our domain only.
- **Open file handles.** On Windows, MSI uses Restart
  Manager to close handles before delete; on macOS we
  signal SIGTERM and wait 10s before removing the bundle.
- **Ongoing transcribes.** Pipeline jobs are killed; the
  Epic 03 last-segment-end-sec snapshot ensures no data
  loss if the user reinstalls and resumes.
- **Cloud entitlement cleanup.** Local server holds the
  bearer token; on uninstall we call
  `DELETE /api/servers/{id}` to notify the cloud (with
  10s timeout). If offline, the cloud reaper detects
  long-offline servers and the user can manually unlink
  via the cloud dashboard (25.16).
- **Kernel modules.** None; nothing to unload.
- **Docker containers without compose.** Document
  `docker stop maktaba && docker rm maktaba` plus the
  volume cleanup.
- **NAS shared folders.** We never touch user shares.
- **Permission errors.** Logged; uninstall continues for
  what it can; user gets a summary of what's left.
- **Reinstall over leftover state.** The wizard (25.35)
  detects existing data and offers "use existing" or
  "wipe and start over".
- **Dry-run mode.** `maktaba uninstall --dry-run` lists
  what would be removed; useful for documentation and
  for sysadmins.

## Files / packages

- `desktop/macos/uninstall.sh` and Sparkle's relaunch
  hooks.
- `desktop/windows/installer/UninstallActions.cs`.
- `packaging/debian/postrm`, `packaging/rpm/maktaba.spec`.
- `packaging/synology/scripts/postuninst`.
- `cli/maktaba/uninstall.go` — `maktaba uninstall`
  cross-platform fallback.
- Documentation: `docs/uninstall.md` per platform.

## Open questions

- **Telemetry on uninstall.** Out — privacy-respecting;
  we don't collect "why did you uninstall" surveys.
- **Account deletion linkage.** A button in the local
  uninstall flow that opens the cloud's "delete account"
  page is friendly; we add it as an explicit secondary
  link, never auto-trigger.

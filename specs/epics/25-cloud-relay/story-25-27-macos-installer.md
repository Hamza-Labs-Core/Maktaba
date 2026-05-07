# Story 25.27 — macOS installer

> Epic 25 · Cloud relay · Phase 6 (server distribution)

## Description

Native macOS distribution of the Maktaba Server (the local API,
streaming, and pipeline binaries from Epics 07, 08, 03). Two paths:

- **DMG with drag-to-Applications.** A signed, notarized
  `Maktaba-<version>.dmg`. Mounts to a window with the
  `Maktaba.app` bundle and an alias to `/Applications`. The app
  bundle contains the three Go binaries (`api`, `streaming`,
  `pipeline-launcher`), a `uv`-managed Python venv for the
  pipeline, embedded FFmpeg static build, and a SwiftUI front-end
  shell that supervises the three subprocesses.
- **Homebrew cask.** `brew install --cask maktaba` installs the
  same DMG contents via the cask formula at
  `https://github.com/HamzaLabs/homebrew-maktaba`.

Lifecycle:

- **Auto-start at login** via a `LaunchAgent` plist
  (`~/Library/LaunchAgents/app.maktaba.server.plist`). Toggle in
  Preferences > General.
- **Menu bar item** with status (running / starting / stopped),
  current libraries count, "Open in browser", "Pause indexing",
  "Show logs", "Preferences", "Quit".
- **Auto-update** via Sparkle 2 (EdDSA-signed appcast at
  `https://releases.maktaba.app/macos/appcast.xml`). Updates
  cleanly even while running by signaling subprocesses to
  drain.

Code signing & notarization:

- Apple Developer Team ID: `<HAMZALABS_TEAM_ID>` (placeholder).
- All binaries (Go + Python venv launcher + FFmpeg) signed
  with `--options=runtime --timestamp --deep` and stapled.
- Notarization via `xcrun notarytool submit ... --wait`.
- Hardened runtime entitlements: minimal — only
  `com.apple.security.cs.allow-jit` (Whisper MLX),
  `com.apple.security.cs.disable-library-validation`
  (FFmpeg dlopen), `com.apple.security.network.server`,
  `com.apple.security.network.client`,
  `com.apple.security.files.user-selected.read-write`,
  `com.apple.security.files.bookmarks.app-scope`.
- Privacy strings declared in `Info.plist` for any system
  prompts (Microphone is *not* requested; Photos is *not*
  requested; Documents folder is via security-scoped
  bookmarks).

Storage layout:

```
/Applications/Maktaba.app/Contents/
~/Library/Application Support/Maktaba/
    config.toml        # user-editable
    db/maktaba.db      # SQLite single-user default
    cache/             # transcodes, thumbnails (purgeable)
    logs/              # rotated; 10 files × 10 MB
    bookmarks/         # security-scoped bookmarks for library roots
~/Library/Logs/Maktaba/server.log -> ~/Library/Application Support/Maktaba/logs/server.log
```

## Acceptance criteria

- **Given** a user double-clicks the DMG,
  **when** they drag Maktaba to Applications and launch,
  **then** Gatekeeper does not warn (notarization stapled)
  and the menu bar icon appears within 5 seconds.
- **Given** the app is launched for the first time,
  **when** the user grants Documents folder access,
  **then** the access is persisted via security-scoped
  bookmark and survives reboots.
- **Given** "Start at login" is enabled,
  **when** the user logs in,
  **then** the LaunchAgent starts within 10 seconds and
  the menu bar item appears.
- **Given** a new release is available,
  **when** Sparkle checks (every 24h or on demand),
  **then** the user sees an update dialog and can apply
  the update; on relaunch, the new version runs.
- **Given** an update fails to apply,
  **when** the launch fails 3 times in a row,
  **then** the previous version's binaries are restored
  from `~/Library/Application Support/Maktaba/.previous/`.
- **Given** the user runs `brew install --cask maktaba`,
  **when** Homebrew completes,
  **then** the same DMG is installed and the cask
  manages future `brew upgrade --cask maktaba`.
- **Given** the user clicks "Quit" in the menu bar,
  **when** Maktaba shuts down,
  **then** subprocesses receive SIGTERM, drain ≤ 10s,
  and the menu bar item disappears.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | manual      | Apple Silicon Mac | install DMG | Gatekeeper accepts |
| T02 | manual      | Intel Mac | install DMG | Gatekeeper accepts |
| T03 | unit        | appcast XML signature | verify EdDSA | valid |
| T04 | integration | install via brew cask | run | identical to DMG |
| T05 | integration | reboot with autostart on | login | services up < 10s |
| T06 | regression  | uninstall (drag to Trash) | observe | LaunchAgent removed via shutdown helper |
| T07 | manual      | macOS Sonoma + Sequoia | smoke | menu bar UI renders |
| T08 | regression  | privacy prompts | first launch | Documents prompt only; no others |
| T09 | unit        | Sparkle rollback path | simulate launch fail | falls back |
| T10 | integration | external library (USB drive) | scan | works after granting access |

## Edge cases

- **Gatekeeper Quarantine bit.** Files downloaded via
  Safari pick up `com.apple.quarantine`; we test on
  fresh-download flows, not just `cp`.
- **App Translocation.** Apps run from non-Applications
  paths get translocated; the user is gently prompted on
  first run "move me to /Applications".
- **MLX vs CPU paths.** Apple Silicon users get MLX
  Whisper; Intel users get CPU `faster-whisper`.
  Selected at first run based on architecture.
- **External media volumes.** Security-scoped bookmarks
  are mandatory for paths outside the home folder. The
  pipeline uses `NSFileCoordinator` to acquire access.
- **Time Machine backups.** Cache and DB are excluded
  from Time Machine via `com.apple.metadata:kMDItemSupportsExclusionFromBackup`
  + tmutil exclusions. Config and library state are
  included.
- **macOS Sequoia "App Management".** Maktaba may be
  flagged when updating other apps; our binaries are
  inside `/Applications/Maktaba.app`, not modifying
  others, so the prompt is rare.
- **Multi-user Macs.** Each user's `LaunchAgent` runs
  their own server on their own port (default 8080
  → 8081 if taken). Documented; no shared instance.
- **Login keychain access.** We don't store secrets in
  the system keychain; data key is in `Application
  Support/Maktaba/keys/`. Avoids elevation prompts.
- **Notarization service outages.** Releases pause
  if Apple is down; documented as a known risk for our
  release cadence.
- **Beta channel.** Sparkle supports separate appcasts;
  user opts in via Preferences. Beta releases are
  notarized too.

## Files / packages

- `desktop/macos/Maktaba/` — Xcode project for the
  shell app.
- `desktop/macos/scripts/notarize.sh`.
- `desktop/macos/sparkle/appcast.xml.tmpl`.
- `desktop/macos/Maktaba/MaktabaApp.swift`,
  `desktop/macos/Maktaba/Supervisor.swift`,
  `desktop/macos/Maktaba/MenuBarController.swift`.
- Cask: `homebrew-maktaba/Casks/maktaba.rb`.

## Open questions

- **App Store distribution.** Sandboxing constraints
  break our pipeline (subprocess + filesystem access).
  Defer; v1 is direct-distribution only.
- **MAC address as device id.** No — we use a UUID stored
  in `Application Support`.

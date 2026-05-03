# Story 13.1 — macOS app

A signed and notarized `.dmg` for Apple Silicon and Intel.

**Anchors:** [`architecture.md` §6.4](../../architecture.md), §12.4
(distribution).

## AC

- Targets macOS 13+; universal binary.
- Native menu bar (Maktaba, File, Edit, View, Library, Window, Help) with
  standard shortcuts (`Cmd+Q`, `Cmd+W`, `Cmd+,` for Settings).
- Window restore: position and size persisted across launches.
- Light/dark theme follows the OS by default; Maktaba theme overrides if
  set in Settings.
- Notarized with hardened runtime; Gatekeeper accepts on first launch.
- Distributable via `.dmg` and Homebrew tap
  ([architecture.md §12.4](../../architecture.md)).

## TC

- Install the `.dmg`, drag to Applications, first launch: no security
  prompt.
- Quit and relaunch: window opens at last position with last route.
- macOS dark mode toggle: app follows.

## EC

- macOS 12: launch refused with a friendly "Update macOS to 13+" message.
- Notarization revoked on a stale build: user sees the system gatekeeper
  prompt; we publish a new build.
- Multiple windows open: each has independent state but shares the same
  server connection.

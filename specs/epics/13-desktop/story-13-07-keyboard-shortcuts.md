# Story 13.7 — Keyboard shortcuts (desktop-specific)

Native menu items expose all shortcuts; the in-app shortcut layer
([Story 11.9](../11-web-ui/story-11-09-keyboard-shortcuts.md)) augments
them.

**Anchors:** [`architecture.md` §6.4](../../architecture.md).

## AC

- Menu items map to all
  [Story 11.9](../11-web-ui/story-11-09-keyboard-shortcuts.md) shortcuts
  plus desktop-specific:
  - `Cmd/Ctrl+N` → new window.
  - `Cmd/Ctrl+Shift+N` → new private session (no shared local cache).
  - `Cmd/Ctrl+1..9` → switch to library N.
  - `Cmd/Ctrl+,` → Settings.
  - `Cmd/Ctrl+R` → refresh current view.
  - `Cmd/Ctrl+F` → focus search.
- Native menu accelerators visible next to each item.
- Shortcuts work even when the app is not focused, for global media keys
  (Play/Pause/Next/Previous) on Windows and macOS.

## TC

- `Cmd+R` reloads the current route without losing player state.
- Press the keyboard Play/Pause hardware key with Maktaba in the
  background: playback toggles.
- Conflict with a global OS shortcut: the OS wins.

## EC

- Linux media keys vary by DE: documented per-DE behavior.
- An app with conflicting global media keys (Spotify, Apple Music): we
  yield to whichever was most recently in foreground.

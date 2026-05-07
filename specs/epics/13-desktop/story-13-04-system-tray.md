# Story 13.4 — System tray integration

A tray / menu-bar icon shows current activity and exposes quick actions.

**Anchors:** [`architecture.md` §6.4](../../architecture.md).

## AC

- Tray icon present on macOS menu bar, Windows system tray, Linux
  system tray (where supported).
- Click → menu with: Now Playing, Queue (count), Recently Added,
  Settings, Quit.
- "Now Playing" is live; clicking opens the player window to the video.
- Tray icon dot/dot-with-count when jobs are running or notifications
  pending (configurable).
- Click-through closes the window without quitting (macOS / Windows);
  Quit menu item exits.

## TC

- Start a transcribe job in the background: tray icon shows a small
  badge with the count.
- Click "Now Playing" from tray: window comes to foreground at the
  correct route.
- Quit from tray: app exits cleanly; downloads are paused, not lost.

## EC

- Linux DE without tray (GNOME 40+ default): document AppIndicator
  extension or graceful degrade.
- A user disables tray entirely in Settings: window-close means quit.

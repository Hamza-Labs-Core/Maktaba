---
schema: 1
kind: pr
pr: 1588
title: "Story 13.12: the Maktaba app's tray panel on a left click (Windows and the Mac), the native menu kept on a right click"
date: 2026-11-24
session: e17bc07f-84d4-4215-9722-3af0a39bc002
agent: claude-code
---

## What happened
The second build story of the approved desktop design (#1556), D2.
- **The panel:** a left click on the tray icon opens a 360×560 window (`traypanel.rs`, label `tray-panel`), placed by a pure `place()` against the icon's screen work area. It hides on blur, on Escape and after a choice.
- **Its content:** the web app's `TrayPanel` (bundled pages, `main.tsx` branch) draws the tray menu's own entries from `tray_panel_state`, and acts through `choose_tray_item` (the tray's handler, enabled items only).
- **Linux and the right click:** Linux keeps its StatusNotifier menu; the right click keeps the system's menu.
- **Permissions:** the window has its own capability (`tray-panel.json`). `tests/commands_contract.rs` checks that commands are declared, registered and granted alike.
- **Review and branch:** an independent review of c84d0774d found eight problems, all fixed in 1a8a5c737 (listed in the PR). The branch was built on 13.10 and took main with `tools/dev/merge-main.sh` after #1568 merged. The visual references (`web/e2e/desktop/tray.ui.spec.ts`) come from CI's desktop-frame job.

## What we learned
- **Building a Tauri window inside the tray's event handler can deadlock on Windows**: build the panel hidden when the tray is built, then only show, move and hide it.
- **Tray icon rectangles are physical pixels**: matching them against monitors must use each monitor's physical rectangle. On a Retina Mac, treating them as points matched no screen. The Mac then positions the window in logical points.
- **On Windows the press on the tray icon takes focus first**: the panel's blur handler hid it and the release reopened it. A short "just closed by blur" window (400 ms) makes a click on the icon a toggle.
- **A panel that re-renders on every download percent must not move focus on render**: focus only when it opens.
- **Lazy chunks are a failure point for a tiny always-ready window**: import it statically.

## Keep doing
- A pure placement function with table tests for every taskbar edge and screen; the window code only applies it.
- A contract test that reads every capability file, so a new window cannot be granted more than its own commands.

## Stop doing
- Creating windows from event handlers that run on the platform's tray thread.

## Open
- Linux has no panel: its StatusNotifier hosts draw their own menu, as decided in 13.9.
- 13.11 (Maktaba's own error dialog) is stacked on this branch and opens once this merges.
- Linux's clippy failed at first on the panel's window code, which Linux never builds (it has
  no panel); e96d2900b marks it unused there rather than compiling it out, so the placement's
  tests still run on Linux.
- The Windows app job failed once on this pull request (run 45344363547) in
  `pgbundle`'s `TestRealMajorUpgrade`: right after the old cluster's fast shutdown, starting it
  again for recovery said "pre-existing shared memory block is still in use". It passed on the
  rerun. This pull request touches no server code; Windows may release a stopped postgres's
  shared memory late, and the upgrade's recovery start may need to wait for it. Not yet looked
  into.

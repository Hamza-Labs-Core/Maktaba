---
schema: 1
kind: pr
pr: 1630
title: "Story 13.13: setup and the server's status as their own windows, restyled to the desktop design"
date: 2026-11-24
session: e17bc07f-84d4-4215-9722-3af0a39bc002
agent: claude-code
---

## What happened
The sixth build story of the approved desktop design (mockups `setup-welcome`, `setup-check`, `server-status`).
- **Shell:** `ownwindows.rs` makes two 960 × 640 windows of the app's own pages, marked before their page loads (`window.__MAKTABA_PANEL__`), with Maktaba's frame from the first frame.
  - While this machine is not set up, the setup runs in its own window and the main window waits hidden until the setup opens a server.
  - **Server status** in the tray (and the Linux Server menu) opens the status window. `statusview.rs` folds the supervisor's snapshot into one tile per part.
  - Each window has its own capability file.
- **Web:** the setup restyled to the mockups (a brand pane with the five steps and the arch, option cards, a footer with the language switch, memory and disk meters). A new `StatusWindow`. Both are always dark. The desktop token group is now in `tokens.css` and checked against the JSON.
- **References:** Playwright specs `setup.ui.spec.ts` and `status.ui.spec.ts`, English and Arabic; CI writes the PNGs.
- **Independent review** of 80a8c252c found six problems, all fixed in 3102579eb:
  - (high) the frame's state event still reached every window's global listener, so the main window going full screen dropped the setup window's title bar;
  - a second request during a window's build made a second build, which could put the setup in two windows;
  - a failed setup window was retried on every Open, reloading the main window's setup;
  - a server saved but not opened left the main window blank;
  - Big Screen during setup put the hidden main window full screen;
  - started hidden at sign-in, a set-up server was no longer re-registered.
- A saved Start in Big Screen also waits for the setup to end (d0cba6d01).

## What we learned
- **Tauri 2's global `listen` hears events sent to any window.** `emit_to(label)` scopes nothing unless the page listens with `getCurrentWebviewWindow().listen`.
- **A window built off the main thread needs a per-kind "building" flag.** Without one, a second request (a second launch, a double click) builds again.
- **The page's state goes stale when the shell's changes.** A page told "the setup is elsewhere" must be reloaded before it is shown again.
- **The transcription tools' install detail** is `venv_building` with a `percent` value (`serve_appbundle.go`), not `pct`.
- **Sizes need isolating in Arabic.** `formatBytes` values in an Arabic sentence must each be isolated (`bidiIsolate`), or "412.0 GB" reorders to "GB 412.0".
- **A preview stand-in for the shell must answer DesktopGate's first `status` step,** or the gate waits forever.
- **A flake:** `web/src/pages/Admin/Channels.cases.test.tsx` CHN-13 (keyboard and drag reorder) failed once in a full local vitest run and passed three times alone. It is unrelated to this change.

## Keep doing
- Rendering the Playwright specs headless locally to compare them with the mockups, then deleting the PNGs so only CI's are committed.
- A contract test across `build.rs`, `lib.rs`, the capability files and the page's command names.

## Stop doing
- Switching a listener's scope on the sending side only: change both sides.
- Heredocs and runtime `cd` or `git -C` in a worktree-isolated session: the guard refuses them. Use Edit or a script file.

## Open
- Status tiles show states, not live counts (sizes, devices, watchers): that needs the server's API.
- Setup screens after What will run keep their Next inside the page.
- Mica on the new windows.
- Not tried on a real Windows, Linux or Mac window: the hidden-window flows, Snap Layouts on the own windows, the hidden build at sign-in.
- Merging with 13.12 (#1588) and 13.11 touches `main.tsx`, `shell.rs`, `lib.rs`, `build.rs` and the tray.
- CHN-13's flake.

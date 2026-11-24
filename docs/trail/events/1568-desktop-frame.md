---
schema: 1
kind: pr
pr: 1568
title: "Story 13.10: the Maktaba app draws its own window frame (title bar, caption buttons, the title bar's menu), with CI's first desktop visual references"
date: 2026-11-24
session: e17bc07f-84d4-4215-9722-3af0a39bc002
agent: claude-code
---

## What happened
The first build story of the approved desktop design (#1556).
- **The shell (frame.rs):** a page claims the window's frame. Windows and Linux lose the system frame; the menu bar is hidden, not removed, so its shortcuts keep working. The Mac gets the overlay title bar. Snap Layouts come from Win+Z when the pointer rests on maximize. The title bar's menu reads the live menu bar, and the menu bar's words are in English and Arabic (lang.rs `MenuWords`).
- **The web app (DesktopFrame):** the app's header is the title bar; other pages get a slim bar.
- **Visual references:** `web/e2e/desktop` (Playwright, dark, 1280×800, en and ar; Windows, GNOME and the Mac), run by the new `_desktop-frame.yml` in CI's Playwright image. `reference_images.py` now counts them.

An independent review of 51331c4a9 found four bugs and five risks. All were fixed in a64318f35 except the late-claim risk (see Open):
- a crash under the app's error boundary removed the frame and left no way to move or close the window;
- the menu never toggled Start in Big Screen;
- a window already full screen drew the title bar over big screen;
- `data-tauri-drag-region="deep"` let popovers drag the window;
- the release could race a claim;
- client-mode pages had no frame for 4 s;
- Linux-without-tray Server actions were reachable from local pages;
- Win+Z could reach another app.

## What we learned
- **The window loads pages the shell doesn't ship** (the local server's, or another home's in client mode), so a custom frame has to be claimed per page load and given back by the shell. Otherwise an older page leaves a window with no controls.
- **Tauri's drag.js:**
  - It stops the document's mousedown (`stopImmediatePropagation`), so an outside-click listener on the document never hears a press on a drag region. Listen in the window's capture phase.
  - `"deep"` makes every non-clickable descendant drag, including a popover's padding and scroll bar. Use `""` (that element only).
- **Hiding a muda menu bar** (`hide_menu`) keeps the window's accelerators. A click from the page doesn't toggle a check item, though: toggle it before calling the menu handler.
- **Playwright's pointer starts at (0, 0).** In a right-to-left window that is the close button, so the first Arabic references showed it hovered (red). Move the pointer away before each screenshot.
- **A Windows runner checks the repo out with `\r\n` line ends.** A source-reading test that finds `"\n}\n"` failed only there. Normalize the line ends when reading.
- **The affected-tests gate** wants a test change for every changed source file (lib.rs, webcache.rs, App.tsx). Run `go run . gate --range origin/main...HEAD` in `tools/affected-tests` before pushing.
- **This toolchain can't type-check the Windows paths** (`x86_64-pc-windows-msvc` is not usable here), so `desktop-shell-windows` in CI is their first compile.

## Keep doing
- Have an independent reviewer read the diff before the PR is called ready. Here it found a crash path and two dead features that the tests didn't cover.
- Write the references in CI's image, download them, look at each one, then commit. Never commit references rendered on the Mac.

## Stop doing
- Marking a header `data-tauri-drag-region="deep"` when it has popovers.

## Open
- A claim that arrives late from the page being left can't be told apart from the new page's. It is rare; the next page load corrects it.
- The title bar's menu has no About (a predefined native item).
- Story 13.17 extends the references: the real frame captured on a Windows runner, and the tray.
- Next stories, in order: 13.12 (tray panel), 13.11 (dialogs), 13.15, 13.18, 13.13, 13.14, 13.16.

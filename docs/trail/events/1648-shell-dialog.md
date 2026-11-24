---
schema: 1
kind: pr
pr: 1648
title: "Story 13.11: Maktaba's own dialog for the shell's errors, the system's box kept only as the fallback"
date: 2026-11-24
session: e17bc07f-84d4-4215-9722-3af0a39bc002
agent: claude-code
---

## What happened
The third build story of the approved desktop design (#1556).
- **The dialog:** every error the shell reports (`show_error`) opens Maktaba's own dialog: a window of the app's bundled pages (`shelldialog.rs`, `web/src/dialog/ShellDialog.tsx`) with its title bar, the sentence, the programs' own errors, "See the logs" where the server runs, and Close.
- **Window life cycle:** the window is built hidden off the main thread and shown once its page asks for its words (`shell_dialog_content`). A page that never asks within 5 s is closed, and the system's box speaks instead.
- **Permissions:** its own capability, `shell-dialog.json`.
- **Review:** an independent review of f989259fd found six problems, all fixed in ad0c25f83 (listed in the PR).
- **Branch:** built on 13.12's branch, so it waited for #1588 and opened stacked on it.

## What we learned
- **A window of the shell's own must be made hidden and shown only when its page is ready.** A page that fails to load otherwise leaves a blank, frameless window on top. A short timeout with the system's box as the fallback keeps every error said.
- **A window's state machine must cover "being made".** A second error during the build otherwise asks for a second window with the same label, the build fails, and the fallback box appears beside the first.
- **Words belong in a command's answer, not in the page's script.** The page asks for the newest words, so the window always shows the latest error.
- **A window that takes the focus when it opens does so in a layout effect**, not a passive one: the tray panel's test failed on that race in CI, and the dialog had the same code.

## Keep doing
- Every window gets its own capability, held in place by the contract test.
- Visual references for each state (an error with the logs, one without, a failed update), in both languages.

## Stop doing
- Sending a window's data with a global emit: `emit_to` its label.

## Open
- The dialog's reference images are written by this PR's first CI run and committed after a look.
- 13.13 (setup and status windows) touches the same shell files and merges after this.

---
schema: 1
kind: decision
title: The PC's node moves from a hand-built copy to the installed Windows app
date: 2026-10-25
session: f43fad60
source: claude-trail.md
---

## What happened
The PC's node and dizqueTV started from sign-in scheduled tasks (logon +30 s), because `O:` and
`Y:` exist only in the signed-in session. On 2026-10-25 it was decided to install the Windows
app instead: the node moved from the hand-built copy in `E:\tv\maktaba-local` to
`%LOCALAPPDATA%\Maktaba`, task `Maktaba Server <SID>` running `Maktaba.exe --server-agent`.
`node.json` is in `%APPDATA%\Maktaba` (on `E:`); web 8780, API 8781, streaming 8782.

## What we learned
- The Claude desktop app is an MSIX package: registry writes from its shell land in Claude's
  private copy. Installing Maktaba from Claude's shell broke `maktaba://` on this PC (see
  d20261031-pc-maktaba-link-msix).

## Keep doing
- Restart the PC's services only through their sign-in tasks.
- Download the installer, check its sha256 against the asset's digest, then the owner runs it
  or opens Maktaba from the Start menu.

## Stop doing
- Never touch the dizqueTV task.
- Never install Maktaba or start `Maktaba.exe` from Claude's shell on the PC.

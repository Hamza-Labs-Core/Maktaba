---
schema: 1
kind: incident
title: The PC stuck on v0.3.42 by a maktaba:// link registered only in Claude's MSIX registry
date: 2026-10-31
session: c17dd289
source: claude-trail.md
prs: [456, 457]
---

## What happened
Handed off from session f43fad60: get the PC onto v0.3.44 and stop Windows' "Get an app" popup
for the server's `maktaba://install-update` link.

Cause: the Claude desktop app is an MSIX package. The installs and app starts run from Claude's
shell registered `maktaba://` only in Claude's private registry copy. The server, outside it,
could not open the link, so the app never got the v0.3.44 order.

Decided 2026-10-31 (agent installs): the always-running `Maktaba.exe --server-agent` installs a
node's update itself instead of waking the desktop app (#457). Also a fix so the node's copy
notices a dead link to the home's database and ends replication workers that will not stop
(#456).

## What we learned
- Claude's shell sees its own private registry copy, so `reg query` there proves nothing about
  what the rest of Windows sees.

## Stop doing
- Never install or start Maktaba from Claude's shell on the PC; the owner runs the installer.

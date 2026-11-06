---
schema: 1
kind: pr
pr: 457
title: The Windows server agent installs the home's update orders
date: 2026-10-31
session: c17dd289
source: claude-trail.md
---

## What happened
The Windows server agent (`Maktaba.exe --server-agent`) answers the home's order instead of the
window app. It stages from the release's own `latest.json`: a setup .exe on that release's page,
checked with the app's key, its signed version (`requireSignedVersion`) and its signed file
name. At the go it starts the installer out of its own job. The server sends no `maktaba://`
link (`MAKTABA_AGENT_ORDERS=1`); on Windows the window app only shows the order and no longer
settles the orders' attempts. Two review rounds fixed answers that only move forward
(`autoinstall::advance`), the window app's settle race, and a stricter signature and URL check.

Neither #456 nor #457 was tagged at once: the next release was held, and #457 touches the update
path. Until a release carried #457, the PC needed the owner to open Maktaba once (or run the
v0.3.44 installer) to take v0.3.44.

## What we learned
- Proved on CI's Windows runner (`check-task-children.ps1`), never on the owner's PC: once a
  task's process exits the task is `Ready` even with its child still running; the task can run
  again meanwhile; the child outlives it.

## Keep doing
- Prove Windows behaviour on CI's runner, not on the owner's PC.

---
schema: 1
kind: migration
title: CI quirks - CodeQL default setup and notarization timeouts
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Two CI facts recorded in the first trail with no single PR behind them.

## What we learned
- CodeQL is GitHub default setup; there is no workflow file. The `CodeQL` umbrella check fails a PR on new high-severity alerts.
- CodeQL recognises `filepath.Clean` plus a prefix check at the sink as a path sanitiser.
- Notarization sometimes times out.

## Keep doing
- Re-run only the failed legs when notarization times out.

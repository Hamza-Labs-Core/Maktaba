---
schema: 1
kind: pr
pr: 223
title: Streaming shutdown flake fixed, 0 failures in 600 runs
date: 2026-10-21
session: 295d4839
source: claude-trail.md
---

## What happened
`TestShutdownReleasesThePool` was flaky on main. #223 made it count only once the fake server
had drained; the flake went to 0 in 600 runs.

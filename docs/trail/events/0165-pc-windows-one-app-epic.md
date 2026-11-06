---
schema: 1
kind: pr
pr: 165
title: Epic 38, one Windows app that runs everything
date: 2026-10-21
session: 295d4839
source: claude-trail.md
---

## What happened
The same one-app setup as on the Mac was asked for on Windows. Spec #165 and stories 38.1 to
38.14 came to about 30 PRs, reviewed and merged by session f43fad60. v0.3.0-rc.1 was tagged on
#243.

## What we learned
- `os.Rename` cannot cross drives on Windows: `%APPDATA%` is on `E:` on this PC and
  `%LOCALAPPDATA%` on `C:`.

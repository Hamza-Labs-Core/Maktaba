---
schema: 1
kind: decision
pr: 166
title: Epic 39, one Linux app; AppImage first, no apt or dnf repo
date: 2026-10-21
session: 454c33cb
source: claude-trail.md
prs: [173, 174]
---

## What happened
The same one-app setup was asked for on Linux: spec #166 and the story PRs, merged by session
f43fad60. The AppImage is the main download, with .deb and .rpm from the same build, all
updated by the Tauri updater. The server runs from a per-user runtime copy with a `current`
link and rollback (#174), as a systemd user service with linger (#173).

## What we learned
- No apt/dnf repo: it would add a second updater and trust root, hosting, and root for every
  update.
- `find | head` under pipefail exits 141.
- A plain CI re-run reuses the old merge ref; merge main and push instead.

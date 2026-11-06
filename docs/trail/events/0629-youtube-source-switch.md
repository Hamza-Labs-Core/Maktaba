---
schema: 1
kind: pr
pr: 629
title: A switch on every YouTube source; sources from Tartube
date: 2026-11-04
session: unknown
source: claude-trail.md
---

## What happened
- Merge 7ee89e433. Every YouTube source has an on/off switch; turning it on restores the mode it last had (`last_on_mode`, migration 0175 `youtube_source_switch`).
- Each library card on Manage libraries shows its YouTube state, can look up a channel from its own files, and flags a likely duplicate library.
- An admin action (`youtube_tartube` stage) imports every library's sources from Tartube's records, all off. It is idempotent, never creates a library, and reports Tartube channels with no library yet.
- All 55 live sources had been seeded `off`, and 10 libraries had none because the MediaIntel cache had no channel for their folders (addressed by #634).

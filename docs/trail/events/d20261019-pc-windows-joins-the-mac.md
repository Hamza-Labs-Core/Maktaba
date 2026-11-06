---
schema: 1
kind: decision
title: Windows joins the Mac's home as a node, not the other way around
date: 2026-10-19
session: 89e92223
source: claude-trail.md
prs: [75, 97, 104, 108]
---

## What happened
It was decided that the Windows PC joins the Mac, not the reverse. The PC's own home was
retired and on 2026-10-19 the PC joined `Mahmouds-Mac-Studio` as node `windows-pc`. #75 then
let the storage check accept files of unknown size, so the PC's worker took jobs.

In the same session canonical `smb://` paths (#81), Plex through the home (#78), TV hot swap
(#97, #104, #108) and automatic updates were asked for.

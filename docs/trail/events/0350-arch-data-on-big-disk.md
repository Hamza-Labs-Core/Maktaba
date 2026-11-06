---
schema: 1
kind: decision
pr: 350
title: Data and backups live on the big disk
date: 2026-10-27
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [351, 353]
---

## What happened
Maktaba's data and backups live on the big disk, not the internal one. #350 makes room before
a dump and never fills the disk; #351 lets the admin choose where Maktaba keeps its data and
move it to another disk; #353 adds admin backup settings (folder, schedule, retention, room).

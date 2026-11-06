---
schema: 1
kind: decision
title: dizqueTV on the PC led to mediaintel, Maktaba's queues and Maktaba TV; the first working rules
date: 2026-10-14
session: 89e92223
source: claude-trail.md
prs: [46, 47, 48, 52, 55, 57]
---

## What happened
The first PC session was asked for Plex Live TV from the YouTube archive. dizqueTV 1.7.0 was
built from source on the PC (port 8002), and Plex on the NAS tuned it as an HDHomeRun. That is
why Maktaba TV emulates an HDHomeRun with XMLTV.

The rule that followed was to build tools for the work, never do it by hand. That became
mediaintel (#46, #48), then Maktaba's queues (#47), channel rules (#52, #55) and Maktaba TV
(#57, which took in #55).

## What we learned
- PC facts: 192.168.50.24, RTX 2080 SUPER. `O:` is `\\Home-NAS\Originals`, `Y:` is
  `\\Home-NAS\Originals\Backup\Youtube`.
- On the PC, Splunk holds ports 8000 and 8088 and MiniTool ShadowMaker holds 8080, so setup
  picks a free port starting at 8780 (v0.1.0, tag commit 6b88722f).
- The mediaintel data and a hand-over README for the Mac are in `Y:\.maktaba`.
- A guess that NAS disks were failing was wrong: the errors were SMB timeouts from 32 parallel
  reads.
- A shared `git stash` swapped the work of two worktrees.

## Keep doing
- Build tools for the work instead of doing it by hand.
- Everything is config, with a UI for that config: rules are settings, not code.
- Verify every claim before acting on it.
- No placeholder features (a stub audit followed: #123, #128, #130 to #136).

## Stop doing
- Never change security settings from Claude (the Defender exclusion was added by hand).
- Never use `git stash`.

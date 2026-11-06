---
schema: 1
kind: pr
pr: 609
title: Cluster status and process files removed at stop and pruned
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge df3c0bfa7. The cluster folder had kept every worker's status and process file for ever: about 320 status files and about 520 process files across the local folder and the share.
- Each agent deletes its own status and process files when it stops cleanly. Every worker and the hub run a pruning pass at start and every hour: files over an hour old whose process is gone (a reused pid counts as gone) plus orphaned `.tmp` files, never a live process's file.
- On the share, files are matched to this machine by machine id or by being a home process, not by host name, because macOS renamed the Mac twice. The API and the pipeline commands no longer list gone processes.
- Found because Epic 51's test rig had leaked fake workers (live-N, chaos-N, w1) into the live home; they were deleted by hand with the owner's OK.

## What we learned
- Host names are not stable identities (macOS renames the Mac on mDNS conflicts); match by machine id.

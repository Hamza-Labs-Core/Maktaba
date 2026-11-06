---
schema: 1
kind: pr
pr: 243
title: node.json writes get a lock, user-only ACLs and safe encodings
date: 2026-10-22
session: 295d4839
source: claude-trail.md
prs: [227, 231, 239]
---

## What happened
`node.json` writes got a lock, ACLs and encoding handling over four PRs (#227, #231, #239,
#243): a half-failed replace never loses the file, writes run one at a time, a BOM'd file
reads whole, and readers never misread it mid-write. v0.3.0-rc.1 was tagged on #243.

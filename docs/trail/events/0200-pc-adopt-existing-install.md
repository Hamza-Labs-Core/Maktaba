---
schema: 1
kind: decision
pr: 200
title: The Windows app adopts the Maktaba a PC already runs
date: 2026-10-22
session: 295d4839
source: claude-trail.md
prs: [217, 230]
---

## What happened
Adopting an existing install moves its task, keys and firewall rules with one UAC prompt and
never touches dizqueTV (#200, #230). `node.json` is never copied, and only the PC that owns it
uses it (#217).

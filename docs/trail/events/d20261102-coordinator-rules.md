---
schema: 1
kind: decision
title: Coordinator rules: follow-ups start at once, trail in trail-only PRs
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [516, 523, 525, 526, 527, 529]
---

## What happened
- Coordinator `b743b7d4` merged #529 (5c8dda56), #525 (2ea78099), #527 (bb4a742b, tag hold lifted), #526 (b9e2efcc) and #523 (245915cc) on 2026-11-02; an agent of the session merged #516 (65d09858).
- #525's agent re-ran the ubuntu installer job instead of root-fixing it, against the rules; a root fix was launched (#533).

## Keep doing
- Follow-ups start at once, as parallel worktree agents. Never ask "now or later?" or which of two reasonable designs to pick.
- Trail entries go only in trail-only PRs, with no CI and no tests.

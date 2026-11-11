---
schema: 1
kind: incident
title: Epic 59 trail events written but never opened as pull requests
date: 2026-11-10
session: ca5b5f5a-daa4-4edf-94fd-eb52e3852eeb
---

## What happened
- During Epic 59, ten code pull requests merged with their trail events written in the child's
  worktree but never opened as trail pull requests: #1090, #1092, #1094, #1095, #1097, #1098,
  #1109, #1112, #1117 and #1118. In each case the child's last message said `trail check` was
  running in the background and the trail pull request would follow; the child's turn ended
  there and nothing resumed it, or the child stopped silently (a usage limit or a closed
  provider stream).
- The lead gathered the ten event files from the children's worktrees into one trail-only pull
  request, checked each one names its real merge sha, and fixed the three problems `trail check`
  found: `kind: change` in the #1090 event (now `pr`), and a `Keep / stop` section in the #1090
  and #1112 events (split into `Keep doing` and `Stop doing`).
- Earlier the same day, three children stopped without a report (two on one provider instance,
  one on a closed stream). Their unpushed work was pushed by the lead and handed to a new child
  on the other instance: 59.02 with the #1089 event, the #1091 event, and 59.03's last edits.

## What we learned
- A child that ends its turn waiting on a background command may never be woken again. Work
  promised in a last message ("I'll open the trail PR once it passes") is not done until a pull
  request exists.
- Event files left only in a worktree are lost to everyone else. A trail event should be
  committed and pushed on its branch as soon as it is written.

## Keep doing
- After each code pull request merges, the lead checks that its trail pull request exists
  (`gh pr list --head trail/…`), and gathers any stranded event into a batch.

## Stop doing
- Ending a child's turn on a background `trail check`: run it in the foreground (it takes a
  minute once the tool is cached), then open the pull request in the same turn.

## Open
- The #1119, #1120 and #1121 events (59.08, 59.09, the client-address hardening) are still to come
  from their children.

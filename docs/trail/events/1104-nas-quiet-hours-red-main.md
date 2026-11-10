---
schema: 1
kind: pr
pr: 1104
title: "nas-shapes: no quiet hours in the cells; main red since #1096"
date: 2026-11-10
session: d29865cb-5183-4329-8a31-fdc09e432a9e
agent: claude-code
---

## What happened
- Main's nas-shapes went red at d6c1f61dd, the merge of #1096 (59.02). It stayed red at
  6093314cc, the merge of #1100.
  - On the Light and Standard tiers the default quiet hours (01:00-06:00) hold a library's first
    scan until the window opens.
  - Every cell running in the daytime therefore found 0 of 40 clips.
- The product keeps holding first scans (spec §6.3, decision 14). The harness changed instead:
  #1104 writes `[schedule] quiet_hours = ""` in every cell's `server.toml`, as the 16.5
  rehearsal does, with a test that reads it back.
- #1104 merged in 0a290998c with all 19 cells green.

## What we learned
- #1100's own run was green while main was red. Its branch had been cut before #1096 merged,
  and a `CLEAN` merge state does not mean the checks ran against the main it lands on. When a
  sibling story merges while a NAS pull request waits, merge main in and let the cells run again.
- A story that changes when work runs (quiet hours, tiers) changes what every cell sees. Its
  pull request has to run nas-shapes, and its harness change ships in the same pull request.
- Quiet hours are when heavy work runs. A heavy job is held when now is outside the window, so
  the cell proving the hold uses a window that opens hours ahead.

## Keep doing
- Fix the harness, not the product, when the product's behaviour is the decided one. Then prove
  that behaviour in a cell of its own: `quiet-hours-hold`, in #1103.

## Open
- #1097 and #1098 were waiting on main's nas-shapes going green. Each one's `landed` entry is
  kept alongside the others when they merge. #1095 merged in f08965f63, and #1103 turned on
  its `first_scan`.

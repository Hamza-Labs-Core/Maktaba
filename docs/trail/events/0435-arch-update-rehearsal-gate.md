---
schema: 1
kind: decision
pr: 435
title: The update rehearsal gates every tag
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [446]
---

## What happened
CI proves the update paths: the update rehearsal (#435) runs every update path of the Mac app and
gates every tag. Scenario 11 (#446) covers the TV slot release mismatch and fails on pre-#443 code.
v0.3.43 was the first release after it.

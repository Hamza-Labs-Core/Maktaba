---
schema: 1
kind: pr
pr: 544
title: Step-code scan fast: one literal-prefixed regexp per form
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge a24c1c8f. `TestEveryStepErrorHasItsWords` (cmd/maktaba-server) broke test-budget on #532 (1.28 s; cap 3x400 ms).
- Cause: one regexp, a four-way alternation of step-code forms with no literal prefix, over about 1.4 MB of source. RE2 ran every byte through the NFA (1.27 s under -race).
- Fix: one literal-prefixed pattern per form (`\b` before `coded(` checked by hand). Same 61 codes, test 0.08 s. New `TestStepCodesInFindsEveryForm` pins the finder.
- The `api/internal/updates` overrun (31.2 s, over the 30 s package budget) from the same run was handled in #548.

## Keep doing
- When a test regex-scans big sources, give each pattern a literal prefix and do not combine them in one alternation.

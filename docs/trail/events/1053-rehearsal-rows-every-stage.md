---
schema: 1
kind: pr
pr: 1053
title: Every 16.x rehearsal row has a job of each of its stages in every round
date: 2026-11-08
session: b0ea7fd9-6e6d-4c4f-ab23-200b28d0b3bd
prs: [1046]
---

## What happened
- After #1046 gave 16.9 seasons, the other rows' logs were read for stages they never queued. Only one was missing: 16.1's `rate_style` (story 54.28) claims seasons and logged `the seed has no subject of [season]: not queued` in every round, so 16.1 passed without it.
- 16.1's setup now runs 16.9's `seasonsOn`: a series with two seasons, as the detect stage writes them. 16.1's other stages claim libraries, so it changes nothing for them.
- The check that each round has a job of every listed stage is on for every wave row (16.1–16.6, 16.8, 16.9), in every round: through the rollback, through the update, each warm-up, after the flip. 16.7's own scenario already failed when any of its four TV stages did not run.
- No listed stage is excused: each one gets a job in CI. All nine rows passed on the exact head, each inside its downtime budgets.

## What we learned
- A scenario that logs a skipped stage and passes reports coverage it does not have. Make coverage an assertion in every row, not in the one row where the gap was first noticed.

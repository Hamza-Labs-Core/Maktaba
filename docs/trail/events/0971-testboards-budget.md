---
schema: 1
kind: pr
pr: 971
title: TestBoards outgrew the unit test-time budget
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [960]
---

## What happened
- After #960, each `Record` also sums every machine's lines, and `logindex`'s `TestBoards` (180 scrapes) took 1.27 s under `-race` in CI, past test-budget's hard cap (3× the 400 ms soft cap). The `unit` check failed on every pull request.
- The test records an hour of scrapes (60) and checks the same things: minute points, none past MaxPoints, errors per hour, the storage board, the HTTP routes. 0.30 s under `-race` locally.

## What we learned
- A change that makes a hot path costlier can break unrelated PRs through the test-time budget. Run the touched package's tests with `-race -short` against the budget before pushing.
- In production the cost is small: about 7 ms a scrape under `-race`, every 15 s.

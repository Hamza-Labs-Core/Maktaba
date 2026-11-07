---
schema: 1
kind: pr
pr: 748
title: Scenario 12 expects the loops of the release it updates from
date: 2026-11-07
session: 14c6f63b-ff36-454d-9735-8989bcb48e6d
agent: claude-code
prs: [686]
---

## What happened
- The first release since v0.3.82 (v0.3.86) moved the rehearsal's "previous release" forward, and scenario 12 started failing everywhere: logs.index's `api:errors_notifier` was no longer "added since the previous release" and wasn't in `tools/rehearsal/loops-full-home.txt`. Added it.

## What we learned
- `loops-full-home.txt` is the *previous published release's* loops. Each release that starts a new switchable api or streaming loop must add it there, or the rehearsal breaks on the release after.

## Open
- Derive the list from the previous release's own catalog instead of keeping it by hand.

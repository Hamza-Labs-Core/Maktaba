---
schema: 1
kind: pr
pr: 1717
title: "The relay's own deadline close is tested again, and Shutdown counts a cut once"
date: 2026-11-25
session: 8fc89d92-c76f-4cb4-b0eb-8036123ea989
agent: claude-code
prs: [1711]
---

## What happened
- #1711's review found that after its fix no test needed the relay's own close at the drain
  deadline: the test's home always closed as well. It also found that `Shutdown` could count a
  tunnel its drain had counted.
- #1717 adds a test with a home that never closes, and makes `Shutdown` set the cut time and close,
  so `Close` alone counts. Four mutations of the code each fail a test.

## What we learned
- A fix that makes one side's timing not matter can leave that side untested. After it, remove each
  side in turn and see a test fail.
- A count kept in two places is counted twice sooner or later: one function counts.

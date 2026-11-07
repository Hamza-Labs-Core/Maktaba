---
schema: 1
kind: pr
pr: 813
title: Apple pages add From plugins only when they have a panel (Epic 54, story 54.20)
date: 2026-11-07
session: 28c0b780-1dd6-4ba0-b27f-a3382e1c1cd1
issue: 741
---

## What happened
- The iPhone, iPad and Mac series and library pages kept #787's panels in a stack that is always there. With no panels it still took the page's spacing: a gap between Play and the tabs on a library. A page now keeps its panels in state (`.subjectPanels`) and adds the section only when there is one, as Android does since #787.

## What we learned
- On Apple, a fix for "a task on nothing never starts" (an always-present stack) brings back the empty-item spacing Android's goldens caught. The answer on both platforms is the same: load at the page, add the item only with content.
- Apple's UI screenshots are not compared against goldens, so a layout change there needs a test that asserts what is absent, not only what shows.

## Keep doing
- When a fix lands on one platform, check whether the other platforms have the same fault.

## Stop doing
- Merge watchers that read checks with `|| true`: an empty read counted as green. #787's watcher now fails closed (one JSON read with jq, every check completed SUCCESS, SKIPPED or NEUTRAL, at least one, the merge pinned to the read head).

## Open
- None.

---
schema: 1
kind: pr
pr: 765
title: Plugin panels on the web's series and library pages (Epic 54, story 54.20)
date: 2026-11-07
session: 28c0b780-1dd6-4ba0-b27f-a3382e1c1cd1
prs: [742]
issue: 741
---

## What happened
- The web's series and library pages show the plugin panels section, as the video page does; one item of issue #741.

## What we learned
- The web's page harness gives every page the all-on capabilities, which have `panels: false`. To test a page with panels on, mock `useCapabilities` in a file of its own; in the pages' own tests, check only that no `/api/panels` request is made.
- #742's UI-test failure was a counting race in the shimmer gallery test (`all("shimmer").count` read as soon as the first shimmer existed). The fix waits for the count, up to 5 s.

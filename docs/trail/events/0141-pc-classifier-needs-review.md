---
schema: 1
kind: pr
pr: 141
title: Test race fixes that held v0.2.0 until a human review
date: 2026-10-20
session: 89e92223
source: claude-trail.md
prs: [138]
---

## What happened
v0.2.0 (#138) waited on #141 (the hdhr nil-conn race and a Toast timer that outlived its
provider). The auto-mode classifier would not merge #141 without a human review, so the first
PC session ended with the release waiting. Session 798feb8d merged #141 and then #138.

## What we learned
- The classifier can refuse to merge a PR without a human review; the release then waits.

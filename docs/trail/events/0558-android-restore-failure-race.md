---
schema: 1
kind: pr
pr: 558
title: Android AppSession: a failed restore says Failed before its problem
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge d2e2d711. CloudTest.aHomeNotAnnouncedSaysWhyThenIsAskedWhenItIs failed on Windows desktop-player (job 125472803032, PR #513; real line CloudTest.kt:253).
- Root cause: a race in `AppSession`. A failed kept-sign-in check finishing on OkHttp's callback thread set `restoreProblem` before `progress = Failed` in two writes, so another thread could see the problem without the Failed state.
- Fix: `failWith` stores Failed first and the problem last, under a lock shared with `heard()`. The network look's start and end are under the same lock, so a home announcing itself in between is no longer missed.
- Test: a `SlowToFail` progress state forces the bad order every time (old code fails at the CI assertion). 163/163 client tests; 15 runs under CPU load clean.

## What we learned
- Two related state writes from a callback thread are visible in between; write them under one lock, in the order readers depend on.

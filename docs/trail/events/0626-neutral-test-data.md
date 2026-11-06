---
schema: 1
kind: pr
pr: 626
title: Neutral test data; no raw {percent}; Remote Arabic attention
date: 2026-11-04
session: unknown
source: claude-trail.md
---

## What happened
- Merge 53cef37b3. Android, Android TV, Apple and web test data use an invented home ("Maktaba Home"), documentation-range addresses or `.local` names, and an invented media path. maktaba.dev now shows the Android start screens, the full Android Remote shot and an Android TV sign-in.
- Processes in the iPhone Remote, the Android Remote and the web show the home's English sentence when a detail code arrives without its values, never a raw `{percent}`.
- The Android Remote says the clips count in Arabic from a plural resource, and its bar label is «انتباه». The Apple Player keeps a paused video paused when its quality changes.
- A Player accessibility-audit failure on an iPhone shard was traced to the simulator registering XCTest's per-tap touch device twice, which split the touch-down from its lift and cancelled the touch before the app could act. The audit now opens each Profile page through a Debug launch option instead of a row tap; the row taps keep their own tests.
- No CI script or workflow step pipes into `head` under pipefail any more; `tools/ci/tests/test_no_head_pipes.py` keeps it that way.

## What we learned
- The simulator can register XCTest's per-tap touch device twice and cancel a tap; a launch option is a sturdier way to reach a page in an audit.

## Open
- The early-exit pipe sweep was not finished: a `head` in testflight.yml and `grep -q` (#647). The simulator tap race is #639.

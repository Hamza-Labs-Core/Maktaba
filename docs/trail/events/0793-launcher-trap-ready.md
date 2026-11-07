---
schema: 1
kind: pr
pr: 793
title: The launcher test sends the stop once the script's trap is set
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
issue: 682
---

## What happened
- `TestRunLauncherPassesTheStopOn` sent SIGTERM after a fixed 200 ms. On a loaded runner, `sh` had not run its `trap` yet.
- The script now prints `ready` after the trap, and the test waits for that line.

## What we learned
- A test that signals a child must wait for the child to say its handler is in place, not for a fixed time.
- Delaying the trap past the old fixed wait proves the new test waits for the right thing. The old test fails every time under that delay.

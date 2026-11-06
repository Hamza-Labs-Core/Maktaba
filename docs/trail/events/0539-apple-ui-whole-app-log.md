---
schema: 1
kind: pr
pr: 539
title: Apple UI tests keep the app's whole log
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [567]
---

## What happened
- #567, folded into #539. `log show --info --debug` was already used, but the simulator keeps a subsystem's info/debug lines only while `log config --subsystem <exact bundle id> --mode "level:debug,persist:debug"` asks it to.
- Each shard sets it after boot and reads the log once after the tests; the Mac job now keeps `mac-app.log`.
- Every launch writes `launched ...` (info) and `launch arguments: N` (debug) via `Log.launched()`, and `tools/apple/check-app-log.py` fails a job missing either (or says "never ran" when the app never launched).
- `testADelayKeepsTheQuerysAnswer` reads `MockHome.delay(of:)` instead of timing.

## What we learned
- On the simulator, a parent subsystem's log config is not inherited; set it on the exact bundle id.

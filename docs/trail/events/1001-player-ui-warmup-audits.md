---
schema: 1
kind: pr
pr: 1001
title: "Player UI warm-up audits until the process is warm"
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [1004]
---

## What happened
- Main's ipad 3/6 (run 42455914091) ended TEST EXECUTE FAILED, exit 65, with \"56 tests, 0 failures\": its first test, `PlayerComponentsUITests.testRails`, failed on \"Audit failed to complete in time\" (code -56), the runner relaunched the test process, and the closing totals counted only the relaunch.
- The warm-up's one audit had taken 10.2 s (the simulator still slow), and the rail's first audit then went past XCTest's 15 s; after the relaunch audits took 2.3 s.
- #1001 repeated the warm-up audit until one took under 5 s (at most four). Its own run showed that holds on iPhone (13.5 then 2.0 s) but never on iPad, whose warm Libraries audit takes 5 to 12 s: every iPad shard ran four audits, 30 to 40 s each for nothing. #1004 also stops at an audit no more than 15% quicker than the one before.

## What we learned
- \"N tests, 0 failures\" with exit 65 is a failure before a relaunch (\"Restarting after unexpected exit, crash, or test timeout\"): search the log above the totals.
- A warm-up's \"warm\" is when the measure stops improving, not a fixed time that holds on one device: check a threshold on every device in the PR's own run.

## Open
- With #1004 the iPad warm-ups ran two to four audits (9.6, 8.1, 6.8, 6.6 s; 8.8, 7.9 s). One iPad shard's warm audits took 15.5 then 14.0 s: on a runner that slow, a test's first audit can still reach XCTest's 15 s deadline, and no warm-up changes that.

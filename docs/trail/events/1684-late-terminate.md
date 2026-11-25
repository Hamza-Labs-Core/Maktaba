---
schema: 1
kind: pr
pr: 1684
title: "XCTest's \"Failed to terminate\": xcodebuild was late with the SIGTERM, held up by screenshot attachments"
date: 2026-11-24
session: 8fc89d92-c76f-4cb4-b0eb-8036123ea989
agent: claude-code
---

## What happened
- Three Player UI shards failed on 2026-11-24 (issue #1678; runs 45492257208, 45444285100,
  45495588048).
- `terminate()` on a simulator asks xcodebuild, which sends the SIGTERM; XCTest waits 60 s. The
  simulator's launchd log has `exited due to SIGTERM | sent by xcodebuild` 45, 34 and 93 s after
  the request. The app ran normally until then.
- xcodebuild takes the runner's messages in turn and was 35 to 98 s behind. 7 of its 8 stalls over
  5 s were on a screenshot attachment, all in a shard's first minutes.
- #1684 writes a screenshot to `MAKTABA_SHOTS` or attaches it, not both. A late end that XCTest
  has seen is said in the log instead of failing the test; a real one no longer hangs tearDown.

## What we learned
- The hang fix of #1561 (`continueAfterFailure = true` in tearDown) does not cover XCUITest's own
  failures: XCUIAutomation marks them `shouldInterruptTest`.
- The result bundle answers this without a re-run. `xcresulttool export diagnostics` gives
  xcodebuild's `Session-*.log`, which has both the runner's clock and xcodebuild's on each message,
  and the simulator's `system.logarchive` (`/usr/bin/log show --archive`; in zsh `log` is a
  builtin).

## Keep doing
- Compare the two clocks in the Session log before blaming the app for a slow step.

## Open
- That an attachment is what blocks xcodebuild is inferred from its timings; no stack was taken.
- Whether the rate drops is to be read from later runs.
- `RemoteDiagnosticsUITests` and `RemoteDemoShotsUITests` still rely on `continueAfterFailure`.

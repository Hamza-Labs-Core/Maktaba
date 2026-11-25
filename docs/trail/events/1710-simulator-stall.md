---
schema: 1
kind: pr
pr: 1710
title: "The Player UI tests' stall is the simulator drawing its wallpapers; the hangs are XCTest's async hooks"
date: 2026-11-24
session: 8fc89d92-c76f-4cb4-b0eb-8036123ea989
agent: claude-code
prs: [1561, 1684]
---

## What happened
- Issue #1678's "does not have a process ID" and the slow first launch were traced from the result
  bundles.
- **The stall:** PosterBoard's prewarm (`com.apple.springboard.posterboardupdate.prewarm`), which
  `dasd` starts a few minutes after a simulator's first boot, draws the whole wallpaper gallery. On
  a CI VM that takes about two minutes. Five failures of five whose log covers it began in it: four
  "Failed to terminate" and the "process ID" one.
- **The slow first launch:** `apsd` retries its push connection 8 to 17 times a second for good,
  and `diagnosticd` reads its log. Together they held over one of the three cores.
- **The hangs:** with an async `setUp` or `tearDown` in the class, XCTest runs the tear-down from
  inside what runs and waits for it on the main thread.
- #1710 turns PosterBoard and apsd off after the boot, and makes `PlayerUITestCase`'s hooks
  synchronous.

## What we learned
- #1684 named xcodebuild, late with its messages, and the screenshot attachments it stalled on.
  Both were true and neither was the cause: they were what the prewarm held up.
- #1309 saw apsd's loop and took it for background load, because it is there in passing runs too.
  What is always there still takes the room the rest needs.
- A spindump covers the whole machine, and its threads' priorities say what is starved: runnable
  and not running.
- `launchctl disable` in a simulator survives `simctl erase`. A probe needs a new simulator.
- In a simulator `launchctl` takes `user/foreground/<service>`; PosterBoard is a launch angel there.

## Keep doing
- When several failures differ in their message, line up what else ran at the same minute.
- Try a fix of the harness on a throwaway simulator first: the first candidate (`dasd` off) also
  worked, and the narrower one was enough.

## Open
- The effect on a CI runner is not shown yet: read the settle logs and the shards' times.
- The TV and Remote jobs boot their own simulators and are unchanged.
- #1607's iPad 1/6 audit (XCTest waiting for an animation's end) has no prewarm in its log and is
  not traced.

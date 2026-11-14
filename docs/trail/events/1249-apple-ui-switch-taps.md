---
schema: 1
kind: pr
pr: 1249
title: "Apple UI tests: switch taps re-checked, Remote sign-in by Return, crash answers and PIN lock fixed"
date: 2026-11-14
session: b47dc279-403a-4d10-8efc-873b659d9e1a
prs: [1000, 1004]
---

## What happened
- Main's Apple UI jobs failed one test at a time, a different shard each run (runs 43556127370, 43409448093, 43409306425, 43372573155, 43344331335 and others), blocking tags. Of 14 failures read, three causes:
  - 7 were a switch tap that left a SwiftUI `Toggle` unchanged (`testPlaybackSettingsAreKept`, `testAlsoSendToMaktabaWhenTheHomeAllowsIt`): dead centre on a still switch, no touch cancelled, app in front.
  - 1 was the Remote's Connect tap lost while the keyboard settled after typing (`RemoteDiagnosticsUITests`; no `connect:` line for that launch).
  - 5 were "the simulator may have dropped a tap" on buttons; not fixed (see Open).
- Fixes: `PlayerUITestCase.flip` (tap once still, read until it changes, tap again only if it still reads what it read before; a second miss fails), as #1000 did for a Home row. The Remote's sign-ins submit with Return, as `PlayerUITestCase.signIn` already did. `TouchWatcher` logs each touch's held time and point (debug, kept app log).
- Proving it with `-test-iterations 3` found two more:
  - The crash question's answer is kept per home id, the mock home's id never changes, and no reset cleared it, so each crash-question test failed when run twice. `-maktaba-settings-reset` now calls `CrashConsent.forgetAll()`.
  - The locked PIN pad's 12 s lock was shorter than the test's own steps on a slow iPad: the "closed pad" tap typed a real 1 and the right PIN finished early. Now 30 s, and the keys must still be closed after that tap.
- Proof: run 43582447874 passed 1200 of 1200 test runs (12 shards × 3 plus the Remote). The held-time line caught a switch miss: `touch held 0.07 s at (341, 194)`, a whole quick tap the switch ignored; `flip`'s second tap (same 0.07 s, same point) worked. It was needed once in each of the two proof runs.

## What we learned
- An XCTest screen recording keeps a frame only when the screen changes. A frame pulled at a time (`ffmpeg -ss`) is the last change before it, so the Home Screen 9 s later (tear-down) read as "sent home at the tap". List the frame times (`ffprobe -show_entries frame=pts_time`) before reading timing from a recording.
- XCTest's "already have a main display digitizer" line appears on about every synthesized tap (about 500 per shard): routine, not the rare #639 race.
- `TouchHook`'s cancelled-touch count includes UIKit's own cancels, such as the one a long press makes when it opens a context menu, so "the simulator may have dropped a tap" is weak evidence alone. Read the held-time lines.
- A UI test that checks a timed state (a lock, a countdown) must make the time outlast its own steps on a slow runner, and assert the state still held after acting, or the check passes vacuously.
- `-test-iterations N` also finds state that leaks between launches: a test that passes once and fails on its second iteration.

## Keep doing
- Read the xcresult's hierarchy and synthesized events (tap point against the element's frame) before naming a cause, and correct a wrong reading in the PR when the evidence turns.

## Open
- The cancelled-touch failures on buttons (Live, Libraries, Parental, ConnectProgress): the new held-time lines will show what those touches were the next time one fails.
- Why a quick, centred synthesized tap is sometimes ignored by a `UISwitch` on the iOS 26.5 simulator is not known; `flip` handles it.

---
schema: 1
kind: pr
pr: 1713
title: "The Mac full-screen test waited for a menu bar macOS sometimes does not slide down"
date: 2026-11-25
session: 8fc89d92-c76f-4cb4-b0eb-8036123ea989
agent: claude-code
---

## What happened
- `PlayerMacMenuUITests.testThePlayerFillsTheWindowAndGoesFullScreen` failed on #1609 and #1616
  (issue #1678, jobs 138795912356 and 138984611138), each at "the menu bar shows View" in full
  screen.
- The recordings show the pointer at the screen's top edge and the menu bar hidden for 10 s. macOS
  slides the bar down for a pointer there, and for the one move the test made it did not. The app
  sets nothing about it.
- #1713 checks the item's title in the UI test and leaves by its key, and tests the state and
  action behind the item in a unit test. Clicking it in the bar macOS slides down is no longer
  covered.
- The first head's Mac job was red (run 45551124155): its UI test passed and its new unit test
  failed. That waited 5 s for SwiftUI to redraw the menu in the test host, which did not happen;
  in the app, on the same run, it did.
- The pull request also carries #1710's follow-ups, one of which was a mistake of its own: head
  964ee1d5f gave each `simctl spawn` of the services step 30 s. Just after a boot these calls take
  longer. The limit cut calls on all 14 shards (run 45553400654), 13 warned, and apsd went on in 5;
  with no limit it had gone in 14 of 14. `tools/apple/sim-services.py` now asks, acts and asks
  again in one budget.

## What we learned
- The result bundle has the screen's recording: the frames settled in a minute whether the bar was
  there, which the log's "not hittable" did not.
- WindowServer's "SetMenuBars: ignoring menu bars from app" comes 60 times a run and means nothing
  here.
- A hosted unit test cannot count on SwiftUI redrawing the main menu. Test what the item reads and
  calls; leave its title to the UI test.
- A time limit needs the measured time of what it limits, and a limit that cuts a setup step undoes
  the step. The shards' green results did not show it; the warnings did: read them on the first
  run of a change to the setup.

## Open
- Why macOS does not slide the bar down is not known: passing runs keep no such logs.
- Why the test host does not redraw the menu is not traced.

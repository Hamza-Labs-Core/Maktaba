---
schema: 1
kind: pr
pr: 803
title: iPad Player UI waits count looks as well as seconds, and wait for Home to settle
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
issue: 761
---

## What happened
- Three iPad Player UI failures, read from the shards' job logs and xcresults, were each a wait that could not succeed on a loaded runner. No timeout was raised to fix them.
- `testShimmer` waited on a poster the tests' home delayed by 60 s, but `ImageCache` gives a fetch 15 s (NFR-15). The Arabic AX3 variant counted 25 s after its launch, when the card already showed its title. The Debug gallery's card now asks for no picture (`neverLoads`).
- #761: one look at the app's tree took 4.2 s, so a 5 s `XCTWaiter` on `isHittable` had a single look, taken mid-pop. `waitUntil` takes at least three looks as well as its seconds.
- `testNotInterestedRemovesTheCard` long-pressed while the recommendations and the cards' lazily fetched titles redrew Home, so no menu opened. `waitUntilHomeIsQuiet` waits until the app has asked the mock home nothing for 1.5 s.

## What we learned
- The xcresult's activity times (`xcresulttool get test-results activities`) and the job log's `t = …` lines show how long each look took. That tells a slow runner apart from a state that never came.
- The shard's screenshots show which variant of a looped test failed: the one whose shot is missing.
- A mock's delay is no hold when the client has its own timeout. Make the app never ask, or hold in the app.

## Keep doing
- Read the activity timing before touching a timeout.

## Open
- Nothing.

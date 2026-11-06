---
schema: 1
kind: pr
pr: 656
title: The rehearsal airs the TV on a seeded previous release; secrets store system writers as no user
date: 2026-11-06
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
---

## What happened
- No release could be tagged after v0.3.81: update-rehearsal scenarios 02, 11 and 12 failed on every push. #635 was only the first push after the tag, not the cause.
- The rehearsal updates from the newest release, and since v0.3.81 that release has Epic 51, where the database decides which TV parts run. The saved test homes had the TV slots off, so turning them on through `server.toml` no longer worked.
- A helper now turns the TV slots on through the components apply call (as Settings does) and restarts the home. Scenario 12 now checks that an update leaves a seeded home's choices as they were, since the previous release already seeds the components.
- The same logs showed the cluster CA's sealed copy had never been saved: it wrote `"cluster-ca"` into a uuid writer column, the insert failed and the error was dropped. System writers now record no user (`""`).

## What we learned
- Once a release that changes how state is owned ships, rehearsal fixtures built for the old owner break on the next push, not on the PR that made the change.
- A test that writes straight to `extensions` is only mirrored into components when the API starts (`RepairMirrors`); in scenario 12 that start was the update's restart, so the test saw a change the product didn't make. Drive state the way the owner does.
- The affected-tests gate wants real tests for changed files (`ca.go`, `controller.go`); a test of the in-memory secrets store's writer field gave both.

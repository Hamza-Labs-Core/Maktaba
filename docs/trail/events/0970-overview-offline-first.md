---
schema: 1
kind: pr
pr: 970
title: "53.12: an offline machine ranks first on the Overview"
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [960, 971]
---

## What happened
- An approved machine is offline when neither it nor any of its workers was seen in the last 60 s, the Machines page's own rule (`clusterh.Handler.OfflineMachines`), so Machines and Dashboards agree. A stale latest sample was not used: the store cannot tell an offline machine from a removed one (both keep their lines), and a joined machine with no lines would never show.
- On the Overview the API asks the cluster for the offline machines and passes them to the logs role as `offline=`; a caller's own `offline=` is always dropped and other boards never get one. If the cluster cannot be read, the board still answers, ranked by values, and a warning is logged. The cluster handler is mounted after the logs routes, so an `atomic.Pointer` set in `MountP31` joins them.
- The log index ranks offline machines first, then the existing order, and lists them in `machines.offline` (always present); an offline machine with no lines is still counted and shown. At most 1,000 ids of 128 bytes each; the 50-series and 2,000-point bounds stand.
- Web and both Remotes show an Offline line, "(offline)" in the machine picker, and on an offline machine's own view a line saying its lines end at its last values, in English and Arabic. Spec 53.12 gained test rows T10–T12.
- CI took four rounds:
  - gofmt in a test file;
  - the unit test-time budget, solved by a 9-machine fixture, and on the conflict with #971 its `TestBoards` was kept;
  - the Remote UI test read "Disconnect", because the Offline line's identifier sat on its SF Symbol icon;
  - a dropped simulator tap on Player iPhone 1/6 (#639), rerun once.

## What we learned
- A SwiftUI `Label` with `.accessibilityIdentifier` can expose the SF Symbol's spoken name as the element's label: use `Text` when a UI test reads the words.
- Every Overview test using the 60-machine fixture pays about 0.6 s under `-race`: size a new test's fixture to what it asserts.
- `ci-success` fails whenever any other job fails, so a "rerun only if the flaky job is the sole failure" check must leave it out.
- The affected-tests gate does not run gofmt: run it before every push.
- Compile an app locally (no simulator) after an untested UI fix, so a build error does not cost a full Apple CI round.

## Open
- The Overview names machines by node id (picker, Offline line, legends), not by their names.
- The four 60-machine Overview tests from #960 take 0.6–0.7 s each under `-race`, over the 400 ms soft cap.

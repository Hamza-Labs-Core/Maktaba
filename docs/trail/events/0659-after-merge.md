---
schema: 1
kind: pr
pr: 659
title: After the merge of #659 — the components conftest clash, and a stale local server binary
date: 2026-11-06
session: f0e36dc6-64c3-44f2-980b-fac44d831b70
issue: 670
---

## What happened
- After #659 merged, a local run that collected `pipeline/tests/components` and the root `tests/components` in one pytest session failed at collection (`ImportPathMismatchError`): both now have a `conftest.py` imported as `tests.components.conftest`. CI never collects both together, so it is not affected; filed as #670.
- A local run of `test_server_live.py` (#666) failed with the API crash-looping: the scratch binaries had been built before slot 0184 merged, and the API refuses a database newer than it knows ("the database's schema is at 0184 and this maktaba-api knows migrations up to 0183"). Rebuilt from the branch, it passed; CI builds them from the commit.
- The PRs of this session merged in the order #662 (slot 0184), #666, #665, #668, #659, #663 (slot 0185). Each changelog entry inserted at the top of `### Fixed` conflicted with the next PR's: moving #663's entry to the end of the first `### Added` block stopped the cycle of merges of main and CI reruns.

## What we learned
- A `conftest.py` added to the root `tests/components` package collides with `pipeline/tests/components/conftest.py` under pytest's default import mode.
- Rebuild the server binaries a local scenario runs after main gains a migration; the API refuses a newer schema by design.
- `cmd/maktaba-server`'s `TestHeldTVStaysOnItsStreamingAcrossTheAppUpdate` fails on a Mac running the live Maktaba (its TV slot holds port 8792), on main as on any branch: not a code failure.

## Open
- #670: one pytest session for both components test folders.

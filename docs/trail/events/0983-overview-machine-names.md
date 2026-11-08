---
schema: 1
kind: pr
pr: 983
title: "53.12: the Overview names machines; lighter 60-machine tests"
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [960, 970]
---

## What happened
- The Overview answer carries `names: {<node id>: <name>}` from the cluster data the Machines page uses (the home's own `machine=home` gets the home's name). Only the ids the answer mentions are named.
- Names are display text only: control and bidi-override characters are dropped, spaces trimmed, and the name is cut to 64 characters with an ellipsis. A machine with no name, or every machine when the cluster cannot be read (`dashboard_names_failed`), shows its node id. The node id stays what clients send as `machine=`.
- Web and both Remotes show names in the picker ("Attic NAS (n07)"), the Offline line and chart legends, in English and Arabic.
- The four 60-machine Overview tests record their data once into a 51-machine, 4-component fixture and each opens its own copy; three now take 0.09–0.14 s under `-race`, with every assertion kept.
- CI: one combined rerun of three unrelated failures: plugins/first-party (below), a lost runner on Player iPhone 3/6, and a dropped simulator tap on Player iPad 4/6 (#639).

## What we learned
- An API handler that adds a field to an answer proxied from the logs role buffers that role's answer and re-encodes it, so the log index stays ignorant of the cluster.
- On this store half a second of `Record` under `-race` is mostly SQLite WAL locking: share one recorded database file and copy it per test for fixtures several tests read.
- Piping `watch-merge2.sh` into `tail` hides its exit code: read its last output line instead.

## Open
- `plugins/first-party/classify/tests/test_classify_parity.py::test_classify_wave_1_is_identical` (story 54.31) already takes about 10 s on main against a 5 s soft cap and hit 16–19 s here, past the 15 s hard cap: it fails PRs on slower runners and needs its owner.
- `TestOverviewManyMachines` is about 0.5 s under `-race` (over the 400 ms soft cap) because it pays the one-time recording.
- Android decodes `names` strictly; a non-text value would fail the whole answer (the API always sends text; web and Apple skip bad values).

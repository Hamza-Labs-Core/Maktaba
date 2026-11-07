---
schema: 1
kind: pr
pr: 788
title: The Android TV library test's request list is safe to read while the fake server writes
date: 2026-11-07
session: 14c6f63b-ff36-454d-9735-8989bcb48e6d
prs: [787]
---

## What happened
- On #787, `LibraryViewTest.theCollectionsTabKeepsOneKind` failed with `ConcurrentModificationException` at `urls.any { … }`. #787 did not touch the library view. The race was in the test on main.
- `urls` was a `Collections.synchronizedList`. Its add is locked, but iterating it is not, so the fake server's thread could add a request while `waitUntil` walked the list. It is now a `CopyOnWriteArrayList`.

## What we learned
- `Collections.synchronizedList` does not make `any`, `filter` or a `for` loop safe. A list that one thread writes while another iterates needs a copy-on-write list, or a lock held around the read.

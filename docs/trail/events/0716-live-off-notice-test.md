---
schema: 1
kind: pr
pr: 716
title: The Live TV off notice is looked for before it hides itself
date: 2026-11-06
session: 14c6f63b-ff36-454d-9735-8989bcb48e6d
---

## What happened
- An iPad UI test failed three times on PRs that don't touch the Player. It looked for a notice that hides itself after 5 s only after slower checks, and a loaded simulator spent the 5 s on those. The test now waits for the notice first.

## What we learned
- Before calling a UI test a flake, read what it waits for: a self-dismissing element checked after other queries is a race on a loaded simulator. The fix is the test's order, not a longer timeout or another re-run.

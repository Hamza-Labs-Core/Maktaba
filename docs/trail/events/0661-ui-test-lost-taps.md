---
schema: 1
kind: pr
pr: 661
title: A Player UI test failure after a lost tap says the simulator dropped it
date: 2026-11-06
session: t3-delegate-aa865d4e-422b-48fe-b7d8-1a0afef5db09
---

## What happened
- #639: synthesized taps are sometimes dropped by a simulator digitizer race. A Debug-only hook in the Player counts touches the system cancels; a test that fails after one now starts its failure with a pointer to #639.
- The one-line start call in `PlayerApp.swift` used the gate's `No-Test-Change` trailer: the tests the gate maps to that file are the Mac Player's, where the call is compiled out.

## Open
- Unverified: whether a tap lost to the real race shows up as a cancelled touch in the app. Only a CI run that hits it (about once in a few hundred taps) will show it; if the touch never reaches the app, failures read as before.

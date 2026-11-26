---
schema: 1
kind: pr
pr: 1779
title: Xcode 27 CI and Apple UI test follow-ups
date: 2026-11-26
session: 01a218c5-8e0d-71e0-bebb-8bcbb2172778
agent: codex
---

## What happened
- [PR #1779](https://github.com/Hamza-Labs-Core/Maktaba/pull/1779) merged to main as `cbd200509143ccfcc20bcbe13a0ef42110af09ec`, closing [#1487](https://github.com/Hamza-Labs-Core/Maktaba/issues/1487), [#1702](https://github.com/Hamza-Labs-Core/Maktaba/issues/1702), and [#1706](https://github.com/Hamza-Labs-Core/Maktaba/issues/1706). The work was part of [#1740](https://github.com/Hamza-Labs-Core/Maktaba/issues/1740).
- Apple release jobs select released Xcode 27 for the required SDK. The batch hardened simulator service and log collection, made log collection failures visible in CI, and updated Cast, tvOS, Android TV, and Fire TV test coverage.

## What we learned
- Player UI tests start with Cast disabled by default and Cast scenarios opt into the context they exercise. CI requires the real SDK test to find the SDK configured with the expected receiver and verify discovery after the local network prompt.

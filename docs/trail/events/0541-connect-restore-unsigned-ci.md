---
schema: 1
kind: pr
pr: 541
title: Saved sign-in restore UI test runs on unsigned CI
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge baa7e1ec. `PlayerConnectProgressUITests` sign-in-and-restore no longer skips on unsigned CI. Cause of the skip: an unsigned iOS app cannot use the Keychain (`errSecMissingEntitlement`), so no token survived the relaunch.
- Decision: a Debug-only `-maktaba-secrets-in-file` seam puts `TokenStore` on `UITestSecrets` (JSON in Application Support, removed by `-maktaba-session-reset`); `PlayerUITestCase.launch` always passes it. The Keychain stays covered by the `MacKeychain`/`Tokens` unit suites.
- Bug the skip hid: `RestoreSection`'s Section-level `accessibilityIdentifier` replaced the progress row's `connect.progress.<step>`, so the restore steps were invisible to VoiceOver and tests. Fix: no id on the Section; `signin.restoreProblem` on the problem text.
- All 4 tests pass on iPhone and iPad CI.

## What we learned
- In SwiftUI an identifier on a wrapping Section replaces its children's ids; a wrapping container does not help (SwiftUI merges it and the outer id wins).
- A skipped test can hide a real bug.
- The scratchpad is shared between sessions, so `xcodebuild -resultBundlePath` needs a unique subfolder.

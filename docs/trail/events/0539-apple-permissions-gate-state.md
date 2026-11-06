---
schema: 1
kind: pr
pr: 539
title: Permissions walkthrough stayed up after Continue: @State init side effect
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Folded into #539 (PlayerVideoUITests deep-link test). `PermissionsGate`'s `@State` initializer called `forThisApp()`, which under `-maktaba-permissions-reset` deleted "seen" every time the app's body ran, so the next refresh showed the sheet again.
- The reset now runs once from the app's `init` (`applyLaunchArguments`), and `forThisApp()` has no side effects; `buildingTheAppsModelAgainKeepsSeen` fails on the old code. Branch `fix/permissions-gate-state-init` @ 0569cb052.

## Keep doing
- A `@State` initializer must have no side effects: SwiftUI runs it on every body evaluation.

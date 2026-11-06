---
schema: 1
kind: pr
pr: 480
title: "48.10: the Apple brand, dark by default"
date: 2026-11-01
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
`ThemeMode.storageKey` registers `dark` as the default, so the apps' `@AppStorage(…) = .system` reads dark without editing other stories' files.

## What we learned
- The plan's hand-edited `Info-*.plist` launch screen cannot work, because xcodegen regenerates it. The launch screen lives in `project.yml`'s `IOSApp` template.
- A new `UITests/*.swift` file needs `xcodegen generate`, or the run executes 0 tests.

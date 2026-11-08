---
schema: 1
kind: pr
pr: 936
title: Plugin panel snapshots on Android TV, Apple TV and the web found four real bugs (54.20 T05)
date: 2026-11-08
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [787, 783, 893, 929]
---

## What happened
- Issue #741's other items had already landed: the native series, library and TV pages (#787), the admin panel order (#783), the search chips, and the Apple Mac page snapshots (#893, #929). #936 added T05 (en and ar, dark, truncation) for the three apps that lacked it, and #741 is closed.
  - **Android TV:** Roborazzi goldens of the real video page on a 1080p TV.
  - **Apple TV:** the TV form of `PluginPanelView` was compiled for tvOS only. The environment value `pluginPanelsTVForm` now selects it, so #893's Mac harness draws it at 960×540, with image and text references. It uses the Mac's text sizes, since CI has no tvOS snapshot job.
  - **Web:** a vitest text reference of the series and library pages, and a check that every panel colour is a token both themes define.
- The checks found four bugs, all fixed in the PR:
  1. The Android TV theme set only TV Material's colours, so the panel (a phone component) fell back to Material 3's light defaults: a pink card on a dark page.
  2. The Android TV video page did not scroll, so a tall panel's marker was out of reach.
  3. `PluginPanel.css` named `--mk-*` variables that no stylesheet defines, so it always drew its light-grey fallbacks.
  4. The Apple Mac job would have failed every Apple PR from 2026-11-08: a fixture date written as "20 days before now" moved #929's text references every day.

## What we learned
- Compose's "text is shown" checks pass for content that is clipped or scrolled out of view. Only a screenshot caught the cut-off panel.
- A CSS variable with a fallback hides a wrong name, because the fallback always draws. Test that each variable is defined.
- Text references that contain dates need a fixed clock (`MockHome.clock`), not `{{now-N}}` fixtures.
- An app that sets only TV Material's theme must also give Material 3 its palette for any phone component it shows (`MaktabaTvTheme` now does).
- `tools/apple/snapshots.py <run>` copies every page's images. When bootstrapping new references, keep only the new ones and restore the rest from git.
- Homebrew `openjdk@17` plus `~/Library/Android/sdk` is enough to record Roborazzi goldens on the Mac, which has no system Java.

## Keep doing
- Bootstrap Apple references from a CI run that fails on purpose ("no reference"), and read every image before committing it.

## Open
- No tvOS snapshot job: the Apple TV form is drawn on the Mac host.

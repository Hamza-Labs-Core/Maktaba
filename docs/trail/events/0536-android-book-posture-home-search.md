---
schema: 1
kind: pr
pr: 536
title: Android book posture: Home and Search split at the hinge
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge d907aba7. Home's rows go to two pages (`bookPages` in HomeScreen.kt; with a hero the start page takes the smaller half). Search shows results on the start page and `VideoScreen` for `SearchModel.chosen` (else the first result) on the other. New string `search_preview_hint` (en+ar). In RTL the start page is the right one.
- androidTest in core and the TV app moved to `junit4.v2.createComposeRule`.
- The first push was blocked by the affected-tests gate (SearchModel.kt without SearchModelTest.kt); fixed with real tests, not a `No-Test-Change` trailer.

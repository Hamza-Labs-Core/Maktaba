---
schema: 1
kind: pr
pr: 539
title: Apple app names in Arabic
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [568]
---

## What happened
- #568, folded into #539. Player (iPhone/iPad) and TV InfoPlist.xcstrings localize CFBundleDisplayName "Maktaba" / «مكتبة».
- The Mac Player has its own `Player/Mac/InfoPlist.xcstrings` (CFBundleDisplayName and CFBundleName "Maktaba Player" / «مشغل مكتبة»), so the menu bar no longer says "Maktaba Player Mac".
- `test_apple_app_names.py` requires one InfoPlist catalog per app, an Arabic display name containing «مكتبة», and en+ar for every Info.plist key people see.

## What we learned
- Finder still shows "Maktaba Player Mac.app": macOS needs the file name to match the display name.

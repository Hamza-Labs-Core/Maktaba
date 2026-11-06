---
schema: 1
kind: pr
pr: 560
title: App Store rejected the name Remote: Maktaba Remote
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge 338f813c2. App Store Connect rejected Maktaba Remote 0.3.55 (build 20261102.182725) with ITMS-90129: the display name "Remote" was already taken.
- The iPhone/iPad display name is now "Maktaba Remote" (Arabic «ريموت مكتبة») in project.yml, the regenerated pbxproj and Remote/InfoPlist.xcstrings; bundle name and ids unchanged. Player, Player Mac and TV already started with Maktaba.
- New `tools/ci/tests/test_apple_app_names.py` (lint/repo): every Apple app's display and bundle name in every localization starts with "Maktaba" or contains «مكتبة», and the pbxproj matches project.yml. The next tag re-uploaded the Remote.

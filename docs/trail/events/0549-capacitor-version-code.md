---
schema: 1
kind: pr
pr: 549
title: Capacitor app versionCode from play-version.sh
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge 9602104a. The Capacitor app (mobile-release.yml, `com.hamzalabs.maktaba`) takes versionName/versionCode from `tools/release/play-version.sh`, the #545 rule, with no formula of its own. The version step runs before any build, so a bad tag fails first.
- Before, a pre-release shared its release's code (v0.3.0-rc.7 and v0.3.0 both 3000). Highest old code 3055 (v0.3.55, run 41498853328); the new rule's first after it is 305601 (v0.3.56-alpha.1), so no offset was needed.
- Tests: play-version-test.sh and test_play_release.py. The next tag was the first real run.

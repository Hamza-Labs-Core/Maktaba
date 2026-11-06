---
schema: 1
kind: pr
pr: 447
title: TestFlight upload on tags, and the three fixes it took
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
`.github/workflows/testflight.yml` (#447) uploads the iPhone Player and Remote to TestFlight on
every release tag; see docs/testflight.md. Player and Remote 0.3.44 were the first uploads (builds
20261031.214201 and 20261031.214159). Getting there took three fixes.

## What we learned
- The App Store Connect API cannot create apps, and `xcodebuild` reports `missingApp`. The two app records (`com.hamzalabs.maktaba.player`, `com.hamzalabs.maktaba.remote`) were created through Xcode's Organizer.
- Xcode's create-record step reported an error although the POST returned 201: its immediate re-check did not see the new app yet. The record was there.
- The upload then failed with "Cloud signing permission error": the CI's API key needs the Admin role (or App Manager with cloud-managed certificate access). The owner changed the role and both apps uploaded.

---
schema: 1
kind: pr
pr: 625
title: Maktaba Remote iPad joins TestFlight
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 4a44ae6fc. Maktaba Remote iPad (`com.hamzalabs.maktaba.remote.tablet`) joined the TestFlight matrix once its App Store Connect record existed (created through Xcode Organizer) and its first build, 0.3.73 (20261104.001839), had been uploaded from Xcode.
- Every `v*` tag now uploads six Apple apps, and the Apple apps check builds the iPad Remote scheme on pull requests. `tools/ci/tests/test_testflight.py` fails if a TestFlight app is not built by a pull-request job.

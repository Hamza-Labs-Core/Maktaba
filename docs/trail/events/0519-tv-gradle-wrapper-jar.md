---
schema: 1
kind: pr
pr: 519
title: v0.3.51 had no Play bundles: the TV wrapper jar was not Gradle's
date: 2026-11-02
session: unknown
source: claude-trail.md
---

## What happened
- play-bundles.yml on v0.3.51 stopped at the "Gradle wrappers are Gradle's" check: apps/tv/android named Gradle 9.8.2 but its jar was not 9.8.2's (sha cb0da675...). The phone apps' jar matched.
- Fix (#519, merge 282eee50a): the TV app takes the phone apps' verified 9.8.2 wrapper (jar, gradlew, gradlew.bat). _android-tv.yml runs the same check on every PR, so a wrong jar fails the PR, not the release tag.
- v0.3.51 stays without .aab files; the next tag carries them.

## What we learned
- A check that runs only on the release tag finds the problem after the tag is cut. Run the same check on every PR.

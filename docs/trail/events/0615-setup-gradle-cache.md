---
schema: 1
kind: pr
pr: 615
title: Gradle builds cached with setup-gradle; Google repo only for Android groups
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 02265023c. `desktop-player (linux)` failed on #604 because the Gradle jobs' `actions/cache` entries were evicted from the over-full Actions cache (11.6 GB of 10 GB, mostly pull-request Go and Rust caches), so every run downloaded all plugins and dependencies, and one wrong answer from Maven Central failed the build.
- The desktop Player, Android apps and Android TV jobs use `gradle/actions/setup-gradle@v6.4.0`, keyed on the wrapper, catalogs and build scripts, with only main writing.
- Google's repository serves only the `com.android*`/`com.google*`/`androidx*` groups in all three builds. A read-only PR run restored all 8 entries on exact keys.

## What we learned
- PR caches can evict main's from the 10 GB Actions cache, making every run download everything and exposing it to registry hiccups.

---
schema: 1
kind: pr
pr: 742
title: Plugin panels declared as data, drawn by every app (Epic 54, story 54.20)
date: 2026-11-06
session: 28c0b780-1dd6-4ba0-b27f-a3382e1c1cd1
issue: 741
---

## What happened
- Panels (`ui/panel.json`, ui@1) with plain-text rules checked on `data.put`, `GET /api/panels` for everyone who can see the subject while the plugin is on, the `plugin_panels` capability (read missing as off), and renderers on the video page of the web, the Apple Player (MaktabaKit) and the Android Player (core), each tested with hostile values.

## What we learned
- A new catalog feature reaches far: the components snapshot test counts features, `tools/ci/catalog_words.mjs` wants its off and unavailable sentences on the web, Android and Apple alike, and MaktabaKit counts its feature names. Grep the last feature's id across `apps/` and `web/` before adding one.
- The xcstrings file and the wiki's Arabic JSON both lose their formatting to a JSON rewrite: insert entries as text at their sorted place.
- `go test` timing on this Mac at load average 270 swung a 10 ms median to a 165 ms tail. A p95 assertion is a flake source: hold the median, log the tail.
- `home.features()` in the Android Player is composable: read it in the screen body and key the effect on it.
- Android's `client` module is plain JVM (`:client:test`); its `core` and `player` are Android (`testDebugUnitTest`). Gradle needs `JAVA_HOME` pointed at Homebrew's `openjdk@17` on this Mac.

## Open
- Series and library pages, the TV apps, Android chips searching, an admin order and the T05 snapshots (issue #741).

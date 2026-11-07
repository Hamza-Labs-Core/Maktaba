---
schema: 1
kind: pr
pr: 795
title: The Updates page names the plugins an update will pause (Epic 54, 54.07 §3)
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
---

## What happened
- `plugins.PausedBy` lists the installed plugins whose `maktaba` range excludes a version (the gate's own `components.VersionInRange`); the Updates overview fills `plugins_paused` on each catalog release and the plan's target, and the Updates page lists them under the release it is about to install. The update is not blocked. Closes #709.

## What we learned
- The spec placed this list in the updater's packs step, which runs during the update on each machine and never reads the plugin tables; the home's Updates overview answers it from `plugin_installs` before anything updates, so the page can show it in time. The spec now says so.
- The Updates page's tests render it without a router; a `Link` on the page needs the test wrapped in a `MemoryRouter`.
- The affected-tests gate maps a router file to the router package's tests: a hook added in `p35.go` needs a router test of its own, even when the package it wraps is tested.

## Keep doing
- Run the gate locally (`cd tools/affected-tests && go run . gate -C ../.. --range origin/main...HEAD`) before the push; macOS has no `timeout`, so `perl -e 'alarm shift; exec @ARGV' 600 …` bounds it.

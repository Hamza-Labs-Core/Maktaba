---
schema: 1
kind: pr
pr: 679
prs: [667]
title: Seven fixes from the review of the desktop Player's sections
date: 2026-11-06
session: fa4794fc-4806-44e8-b662-381bbd6a740c
---

## What happened
- A review of #667 found seven defects; all seven checked out against the code and are fixed, each with a test that fails on the code from before.
- The biggest one: `Playback`'s track and quality changes ran on the window's scope, so Close and the next video did not stop them. They now run under the video's job, and `open()`/`reload()` check a generation counter after every wait.
- Playback now drives a `Video` seam (`FxVideo`/`FxPlayers`), so `PlaybackTest` runs it with fake players against a MockWebServer home. Direct-play files (`direct_url` `.mp4`) skip `waitUntilPlayable`, which keeps those tests fast.
- Extras: the stream proxy relays only the remote session's own Cloudflare host (`relaySession`). `Navigator.follow` now closes a Channel page on any stack when Live TV goes off. That bug was real: a channel opened from Home stayed open.

## What we learned
- A save launched on a Compose `rememberCoroutineScope()` dies with its composable. A LazyColumn row goes away when its last card does, so "Not interested" cancelled itself and `putBack` ran. Saves that must reach the home go through `DesktopHome.change` (the background scope's job on `Dispatchers.Main`). The app's `background` scope runs on `Dispatchers.IO`, and the page states are not built for that thread.
- The failure differed by page. Video page saves flipped back on cancel. Libraries and Series saves (the `loading` helper) only lost the home's answer, so their test checks that the home's order is applied.
- Within one ticket, a paging read must not move its cursor before the `ReadOrder.fresh` check. Return the page and apply it only when fresh.
- In tests, a single-thread "main" dispatcher deadlocks when a `runBlocking(main)` helper runs inside another one. Read state directly inside the block.
- `./gradlew :client:test test --tests X` fails in `:client` when it has no match. Filter `:test` alone.

## Keep doing
- Verify that each test fails without its fix by checking out origin/main's versions of the source files on a WIP commit, then restoring them.

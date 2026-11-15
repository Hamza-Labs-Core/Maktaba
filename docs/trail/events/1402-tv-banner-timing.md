---
schema: 1
kind: pr
pr: 1402
title: "TV UI test: Menu with the banner up, without racing the banner's 6 s"
date: 2026-11-15
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1402 merged as 343e5187e. `TVLiveUITests testMenuAlwaysLeavesTheLivePlayer` failed on main
  and on Apple PRs: on a loaded runner the check that the channel plays took about 5 s, and the
  app hides a playing channel's banner 6 s after the start (by design), so "the banner is up"
  was already false. The step came in with #1319. The test now presses Play/Pause, which raises
  the banner for its full time, then Menu at once, and checks the player stays; the next Menu
  leaves as before.
- Its first CI runs failed only on the StreamProxy test flake; it merged after #1405 with main
  brought in.

## What we learned
- A UI assertion on something the app hides on a timer must raise it first and act with no
  query in between; runner query time eats the window.

---
schema: 1
kind: pr
pr: 1653
title: "Nothing about broadcasting in the help or the settings while it is off"
date: 2026-11-24
session: ab31266a-f317-49e9-896a-bcd2542713f1
agent: claude-code
---

## What happened
#1653, stacked on #1624, closes what #1624 left open:
- **Help:** the Broadcast access topic is gone from the in-app help and from maktaba.tv/help (EN, AR). Its addresses 301 to the help index.
- **Rules topic:** its Broadcasting section is gone.
- **Home web app:** the Rules tab, the Components row and the Subscription line for broadcasting show only while the licence carries the `broadcast` flag.
- **Spec 50.36:** the help topic that comes back with broadcasting is written invite-only.

## What we learned
- **The app's help and maktaba.tv's help are one source** (`web/src/help`). Removing a topic also changes the counts in `web/packages/website/scripts/help.test.mjs`, `dist-manifest.json`, the help index's 8 references, and `e2e/representatives.json` when the topic was a representative (Broadcast access was the shortest; Word review is now).
- **`vitest run` alone passes on a removed topic.** The counts live in the `node --test` half of `npm test`; run the whole script.
- **A CI shard can fail without running a test.** #1624's `Player UI tests (ipad 1/6)` waited 30 minutes for `Player UI tests build (ipad)`, which was still queued for a macOS runner, and ended with "no build in 30 minutes". The shard's wait counts the build's queue time, so a starved macOS pool fails shards that are scheduled before their build.
- **Docker Desktop's VM crashed on the owner's machine** (14:40 UTC). `docker desktop start` said it was already running; `docker desktop restart` brought it back.

## Keep doing
- Stacking a follow-up as a draft on the code pull request's branch: it starts no CI run of its own (and no macOS jobs) until its base becomes main.

## Open
- Once #1624 merges, #1653 needs main merged in and a push, so its CI runs against main.
- The UI-test shards' 30-minute build wait still counts time the build spends queued.

---
schema: 1
kind: pr
pr: 1624
title: "Broadcasting stays off, and no copyright surface anywhere"
date: 2026-11-24
session: ab31266a-f317-49e9-896a-bcd2542713f1
agent: claude-code
---

## What happened
On 2026-11-24 it was decided that home broadcasting stays off for now and that nothing about copyright is shown anywhere. The decision came through the decisions inbox (`broadcast-invite-only-copyright`). #1624 makes the code match:
- **Edge:** a `BROADCAST_ENABLED` switch, off. While it is off, watch and live answer 404.
- **Open-access gap:** closed. A visitor with no pass gets no channel list or guide from the edge.
- **Cloud:** `/v1/auth/providers` says whether broadcasting is on, and the plans list carries the broadcast catalog only while it is.
- **Web apps:** the cloud and home web apps hide their broadcast pages.
- **Native apps:** a build-time `GuestBroadcast` switch (off) on Apple and Android, phone and TV.
- **maktaba.tv:** the copyright page is gone; its addresses 301 to the terms page.
- **maktaba.dev:** its Watch a broadcast entries are gone.

#1613 (the spec) was updated in the same turn: 50.24 is deferred, and the gate's item 4 says deferred rather than waiting on a decision.

## What we learned
- **The edge served pages without a pass.** `lineup` and `apiGuide` fell back to `openChannels()`, so a publisher's grid and guide rendered for anyone while an open channel existed. A visitor with no pass now gets nothing at all.
- **The apps had no broadcasting switch.** "Watch a broadcast" was gated only by the parental limit, so a build-time switch was the smallest safe off. A remote flag would need a source that doesn't depend on a home, because guests have none.
- **The home's broadcast flag already came from the cloud's licence.** `broadcast.enabled` off withholds it. Only the Broadcast access nav entry ignored the flag.
- **The footer change barely moved maktaba.tv's reference images.** Removing the footer's Copyright link changed most pages by less than the 0.1 % pixel threshold. Only 22 references were re-rendered (in the CI image, through Docker), and 8 were deleted.

## Keep doing
- Splitting a cross-platform change into disjoint scopes (apps, website, everything else) for parallel agents in one worktree. The lead keeps the CHANGELOGs and the commit.

## Open
- The in-app help topic **Broadcast access** (and its copy on maktaba.tv/help) still exists. It describes a page that no longer shows.
- The broadcast edge's own legal and Report pages and the cloud's moderation code are dormant, not removed.
- #1613 and #1624 are held by the merge freeze.

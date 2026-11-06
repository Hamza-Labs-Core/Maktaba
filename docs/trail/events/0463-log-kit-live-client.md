---
schema: 1
kind: pr
pr: 463
title: "48.7: MaktabaKit's Live client"
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
MaktabaKit's `Live.swift`: now, the guide window, one channel's guide, the logo, `tv/info`, the LAN playlist, and remote tune, heartbeat and end, as an `APIClient` extension.
`LiveSource` picks LAN or relay from `tv/info`, tunes through 202s, and heartbeats every 60 s. A new token is swapped in through `rewrite(_:)`, with no new tune and no new player item.

## What we learned
- Premise gap: an extension in another file cannot POST or DELETE, because `APIClient.send` and `sendRaw` were `private`. Made both internal (two words in `APIClient.swift`). 48.6 (`Browse.swift`) needed the same change.
- Decided: the clock is an injected `sleep`, as `waitUntilPlayable` already does. The tests use a hand-advanced `FakeClock` and a `FakeLiveAPI`.
- The HTTP tests use their own `LiveStub`, separate from the shared `Stub` other suites use.

## Open
- Left to story 48.21: the encoder warm-up retry on the LAN path, and the AVPlayer side of `rewrite(_:)` (a resource loader or proxy).

---
schema: 1
kind: pr
pr: 458
title: Epic 48 spec, the native Players
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Wrote `specs/epics/48-native-players/` after a review found the Players thin.
Epic 42 asked only for libraries and playback, so its checks passed with no Home, nav, Live TV or brand.
Found a live security gap (G1): the web port's `/stream/channel/{id}` gate checked sign-in only, not the channel's library (`cmd/maktaba-server/stream_gate.go`). Story 48.3 fixes it first, with role tests.

## What we learned
- Decided: Live TV for the TV apps is in Epic 48 (own track, files disjoint). Downloads, the Remote's screens and the Windows/Linux player are follow-ups.
- Decided: one token source in `web/design-system/tokens/` generating Swift and Kotlin. Its dark set was stale grey; the shipped values are in `web/src/styles/tokens.css`.
- Both apps defaulted `ThemeMode` to System, so a light phone started light.
- The Apple Player's iOS picture was SwiftUI `VideoPlayer` (no PiP).
- CI built the Apple apps but ran no UI tests, and ran no Android Player tests.

## Open
- The PR's five open questions were left for the owner.

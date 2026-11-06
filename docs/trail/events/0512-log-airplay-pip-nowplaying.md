---
schema: 1
kind: pr
pr: 512
title: "48.24: AirPlay, Picture in Picture and Now Playing in the Apple Player"
date: 2026-11-01
session: local_7c0a4c88
source: claude-trail.md
---

## What happened
The Apple Player got an AirPlay route button, PiP (button, and on going home) and Now Playing with remote commands, for videos and live TV, in English and Arabic.
A receiver cannot reach the app's loopback `StreamProxy`. A video hands it the signed session URL (48.12).
A channel at home has no signed URL (the web port's gate keeps channel paths to a signed-in caller), so a second `StreamProxy` listens on the phone's LAN address while AirPlay is on, serving that one channel under its own secret: no server change.
Away from home, the Cloudflare signed URL is handed over and re-handed at each new token (about every 30 min). The phone stays in the loop either way (progress, heartbeat).
Work of a side session of `local_7c0a4c88`.

## What we learned
- A closure a system framework calls on its own queue (`MPMediaItemArtwork`'s image, KVO handlers, `MPRemoteCommand` targets) must not be made inside a `@MainActor` method: Swift 6 gives it main-actor isolation and it traps off the main thread. Make it in a `nonisolated static func` and hop on. The first CI run of this PR crashed every watch test that way.
- The iPhone simulator reports no Picture in Picture (the iPad one floats it). The PiP UI test asserts there is no PiP button on iPhone, with no skip: tests are never skipped.
- `AVRoutePickerView` adds its inner button late on iPad; `AirPlayButton.Picker` names every control in it once it is on screen.
- The Mac menu helper clicked View ▸ Profile when asked for Live TV. A coordinate on a menu item finds it again by identifier, and every SwiftUI menu item's identifier is `menuAction:` (the recording showed the pointer move from Live TV to Profile). The helper now clicks the item's point from the menu bar item, whose title is unique.

## Keep doing
- Make closures for system-framework callbacks in a `nonisolated static func`.

## Open
- Playback on a real Apple TV is not verified (no receiver in CI). The spec box stays unticked until the owner checks it by hand.

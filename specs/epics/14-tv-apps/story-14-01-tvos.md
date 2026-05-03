# Story 14.1 — tvOS app (Swift / SwiftUI)

A native tvOS app targeting tvOS 17+, distributed via TestFlight then App
Store.

**Anchors:** [`architecture.md` §6.5](../../architecture.md).

## AC

- Built with SwiftUI for tvOS using the native focus engine.
- Top Shelf integration: "Continue Watching" surfaced on the Home
  screen.
- Tabs: Home, Library, Search, Settings (trimmed — see
  [README cross-cutting checklist](README.md)).
- AVPlayer for HLS playback; HDR (HLG, Dolby Vision where the device
  supports it).
- Apollo iOS GraphQL client generated from `shared/graphql/schema.graphql`.
- Apple TV Remote: focus, swipe seek, Siri Remote click, double-tap.
- Server pairing: QR code on TV → scan with phone
  ([Story 15.5](../15-discovery/story-15-05-qr-pairing.md)).

## TC

- Cold launch on Apple TV 4K: ≤ 5 s to Home with a populated Continue
  row (assuming previous activity).
- Play a 4K HDR HEVC source: direct play succeeds; HDR metadata is
  preserved.
- Voice search "lectures about gratitude" → results within 1 s.

## EC

- Server unreachable: Home shows the last-cached rows with a banner.
- An item in Continue row points to a deleted video: row entry hidden,
  not crashy.
- App suspended mid-playback for 30 minutes: AVPlayer resumes; if the
  manifest expired, we mint a new session and resume from
  `position_sec`.

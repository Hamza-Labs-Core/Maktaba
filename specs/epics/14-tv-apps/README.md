# Epic 14 — TV Apps

**Goal.** Native tvOS (Swift / SwiftUI / AVPlayer) and Android TV
(Kotlin / Compose for TV / ExoPlayer) apps that consume the same GraphQL
schema as every other client. 10-foot UI, D-pad navigation, voice search,
and content rows tailored for living-room viewing.

**Anchors:** [`architecture.md` §6.5](../../architecture.md), §2.1
(TV stack).

---

## Stories

| # | Story | Status |
|---|-------|--------|
| 14.1 | [tvOS app (Swift / SwiftUI)](story-14-01-tvos.md) | spec |
| 14.2 | [Android TV app (Kotlin / Leanback)](story-14-02-android-tv.md) | spec |
| 14.3 | [10-foot UI design](story-14-03-10-foot-ui.md) | spec |
| 14.4 | [Voice search integration](story-14-04-voice-search.md) | spec |
| 14.5 | [Continue Watching row](story-14-05-continue-watching.md) | spec |
| 14.6 | [Recommendations UI](story-14-06-recommendations-ui.md) | spec |
| 14.7 | [API: recommendations endpoint](story-14-07-recommendations-api.md) | spec (added per REVIEW §3.2) |

---

## Dependencies

- **Epic 17** (Design System) Story 17.1 (tokens — Swift / Kotlin
  outputs).
- **Epic 7** Stories 7.4 (videos), 7.10 (sessions), 7.11 (watch progress
  with cross-device sync), 7.17 (GraphQL).
- **Epic 8** Stories 8.3 (direct play), 8.5 (HLS), 8.12 (chapters).
- **Epic 15** Story 15.5 (QR pairing) for first-run setup.
- **Epic 14.7** owns the recommendations API consumed by 14.6.

## Cross-cutting checklist

- **Settings parity:** tvOS / Android TV intentionally **trim** Settings
  to the absolute minimum — Account, Playback defaults, Subtitles,
  Sign Out. All other Settings live on web/mobile/desktop. (Resolves
  Open Question 3 from the original Epic 03 spec.)
- **D-pad first:** every flow completable with a remote alone.
- **Top Shelf / Recommendations channel:** Continue Watching surfaced via
  platform APIs (Apple TV Top Shelf, Android TV Home Channels).
- **HDR:** HLG and Dolby Vision passthrough where the device supports
  them; never up-/down-converted.

## Out of scope

- Game console apps (PS5, Xbox).
- Web TV browsers (Tizen, webOS) — covered by [Epic 15
  Story 15.4](../15-discovery/story-15-04-dlna-upnp.md) DLNA fallback.
- Settings parity with web (intentional — see above).

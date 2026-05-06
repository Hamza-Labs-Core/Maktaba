# Epic 14 — TV Apps

> **Status:** spec + plans complete. **Source:** `specs/epics/14-tv-apps/`.
> **Anchors:** [`architecture.md` §6.5](../../../specs/architecture.md), §2.1.

## Goal

Native tvOS (Swift / SwiftUI / AVPlayer) and Android TV (Kotlin / Compose for TV / ExoPlayer) apps that consume the same GraphQL schema as every other Maktaba client. 10-foot UI, D-pad navigation, voice search, HDR pass-through, and content rows tailored for living-room viewing. Both platforms intentionally trim Settings to the absolute minimum (Account, Playback defaults, Subtitles, Sign Out); all other configuration lives on web/mobile/desktop.

## Stories & Plans

| # | Story | Plan | Summary |
|---|-------|------|---------|
| 14.1 | [tvOS app](../../../specs/epics/14-tv-apps/story-14-01-tvos.md) | [plan-14-01](../../../specs/epics/14-tv-apps/plan-14-01-tvos.md) | tvOS 17+ SwiftUI app, AVPlayer HLS+HDR, Top Shelf extension, QR pairing, Apollo iOS GraphQL client. |
| 14.2 | [Android TV app](../../../specs/epics/14-tv-apps/story-14-02-android-tv.md) | [plan-14-02](../../../specs/epics/14-tv-apps/plan-14-02-android-tv.md) | Android TV 9+ Compose for TV, ExoPlayer HLS+HDR, Recommendations Channel, Apollo Kotlin. |
| 14.3 | [10-foot UI design](../../../specs/epics/14-tv-apps/story-14-03-10-foot-ui.md) | [plan-14-03](../../../specs/epics/14-tv-apps/plan-14-03-10-foot-ui.md) | TV type scale 28–96 pt, focus rings, D-pad geometry primitives, safe-area insets. |
| 14.4 | [Voice search integration](../../../specs/epics/14-tv-apps/story-14-04-voice-search.md) | [plan-14-04](../../../specs/epics/14-tv-apps/plan-14-04-voice-search.md) | Siri App Intents + `SFSpeechRecognizer` (tvOS); Google Assistant action + `RecognizerIntent` (Android TV); "did you mean" suggestions. |
| 14.5 | [Continue Watching row](../../../specs/epics/14-tv-apps/story-14-05-continue-watching.md) | [plan-14-05](../../../specs/epics/14-tv-apps/plan-14-05-continue-watching.md) | First Home row populated from `playback_state` (5–95 % watched), recency-sorted, cross-device sync via WS. |
| 14.6 | [Recommendations UI](../../../specs/epics/14-tv-apps/story-14-06-recommendations-ui.md) | [plan-14-06](../../../specs/epics/14-tv-apps/plan-14-06-recommendations-ui.md) | ≤5 rows × ≤20 items, localized titles + reasons, "Not interested" dismissal, server-cached 24 h. |
| 14.7 | [API: recommendations endpoint](../../../specs/epics/14-tv-apps/story-14-07-recommendations-api.md) | [plan-14-07](../../../specs/epics/14-tv-apps/plan-14-07-recommendations-api.md) | Nightly batch recommender (semantic similarity + speaker heuristic + library affinity + cold-start), 60 s in-process LRU. |

## Key technical decisions

- **Settings parity (intentional trim):** TV apps expose only Account, Playback, Subtitles, Sign Out. Resolves Open Question 3 from the original Epic 03 spec.
- **D-pad first:** every flow (pairing, search, player controls, library browse) completable with the remote alone. Focus is confined per row (`.focusSection()` on tvOS / `.focusRestorer()` on Android TV).
- **Top Shelf / Recommendations Channel:** Continue Watching surfaced via Apple TV Top Shelf (separate `MaktabaTopShelf` extension target reading shared App Group keychain) and Android TV Recommendations Channel (WorkManager + PreviewChannel API).
- **HDR pass-through:** HLG and Dolby Vision preserved end-to-end where the device supports them; never up-/down-converted. tvOS sets `appliesPerFrameHDRDisplayMetadata`; Android TV uses media3's `DefaultRenderersFactory` MediaCodec HDR profile selection.
- **QR pairing:** TV is the *issuer* (`POST /api/auth/pair`); phone is the *claimer* (`POST /api/auth/pair/claim`). The QR URL embeds `code`, `mid` (mDNS ID), `spki` (TLS pin hash), and `n` (32-byte nonce). TLS SPKI hash bootstraps TOFU pinning without prior LAN contact.
- **Recommendations algorithm:** semantic similarity over `media_features` embeddings + speaker-cluster heuristic (≥3 watches = "followed") + library affinity + cold-start fallback. Deterministic per user per day (sort by score DESC, video_id ASC); cached 24 h with nightly pre-warm.
- **Auth refresh single-flight:** `actor RefreshGate` ensures only one `POST /api/auth/refresh` runs at a time even with concurrent 401s.

## API endpoints consumed

- `POST /api/auth/pair`, `POST /api/auth/pair/claim`, `GET /api/auth/pair/{code}` (Story 14.1, 14.2; pairing surface owned by [plan-10-17](../../../specs/epics/10-auth-security/plan-10-17-auth-pair.md), QR extensions in [plan-15-06](../../../specs/epics/15-discovery/plan-15-06-pairing-api.md))
- `GET /api/me/playback-state?in_progress=true` (Story 14.5; canonical via [plan-11-02](../../../specs/epics/11-web-ui/plan-11-02-watch-progress.md), architecture §9.4)
- `POST /api/search`, `GET /api/search/suggest` (Story 14.4)
- `GET /api/recommendations?surface=tv-home`, `DELETE /api/recommendations/rows/{reason_kind}`, `DELETE /api/recommendations/items/{video_id}`, `POST /api/recommendations/refresh` (Story 14.6, 14.7)
- GraphQL: `home`, `continueWatching`, `recommendations`, `mediaById` (consumed via Apollo)

## Migrations claimed by this epic

| Slot | Plan | Purpose |
|------|------|---------|
| `0046` | plan-14-05 | `INDEX playback_state(user_id, updated_at DESC)` for Continue Watching queries (referenced from cross-epic playback_state, owned by Epic 7.11). |
| `0047` | plan-14-07 | `recommendation_dismissals(user_id, kind, key, dismissed_at)` for "Not interested" persistence. |

> Slot numbers above reflect this epic's plan claims. Canonical numbering reservation lives in [`shared/db/migrations/MANIFEST.md`](../../../shared/db/migrations/MANIFEST.md); each plan that ships SQL DDL must claim its slot there before review.

## Dependencies

- **Epic 17** Story 17.1 (design tokens — Swift / Kotlin outputs) blocks any TV UI work.
- **Epic 7** Stories 7.4 (videos), 7.10 (sessions), 7.11 (watch progress + cross-device sync), 7.16 (`playback.changed` WS events), 7.17 (GraphQL).
- **Epic 8** Stories 8.3 (direct play), 8.5 (HLS), 8.12 (chapters).
- **Epic 5** Stories 5.1–5.4 (embeddings + cross-language semantic + FTS) for recommendations algorithm.
- **Epic 9** Story 9.10 (`media_features` table population).
- **Epic 10** Stories 10.3 (refresh tokens), 10.6 (RS256 keys), 10.4 (401 refresh handling).
- **Epic 15** Story 15.5 (QR pairing) for first-run setup.
- **Epic 16** Story 16.2 if relay/federation eventually surface in TV.
- Story **14.7 → 14.6**: recommendations API must land before recommendations UI.

## Related mockups

`web/mockups/tv/` (Epic 14 mockups landed in commit `2c4de7d`).

## Out of scope

- Game console apps (PS5, Xbox).
- Web TV browsers (Tizen, webOS) — covered by [Epic 15 Story 15.4](epic-15-discovery.md) DLNA fallback.
- Settings parity with web (intentional design decision).
- Editor-picks curation table (v2; v1 falls back to "most-played overall last 30 days").
- WebRTC peer-to-peer streaming.
- Cloud DVR / recording features.

## See also

- [Epic 15 — Discovery & Networking](epic-15-discovery.md) (QR pairing, mDNS, federation, relay)
- [Epic 17 — UX Design System](epic-17-ux-design-system.md) (design tokens, RTL, player controls)
- [Glossary](../glossary.md) — Top Shelf, Recommendations Channel, 10-foot UI, HDR pass-through, TOFU pinning, Compose for TV, Leanback

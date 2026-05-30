# Epic 14 — tv-apps: Spec vs Implementation Gap Analysis

**Verdict:** Both TV apps are non-functional scaffolds (~515 LOC, all
services stubbed to `[]`/hardcoded); the recommendations API exists but is
the generic Story 7.21 handler — none of Story 14.7's TV-specific rails,
dismissals, refresh endpoint, or schema ship; the GraphQL contract every
client depends on returns HTTP 501.

Scope reviewed: README + 7 stories + plan-14-07. Code: `apps/tv/tvos`,
`apps/tv/android`, `api/internal/handlers/recommendations`,
`api/internal/graphql`, `shared/db/migrations`.

Status legend: **complete** / **partial** / **missing** / **unwired**
(code exists but no caller/route) / **stub** (placeholder returning
canned data).

---

## Story 14.1 — tvOS app (SwiftUI)

| AC | Status | Evidence |
|---|---|---|
| Built with SwiftUI + native focus engine | partial | `apps/tv/tvos/Sources/Maktaba/MaktabaApp.swift:31-42` `TabView` exists; no `.focusable`/focus-engine config anywhere. |
| Top Shelf "Continue Watching" on Home | missing | No `TVTopShelfContentProvider`, no Top Shelf extension target in `Package.swift:1-27`. README claims Top Shelf but zero code. |
| Tabs: Home, Library, Search, Settings | complete | `MaktabaApp.swift:35-39` all four tabs declared. |
| AVPlayer HLS + HDR (HLG/Dolby Vision) | missing | No `AVPlayer`/`AVPlayerViewController` import in any file; no player view exists. |
| Apollo iOS client from `shared/graphql/schema.graphql` | stub | `GraphQL/Schema.swift:1-52` is hand-written query strings labelled "stubs"; no Apollo dependency in `Package.swift`; no codegen. |
| Apple TV Remote: focus/swipe/click/double-tap | missing | No gesture/remote handling code. |
| Server pairing QR | stub | `Services/PairingService.swift:6-12` returns hardcoded `"ABCD-1234"`; `PairingView.swift` shows text "scan the QR code" but renders no QR image; no `/api/pairing` call. |
| TC: cold-launch ≤5 s w/ populated Continue | missing | `LibraryService.continueWatching()` returns `[]` (`PairingService.swift:23-25`). |
| EC: server-unreachable cached rows + banner | missing | No caching/banner; `reload()` (`HomeView.swift:42-46`) silently swallows errors via `try?`. |
| EC: deleted video in Continue hidden | n/a-missing | No data path exists. |
| EC: resume after suspend / mint new session | missing | No player, no session minting. |

## Story 14.2 — Android TV app (Kotlin/Compose)

| AC | Status | Evidence |
|---|---|---|
| Compose for TV + Leanback rows | partial | `android/app/.../MaktabaApp.kt:16,27-45` uses `androidx.tv.material3` `Text` only; no rows/`TvLazyRow`/Leanback; `MainScreen` is a single Text label. |
| ExoPlayer HLS/DASH + HDR10/Dolby Vision | missing | No ExoPlayer/Media3 import or player anywhere. |
| Recommendations channel on home (Continue + Recently Added) | missing | No `TvContractCompat`/Home Channel/`PreviewChannel` code. |
| Apollo Kotlin GraphQL client | missing | No Apollo dependency; `Models.kt:18-37` is plain interfaces with `Stub*` impls returning `emptyList()`. |
| D-pad / remote / game-controller input | missing | No focus/key handling; UI is non-interactive Text. |
| Server pairing via QR | stub | `MaktabaApp.kt:41-45` hardcodes `"ABCD-1234"` text; `StubPairingService` (`Models.kt:34-37`) canned. |
| TC: cold launch ≤6 s | missing | No real screens/data. |
| TC: D-pad across 50 items smooth | missing | No row UI. |
| TC: Assistant voice → `/api/search/suggest` | missing | No Assistant action / search call; `search()` returns `emptyList()` (`Models.kt:26`). |
| EC: manufacturer-skin degrade to in-app rows | missing | No channel API at all. |
| EC: HDR fail → SDR + toast | missing | No player. |
| EC: network drop 5 s grace | missing | No player. |

## Story 14.3 — 10-foot UI

| AC | Status | Evidence |
|---|---|---|
| Min body type 28 pt@1080p / 36 pt@4K | missing | tvOS uses semantic fonts (`.title2`, `.headline` — `HomeView.swift:29,67`); no pt sizing or 4K branch. Android uses default `Text`. README claims `tv.json` token bundle consumption — no token import in either app. |
| Focus ring 4 px + glow, not color-only | missing | No focus-ring styling in either app. |
| D-pad predictable grid, no diagonals | partial | tvOS `LazyVGrid` fixed columns (`LibraryView.swift:10`) implies a grid but no focus order guarantee; Android none. |
| Rows horizontal-snap / columns vertical-snap | missing | tvOS uses plain `ScrollView(.horizontal)` (`HomeView.swift:33`), no snap/focus section. |
| Back returns to previous focus | missing | No focus restoration logic. |
| Safe-area 5% inset | partial | Hardcoded `padding(96)`/`padding(.horizontal,96)` (`HomeView.swift:23`, `LibraryView.swift:18`) ≈ heuristic, not a computed 5% inset; Android `padding(96.dp)`. |
| All controls remote-reachable, no swipe-only | missing | No remote handling exists to verify. |
| EC: single-item row wraps | missing | No focus model. |
| EC: Back from player → detail page | missing | No player/detail page. |
| EC: modal focus trap + back exits | missing | No modals. |

## Story 14.4 — Voice search

| AC | Status | Evidence |
|---|---|---|
| tvOS `INSpeakableString` Siri intent | missing | No `Intents`/`INSpeakableString`/intent extension in `tvos/`. |
| Android system-keyboard voice + `actions.intent.SEARCH` | missing | No `AndroidManifest.xml` intent filter / `actions.xml` / shortcuts; search box absent on Android. |
| No-hit "did you mean" via `/api/search/suggest` | unwired | Endpoint exists server-side (`api/internal/handlers/search/search.go:110`) but no TV client calls it; tvOS `SearchService.query` returns `[]` (`PairingService.swift:39-42`). |
| Locale-aware (ar FTS / en cross-language) | missing | No locale param in any client search path. |
| Spoken Arabic normalized server-side | n/a (server) | Server FTS config out of epic-14 client scope; no client to exercise it. |
| TC: speak Arabic ≤2 s / English cross-lang / gibberish empty | missing | No voice path, no search wiring. |
| EC: mic-permission / mistranscription / provider-empty fallback | missing | No voice code. |

## Story 14.5 — Continue Watching row

| AC | Status | Evidence |
|---|---|---|
| Row title "Continue Watching" (localized) | partial | tvOS hardcodes English `"Continue Watching"` (`HomeView.swift:13`), no localization; Android has no row. |
| Items: poster+title+remaining+progress bar | partial | tvOS `PosterCard` (`HomeView.swift:49-72`) renders gray Rectangle (no poster image), title, `ProgressView`; **no remaining-time label**; data source returns `[]`. |
| Min 5% / max 95% qualify | partial (server) | API `continueRail` enforces `BETWEEN 0.05 AND 0.95` (`recommendations.go:121`) but TV apps never call it. |
| Cross-device ≤5 s via partial index | missing | Spec requires a **partial** index `WHERE position_sec >= dur*0.05 AND < dur*0.95`. Shipped index `playback_state_user_updated_idx` is **non-partial** `(user_id, updated_at DESC)` on both PG (`0038_playback_state.sql:27-28`) and SQLite (`0038...sqlite.sql:16`). Story 14.5's required migration was never written; no WS `playback.changed` consumer in TV apps. |
| Long-press context menu (Mark Watched / Remove) | missing | No context menu in either app. |
| Empty state copy | missing | tvOS hides row when empty (`HomeView.swift:12`); no empty-state text. |
| TC: 12-min watch updates TV ≤5 s | missing | No client data path. |
| TC: `EXPLAIN` uses partial index | fail | Partial index doesn't exist; query in `recommendations.go:115-124` filters on `position_sec/duration_sec` ratio which the non-partial index cannot cover. |
| EC: duration=0 excluded / >50 caps 20 / dedupe | partial (server) | Server query lacks explicit dedupe & duration=0 guard relies on division; unreachable from TV regardless. |

## Story 14.6 — Recommendations UI

| AC | Status | Evidence |
|---|---|---|
| Source `GET /api/recommendations` `{title,items,reason}` rows | unwired | Endpoint mounted (`router/p6.go:98-99`) but TV apps never call it; tvOS `LibraryService.recommendations()` returns `[]` (`PairingService.swift:27-29`). Server `Rail` (`recommendations.go:27-31`) has **no `reason` field**. |
| Reason strings ("Because you watched X" etc.) | missing | Server emits fixed rail titles ("Continue Watching", "For You", "From your library" — `recommendations.go:125,148,173`); none of the 6 spec `reason_kind` values; tvOS Schema stub references a `reason` field (`Schema.swift:32`) the server doesn't return. |
| Up to 5 rows / 20 items | partial | API has no row cap; item limit clamped to ≤100 (`recommendations.go:87-89`), not 20. |
| Deterministic per user/day, 24 h cache | missing | Cache is 60 s in-memory (`recommendations.go:215-217`), not 24 h; no determinism guarantee; no nightly recompute. |
| "Not interested" hides items/reasons, persisted | missing | No dismissal UI; no `DELETE /rows` or `/items` route (see 14.7). |
| TC / EC (speaker row, hide persists, cold-start) | missing | No personalization logic; `forYouRail` reads `user_recs` only. |

## Story 14.7 — Recommendations API

| AC | Status | Evidence |
|---|---|---|
| Table `recommendation_runs` | n/a (descoped) | plan-14-07 §2 removes it in favor of plan-07-21 in-memory cache — accepted deviation. |
| Table `recommendation_dismissals` (+CASCADE, index) | missing | No `0047_recommendation_dismissals.sql` / `.sqlite.sql`; grep of `shared/db/migrations` finds none. |
| `GET /api/recommendations` envelope w/ `reason_kind`/`reason_args`/localized title | partial | Route exists (`recommendations.go:64-111`) but envelope is `{rails:[{id,title,items}],generated_at,cache_hit}` — no `reason_kind`, `reason_args`, `expires_at`, or localized titles. |
| Returns cached if fresh; recompute on miss ≤1 s else stale+async | partial | 60 s cache works (`recommendations.go:92-96,214-228`); no stale-return / async-refresh / ≤1 s budget logic. |
| `DELETE /api/recommendations/rows/{reason_kind}` | missing | Only `r.Get` mounted (`recommendations.go:64-66`); no DELETE handlers. |
| `DELETE /api/recommendations/items/{video_id}` | missing | Same — absent. |
| `POST /api/recommendations/refresh` (admin) | missing | Absent; no rate-limit bucket. |
| Nightly job 03:00, 30-day window, speaker heuristic, semantic neighbors | missing | No `recommender/` or `scheduler/warm_tv_recommendations.go` package; `forYouRail` just `SELECT ... user_recs` (`recommendations.go:147-167`). No `media_features`/pgvector use. |
| Cold-start → only newly_added + editor_picks | missing | No cold-start branch; rails are continue/for-you/library only. |
| Determinism `(score DESC, video_id ASC)` | partial | `forYouRail` orders `score DESC` only — no `video_id` tiebreak (`recommendations.go:151`). |
| Per-user ≤200 ms @100k segments | unverifiable | Simple SQL likely fast but no benchmark; not the spec'd composer. |
| Auth required, no cross-user leak | complete | `principal.FromContext` 403 guard (`recommendations.go:70-73`); all queries scoped `user_id = $1`. |
| `media_features` empty → 200 not 500 | partial (vacuously) | No semantic path to fail; returns 200 but for unrelated reasons. |
| i18n bundle / localized titles | missing | Hardcoded English titles; no `api/internal/i18n`. |

**Cross-cutting (README):** GraphQL is the stated client contract for
all rows; the GraphQL handler returns **HTTP 501** "resolvers not yet
wired" (`api/internal/graphql/schema.go:357-374`) and the SDL exposes
`recommendations(...)` but **no `continueWatching` query** at all
(`schema.go:303`, no continueWatching in schema). tvOS Schema stub
queries `continueWatching`/`recommendations`/`search` that the server
cannot resolve.

---

## Top gaps by impact

1. **Entire epic is non-functional scaffolding.** Both TV apps (~515
   LOC total) have every service stubbed to canned data
   (`PairingService.swift:20-43`, `Models.kt:24-37`): no networking, no
   GraphQL/Apollo, no player (AVPlayer/ExoPlayer absent), no QR, no
   focus engine, no Top Shelf/Home Channel. Stories 14.1–14.4 are
   ~0% behaviorally complete.

2. **GraphQL backbone returns 501.** README declares all rows are
   GraphQL queries, but `api/internal/graphql/schema.go:357-374` always
   answers POST `/graphql` with `501 schema-only`. Even if clients were
   wired, no data would flow. Schema also lacks a `continueWatching`
   query field entirely.

3. **Story 14.7 ~15% delivered.** Only the generic Story 7.21 handler
   exists. Missing: `recommendation_dismissals` migration, all three
   DELETE/POST endpoints, the `recommender`/nightly-warmer packages,
   reason_kind/reason_args/localized envelope, 24 h determinism. 14.6
   cannot be satisfied because its data source's contract is unbuilt.

4. **Story 14.5 partial-index requirement unmet.** The story explicitly
   owns a *partial* covering index; the shipped
   `playback_state_user_updated_idx` (`0038_playback_state.sql:27`) is
   non-partial, so the ≤5 s cross-device guarantee and the `EXPLAIN`-no-
   seqscan TC fail by construction.

5. **Voice search (14.4) zero implementation** despite a usable server
   endpoint (`/api/search/suggest`, `search.go:110`) — no Siri intent,
   no Android Assistant action, no client call wires to it.

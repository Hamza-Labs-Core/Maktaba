# Story 51.11 — Apps hide or explain what is off

> Epic 51 · Phase 2 · Depends on: 51.09 · Needed by: —

## Why

After 51.09 the home says which features are on, and answers `409 component_off` for a route
whose component is off. The apps still show every tab and button. A viewer on a home without
Live TV sees a Live TV tab that fails; an admin who turned semantic search off sees a search
that seems broken. This story makes every app read capabilities and do one of two things
(H12): **hide** a feature that is off from viewers, and **explain** it to admins with a link to
turn it on.

## Description

### 1. The rule, the same on every platform

| Feature off | Viewer | Admin |
|---|---|---|
| `live_tv` | no Live TV tab, no Guide entry, no channel rows on Home | the tab stays; its screen says "Live TV is off on this home. Turn it on in Components." with a button |
| `transcripts` | no Transcript tab or button on a video | the tab shows the same kind of sentence |
| `subtitles` | no "Generate subtitles" action; existing subtitle files still play | sentence on the action |
| `semantic_search` | search works with keyword results only; no "by meaning" toggle | the same, plus one line under the box: "Searching by words only. Search by meaning is off." |
| `chapters` | no chapter list; the player shows no chapter marks | sentence where the list would be (always on today, 51.09; the gate is built so it works the day a component lists it) |
| `youtube` | no YouTube results in search, no YouTube sources | sentence in the YouTube section |
| `plex`, `remote_access` | admin-only screens; viewers never saw them | sentence on the screen |

- The admin's button opens `<home web URL>/settings/components` (51.10) in the browser on
  native apps, and navigates there in the web app.
- "Admin" means the answer has an `admin` block (51.09). A cloud-linked play sign-in never gets
  one, so it is a viewer even for an admin's account.
- **Until the first answer** arrives, every feature is on (today's behaviour). A failed refresh
  keeps the last good answer. An app never hides a feature because the home did not answer.
- **Refresh**: at sign-in, when the app comes to the front, every 5 minutes with the ETag, and
  on `capabilities_changed` where the app listens to `/ws/home` (web; native apps use the
  timer and the foreground refresh).
- **A screen already open** when a feature goes off is not torn down. Its next call gets
  `409 component_off` and shows the `off` sentence from 51.09. On the next navigation the tab
  is gone.

### 2. Web (`web/`)

- `web/src/lib/capabilities.tsx` (new), the same shape as `lib/flags.tsx`: a provider inside
  `FlagsProvider`, `useFeature("live_tv")` → `"on" | "off"`, `useFeatureAdmin(id)` → the
  admin explanation or null, and `<Feature id fallback>` that renders children when on,
  the admin note when off for an admin, and nothing for a viewer. It holds the answer in memory
  only and subscribes to `/ws/home` through `lib/ws.ts`.
- Applied in `components/AppShell.tsx` (the Guide nav item, `nav.guide`), `App.tsx` routes
  (`/guide`, `/live/:number` show the admin note or redirect a viewer home), `Home.tsx`
  channel rows, `VideoDetail.tsx` transcript and chapters tabs, `Search.tsx` (YouTube section,
  the meaning toggle), and the big-screen UI in `web/src/bigscreen`.
- The Capacitor and Tauri shells use the web bundle, so they get this for free.

### 3. Apple (`apps/apple`, `apps/tv/tvos`)

- `MaktabaKit/Sources/MaktabaKit/Capabilities.swift` (new): `Capabilities` model and a
  `CapabilitiesStore` (`@Observable`) that the session owns, refreshed on `scenePhase ==
  .active`, a 5-minute timer and sign-in. ETag kept in memory.
- **Player** (iPhone, iPad, Mac): `Shell/PlayerSection.swift` drops `.liveTV` for viewers when
  `live_tv` is off; `Video/VideoScreen.swift` hides the transcript; `Search/SearchModel.swift`
  hides YouTube and shows the words-only line for admins; `Live/LiveTVScreen.swift` shows the
  admin note.
- **TV** (tvOS): `Views/LiveView.swift`, `Views/SearchView.swift`, `Views/VideoPage.swift` and
  the tab list in `ContentView.swift`, same rules.
- **Remote**: it is an admin app. `QueueView.swift` shows "waiting: Transcription is off" for
  parked stages (51.14 gives the reason), and `HomeStatusView.swift` gets a one-line "3 parts
  are off" with a link to the Components page.
- Words in the String Catalogs of each target (`Player/Live/Live.xcstrings`,
  `Player/Search/Search.xcstrings`, `Player/Video/Video.xcstrings`,
  `Remote/Localizable.xcstrings`, `tv/tvos/MaktabaTV/Localizable.xcstrings`,
  `MaktabaKit/.../Localizable.xcstrings`), English and Arabic together.

### 4. Android (`apps/android`, `apps/tv/android`, `apps/desktop-player`)

- `client/src/main/kotlin/com/hamzalabs/maktaba/core/api/Capabilities.kt` (new): model and
  `getCapabilities(etag)`; `core/session/AppSession.kt` holds a `StateFlow<Capabilities>`,
  refreshed on `ON_START`, every 5 minutes and at sign-in.
- **Player**: `shell/PlayerSection.kt` filters `LiveTv`; `video/VideoScreen.kt`,
  `search/SearchScreen.kt`, `search/SearchModel.kt` as above.
- **Remote**: the same additions as the Apple Remote.
- **Android TV**: `tv/android/app/.../ui/SignedInApp.kt` (the tab list), `screens/LiveScreen.kt`,
  `screens/SearchScreen.kt`, `screens/VideoScreen.kt`. It already uses the phone apps' `client`.
- **Desktop Player** (Windows, Linux) uses the same `client` and filters its sections the same
  way.
- Words in `core/src/main/res/values/strings.xml` and `values-ar/strings.xml` (shared), plus
  `player`, `remote` and `tv/android` resources where a string is theirs.

### 5. Shared words (English and Arabic)

| Key | English | Arabic |
|---|---|---|
| `feature_off_admin` | %1$s is off on this home. | %1$s متوقفة على الجهاز الرئيسي. |
| `feature_turn_on` | Turn it on in Components | شغّلها من صفحة المكوّنات |
| `search_words_only` | Searching by words only. Search by meaning is off. | البحث بالكلمات فقط. البحث بالمعنى متوقف. |
| `parts_off` | %d parts are off | %d أجزاء متوقفة (with the plural forms) |

Feature names come from 51.09 (`components.feature.*`). Each platform's parity check runs on
them.

### 6. Older apps

An app released before this story does not read capabilities. It keeps working: tabs that are
off fail with `409 component_off`, which 51.09 makes readable on any app that got the `off`
kind, and which older apps show as their generic "something went wrong" sentence. Nothing
crashes: a `409` is already handled by every app's error path. The contract test below keeps
that true.

## Acceptance criteria

- **Given** `live_tv` is off and a viewer signs in on any app,
  **when** the main screen shows,
  **then** there is no Live TV tab or Guide entry, and no channel row on Home.
- **Given** the same home and an admin,
  **when** they open Live TV,
  **then** they read "Live TV is off on this home." with a button that opens Components.
- **Given** a viewer with the Player open,
  **when** an admin turns Live TV off,
  **then** the tab is gone within 5 minutes or at the next foreground (web: within 2 s).
- **Given** semantic search is off,
  **when** anyone searches,
  **then** keyword results show and nothing says "error"; an admin also sees the words-only line.
- **Given** the home does not answer `GET /api/capabilities`,
  **when** the app starts,
  **then** every tab shows as today.
- **Given** a cloud-linked play sign-in on an admin's account,
  **when** Live TV is off,
  **then** the app behaves as for a viewer.
- **Given** an app build from before this story,
  **when** it opens a screen whose component is off,
  **then** it shows its usual error screen and does not crash.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | vitest | `capabilities.tsx` with fake API | first load fails | all on; last good kept after a later failure |
| T02 | vitest | viewer, `live_tv` off | render `AppShell` | no Guide item |
| T03 | vitest | admin, `live_tv` off | open `/guide` | admin note with link to `/settings/components` |
| T04 | vitest | `capabilities_changed` on fake socket | dispatch | refetch with `If-None-Match` |
| T05 | vitest | locale `ar` | render the notes | Arabic text; axe clean |
| T06 | unit (MaktabaKit) | `CapabilitiesStore`, fake client | 304, 200, failure | ETag kept; last good kept |
| T07 | UI (Apple, CI only) | `MockHome` serves `live_tv: off`, viewer | launch Player | no Live TV tab |
| T08 | UI (Apple, CI only) | `MockHome`, admin | open Live TV | admin note; button present |
| T09 | UI (tvOS, CI only) | `MaktabaTVUITests` with the mock | launch | no Live tab |
| T10 | unit (Android `client`) | `Capabilities` parse, unknown feature | parse | ignored; missing feature = on |
| T11 | Robolectric (player) | `PlayerShellTest` with `live_tv` off, viewer and admin | compose | tab hidden / admin note |
| T12 | Robolectric (tv/android) | `SignedInApp` | compose | no Live tab |
| T13 | Robolectric (player) | Search with `semantic_search` off, admin | compose | words-only line in `ar` and `en` |
| T14 | contract | the previous release's apps' error parsers (fixtures in each test suite) | 409 `component_off` body | maps to their generic kind, no exception |

## Edge cases

- **A feature turns back on** while the app is open: the tab comes back on the next refresh;
  the current screen is not moved.
- **Deep links** to a hidden screen (a shared `/live/7` link) open Home for a viewer with a
  toast "Live TV isn't available on this home" (en + ar), and the admin note for an admin.
- **Offline viewing** (downloads, Epic 35 offline TV) is not gated: the app is not talking to
  the home.
- **Multiple homes** (Apple `Homes.swift`, Android saved homes): capabilities are per home and
  refetched on switch.
- **A Remote on an old home** with no `/api/capabilities` (404) treats every feature as on.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `web/src/lib/capabilities.tsx` (new), `capabilities.test.tsx` (new) | Provider, hooks, `<Feature>`. |
| `web/src/App.tsx` | Provider under `FlagsProvider`; gated routes. |
| `web/src/components/AppShell.tsx` | Guide nav item. |
| `web/src/pages/Home.tsx`, `VideoDetail.tsx`, `Search.tsx` | Gates and admin notes. |
| `web/src/bigscreen/` (Live entry) | Same gate. |
| `web/src/i18n/en.json`, `ar.json` | §5 words, deep-link toast. |
| `apps/apple/MaktabaKit/Sources/MaktabaKit/Capabilities.swift` (new) | Model and store. |
| `apps/apple/MaktabaKit/Sources/MaktabaKit/Session.swift` | Own the store; refresh on active. |
| `apps/apple/Player/Shell/PlayerSection.swift`, `Video/VideoScreen.swift`, `Search/SearchModel.swift`, `Live/LiveTVScreen.swift` | Gates and notes. |
| `apps/apple/Remote/QueueView.swift`, `HomeStatusView.swift` | Off reasons and the "parts are off" line. |
| `apps/tv/tvos/MaktabaTV/ContentView.swift`, `Views/LiveView.swift`, `Views/SearchView.swift`, `Views/VideoPage.swift` | Gates. |
| Apple `.xcstrings` files named in §3 | English + Arabic. |
| `apps/apple/UITests/MockHome.swift`, `PlayerLiveUITests.swift` | Serve capabilities; T07, T08. |
| `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/api/Capabilities.kt` (new) | Model and call. |
| `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/session/AppSession.kt` | State and refresh. |
| `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/shell/PlayerSection.kt`, `video/VideoScreen.kt`, `search/SearchScreen.kt` | Gates. |
| `apps/android/remote/` (queue and status screens) | Off reasons, "parts are off". |
| `apps/tv/android/app/src/main/java/com/hamzalabs/maktaba/tv/ui/SignedInApp.kt`, `ui/screens/LiveScreen.kt`, `SearchScreen.kt`, `VideoScreen.kt` | Gates. |
| `apps/desktop-player/src/main/` (its section list) | Gate Live TV. |
| `apps/android/core/src/main/res/values/strings.xml`, `values-ar/strings.xml` (and `player`, `remote`, `tv/android` where needed) | English + Arabic. |

### Tests

- T01–T05 in the web unit job.
- T06 in `apple-apps` (MaktabaKit on macOS); **T07–T09 are CI only** (`apple-apps` UI test
  shards on a per-run created simulator, one UI run at a time; never on the owner's Mac).
- T10, T11, T13 in `android-apps`; T12 in `android-tv`; Robolectric with `logging=stdout` as
  both Android roots already set. Desktop Player in `desktop-player`.
- T14 in each platform's unit job.

### Rollout

Ships with Phase 2, after 51.09. Apps go out through their normal releases; the home needs no
change beyond 51.09.

### Rollback

Each app has a build switch `capabilitiesGate` (default on). Off, the provider answers all on
and nothing is hidden. The home's `409` still works.

### Dependencies

51.09 (capabilities, `/ws/home`, the `off` kind and the feature names).

# Epic 48: The native Players: as good as the web, and as Plex

> **Status:** spec. **Continues:** [Epic 42](../42-apps-player-remote-server/README.md)'s player
> scope (stories 42.1, 42.3, 42.8). **Reuses:** [Epic 46](../46-big-screen/README.md)'s screen
> inventory and APIs, [Epic 17](../17-ux-design-system/README.md)'s tokens, Epic 45's browsing
> API. **Research:** [research.md](research.md). **Plan:** [plan.md](plan.md).

## 1. Why

The owner, 2026-10-31, after opening the Player apps:

> "Why the player app starts light, seem no theme, and very basic no nav, only libraries view,
> where plex like? where is tv? is that epic really DONE??? why both app sign in page so basic,
> no logos, no theme like web?"

Epic 42 asked the Player for "home by address, sign-in, libraries, a video, watching" (story
42.1). That was built, tested and called done. It is a list of videos with a play button. The
web app beside it has Home rails, series, search, Live TV, a profile, and the Ink & Brass look;
so do the Plex, Jellyfin, Infuse and Apple TV apps people compare it with.

### Epic 42, honestly

| A player needs | Web | Apple Player (iPhone, iPad, Mac) | Android Player | TV apps |
|---|---|---|---|---|
| Brand on the first screen (logo, art, dark) | art + wordmark, dark fallback | **none**: system lists, SF Symbols, follows the system so starts light | logo, art, panel (`core/ui/StartScreens.kt`), but `ThemeMode` defaults to System: **starts light** | branded |
| Navigation between sections | side nav / bottom tabs | **none**: one split view of libraries | **none**: one list | top tabs |
| Home: hero, Continue Watching, rows | yes | **no** | **no** | yes (`tv-home` rails) |
| Libraries → library → series → video | yes | libraries → flat video list | libraries → flat list | yes |
| A video's page (Resume, chapters, marks) | yes | 49 lines: poster, title, Play | minimal | yes |
| Search (titles and transcripts) | yes | title filter on the list | title filter | yes |
| **Live TV** (now, guide, live player) | yes | **no** | **no** | **no** |
| Profile and Settings | yes | a theme picker in a menu | an account menu | yes |
| Player: resume | yes | yes | yes | yes |
| Player: next episode, subtitles, audio, quality | yes | **no** | **no** | partly |
| PiP, AirPlay / Cast, lock-screen controls | PiP | background audio only | **no** | n/a |
| UI tests in CI | vitest per page | **builds only** | **no Player tests** | UI tests, not in CI |

What went wrong: the stories listed plumbing (connect, sign in, stream sessions), and the
acceptance checked the plumbing. Nothing in Epic 42 described the screens a viewer expects, so
nothing failed when they were missing. This epic lists every screen, with checks a test can run.

## 2. Research

In [research.md](research.md): how Plex, Jellyfin, Infuse, the Apple TV app and Netflix lay out
phone, tablet and desktop players; Apple's HIG (tab bar → sidebar, iPhone Duo) and Material 3
adaptive navigation; what our web and TV apps already do; and which route serves each screen.

The short version:

- Every reference app is **dark by default**, opens on **Home** (a hero, then Continue
  Watching, then rows), has **tabs on a phone and a sidebar on a tablet or desktop**, a
  **detail page** before playback, and a player with **next episode, track pickers, PiP and
  AirPlay / Cast**. Plex adds **Live TV** (now and guide) in the same app.
- Every screen can be built on routes we already have. Four gaps, two of them about security:
  - **G1** the web port's `/stream/channel/{id}` gate checks sign-in but not the channel's library;
  - **G2** AirPlay and Cast need the signed `?sig=` stream URLs to pass the gate without a cookie;
  - **G3** subtitles must also be HLS renditions so AVPlayer, AirPlay and Cast can show them;
  - **G4** a small poster size for phone grids.

## 3. Goals

1. **The brand from the first frame.** Start, connect and sign-in screens with the logo, the
   library art and the Ink & Brass theme, **dark by default**, on Apple and Android, matching
   the web's Login page.
2. **Real navigation**, adaptive per form factor: five sections (Home, Libraries, Live TV,
   Search, Profile). A tab bar on a phone, a sidebar or rail on a tablet, foldable and Mac.
3. **The web's viewer screens, natively:** Home rails, Libraries, a library, Series, a series,
   a video's page, Search, Live TV (now, guide, live player), Profile and Settings.
4. **A player people expect:** resume, next episode with Up next, subtitles and audio tracks,
   quality, PiP, AirPlay (Apple) and Cast (Android), lock-screen and notification controls,
   sound that continues in the background.
5. **Live TV on the TV apps too** (Apple TV, Android TV): a Live TV tab with what is on now, the
   guide and the live player.
6. **One source for the design tokens** across web, Apple and Android.
7. **Proven by tests:** a UI test per screen, screenshot tests in dark for English and Arabic,
   all running in CI.
8. Roles stay server-side: every library rule is the API's; the four gaps close with role tests.

## 4. Non-goals

- **Downloads / offline viewing.** It needs a download right per user, storage limits, expiry
  and a new API with role tests: a follow-up epic (open question 1). Epic 48 shows cached pages
  read-only when the home is unreachable, and says so.
- **The Remote app's screens.** Remote gains the brand start screens for free (they live in
  MaktabaKit and Android core); its own navigation is unchanged.
- **The Windows and Linux desktop player** (`apps/desktop-player`). It shares
  `apps/android/client`, so it gets the new client calls for free; its screens are a follow-up
  (open question 4).
- Admin pages, recording (DVR), creating channels, a TV launcher, Top Shelf, widgets, CarPlay,
  Android Auto, Chromecast with a custom receiver app.
- New metadata (cast, ratings): the screens show what the API returns.

## 5. Functional requirements

Each FR names the screen and the routes. "Both" means Apple (iPhone, iPhone Duo, iPad, Mac)
and Android (phone, foldable, tablet).

### Start, connect, sign in

- **FR-1** Both: the home picker, by-address, sign-in by password and by code, and the cloud
  sign-in show the library art behind a scrim (dark or light, tall or wide), the logo, «Maktaba»
  in Amiri and the app's name, over a frosted panel, as `core/ui/StartScreens.kt` does on
  Android and `web/src/pages/Login.tsx` on the web.
- **FR-2** Both: with no stored choice the app is **dark** (Ink & Brass) on first launch,
  whatever the system setting. Light and System stay choosable in Settings (FR-30).
- **FR-3** Both: the system's launch screen is ink (`#0B111C`) with the logo, so there is no
  white flash before the first frame.
- **FR-4** Apple: MaktabaKit carries the fonts (Amiri Bold, IBM Plex Sans Arabic), the four
  library art images and the logo as package resources, so Player, Remote and TV share them.

### Navigation

- **FR-5** Five sections: **Home, Libraries, Live TV, Search, Profile**. Live TV is hidden when
  `GET /api/channels/now` returns no channel for this user (open question 2).
- **FR-6** Apple: `TabView` with `.sidebarAdaptable`. iPhone: bottom tab bar. iPad: top tabs that
  become a sidebar, the person's choice kept. Mac: a sidebar. Each tab holds its own
  `NavigationStack`; re-selecting a tab pops it to the root.
- **FR-7** iPhone Duo: folded, the iPhone layout; open flat, the iPad layout; half folded, the
  hinge split kept from today (`ArrangementView`, the player on one half, browsing on the
  other), compiled only under `#if os(iOS) && compiler(>=6.4)`.
- **FR-8** Android: `NavigationSuiteScaffold`: a bottom bar in a compact window or tabletop
  posture, a rail in a medium window, a permanent drawer in an expanded one. List-detail
  screens use `core/layout/Panes.kt` (the page beside the list from 600 dp, the split at the
  hinge).
- **FR-9** Both: Back (Android back gesture, Apple swipe-back, Mac ⌘[) returns to the previous
  screen with its scroll position; deep links `maktaba://video/{id}`, `maktaba://series/{id}`,
  `maktaba://live/{number}` open that screen (the `maktaba` scheme already exists for
  `maktaba://connect`).

### Home

- **FR-10** Home reads `GET /api/home` and shows, top to bottom: the **hero** (`featured`:
  backdrop, title, Resume or Play, Details), then **Continue Watching**, **On Deck**, **Live TV
  on now** (from `GET /api/channels/now`), **Favorites**, **Recently Added**, **Recently
  Watched**, one row per **pinned library**, then the **For You** and **From your library** rails
  of `GET /api/recommendations?surface=mobile-home`. Empty rows are hidden.
- **FR-11** A card shows its poster (`/api/videos/{id}/poster`), title, series name and a
  progress bar; tap opens the video's page (a Continue Watching card resumes on a long press /
  context menu "Resume"). A recommendation card's menu has "Not interested"
  (`DELETE /api/recommendations/items/{video_id}`).
- **FR-12** Pull to refresh (iOS, Android), ⌘R (Mac). A second open shows the last answer at
  once and refreshes behind it.
- **FR-13** Empty home (no libraries visible): says so, with Switch home and Sign out.

### Libraries, a library, Series, a series

- **FR-14** Libraries: a grid of cards from `GET /api/libraries/summary` (art, name, count), the
  pinned first; Pin / Unpin (`PUT /api/me/pinned-libraries`).
- **FR-15** A library: its hub rows (`GET /api/libraries/{id}/hub`: On Deck, Continue Watching,
  Recently Added, Recently Watched), tabs **Videos** and **Series**
  (`GET /api/libraries/{id}/series`), then a grid of all its videos, newest first, paged by
  cursor (`GET /api/videos?library=…`), with the facets of `GET /api/videos/facets` (language,
  speaker) and sort (newest, A–Z). A wide window keeps the list beside the page (today's split).
- **FR-16** Series: a grid of every visible series (`GET /api/series`) with progress.
- **FR-17** A series: art, name, description, **Continue** (its next episode), a season picker,
  episodes with progress and watched marks (`GET /api/series/{id}/episodes`); Mark watched on an
  episode or the season (`POST /api/me/watched`).

### A video

- **FR-18** The page: backdrop and poster, title, series and episode, length, date, description;
  **Resume** from `playback_state` (with "Play from start"), or **Play**; **Mark watched**,
  **Favorite** (`POST /api/me/favorites`); **Chapters** (`/api/videos/{id}/chapters`), each plays
  from its time; a transcript excerpt (`/api/videos/{id}/segments`); in a series, the next
  episodes. 404 says "not available" (a video the user may not see answers 404, as one that
  does not exist).

### Search

- **FR-19** A search field (iOS `.searchable`, Android `SearchBar`) with suggestions
  (`GET /api/search/suggest`) and recent searches (`GET/DELETE /api/search/recent`).
- **FR-20** Results from `POST /api/search` (mode `hybrid`), in two groups: **Titles** and **In
  the transcript**; a transcript match shows the passage and plays from its time. Arabic and
  English queries both work (the home ranks them).

### Live TV

- **FR-21** **Now**: a list (phone) or grid (tablet) of channels from `GET /api/channels/now`:
  logo (`/api/channels/{id}/logo`), number, name, what is on, its progress and what is next.
  Tap tunes.
- **FR-22** **Guide**: channels × time from `GET /api/channels/guide?start=&end=` (three hours
  on a phone, six on a tablet), now-line, scroll in both directions, a day picker; a
  programme's sheet shows its title, time and description, with Watch for what is on now.
  `GET /api/channels/{id}/guide` for one channel's page.
- **FR-23** **The live player**: full screen; the channel, programme and progress; swipe or
  ▲/▼ for the previous / next channel; the number pad on a tablet or keyboard; a mini-guide
  (now / next); "Back to live" after a pause. At home it plays
  `/stream/channel/{id}/index.m3u8` through the web port (retrying while the encoder warms, as
  `web/src/pages/LiveTV/Player.tsx` does); away (`GET /api/tv/info` says `via_relay`) it uses
  `POST /api/tv/remote/tune` and a heartbeat every 60 s, ending the session on leave.
- **FR-24** A channel the user may not read is never listed and never plays: the server refuses
  its stream (gap G1, story 48.3).

### Profile and Settings

- **FR-25** Profile: the user's name, the home's name, **Watch history** (`GET /api/me/history`,
  remove one), **Favorites**, **Playlists** (`GET /api/me/playlists`, play one).
- **FR-26** Account: Switch home, **Approve a sign-in** (exists), **Sessions**
  (`GET /api/me/sessions`, sign out one), Linked cloud account (exists), Sign out.
- **FR-27** Help: opens the home's help page for this app (`/help/apple-apps`, `/help/android-apps`).
- **FR-28** About: version, build, the home's version, Diagnostics (the logs, as today).

### Settings (kept on this device)

- **FR-29** **Theme**: Dark (default), Light, System.
- **FR-30** **Language**: English or Arabic. Apple opens the system's per-app language page;
  Android 13+ uses per-app locales (`locales_config.xml`), older Android an in-app choice. The
  layout mirrors in Arabic.
- **FR-31** **Playback**: play the next episode automatically (on), subtitles on by default
  (off), subtitle language, subtitle size (S / M / L), quality on Wi-Fi and on mobile data
  (Original / 1080p / 720p / 480p, sent as `max_bitrate_kbps`), resume without asking (on).

### The player

- **FR-32** **Resume**: opens at `playback_state.position`; reports progress every 10 s and on
  pause / leave (`/api/stream/sessions/{id}/progress`, as today); "completed" near the end.
- **FR-33** **Next episode**: in a series, near the end an *Up next* card counts down 10 s (Play
  now / Cancel) when FR-31's setting is on; a Next button always. The next is the series'
  following episode (`/api/series/{id}/episodes`), else the play queue's.
- **FR-34** **Subtitles and audio**: a picker lists off and each subtitle (from the session's
  `subtitles`, as HLS renditions after G3) and each audio track; a choice is kept for the next
  video in the series. Direct play on Apple draws the VTT itself.
- **FR-35** **Quality**: Auto plus the session's `ladder`; a change reopens the session at the
  same position with `max_bitrate_kbps`.
- **FR-36** **Controls**: Play / Pause, ±10 s, a scrubber with trickplay thumbnails
  (`/api/videos/{id}/trickplay`), chapters, the time and length; double-tap left / right
  seeks ±10 s on touch (mirrored in Arabic); they hide after 4 s.
- **FR-37** **PiP**: Apple through `AVPlayerViewController` (iPhone, iPad; the Mac's
  `AVPlayerView`); Android auto-enters PiP on Home with play / pause and ±10 s actions.
- **FR-38** **AirPlay** (Apple) and **Cast** (Android, Google's Default Media Receiver): the
  receiver gets the session's signed URL (gap G2); progress keeps reporting from the phone.
- **FR-39** **Background and lock screen**: sound continues with the screen locked (Apple:
  already declared; Android: a `MediaSessionService`); Now Playing (title, art, position) and
  remote commands (play, pause, ±10 s, next).
- **FR-40** **Failures** say why and what to do (the existing `HomeErrorView` / `ErrorState`):
  try again, sign in again, choose another home.

### The Mac player

- **FR-41** A window with the sidebar (FR-6), at least 760 × 480, default 1200 × 780 (today's).
- **FR-42** Menus: **Playback** (space, ←/→ 10 s, ⇧←/⇧→ 30 s, N next, S subtitles, ⌘.
  stop: today's `PlaybackCommands` plus the new ones), **View** (each section ⌘1–⌘5, ⌘F
  search, full screen ⌃⌘F), **Account** (exists), **Help**.
- **FR-43** The player fills the window and goes full screen on its own space; PiP floats.

## 6. Non-functional requirements

Measured on: iPhone 15, iPad (A16), a MacBook Air M2, Pixel 7, a Galaxy Z Fold (any recent),
a mid tablet; home on the LAN unless said. Each has its test in the stories.

### Performance

- **NFR-1 Cold start:** launch to the first Home frame with cached content ≤ 1.5 s (p50) and
  ≤ 2.5 s (p90); with no cache, first rows ≤ 2.5 s on the LAN. Measured by `XCTApplicationLaunchMetric`
  and Android Macrobenchmark `StartupTimingMetric`.
- **NFR-2 Time to first frame:** tap Play to first frame ≤ 2 s for direct play and remux, ≤ 5 s
  for a transcode; a live channel ≤ 4 s warm, ≤ 10 s from a cold encoder.
- **NFR-3 Scrolling:** Home and a library grid at the display's rate (60 / 120 Hz) with ≤ 1 %
  hitches (`XCTOSSignpostMetric.scrollingAndDecelerationMetric`) and ≤ 5 % janky frames
  (`FrameTimingMetric`).
- **NFR-4 Images:** posters decoded at display size; a memory cache ≤ 50 MB and a disk cache
  ≤ 200 MB, LRU; the small poster (G4) in grids; a repeat visit fetches nothing within the
  server's `max-age`.
- **NFR-5 Memory:** ≤ 250 MB resident while playing 1080p on a phone.

### Accessibility

- **NFR-6** Every control has a label and role; each screen passes
  `XCUIApplication.performAccessibilityAudit()` and Compose's accessibility checks
  (`enableAccessibilityChecks`) in its UI test.
- **NFR-7** Dynamic Type to AX3 / font scale 200 %: titles wrap, nothing is cut, rows grow.
- **NFR-8** Contrast WCAG 2.2 AA: text 4.5:1, large text and controls 3:1, in dark and light
  (checked on the token pairs in story 48.2's test). Touch targets ≥ 44 pt / 48 dp.
- **NFR-9** Reduce Motion: no hero parallax or scale animations; the focus ring and progress stay.

### Language

- **NFR-10** English and Arabic in the same PR, every string (the owner's rule):
  `tools/apple/check-string-catalogs.py` and Android's `TranslationsTest` fail on a missing one.
  Arabic wording reuses `web/src/i18n/ar.json`; "the home" is «الجهاز الرئيسي».
- **NFR-11** RTL: layouts mirror, rows start on the right, the scrubber and ±10 s mirror,
  numbers and dates follow the locale; a screenshot test per screen in Arabic.

### Security, privacy, roles

- **NFR-12** **Roles are the server's.** Every screen reads routes that already scope to the
  caller; a viewer with library A gets 404 for library B's library, series, video, hub and
  search results. Each new or changed server path ships a role test (no one, viewer without
  access, viewer with access, admin).
- **NFR-13** Tokens stay in the Keychain / Android's encrypted store (today's `TokenStore`); never
  in logs, URLs or crash reports. The only credential in a URL is the stream `sig`, scoped to one
  session and expiring with it.
- **NFR-14** No third-party analytics or ads SDK. Usage data as Epic 44: per app, no identities.
  The Cast SDK is the one Google library, Android only (open question 3).

### Offline, battery, stability, size

- **NFR-15** Home unreachable: cached Home, Libraries and a library show read-only with a banner
  ("Can't reach the home"), Play says why; no crash, no spinner forever (timeouts ≤ 15 s).
- **NFR-16** Battery: nothing polls in the background; Live TV refreshes now / guide only while
  visible (≤ 1 request / 60 s); one hour of 1080p playback on a phone uses ≤ 15 % battery,
  screen on.
- **NFR-17** Crash-free sessions ≥ 99.5 % per TestFlight / internal build (crash reports from
  `CrashReports.swift` and `core/diagnostics`).
- **NFR-18** Size: iOS Player download ≤ 30 MB; Android APK per ABI ≤ 25 MB (fonts and art add
  about 2 MB).

### Tests

- **NFR-19** A UI test per screen per platform, against a mock home (`apps/apple/UITests/MockHome.swift`;
  an OkHttp `MockWebServer` on Android), run in CI on every PR that touches the apps.
- **NFR-20** Screenshot tests per screen in **dark**, in **English and Arabic**, phone and tablet
  (Apple: XCUITest attachments compared with a stored reference; Android: Roborazzi on the JVM),
  run in CI; a change to a reference is reviewed in the PR.
- **NFR-21** Unit tests for every new client call (decoding a recorded answer) and every
  view model; Go tests for every server change, with role tests.

## 7. Design

### Navigation map

```
                    ┌ Start (brand) ─ Home picker ─ Sign in (password | code | cloud)
                    │
   signed in ──► Shell
        Home ──► Video ──► Player
         │  └──► Series ──► Video
         │  └──► Live player
        Libraries ──► Library (Videos | Series) ──► Video | Series
        Live TV (Now | Guide) ──► Live player
        Search ──► Video (at a time) | Series
        Profile ──► History | Favorites | Playlists | Sessions | Settings | Help | About
```

| Form factor | Sections | Detail screens |
|---|---|---|
| iPhone, Duo folded | bottom tab bar | pushed in the tab's stack |
| Duo open flat, iPad | top tabs ↔ sidebar | pushed; Libraries keeps the list beside the page |
| Duo half folded | the hinge split: navigation on one half, page or player on the other | as today |
| Mac | sidebar, menus, ⌘1–⌘5 | pushed; Libraries list beside the page |
| Android phone, tabletop | bottom bar | pushed (Navigation Compose) |
| Android foldable open, small tablet | rail | `Panes`: list beside page from 600 dp |
| Android large tablet | permanent drawer | `Panes` |
| Apple TV, Android TV | top tabs (today's) + **Live TV** | today's |

Players open full screen over everything, except the Duo's half fold.

### Shared design tokens

One source: **`web/design-system/tokens/`** (Story 17.1's JSON).

1. Story 48.2 brings `tokens.json` / `tokens.dark.json` up to the shipped Ink & Brass (today they
   hold a neutral grey dark set; the real values live in `web/src/styles/tokens.css`), adds the
   surfaces the apps need (`raised`, `nav`, `scrim`, `chosen`), and a test that fails when
   `web/src/styles/tokens.css`'s semantic colours differ from the JSON.
2. `build-tokens.mjs` also emits **`apps/apple/MaktabaKit/Sources/MaktabaKit/Tokens.swift`**
   (`Color` light/dark pairs, spacing, radii, type sizes) and
   **`apps/android/core/src/main/kotlin/com/hamzalabs/maktaba/core/ui/Tokens.kt`** (Compose
   `Color`s and `Dp`s). Both are committed, marked generated; `make test-tokens` fails when they
   are stale, so CI catches a hand edit.
3. MaktabaKit's `Theme.swift` and core's `Theme.kt` read the generated tokens. The two TV
   themes (`apps/tv/tvos/MaktabaTV/Theme/Theme.swift`, `apps/tv/android/.../ui/theme/Color.kt`)
   move to them in a follow-up: not needed for Live TV, and it keeps TV files out of this
   epic's design track.

Why generate rather than share at runtime: no runtime cost, no new dependency, each platform
keeps native types, and the source stays one file the web already owns.

### Shared code

- **Apple, MaktabaKit** (`apps/apple/MaktabaKit`): new `Browse.swift` (home, recommendations,
  libraries summary, hub, series, episodes, facets, search, marks, history, playlists, sessions)
  and `Live.swift` (channels now, guide, channel guide, logo URL, `tv/info`, remote tune and
  heartbeat), each with its models in the same file, as extensions of `APIClient` so
  `APIClient.swift` itself is not edited in parallel. The brand (fonts, art, logo, backdrop and
  header views) in `Theme.swift` + `Resources/`. Used by Player, Remote and tvOS.
- **Android, client** (`apps/android/client`, plain Kotlin): `api/Browse.kt`, `api/Live.kt`, used
  by the Player, Android TV and the desktop player. **Core** (`apps/android/core`): theme, brand,
  `Panes`.
- **The players** keep their screens in their own folder: `apps/apple/Player/<Screen>/` and
  `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/<screen>/`, one folder per
  screen, so stories don't share files. Strings: one String Catalog table per screen on Apple
  (`Home.xcstrings`, `Live.xcstrings` …), one `values/strings_<screen>.xml` (+ `values-ar/`) per
  screen on Android.

### Live TV on the TV apps: in this epic

Decided: **in**, as its own track (stories 48.34, 48.35).

- The owner's "where is tv?" is about the TV most of all; shipping Live TV on the phone but not
  on the television would repeat Epic 42's gap.
- The hard parts are shared and built here anyway: the Live client (48.7, 48.9), and the server
  fix G1 that must land before any native live player.
- The files don't overlap: `apps/tv/tvos` and `apps/tv/android` are touched by nothing else in
  this epic, so the track runs in parallel without slowing the phone work.
- Epic 46's 10-foot rules and big-screen Live TV screen are the reference for the layout.

### Release safety

- TestFlight uploads both apps on every `v*` tag (`.github/workflows/testflight.yml`), so a
  half-built screen must never be reachable: the shell (48.13, 48.15) shows only the sections
  whose screens have merged (one list, `PlayerSection.enabled`); each screen story turns its
  section on.
- One release at a time (the owner's rule): this epic adds no release step; its PRs ride the
  normal releases.

## 8. Stories

Each story is one agent in one worktree. "Files" are the files it may edit (the track rule in
[plan.md](plan.md)); a story that needs another file says so in its PR and waits. Every story:
English and Arabic, dark screenshots in both, CI green, the trail updated.

### Phase 0

- **48.1 The spec.** This folder, the epic index row, the Epic 42 note, CHANGELOG, trail.
  - [ ] `specs/epics/48-native-players/{README,research,plan}.md` exist; the index links them.

### Phase 1: foundations (all parallel)

- **48.2 One token source.** Files: `web/design-system/tokens/*.json`, `web/design-system/build/*`,
  new `Tokens.swift` (MaktabaKit), new `Tokens.kt` (core), `Makefile` (`test-tokens` only).
  - [ ] `tokens.dark.json`'s `color.semantic.bg` is `#0B111C` and `accent` `#E3B252`.
  - [ ] A test fails when a semantic colour in `web/src/styles/tokens.css` differs from the JSON.
  - [ ] `make build-tokens` writes `Tokens.swift` and `Tokens.kt`; `make test-tokens` fails if
        either is stale.
  - [ ] A test computes contrast for every text/background pair in dark and light: ≥ 4.5:1.
  - [ ] MaktabaKit and Android core compile with the generated files (nothing reads them yet).
- **48.3 A channel's stream follows library access (G1).** Files: `cmd/maktaba-server/stream_gate.go`,
  its tests.
  - [ ] `/stream/channel/{id}/…` asks `GET /api/channels/{id}` with the caller's credentials
        and caches the verdict per credential and channel for the session cache's TTL.
  - [ ] Role tests: no credentials → 401; viewer without the channel's library → 403; viewer
        with it → 200; a multi-library channel → 200 for any viewer; admin → 200.
  - [ ] Segments of an allowed channel cost no extra API call within the TTL (counted in a test).
  - [ ] The API down → 502, not 401 (today's behaviour kept).
- **48.4 Subtitles as HLS renditions (G3).** Files: `streaming/internal/handlers/manifest.go`, a new
  `subtitle_playlist.go`, tests.
  - [ ] A session with subtitles has `#EXT-X-MEDIA:TYPE=SUBTITLES` lines (language, name,
        `DEFAULT=NO`) in its master, each pointing at a one-segment WebVTT playlist over the
        existing sidecar route, signed like the rest.
  - [ ] hls.js in the web still plays and shows subtitles (the web's player tests pass).
  - [ ] A session without subtitles has an unchanged master (golden test).
- **48.5 Small posters (G4).** Files: `api/internal/handlers/videos/poster.go`, tests.
  - [ ] `?size=small` serves the pipeline's small copy, else the full one; same library guard.
  - [ ] Role test: a viewer without the library → 404 for both sizes.
- **48.6 MaktabaKit: the browse client.** Files: new `MaktabaKit/Sources/MaktabaKit/Browse.swift`,
  new test file and fixtures.
  - [ ] Calls for every browse route in [research.md §4](research.md#4-apis-per-screen) (Home
        to Profile), each decoding a recorded answer from the live API's shape.
  - [ ] Unknown fields and missing optional rows decode (lenient, as `Lenient.swift`).
  - [ ] A 404 maps to `.notAvailable`, a 401 to the existing signed-out path.
- **48.7 MaktabaKit: the Live client.** Files: new `MaktabaKit/Sources/MaktabaKit/Live.swift`, tests.
  - [ ] Now, guide (range), one channel's guide, logo URL, `tv/info`, tune, heartbeat, end.
  - [ ] `LiveSource` picks LAN or relay from `tv/info` and swaps on a heartbeat's new token
        without reopening the player (a unit test with a fake clock).
- **48.8 Android client: browse.** Files: new `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/api/Browse.kt`, tests.
  - [ ] Same checks as 48.6, on `ApiClientTest`'s MockWebServer.
- **48.9 Android client: Live.** Files: new `…/core/api/Live.kt`, tests.
  - [ ] Same checks as 48.7.
- **48.13 Apple Player shell and test harness.** Files: `apps/apple/Player/PlayerApp.swift`, new
  `Player/Shell/*`, `Player/LibraryBrowser.swift` (moved into the Libraries section),
  `apps/apple/project.yml`, `apps/apple/UITests/MockHome.swift`, new `UITests/Player*UITests.swift`,
  `.github/workflows/_apple-apps.yml`, `tools/apple/check-string-catalogs.py`.
  - [ ] `TabView` + `.sidebarAdaptable` with the five sections; `PlayerSection.enabled` holds
        only Libraries (today's screen) until the others merge.
  - [ ] A route enum (`video`, `series`, `library`, `channel`, `watch`) and its
        `navigationDestination`s, each to its screen's view (a stub file the screen story owns).
  - [ ] Duo: the hinge split still works (built locally with Xcode 27; CI builds without it).
  - [ ] Per-screen String Catalog tables, and the guard checks every table of a target.
  - [ ] CI runs the Player UI tests on a throwaway simulator (`xcrun simctl create`, deleted
        after), and stores the screenshots as artifacts; `MockHome` answers every browse and
        live route with fixtures.
- **48.15 Android Player shell and test harness.** Files: `apps/android/player/build.gradle.kts`,
  `apps/android/gradle/libs.versions.toml` (adds navigation-suite, navigation-compose,
  media3-session, media3-cast, play-services-cast-framework, Coil, Roborazzi), `player/…/MainActivity.kt`,
  new `player/…/shell/*`, `player/…/LibraryBrowser.kt` (moved), `.github/workflows/_android-apps.yml`.
  - [ ] `NavigationSuiteScaffold`: bar at compact width / tabletop, rail at medium, drawer at
        expanded (a test per size class with `WindowSizeClass` overrides).
  - [ ] Routes and destinations as on Apple; `PlayerSection.enabled` as on Apple.
  - [ ] CI runs `:player:testPhoneDebugUnitTest` and Roborazzi `verifyRoborazziPhoneDebug`.
  - [ ] Back from a pushed screen restores the list's scroll position (a test).

### Phase 2: brand and components

- **48.10 Apple brand.** After 48.2. Files: `MaktabaKit/Sources/MaktabaKit/Theme.swift`,
  `StartViews.swift`, new `Brand.swift`, `MaktabaKit/Sources/MaktabaKit/Resources/*` (fonts, art, logo),
  `MaktabaKit/Package.swift`, `apps/apple/Player/Info-*.plist` and `Remote/Info-*.plist`
  (launch screen only).
  - [ ] `ThemeMode` with nothing stored is `.dark`; a stored `system` or `light` is kept.
  - [ ] The picker, by-address and sign-in screens show art, logo, «Maktaba» in Amiri and the
        app's name on a frosted panel (screenshot tests, dark and light, en and ar, iPhone and iPad).
  - [ ] Fonts register at start from `Bundle.module`; a test checks Amiri and Plex resolve.
  - [ ] The launch screen is ink with the logo (no white frame in a launch recording).
  - [ ] Remote and tvOS still build and pass their tests.
- **48.11 Android brand defaults.** After 48.2. Files: `apps/android/core/src/main/kotlin/com/hamzalabs/maktaba/core/ui/Theme.kt`,
  `core/…/ui/StartScreens.kt`, `core/src/main/res/values*/themes.xml`, `ThemeStoreTest.kt`.
  - [ ] `ThemeMode.parse(null)` is `Dark`; `InkAndBrass` reads `Tokens.kt`.
  - [ ] The window background and the system splash are ink with the logo in light mode too
        (today `Theme.Maktaba` inherits `Theme.Material.Light`).
  - [ ] Start screens' screenshots (dark, en/ar, phone/foldable/tablet) match the references.
- **48.12 Signed stream URLs pass the gate (G2).** After 48.3. Files: `cmd/maktaba-server/stream_gate.go`, tests.
  - [ ] `/stream/{session}/…` and `/stream/direct/{video}` with a `sig` pass without a cookie or
        bearer token; the streaming service still verifies it.
  - [ ] Channel paths still need a session. Role tests: none → 401; another session's `sig` →
        403; an expired `sig` → 403; a viewer's `sig` for a video outside their libraries cannot
        be minted (the session open refuses it: existing test referenced).
- **48.14 Apple Player components.** After 48.2, 48.6, 48.13. Files: new `apps/apple/Player/Components/*`.
  - [ ] `PosterCard`, `Rail`, `Hero`, `ProgressStrip`, `ChannelLogo`, `Shimmer` (the 0.4 s
        placeholder rule of the web), `ImageCache` (NFR-4 limits, unit-tested).
  - [ ] Each has a preview and a screenshot test (dark, en/ar, Dynamic Type AX3).
- **48.16 Android Player components.** After 48.2, 48.8, 48.15. Files: new `player/…/ui/components/*`.
  - [ ] Same set and checks; images through Coil with NFR-4's limits.

### Phase 3: screens (parallel within each platform; one folder each)

Apple (after 48.13 and 48.14; Files: `apps/apple/Player/<Screen>/*` and its catalog table):

- **48.17 Home.** FR-10 to FR-13.
  - [ ] With the mock home: the hero, then the rows in FR-10's order; an empty row is absent.
  - [ ] Tap a card → the video's page; "Resume" in the context menu → the player at the position.
  - [ ] "Not interested" removes the card and calls the DELETE route.
  - [ ] Second launch shows cached rows before the network answers (mock delays 3 s).
  - [ ] Accessibility audit passes; screenshots dark en/ar, iPhone and iPad.
- **48.18 Libraries, a library, Series, a series.** FR-14 to FR-17.
  - [ ] Pin moves a library first and calls PUT; the grid pages by cursor to the end.
  - [ ] Facets and sort change the request's query (asserted on the mock).
  - [ ] A series' Continue opens its next episode; Mark watched on a season marks each episode.
- **48.19 A video's page.** FR-18.
  - [ ] Resume shows the position from `playback_state`; Play from start opens at 0.
  - [ ] A chapter tap opens the player at its time; Favorite and Mark watched toggle and persist
        on reload.
  - [ ] A 404 shows "not available".
- **48.20 Search.** FR-19, FR-20.
  - [ ] Typing shows suggestions; submit shows Titles and In the transcript; a passage opens the
        player at its time; an Arabic query is sent unchanged.
- **48.21 Live TV.** After 48.7, 48.3. FR-21 to FR-24.
  - [ ] Now lists the mock's channels with progress; Guide scrolls both ways, the now-line at the
        right x; a programme's sheet opens.
  - [ ] The live player plays the mock's HLS; next / previous channel; "Back to live" after
        pause.
  - [ ] With `tv/info` `via_relay`, it tunes, heartbeats (fake clock) and ends on leave.
  - [ ] No channels → the Live TV section is hidden.
- **48.22 Profile and Settings.** FR-25 to FR-31.
  - [ ] Theme Dark is selected on a fresh install; Light changes the app at once.
  - [ ] Language opens the system's app settings (asserted by the open URL); Arabic mirrors.
  - [ ] History removes one item; Sessions signs out one; Sign out returns to the start screen.
- **48.23 The player: resume, next, tracks, quality.** After 48.4. Files: `apps/apple/Player/Playback.swift`,
  new `Player/Watch/*`, `MaktabaKit/PlayerSurface.swift`.
  - [ ] Opens at the resume position; reports progress (asserted on the mock).
  - [ ] Up next counts down and plays the next episode; off in Settings → it stops at the end.
  - [ ] The subtitle picker lists the session's renditions and shows the chosen one; audio
        tracks switch; quality reopens at the same position with `max_bitrate_kbps`.
  - [ ] Controls hide after 4 s; double-tap seeks (mirrored in Arabic).
- **48.24 The player: PiP, AirPlay, Now Playing.** After 48.23, 48.12. Files: as 48.23.
  - [ ] iOS: PiP starts from the button and on going home while playing (UI test checks the PiP
        state); playback continues locked.
  - [ ] AirPlay: the route button shows; with an external route the player hands over the
        signed URL, not the loopback one (unit test on the URL chosen).
  - [ ] Now Playing has title, art, position; remote play / pause / skip work (`MPRemoteCommandCenter`
        test hooks).
- **48.25 The Mac player.** After 48.13. Files: `apps/apple/Player/PlayerApp.swift` (commands only),
  new `Player/Mac/*`, `PlayerMacTests/*`.
  - [ ] View menu ⌘1–⌘5 select sections; ⌘F focuses search; Playback menu has FR-42's keys.
  - [ ] The player fills the window and full-screens on ⌃⌘F; PiP floats.
  - [ ] Mac UI test drives the menus.

Android (after 48.15 and 48.16; Files: `player/…/<screen>/*` and `values*/strings_<screen>.xml`):

- **48.26 Home.** As 48.17.
- **48.27 Libraries, a library, Series, a series.** As 48.18, and list-detail through `Panes` on
  600 dp and the hinge (instrumented `HingeSplitTest` pattern, run locally; JVM test in CI).
- **48.28 A video's page.** As 48.19.
- **48.29 Search.** As 48.20.
- **48.30 Live TV.** After 48.9, 48.3. As 48.21.
- **48.31 Profile and Settings.** As 48.22; Language uses `setApplicationLocales` and
  `locales_config.xml` (a test on Android 13+ and the in-app fallback below).
- **48.32 The player: resume, next, tracks, quality.** After 48.4. Files: `player/…/Playback.kt`,
  `Watch.kt`, `PlaybackFailure.kt`, new `player/…/watch/*`. As 48.23.
- **48.33 The player: PiP, background, Cast.** After 48.32, 48.12. Files: as 48.32,
  `player/src/main/AndroidManifest.xml`, new `player/…/media/*`.
  - [ ] Auto-enter PiP on Home while playing, with play / pause and ±10 s actions.
  - [ ] A `MediaSessionService`: sound continues with the screen off; notification controls.
  - [ ] Cast: the button shows when a receiver is on the network; casting hands the signed URL to
        the Default Media Receiver; progress still reports (unit test with a fake `CastPlayer`).

TV (own track):

- **48.34 Apple TV: Live TV.** After 48.7, 48.3. Files: `apps/tv/tvos/MaktabaTV/ContentView.swift`,
  new `Views/Live*.swift`, its catalog, `UITests` for TV.
  - [ ] A Live TV tab: Now (a grid with progress) and Guide (focusable grid, Epic 46's 10-foot
        sizes); Select tunes; the live player with ▲/▼ channels, the mini-guide, Menu to go back.
  - [ ] `TVLiveUITests` against the mock home, English and Arabic.
- **48.35 Android TV: Live TV.** After 48.9, 48.3. Files: `apps/tv/android/app/src/main/java/com/hamzalabs/maktaba/tv/ui/screens/Live*.kt`,
  `ui/SignedInApp.kt`, `ui/components/TopBar.kt`, `data/TvApi.kt`, strings.
  - [ ] The same, D-pad only; a JVM test for the guide's focus moves.

### Phase 4

- **48.36 Help, store screenshots, verification.** After everything. Files:
  `web/src/help/content/{en,ar}/{apple-apps,android-apps,apple-tv,android-tv,watch-tv}.md`,
  CHANGELOG, `docs/claude-trail.md`.
  - [ ] Help topics describe each section, in both languages; the help coverage test passes.
  - [ ] Every screen walked on a branch stack (scratch Postgres, never the live DB): iPhone,
        iPad, Mac, Pixel phone, a foldable emulator, a tablet emulator, Apple TV and Android TV
        simulators; dark, English and Arabic; the screenshots attached to the PR.
  - [ ] The owner's first five complaints each answered by a screenshot.

## 9. Acceptance

- A fresh install on iPhone and Android phone opens **dark**, with the logo and the library art,
  in English or Arabic.
- Signed in, the app has five sections (four when the home has no channels the user may see),
  adapted per form factor as in §7's table.
- Home shows the hero and the rows of FR-10; Libraries, a library, Series, a video's page,
  Search, Live TV and Profile work as their FRs say, on every form factor.
- The player resumes, plays the next episode, switches subtitles, audio and quality, goes into
  PiP, plays to AirPlay (Apple) and Cast (Android), and keeps sound with the screen locked.
- Apple TV and Android TV have a Live TV tab.
- A viewer limited to one library sees only that library everywhere, and cannot play another
  library's channel even by its id (48.3's role test).
- CI runs a UI test per screen and the dark en/ar screenshot tests on both platforms; NFR-1 to
  NFR-3 are measured once per platform and recorded in the PR of 48.36.

## 10. Open questions for the owner

Only product decisions. Each has a recommendation; work starts on the recommendation unless the
owner says otherwise.

1. **Downloads for offline viewing, in this epic?** Recommended: **no, a follow-up epic.** It
   needs a per-user download right (Plex makes it an owner-granted privilege), storage limits,
   expiry and new API routes with role tests; doing it here would double the server work.
2. **Hide the Live TV section when the user can see no channel?** Recommended: **yes, hide it**
   (Apple's HIG: don't show a tab with nothing in it). The alternative is an empty page that says
   "No channels yet".
3. **Chromecast on Android (Google's Cast SDK, a proprietary Google Play Services library)?**
   Recommended: **yes**, with Google's Default Media Receiver (no app registration), LAN only.
   Without it, Android has no way to put a video on a TV other than the Android TV app.
4. **The Windows and Linux desktop player: same screens now?** Recommended: **follow-up.** It
   shares the Android client, so 48.8 and 48.9 serve it; its Compose Desktop screens are another
   epic once the phone screens settle.
5. **Light theme kept as a choice?** Recommended: **yes**, Dark by default, Light and System in
   Settings, as the web.

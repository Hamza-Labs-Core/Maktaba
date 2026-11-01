# Epic 48 research: how good players are laid out, and what Maktaba already has

Read with the [README](README.md). Everything under *Maktaba today* was checked in the code on
2026-10-31 (main at `64f71b2c`).

## 1. Other players

### Plex (phone, tablet, desktop, TV)

- **Navigation.** The mobile apps open on Home, a page of recommendation rows (Continue
  Watching / Up Next, Recently Added per library, Live TV). A bottom bar picks the source
  (Home, Live TV, the libraries); a user menu holds the profile, downloads, settings and
  switching user. Tablets and desktop put the same items in a sidebar.
  ([Navigating the mobile apps](https://support.plex.tv/articles/navigating-the-mobile-apps/))
- **Live TV.** A server with a tuner shows Live TV in the same app: what is on now, a guide,
  and a full-screen live player; time shifting on recent servers. Apps: Android, iOS, Android
  TV, Apple TV, Fire TV, Chromecast. Admins choose whether managed users get it.
  ([Live TV & DVR](https://support.plex.tv/?p=39105),
  [Live TV on Plex](https://support.plex.tv/articles/free-live-tv-streaming-overview/))
- **Downloads.** Offline copies on Android, iOS, Mac and Linux, behind Plex Pass and a
  per-user "Download" right granted by the server owner.
  ([Downloads](https://support.plex.tv/?p=46164))
- **Detail page.** Backdrop, poster, title, year, length, Play / Resume, a seasons picker and
  episodes with watched marks, cast, related rows.
- **Player.** Big centre Play / Pause and ±10 s, a scrubber with thumbnails, subtitle and
  audio pickers, quality (Original / Convert to …), Cast / AirPlay, PiP, auto-play next with
  a countdown, Skip intro.

### Jellyfin clients

- Official and community clients follow the same shape: Home rows (Continue Watching, Next
  Up, Latest per library), library grids with filters, a series page with seasons, and a
  player with track pickers. Findroid (native Android) lists trickplay and media segments
  (skip intro) as done and Chromecast as planned; Swiftfin (native iOS/tvOS) uses VLC for
  direct play. ([Jellyfin clients](https://jellyfin.org/downloads/clients/all),
  [Findroid](https://github.com/jarnedemeulemeester/findroid))
- Lesson for us: the screens are table stakes; quality is in the player (direct play first)
  and in a Home that answers "what do I watch next".

### Infuse

- Connects to Plex, Jellyfin and Emby in *Direct mode* (the server's own rows and metadata)
  or *Library mode* (its own categories over several sources). Favourite libraries become
  Home rows; on Apple TV it fills the Top Shelf.
  ([Streaming from Plex, Emby, and Jellyfin](https://support.firecore.com/hc/articles/360006462093),
  [App Store listing](https://apps.apple.com/app/id1136220934))
- Lesson: show the server's rows as they are (Maktaba's `/api/home` and
  `/api/recommendations` already shape them); polish is in artwork, type and the player.

### Apple TV app and Netflix

- **Apple TV app** (iPhone, iPad, Mac): tabs on iPhone (Home, Apple TV+, Store, Library,
  Search); the same items as a sidebar on iPad and Mac; a full-bleed hero at the top of
  Home, then rows; Up Next is the first row; a detail page with a big Play button and a
  seasons picker. ([Apple TV User Guide](https://support.apple.com/guide/tv/welcome/web))
- **Netflix**: bottom tabs on phones (Home, New & Hot, My Netflix); downloads live in "My
  Netflix", per-title and "Downloads for You", with limits per title and device.
  ([Downloading on Netflix](https://help.netflix.com/en/node/54816))
- Common to every one of them: **dark by default**, the brand on the first screen, a hero,
  poster rows, Continue Watching first, a detail page before playback, PiP and AirPlay /
  Cast in the player, sound that continues with the screen locked.

### What we take

| Pattern | Plex | Jellyfin | Infuse | Apple TV | Netflix | Maktaba 48 |
|---|---|---|---|---|---|---|
| Phone: bottom tabs | yes | yes | yes | yes | yes | yes (5 tabs) |
| Tablet / desktop: sidebar | yes | yes | yes | yes | n/a | yes |
| Home hero + rows | yes | rows | rows | yes | yes | yes (`featured` + rows) |
| Continue watching first | yes | yes | yes | yes | yes | yes |
| Live TV now + guide | yes | yes | no | no | no | yes |
| Detail page with Resume | yes | yes | yes | yes | yes | yes |
| Next episode countdown | yes | yes | yes | yes | yes | yes |
| Subtitle / audio pickers | yes | yes | yes | yes | yes | yes |
| PiP, AirPlay / Cast | yes | partly | yes | yes | yes | yes |
| Background audio | yes | yes | yes | yes | no | yes (already on Apple) |
| Downloads | Pass only | some | yes | yes | yes | **not in 48** (open question 1) |

## 2. Platform guidance

### Apple (iPhone, iPhone Duo, iPad, Mac)

- **Tab bar on iPhone, sidebar on iPad and Mac.** `TabView` with
  `.tabViewStyle(.sidebarAdaptable)`: a bottom tab bar in compact width, a top tab bar that
  turns into a sidebar on iPad (the person can switch), always a sidebar on macOS and tvOS.
  Tab sections show in both. ([sidebarAdaptable](https://developer.apple.com/documentation/swiftui/tabviewstyle/sidebaradaptable),
  [Enhancing your app with tab navigation](https://developer.apple.com/documentation/swiftui/enhancing-your-app-content-with-tab-navigation))
- Known catch: a `NavigationSplitView` inside a tab can show two sidebar buttons once the
  tab bar becomes a sidebar ([Apple forums](https://origin-devforums.apple.com/forums/profile/benjidea)),
  so tabs hold `NavigationStack`s, and only the Libraries tab on a wide window keeps the
  split.
- HIG: [Tab bars](https://developer.apple.com/design/human-interface-guidelines/tab-bars)
  (at most five on iPhone, never hide one to mean "disabled"),
  [Sidebars](https://developer.apple.com/design/human-interface-guidelines/sidebars),
  [Designing for iPadOS](https://developer.apple.com/design/human-interface-guidelines/designing-for-ipados),
  [Designing for macOS](https://developer.apple.com/design/human-interface-guidelines/designing-for-macos)
  (menus and keyboard shortcuts for every command),
  [Playing video](https://developer.apple.com/design/human-interface-guidelines/playing-video).
- **iPhone Duo.** The Player already follows Apple's guidance (in-code comments in
  `apps/apple/Player/LibraryBrowser.swift`): a `NavigationSplitView` that is one column
  folded and columns open flat; half folded, an `ArrangementView` split at the hinge
  (`onHingeChange`), behind `#if os(iOS) && compiler(>=6.4)` and `if #available(iOS 27.1, *)`.
  Epic 48 keeps that: the shell's open-flat layout is the iPad one, the half-folded layout
  stays the hinge split with the player on one half.
- **Player.** `AVPlayerViewController` gives PiP, AirPlay, the Now Playing info and remote
  commands nearly free; the custom overlay must not break them.
  [Adopting Picture in Picture in a standard player](https://developer.apple.com/documentation/avkit/adopting-picture-in-picture-in-a-standard-player),
  [AVRoutePickerView](https://developer.apple.com/documentation/avkit/avroutepickerview),
  [MPNowPlayingInfoCenter](https://developer.apple.com/documentation/mediaplayer/mpnowplayinginfocenter).
- **AirPlay catch for Maktaba.** The Player reads every stream through `StreamProxy` on the
  phone's loopback (`apps/apple/MaktabaKit/Sources/MaktabaKit/StreamProxy.swift`) so the token
  stays fresh. An AirPlay receiver is handed the URL and fetches it itself, so a loopback URL
  cannot work there. AirPlay needs a URL the receiver can fetch on its own: the session's
  signed URL (`?sig=`), which today the web port refuses without a cookie or bearer token
  (gap G2 below).

### Android (phone, foldable, tablet)

- **Adaptive navigation.** Material 3's `NavigationSuiteScaffold` shows a navigation bar
  when the window is compact (or the device is in tabletop posture) and a navigation rail
  otherwise, changing at runtime; `layoutType` can force a permanent drawer on expanded
  widths. ([Build adaptive navigation](https://developer.android.com/develop/ui/compose/layouts/adaptive/build-adaptive-navigation),
  [Window size classes](https://developer.android.com/develop/ui/compose/layouts/adaptive/use-window-size-classes))
- Material 3: [Navigation bar](https://m3.material.io/components/navigation-bar/overview)
  (3–5 destinations), [Navigation rail](https://m3.material.io/components/navigation-rail/overview),
  [Navigation drawer](https://m3.material.io/components/navigation-drawer/overview).
- **Foldables.** The Player already splits at the hinge (`apps/android/core/src/main/kotlin/com/hamzalabs/maktaba/core/layout/Panes.kt`:
  one pane under 600 dp, list beside page from 600 dp, tabletop and book postures). The new
  shell keeps `Panes` for list-detail screens and lets `NavigationSuiteScaffold` pick bar or
  rail.
- **Player.** Media3: a `MediaSessionService` keeps sound going in the background and gives
  the notification and lock-screen controls
  ([Background playback](https://developer.android.com/media/media3/session/background-playback));
  PiP with `enterPictureInPictureMode` and `setAutoEnterEnabled`
  ([Picture-in-picture](https://developer.android.com/develop/ui/views/picture-in-picture));
  Cast through the Cast SDK and Media3's `CastPlayer`
  ([Cast Android sender](https://developers.google.com/cast/docs/android_sender)). A Cast
  receiver also fetches the URL itself: same need as AirPlay (gap G2).
- **Per-app language** (Android 13+): `AppCompatDelegate.setApplicationLocales` with a
  `locales_config.xml`; on Apple, the system's per-app language in Settings.

## 3. Maktaba today

### The web (`web/src`)

- **Routes** (`web/src/App.tsx`): `/` Home, `/library`, `/library/:libraryId`, `/series`,
  `/series/:id`, `/videos/:videoId`, `/videos/:videoId/watch`, `/search`, `/guide`,
  `/live/:number`, `/playlists`, `/account`, `/account/history`, `/account/preferences/*`,
  `/help/:topic`, and `/big/*` (Epic 46).
- **Navigation** (`web/src/components/AppShell.tsx`): a side nav on desktop; a bottom tab bar
  at ≤ 640 px with Home, Libraries, Guide, Search, More.
- **Home** (`web/src/pages/Home.tsx`, `web/src/lib/browse.ts`): the hero (`featured`), then
  Continue Watching, On Deck, Favorites, Recently Added, Recently Watched, Live TV on now
  (`components/WhatsOnNow.tsx`), one row per pinned library.
- **Live TV** (`web/src/pages/LiveTV/`): `Guide.tsx` (channel × time grid, What's on now),
  `Player.tsx` (tune banner, mini-guide, number entry, PiP, return to live; LAN path
  `/stream/channel/{id}/index.m3u8`, away path `POST /api/tv/remote/tune` plus heartbeat),
  `useLiveHls.ts`.
- **Player** (`web/src/pages/VideoPlayer.tsx`): stream session, HLS or direct, sidecar
  subtitles (`session.subtitles`), progress posts, Up next.
- **Login** (`web/src/pages/Login.tsx`, `web/src/styles/library.css`): the library art behind
  (`web/public/art/library/{dark,light}-{wide,phone}-{1,2}.jpg`), a frosted panel, the Amiri
  wordmark.
- **Theme**: `web/src/styles/tokens.css` is the shipped *Ink & Brass* (dark) and *gold on
  white* (light); `web/public/theme-boot.js` resolves "system" and falls back to dark.

### The TV apps

- **Apple TV** (`apps/tv/tvos/MaktabaTV`): tabs Home, Libraries, Search, Settings
  (`ContentView.swift`); its own `Theme/Theme.swift` (325 lines, the full palette, fonts,
  art, focus ring), fonts and art in `Resources/`. Home rows from
  `/api/recommendations?surface=tv-home`. **No Live TV.**
- **Android TV** (`apps/tv/android`): Home, Libraries, Search, Video, Player, Account
  (`ui/screens/`), its own `ui/theme/Color.kt`; builds the phone apps' `:client` and `:core`.
  **No Live TV.**

### The phone, tablet and Mac players

- **Apple Player** (`apps/apple/Player`, 673 lines in four Swift files): `PlayerApp.swift`
  (theme default `ThemeMode.system`), `LibraryBrowser.swift` (the split view and the Duo
  hinge split), `VideoDetail.swift` (49 lines), `Playback.swift` (stream session through
  `StreamProxy`, progress every 10 s, background audio category). On iOS the picture is
  SwiftUI's `VideoPlayer` (`MaktabaKit/PlayerSurface.swift`): no PiP, no subtitle or audio
  picker, no next episode; the Mac uses `AVPlayerView` (`PlayerHostView.swift`). MaktabaKit's start screens
  (`StartViews.swift`: `HomePickerView`, `SignInView`) use system lists and SF Symbols: no
  logo, no art, no fonts. The only brand asset in `apps/apple/Shared/Assets.xcassets` is the
  app icon.
- **Android Player** (`apps/android/player`, about 930 lines): `LibraryBrowser.kt` (a list,
  `Panes`), `Watch.kt`, `Playback.kt` (Media3 ExoPlayer, HLS, OkHttp); no MediaSession, no
  PiP, no Cast. **Core is branded**: `core/ui/StartScreens.kt` draws the library art
  (`res/drawable-nodpi/library_{dark,light}_{phone,wide}.jpg`), the logo (`maktaba_logo.png`)
  and a panel, in Amiri and IBM Plex Sans Arabic (`res/font/`). But `core/ui/Theme.kt`'s
  `ThemeMode.parse` defaults to `System`, so a phone in light mode starts light.
- **Tests in CI**: `_apple-apps.yml` runs MaktabaKit's tests and *builds* the apps (no UI
  tests run); `_android-apps.yml` runs `:client:test :core:testDebugUnitTest
  :remote:testPhoneDebugUnitTest` and lint (the Player has no unit tests in CI, and no
  instrumented tests run).

### Design tokens

- `web/design-system/tokens/tokens.json` (+ `.dark`, `.high-contrast`) and
  `web/design-system/build/build-tokens.mjs` (Story 17.1) emit CSS, TS and JSON. The Makefile
  says the build makes Swift and Kotlin too; it does not. Its dark semantic set is neutral
  grey, not Ink & Brass: the shipped values live in `web/src/styles/tokens.css`.
- The palette is copied by hand five times: `web/src/styles/tokens.css`,
  `apps/apple/MaktabaKit/Sources/MaktabaKit/Theme.swift` (five colours),
  `apps/tv/tvos/MaktabaTV/Theme/Theme.swift`, `apps/android/core/src/main/kotlin/com/hamzalabs/maktaba/core/ui/Theme.kt`,
  `apps/tv/android/app/src/main/java/com/hamzalabs/maktaba/tv/ui/theme/Color.kt`.

## 4. APIs per screen

Every route below was found in the Go handlers. "Scope" is the server-side rule.

| Screen | Route(s) | Handler | Scope |
|---|---|---|---|
| Connect, sign in | `GET /api/discovery/home`, `POST /api/auth/login`, `POST /api/auth/refresh`, `POST /api/pairing/device`, `/api/pairing/device/token`, `POST /api/auth/cloud` | `api/internal/handlers/auth/auth.go`, `handlers/discovery/` | public / pairing |
| Home | `GET /api/home`, `GET /api/recommendations?surface=mobile-home` | `handlers/browse/browse.go`, `handlers/recommendations/recommendations.go` | the caller's libraries |
| Home: dismiss | `DELETE /api/recommendations/items/{video_id}`, `/rows/{reason_kind}` | recommendations | the caller's own |
| Libraries | `GET /api/libraries/summary`, `GET/PUT /api/me/pinned-libraries` | browse | the caller's libraries |
| A library | `GET /api/libraries/{id}/hub`, `GET /api/videos?library=…`, `GET /api/videos/facets`, `GET /api/libraries/{id}/series` | browse, `handlers/videos/videos.go` | 404 when not visible |
| Series | `GET /api/series`, `/api/series/{id}`, `/api/series/{id}/episodes` | `handlers/series/series.go` | the caller's libraries |
| A video | `GET /api/videos/{id}`, `/api/me/videos/{id}`, `/api/videos/{id}/chapters`, `/subtitles`, `/segments`, `/trickplay`, `/poster` | videos, browse | library guard |
| Marks | `POST /api/me/watched`, `POST /api/me/favorites` | `handlers/browse/mine.go` | the caller's own, visible videos |
| Search | `POST /api/search`, `GET /api/search/results/{sid}`, `/api/search/suggest`, `/api/search/recent` | `handlers/search/search.go` | the caller's libraries |
| Live TV: now, guide | `GET /api/channels/now`, `/api/channels/guide`, `/api/channels/{id}/guide`, `/api/channels/{id}/logo` | `handlers/guide/guide.go`, `handlers/channels/channels.go` | `canRead`: admin / all-library, or the channel's library |
| Live TV: play (home) | `GET /stream/channel/{id}/index.m3u8` through the web port | `cmd/maktaba-server/stream_gate.go` → `streaming/internal/frontdoor` | **signed-in only** (gap G1) |
| Live TV: play (away) | `GET /api/tv/info`, `POST /api/tv/remote/tune`, `/api/tv/remote/sessions/{id}/heartbeat`, `DELETE …/sessions/{id}` | `handlers/tv/tv.go`, `handlers/tvremote/viewer.go` | signed-in viewer |
| Player (VOD) | `POST /api/stream/sessions` (`start_sec`, `audio_track`, `subtitle_track`, `max_bitrate_kbps`, `client_profile`), `/progress`, `/errors`, `DELETE` | `handlers/streaming/streaming.go` | session per user; `/stream/{session}` and `/stream/direct/{video}` signed (`?sig=`) and library-guarded in `streaming/internal/server/server.go` |
| Profile | `GET/PATCH /api/me`, `/api/me/sessions`, `/api/me/history`, `/api/me/activity/settings`, `/api/me/cloud-link` | auth, `handlers/watch/watch.go` | the caller's own |
| Settings | per device (theme, language, playback), as on the web (`web/src/pages/Profile/Preferences.tsx` keeps them in local storage) | — | — |

### Gaps

- **G1. A channel's live stream ignores library access.** `/api/channels/now` and the guide
  hide a library-scoped channel from a viewer without that library (`canRead`), but the web
  port's gate (`cmd/maktaba-server/stream_gate.go`) lets *any* signed-in session fetch
  `/stream/channel/{uuid}/…`. A viewer who learns a channel id can watch it. Native Live TV
  must not ship on that. Fix: the gate checks the channel with the caller's credentials
  (`GET /api/channels/{id}` answers 404 when the caller may not read it) and caches the
  verdict per credential and channel, like the sign-in check. Role tests: no one → 401,
  viewer without the library → 403, viewer with it → 200, admin → 200, multi-library channel
  → 200 for any viewer.
- **G2. A signed stream URL is refused without a cookie or bearer token.** AirPlay and Cast
  receivers fetch the URL themselves and carry neither. `/stream/{session}/…` and
  `/stream/direct/{video}` are already verified by the streaming service (`?sig=`, audience,
  expiry, `LibraryGuard`). Fix: the gate lets those two path families through when they carry
  a `sig` the streaming service accepts, and still demands a session for channel paths
  (whose `?sig=` does not exist). Role tests: no `sig` and no session → 401; a `sig` for
  another session → 403 from the streaming service; an expired `sig` → 403.
- **G3. Subtitles are sidecar only.** The session lists `subtitles` (VTT) beside the HLS;
  hls.js and ExoPlayer side-load them, AVPlayer cannot, and neither can an AirPlay or Cast
  receiver. Fix: the master playlist (`streaming/internal/handlers/manifest.go`) also lists
  each subtitle as an `EXT-X-MEDIA TYPE=SUBTITLES` rendition (a one-segment WebVTT media
  playlist over the existing sidecar route). Direct play (an MP4 URL) on Apple draws the VTT
  itself.
- **G4 (performance, small). Posters come at full size.** `GET /api/videos/{id}/poster`
  serves the best image, `Cache-Control: private, max-age=3600`; the pipeline already keeps a
  small copy (`handlers/videos/poster.go`). Fix: `?size=small` serves it, same library guard.

No other new route is needed: every screen reads routes the web and the TV apps already use.

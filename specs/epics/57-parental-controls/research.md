# Epic 57 research: Apple's parental controls, and what the home has to work with

Read on 2026-11-08: Apple's developer documentation (through each page's `.md` variant, since the
pages render with JavaScript), Apple's support articles, and the Maktaba repository on `main` at
c4b6d541b. Where a claim could not be confirmed from Apple's own pages it says so.

## 1. Apple's stack

### 1.1 Declared Age Range

- Framework `DeclaredAgeRange`, **iOS, iPadOS, macOS and Mac Catalyst 26.0+; not tvOS, watchOS or
  visionOS.** ([AgeRangeService](https://developer.apple.com/documentation/declaredagerange/agerangeservice))
- Entitlement `com.apple.developer.declared-age-range`, added with the Declared Age Range
  capability in Xcode. Apple's page names no approval step.
  ([entitlement](https://developer.apple.com/documentation/bundleresources/entitlements/com.apple.developer.declared-age-range))
- Request: `AgeRangeService.shared.requestAgeRange(ageGates:_:_:in:)` (UIKit, a view controller),
  or SwiftUI's `@Environment(\.requestAgeRange)`. An app can pass at most 3 gates (4 ranges), each
  range at least 2 years wide. Local law may override the gates.
  ([Requesting people share their age range](https://developer.apple.com/documentation/declaredagerange/requesting-people-share-their-age-range-with-your-app))
- Answer: `.sharing(AgeRange)` or `.declinedSharing`. `AgeRange` has `lowerBound` (nil: under the
  lowest gate), `upperBound` (nil: at or over the highest), `ageRangeDeclaration`
  (`.selfDeclared`, `.guardianDeclared`, and `.confirmed` from 26.5) and
  `activeParentalControls` (today only `.communicationLimits`). Errors are `invalidRequest` and
  `notAvailable`.
  ([AgeRangeDeclaration](https://developer.apple.com/documentation/declaredagerange/agerangeservice/agerangedeclaration),
  [ParentalControls](https://developer.apple.com/documentation/declaredagerange/agerangeservice/parentalcontrols))
- Consent: a system sheet. For a child in Family Sharing the parent picks always share, ask each
  time, or never. Answers are cached, and a child crossing into a new range keeps the old one
  until the anniversary of the declaration unless it is updated in Settings. Some regulated
  regions do not allow declining.
- Regulation after 26.0: `isEligibleForAgeFeatures` (26.2+, true where age assurance is
  required; always false on macOS), `requiredRegulatoryFeatures` (26.4), the significant-update
  acknowledgement sheet for adults, PermissionKit's `SignificantAppUpdateTopic` for minors'
  parental consent, `AppStore.ageRatingCode` (StoreKit) to see a changed age rating, and the App
  Store Server Notification `RESCIND_CONSENT`.
  ([Implementing age assurance and permissions](https://developer.apple.com/documentation/declaredagerange/implementing-age-assurance-and-permissions),
  [Age assurance](https://developer.apple.com/support/age-assurance/))
- Texas SB2420: Apple's notice of 2025-11-04 ([news](https://developer.apple.com/news/?id=2ezb6jhj))
  required age assurance and parental consent for minors from 2026-01-01; a court enjoined the
  law on 2025-12-23 and Apple paused. Press reports a stay in about June 2026 and a resumption;
  **not confirmed on Apple's own site.** Developer forum threads report `isEligibleForAgeFeatures`
  returning false where it should be true
  ([828152](https://developer.apple.com/forums/thread/828152)).
- WWDC26 (2026-06-08): a new Child Account (required under 13), a redesigned Screen Time, Ask to
  Browse and Time Allowances on iOS, iPadOS and macOS 27; press names no tvOS support and no new
  DeclaredAgeRange API. **Secondary source only.**

**For Maktaba:** the Player targets iOS 26.0, so the API is on every supported iPhone and iPad
with no availability checks. Gates 9, 13, 18 (story 57.04). The Mac Player targets macOS 15, so
it would need `if #available(macOS 26, *)`; it is outside this epic.

### 1.2 Screen Time API (FamilyControls, ManagedSettings, DeviceActivity)

- `FamilyControls.AuthorizationCenter`: iOS and iPadOS 15+ only.
  `requestAuthorization(for: .child)` is approved by a parent in the same family;
  `.individual` by the owner with Face ID or Touch ID. `FamilyActivityPicker` returns opaque
  tokens. ([FamilyControls](https://developer.apple.com/documentation/familycontrols))
- **Entitlement `com.apple.developer.family-controls` needs Apple's approval for distribution.**
  It works in development as soon as the capability is added. For the App Store a request goes
  through [the request form](https://developer.apple.com/contact/request/family-controls-distribution)
  or the Capability Requests tab, and **each extension** (Device Activity Monitor, Device Activity
  Report, Shield Action, Shield Configuration) needs its own.
  ([Requesting the Family Controls entitlement](https://developer.apple.com/documentation/familycontrols/requesting-the-family-controls-entitlement))
- `ManagedSettingsStore`: iOS 15+, tvOS 26+. Its `media` group (`MediaSettings`, iOS 15+) has
  `maximumMovieRating`, `maximumTVShowRating`, `denyExplicitContent`. US movie scale: 1000 All,
  500 NC-17, 400 R, 300 PG-13, 200 PG, 100 G, 0 None. US TV scale: 1000 All, 600 TV-MA,
  500 TV-14, 400 TV-PG, 300 TV-G, 200 TV-Y7, 100 TV-Y, 0 None.
  ([MediaSettings](https://developer.apple.com/documentation/managedsettings/mediasettings))
- **Reading the device's limit needs no authorization.** Apple's article "Confirming the effective
  TV and movie ratings" says a media app can read `effectiveMaximumMovieRating`,
  `effectiveMaximumTVShowRating` and `effectiveDenyExplicitContent` without Family Controls
  authorization (1000 means no limit). Observing changes through the `$effective…` publishers
  needs the entitlement. `effectiveMaximumMovieRating` lists tvOS 26+.
  ([Reading media](https://developer.apple.com/documentation/managedsettings/readingmedia))
- **Open:** Apple's article describes "the parental control app" setting the value. Whether the
  effective value also reflects the parent's own Settings > Screen Time > Content & Privacy
  Restrictions is **not stated**; story 57.04 carries a real-device check before release.
- `DeviceActivity`: schedules, a monitor extension, a report view and extension. Not used here.
- App limits, downtime and Always Allowed shield the app; no Apple text asks anything of the app
  itself (inferred from the docs).

### 1.3 Content & Privacy Restrictions and App Store age ratings

- Parents set them in Settings > Family > (child) > Content & Privacy Restrictions (or Screen Time
  on the child's device): each content type Don't Allow, Allow All, or a rating; **Ratings For**
  picks the country whose labels show. ([support 105121](https://support.apple.com/en-us/105121))
- App Store age ratings are now **4+, 9+, 13+, 16+, 18+** (12+ and 17+ are gone), with a new
  questionnaire including In-App Controls (Parental Controls, Age Assurance).
  ([Age ratings values](https://developer.apple.com/help/app-store-connect/reference/app-information/age-ratings-values-and-definitions/))
  The 2026-01-31 deadline for answering it is from press, not Apple's page.
- A child whose limit is under an app's rating can ask a parent for an exception to download it.

### 1.4 Family Sharing and Ask to Buy

- Ask to Buy covers App Store downloads and in-app purchases, not redownloads or updates; in some
  regions it is on and fixed for under-18s. ([support 105055](https://support.apple.com/en-us/105055))
- StoreKit 2: a purchase awaiting a parent returns `Product.PurchaseResult.pending`; an approval
  arrives on `Transaction.updates`.
  ([pending](https://developer.apple.com/documentation/storekit/product/purchaseresult/pending))
- **Maktaba's iOS app has no StoreKit and sells nothing** (no `import StoreKit` in `apps/`), so
  Ask to Buy applies only to installing the free app.

### 1.5 tvOS and the Mac

| API | iOS/iPadOS | tvOS | macOS |
|---|---|---|---|
| DeclaredAgeRange | 26+ | no | 26+ |
| FamilyControls authorization | 15+ | no | no |
| `ManagedSettingsStore` effective movie/TV limit (read) | 15+ | 26+ | (Catalyst 15+) |
| iOS 27 Child Account, Ask to Browse | 27 | not mentioned | 27 |

So Apple TV gets the rating limit but not the age range (story 57.11).

### 1.6 Rating systems by region

- Apple's film delivery spec names the US MPA and TV Parental Guidelines, with country sections
  for Brazil, India, Korea, New Zealand, South Africa, Türkiye and Vietnam.
  ([filmspec](https://help.apple.com/itc/filmspec/en.lproj/apdA57c603fc.html))
- Saudi Arabia's movie ratings PG12, R12, R15, R18 were added in 2021; the UAE catalogue listed
  only UR (unrated) in 2022. ([announcement](https://tvpartners.apple.com/support/3865-apple-tv-app-ratings-update-announcement))
- **Not confirmed:** the BBFC and Egypt sections, the current Ratings For country list, and how
  the 0–1000 scale reads for non-US regions. Story 57.01's table treats the scale's US anchors as
  the values and maps other regions' labels by their age.

## 2. The home

### 2.1 Users, roles, tokens

- `users` (`shared/db/migrations/0029_users.sql`): `is_admin` boolean, so two roles: admin and
  viewer. `library_acl` (0030; `role` added in 0072) grants per-library read.
- `principal.Principal` (`api/internal/auth/principal/principal.go:42-77`): `UserID, IsAdmin,
  Libraries, AccessAllLibraries, Source, MustChangePassword, CloudScope`; `HasLibrary` (:161),
  `ApplyCloudScope` (:94). No device or age.
- JWT claims `usr, lib[], is_admin, scp` (`api/internal/auth/jwt/jwt.go`); access tokens 15 min
  (`api/internal/handlers/auth/auth.go:67`), cloud tokens 5 min (`auth/cloud.go:227`). Cookie and
  PAT principals read the ACL per request (`auth.go:807`).
- `refresh_tokens` (0053) carry a nullable `device_id`; `devices` (0040, 0065) is the push-token
  registry (`platform ios|android|web`). `X-Device-ID` is client-asserted and used only by
  downloads (`api/internal/handlers/devices/downloads.go:73`).
- Gates: `RequireAuthExcept`, `RequireAdmin` (`api/internal/auth/middleware/middleware.go:189,
  280, 357`), plus about 18 local `requireAdmin`/`adminOnly` helpers. The role-test pattern is
  `TestAdminOnly` (`api/internal/handlers/cluster/cluster_test.go:629`) with `admin()` and
  `viewer()` fixtures (:113-114).
- Code sign-in (`api/internal/handlers/discovery/device.go`): `Approve` (:354) signs the device
  in as the approver; cloud sign-ins cannot approve.
- Cloud: the relay agent forwards to the loopback API; `StripVia` trusts `X-Maktaba-Via` only from
  loopback (`api/internal/middleware/via.go`). Linked accounts (0153, `home_user_links.scope`
  play|manage) sign in at `POST /api/auth/cloud`; `ApplyCloudScope("play")` clears admin;
  `cloudlink.Gate` re-checks the link every 5 s.
- No profile, household, kid, guardian, parental or age concept anywhere. Epic 45's README lists
  parental controls as missing and shelves story 45.23
  (`specs/epics/45-libraries-ui/README.md:250-251, 350`).

### 2.2 Where content is served

No central check; each package scopes by library:

| Surface | Route(s) | Scope helper |
|---|---|---|
| Home, hub, categories, On Deck, Continue Watching, favourites, playlists | `/api/home*`, `/api/libraries/{id}/hub`, `/api/me/playlists` | `browse/browse.go:138-169`; row cache `browse/rowcache.go:3-10` (per library, "no per-user part") |
| Videos | `/api/videos`, `/api/me/videos`, `/api/videos/{id}` and `/poster`, `/trickplay`, `/segments`, `/subtitles`, `/chapters`, `/context`, `/facets` | `videos.CanRead` (`videos/videos.go:1003`) |
| Search | `/api/search`, `/results/{sid}`, `/passages`, `/suggest`, `/saved`; `/api/search/youtube` and `import-youtube` reach YouTube itself | `search/results.go:219, 749`, `search/youtube.go:184` |
| Recommendations | `/api/recommendations` (from nightly `user_recs`, 0041) | `recommendations.go:359` |
| Collections, series | `/api/collections*`, `/api/series*` | `collections.go:856`, `series.go:229` |
| Play queues (Epic 56) | `/api/me/play-queues*` | `playqueue/chunks.go:450`, `sources.go:371` |
| Streaming | `POST /api/stream/sessions` → signed URLs (`auth/signedurl`, 24 h cap) | `streaming/streaming.go:372`; streaming service `SignedURL` + `LibraryGuard` (`streaming/internal/server/server.go:99-181`) |
| Live | `/api/channels*`, guide, `/now`, `/xmltv`, `/playlist.m3u`, `/api/tv/*`; streams `/stream/channel/{id}/…` and HDHomeRun | `channels.go:633`, `guide.go:328`; **channel streams and HDHomeRun unsigned** (`streaming/internal/channel/handler.go:35`, `streaming/internal/hdhr/handler.go`); a channel with `library_id` null is visible to all (`channels.go:631`) |
| Plugin panels | `/api/panels`, `/api/panels/image` | `plugins/panels.go:146, 182` |
| Thumbnails | `/api/thumbnails/{hash}/small.jpg` | `thumbVisible` (`reports/reports.go:350`) |
| Websocket | `/api/ws` | `ws/ws.go:166` |
| GraphQL | `/graphql` | replays REST handlers (`graphql/bridge.go`) |
| Watch history | `/api/watch/*`, `/api/me/history` | `watch/watch.go` |
| Downloads | `/api/videos/{id}/downloaded` flag only; bytes over `/stream/direct` | `devices/downloads.go:87` |

### 2.3 Metadata that can carry a rating

| Source | Where | Today |
|---|---|---|
| Plex | `plex_items.detail->>'contentRating'` (0092; `pipeline/.../extensions/plex_items.py:38`) | kept, unused; also copied into dizqueTV programmes |
| Live guide | `channel_programs.title_snapshot.rating` → XMLTV `<rating>` (`guide/xmltv.go:142`, `snapshot.go:27`) | kept, exported, not enforced |
| Plugins | `videos.metadata->>'content_rating'` via `set-field` (`plugins/routes.go:356-410`, WIT `:147`) | free text |
| Enrichment | `media_metadata_enrichment.mapped.content_rating` (0077), read by the context card (`contextcard/contextcard.go:136`) | nothing writes it |
| YouTube | `youtube_videos` (0090): title, date, duration, tab, description | no age field; yt-dlp's `age_limit` is not kept |
| Classification | `videos.content_type` (0045), `video_classification`, `media_entities` (0074), tags (0034), topics (0046, 0090) | useful for a library's default tier, not a rating |
| Transcripts | `transcript_segments.search_tsv` (0016) | no screening (non-goal) |

No rating column exists in any migration. The next free migration slot is 0216.

### 2.4 The Apple apps

- XcodeGen `apps/apple/project.yml`: Player (iPhone `com.hamzalabs.maktaba.player`, iPad
  `.player.tablet`, Mac `.player.mac`), Remote, and TV (`com.hamzalabs.maktaba.tv`); iOS 26.0,
  tvOS 26.0, macOS 15.0; shared `MaktabaKit` (`Package.swift`).
- No iOS or tvOS entitlements file; Info.plist has Local Network, Bonjour, camera, background audio.
- Player tabs `home, libraries, liveTV, search, profile` (`Player/Shell/PlayerSection.swift`);
  `Capabilities.swift` reads `/api/capabilities` (the hook for the restricted UI);
  `StreamProxy.swift` adds a fresh token per request; `AirPlay.swift`, `PictureInPicture.swift`;
  `PluginPanels.swift`; `PlayQueue.swift`. No downloads.
- Strings: per-screen `.xcstrings`; `tools/apple/check-string-catalogs.py` requires `ar` for every
  key and, with `--stringsdata`, every used string.
- CI: `.github/workflows/_apple-apps.yml` (MaktabaKit tests, catalog checks, Player UI shards on
  throwaway simulators against `apps/apple/UITests/MockHome.swift`); TestFlight in
  `testflight.yml`.

## 3. Plex, for comparison

Plex Home has managed users with a restriction profile (Younger Kids, Older Kids, Teen) and
per-user label allow and block lists, a PIN per user to switch, and filters by content rating on
the server. Maktaba takes the server-side filter, the allow and block lists and the rating ceiling.
It leaves out the switch-user PIN grid, because Apple's Family Sharing gives each child their own
device account, and Maktaba follows with one account per child (README decision 3).

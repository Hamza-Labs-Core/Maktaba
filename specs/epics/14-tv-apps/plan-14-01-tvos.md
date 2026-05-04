# Implementation Plan — Story 14.1 tvOS app (Swift / SwiftUI)

> Companion to [story-14-01-tvos.md](story-14-01-tvos.md).
> The story states *what* and *why*; this plan states *how*.
> Layout follows [architecture.md §6.5](../../architecture.md) and the
> tree spelled out in §12.1 under `apps/tvos/`.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Xcode project | `apps/tvos/Maktaba.xcodeproj`, target `MaktabaTV` (deployment target tvOS 17.0). |
| Module layout | `Sources/App` (entry, dependency wiring), `Sources/Features/{Home,Library,Search,Settings,Player,Pairing,TopShelf}`, `Sources/API` (Apollo codegen), `Sources/UI` (token bindings + 10-foot styles from [Story 14.3](story-14-03-10-foot-ui.md)). |
| GraphQL client | Apollo iOS 1.x with `apollo-codegen-config.json` pointing at `shared/graphql/schema.graphql`. Generated sources live under `Sources/API/Generated/` and are checked in (no codegen at build time on developer machines). |
| Top Shelf | A separate `MaktabaTopShelf` extension target reads the same shared `App Group` keychain entry to call the recommendations API and Continue Watching row ([Story 14.5](story-14-05-continue-watching.md)). |
| QR pairing | Wired to the API surface owned by [Story 15.6](../15-discovery/story-15-06-pairing-api.md); the TV is the *issuer* (calls `POST /api/auth/pair`) and the phone claims. |
| Out of scope | The actual recommendations algorithm ([Story 14.7](story-14-07-recommendations-api.md)), pairing API ([Story 15.6](../15-discovery/story-15-06-pairing-api.md)), Continue Watching SQL index ([Story 14.5](story-14-05-continue-watching.md)). |

## 1. Architecture diagram

```
┌─────────────────────────────────────┐
│ Apple TV                            │
│  ┌────────┐  ┌─────────┐  ┌──────┐  │
│  │ Top    │  │ Main    │  │ Auth │  │
│  │ Shelf  │  │ App     │  │ KCV  │  │  ← shared App Group keychain
│  │ Ext.   │  │ (SwiftUI)│ │      │  │
│  └───┬────┘  └────┬────┘  └──┬───┘  │
└──────┼────────────┼──────────┼──────┘
       │            │          │
       │       AppRouter (NavigationStack per Tab)
       │            │
       │   ┌────────┴───────────────┐
       │   ▼                        ▼
       │ Home   Library   Search  Settings
       │  │       │         │        │
       │  └──┬────┴────┬────┴────────┘
       │     ▼         ▼
       │  GraphQLClient       AVPlayerHost
       │   (Apollo)            (HLS + HDR)
       │     │                   │
       └─────┴───────────────────┘
                  │
                  ▼
            ┌──────────────────┐
            │ Maktaba server   │
            │  GraphQL @ /api  │
            │  HLS @ /stream   │
            └──────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `apps/tvos/Maktaba.xcodeproj/project.pbxproj` | Two targets: `MaktabaTV` (app), `MaktabaTopShelf` (extension). |
| `apps/tvos/Sources/App/MaktabaApp.swift` | `@main` SwiftUI entry; environment objects (auth, client, theme). |
| `apps/tvos/Sources/App/AppRouter.swift` | `TabView` shell; `Tab` enum; deep-link handling for `maktaba://watch/{id}`. |
| `apps/tvos/Sources/App/AuthSession.swift` | Token store (Keychain via App Group), refresh on 401. |
| `apps/tvos/Sources/API/GraphQLClient.swift` | Apollo `ApolloClient` wired to `URLSessionTransport` with auth interceptor. |
| `apps/tvos/Sources/API/AuthInterceptor.swift` | Inserts `Authorization: Bearer …`; refresh on 401 (Story 10.4). |
| `apps/tvos/Sources/Features/Home/HomeView.swift` | Vertical list of rows: Continue Watching, Recommendations, Newly Added. |
| `apps/tvos/Sources/Features/Home/RowView.swift` | Horizontal `ScrollView` with `LazyHStack`; focus-aware. |
| `apps/tvos/Sources/Features/Library/LibraryView.swift` | Library picker + grid. |
| `apps/tvos/Sources/Features/Search/SearchView.swift` | Search field + voice intent (Story 14.4). |
| `apps/tvos/Sources/Features/Settings/SettingsView.swift` | Trimmed settings: Account, Playback, Subtitles, Sign Out. |
| `apps/tvos/Sources/Features/Player/PlayerView.swift` | `AVPlayerViewController` wrapper with custom controls + chapter ticks ([Story 17.8](../17-ux-design-system/story-17-08-player-controls.md)). |
| `apps/tvos/Sources/Features/Player/HLSAssetBuilder.swift` | Builds `AVURLAsset` with `AVURLAssetHTTPHeaderFieldsKey` for the streaming JWT. |
| `apps/tvos/Sources/Features/Player/HDRPolicy.swift` | Probes display capabilities; selects HLG/Dolby Vision; never up/down-converts. |
| `apps/tvos/Sources/Features/Pairing/PairingView.swift` | Renders the QR code + 6-digit code from `POST /api/auth/pair`. |
| `apps/tvos/Sources/Features/TopShelf/TopShelfProvider.swift` | `TVTopShelfContentProvider` impl in the extension; reads cache + calls `/api/recommendations`. |
| `apps/tvos/Sources/UI/Tokens.swift` | `enum DesignTokens` produced by [Story 17.1](../17-ux-design-system/story-17-01-design-tokens.md) build pipeline. |
| `apps/tvos/Sources/UI/FocusableCardStyle.swift` | Focus ring + 4% scale on focus per [Story 14.3](story-14-03-10-foot-ui.md). |
| `apps/tvos/Tests/MaktabaTVTests/` | Unit tests (XCTest). |
| `apps/tvos/Tests/MaktabaTVUITests/` | UI tests (XCUITest) for D-pad flows. |
| `apps/tvos/apollo-codegen-config.json` | Apollo iOS codegen config pointing at `shared/graphql/schema.graphql`. |
| `Makefile` (modified) | `make tvos` invokes `xcodebuild -project apps/tvos/Maktaba.xcodeproj`. |

### 2.2 Apollo codegen config

```json
{
  "schemaNamespace": "MaktabaAPI",
  "input": {
    "operationSearchPaths": ["apps/tvos/Sources/**/*.graphql"],
    "schemaSearchPaths": ["shared/graphql/schema.graphql"]
  },
  "output": {
    "schemaTypes": {
      "path": "apps/tvos/Sources/API/Generated/Schema",
      "moduleType": { "embeddedInTarget": { "name": "MaktabaTV" } }
    },
    "operations": {
      "inSchemaModule": {}
    }
  }
}
```

`.graphql` operation files (e.g., `HomeQuery.graphql`, `WatchQuery.graphql`) sit beside the SwiftUI view that consumes them; the codegen path globs find them.

### 2.3 Type definitions

```swift
// AppRouter.swift
enum Tab: String, CaseIterable, Hashable {
    case home, library, search, settings
}

// AuthSession.swift
@MainActor
final class AuthSession: ObservableObject {
    @Published private(set) var state: State = .unknown
    enum State { case unknown, signedOut, pendingPair(code: String, qrURL: URL), signedIn(User) }
    func bootstrap() async        // reads keychain, validates token
    func signOut() async          // POST /api/auth/logout
    func refreshIfNeeded() async  // sliding refresh; idempotent
}

// HDRPolicy.swift
enum HDRMode: String { case sdr, hlg, dolbyVision }
enum HDRPolicy {
    static func preferred(for displayCaps: AVDisplayCriteria, source: VideoTrack) -> HDRMode
    // never returns dolbyVision if displayCaps.hdrModes does not include it.
}
```

### 2.4 Modified files

| Path | Change |
|---|---|
| `Makefile` | New target `tvos`. |
| `shared/graphql/schema.graphql` | Add the queries used by Home (`home`, `continueWatching`) — already shipped under Epic 7 Story 7.4 / 7.11 / 7.17; we only consume. |
| `specs/epics/14-tv-apps/README.md` | Tick story 14.1 once landed. |

## 3. Top Shelf integration

Apple TV's Top Shelf surfaces above the home screen when the user focuses the Maktaba icon. The extension target `MaktabaTopShelf`:

```swift
import TVServices

final class TopShelfProvider: TVTopShelfContentProvider {
    func loadTopShelfContent() async -> (any TVTopShelfContent)? {
        guard let token = SharedKeychain.read(.accessToken) else { return nil }
        let cache = TopShelfCache.load()
        let items = cache.continueWatching.prefix(10).map { TVTopShelfSectionedItem(...) }
        let section = TVTopShelfItemCollection(items: items)
        section.title = "Continue Watching"
        return TVTopShelfSectionedContent(sections: [section])
    }
}
```

The cache is written by the main app on every `playback.changed` WS event (Epic 7 Story 7.16) into the shared App Group container. The extension never makes a network call on the cold path to keep the home screen responsive.

## 4. Auth, refresh, and 401 handling

```swift
final class AuthInterceptor: ApolloInterceptor {
    func interceptAsync<Operation>(...) {
        request.addHeader(name: "Authorization", value: "Bearer \(session.accessToken)")
        chain.proceedAsync(request: request, response: nil) { result in
            if case .failure(.statusCode(401)) = result {
                Task {
                    let ok = await session.refreshIfNeeded()
                    if ok { /* retry once */ } else { session.signalSignedOut() }
                }
            }
        }
    }
}
```

Refresh is **single-flight**: an `actor RefreshGate` ensures only one refresh runs at a time and concurrent 401s wait on the same continuation. This is the same pattern used by mobile (Epic 12 Story 12.x); the actor abstracts away the difference.

## 5. Player

```swift
struct PlayerView: View {
    let video: Video
    @StateObject private var model = PlayerModel()
    var body: some View {
        VideoPlayer(player: model.player)
            .ignoresSafeArea()
            .onAppear { model.load(video) }
            .onDisappear { model.persistProgress() }   // POST /api/playback/state
    }
}

final class PlayerModel: ObservableObject {
    func load(_ v: Video) {
        let asset = HLSAssetBuilder.make(streamingURL: v.hlsURL, jwt: v.streamingJWT)
        let item = AVPlayerItem(asset: asset)
        // HDR: AVAssetResourceLoader chooses the variant; we set
        // appliesPerFrameHDRDisplayMetadata to preserve HDR metadata end-to-end.
        item.appliesPerFrameHDRDisplayMetadata = HDRPolicy.preferred(...) != .sdr
        player.replaceCurrentItem(with: item)
        player.seek(to: CMTime(seconds: v.resumeAt, preferredTimescale: 1))
        player.play()
        startProgressTicker()  // 5 s POST cadence
    }
}
```

`HLSAssetBuilder` injects the streaming JWT as a custom HTTP header via `AVURLAsset` options. JWT renewal during long-form playback (> 15 min) is handled by `AVAssetResourceLoaderDelegate` — when an inner manifest fetch returns 401, the delegate calls back to `StreamingTokenRefresher` which mints a new session via the API and replays the request with the fresh token. This handles the EC "App suspended mid-playback for 30 minutes: AVPlayer resumes; if the manifest expired, we mint a new session and resume from `position_sec`."

## 6. QR pairing flow

The TV is the *issuer*:

```swift
struct PairingView: View {
    @State private var pair: PairResponse?     // POST /api/auth/pair
    var body: some View {
        VStack {
            Image(uiImage: pair?.qrImage ?? .placeholder)
            Text(pair?.humanCode ?? "")
                .font(DesignTokens.Type.display)
            Text("Open the Maktaba app on your phone and scan this code.")
        }
        .task { pair = try? await PairingAPI.create(deviceKind: .tv) }
    }
}
```

Pair refresh on a 5-min timer; once the response from polling `GET /api/auth/pair` shows `claimed_at`, the TV transitions to the signed-in state.

## 7. Test plan

### 7.1 Unit (XCTest)

| Test | What it pins |
|---|---|
| `testAuthInterceptorAttachesBearer` | Mocks `URLProtocol`; assert outgoing request has `Authorization` header. |
| `testRefreshSingleFlight` | Spawn 5 concurrent 401s; only one `POST /api/auth/refresh` is made. |
| `testHDRPolicyDeniesDVOnNonDVDisplay` | Display caps without DV + DV source → returns `.hlg` or `.sdr`, never `.dolbyVision`. |
| `testHDRPolicyPreservesHLGOnHLGDisplay` | HLG source + HLG-capable display → returns `.hlg`. |
| `testHLSAssetBuilderInjectsJWT` | Asset's `URLSession` task headers include `Authorization`. |
| `testTopShelfCacheReadsLatestPlaybackChanged` | Write a cache row; `TopShelfProvider.loadTopShelfContent()` returns it. |
| `testPlayerProgressPostsEvery5s` | Drive `CMTime` clock; verify `PlaybackAPI.report(position:)` called at 5 s intervals. |

### 7.2 UI (XCUITest)

| Test | What it pins |
|---|---|
| `testColdLaunchToHomeUnder5s` | Cold-launch on simulator; assert `home_row_continue` exists within 5 s. |
| `testDpadAcrossMixedRow` | Inject D-pad events; assert focus moves through 50 cards without skip. |
| `testBackFromPlayerReturnsToDetail` | Play → Menu button → assert detail view is on top, not Home. |
| `testEmptyContinueRowHiddenNotEmpty` | Empty playback_state → row absent (not "Nothing here"). |

### 7.3 Snapshot

- Home row, Library grid, Search empty/filled, Settings — both light and dark.

## 8. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Server unreachable on launch | Banner "Working from cache"; rows render from `URLCache` + `TopShelfCache`. | `testOfflineLaunchRendersCache` |
| Continue Watching item points to deleted video | Row entry hidden; on next refresh `mediaById` returns null → filtered. | `testDeletedVideoFiltered` |
| 30-minute suspension during playback | `AVPlayerItemFailedToPlayToEndTime` → `StreamingTokenRefresher` mints new session; `AVPlayer.seek(to: position)`. | `testSessionExpiredResumesFromPosition` |
| 4K HDR direct play | Not transcoded; `appliesPerFrameHDRDisplayMetadata = true`. | `testHDRPassthrough` |
| Apple TV (HD only) connected to 4K source | `AVAssetVariantQualifier` selects 1080p variant. | `testQualityVariantSelectsByDisplay` |
| Sign-in lost mid-session | Refresh fails → bounce to PairingView, persist deep-link target for resume. | `testSignedOutPreservesDeepLink` |
| Top Shelf called when access token expired | Extension calls `refreshIfNeeded` against shared keychain; if it fails, returns empty content (Apple home screen falls back to icon). | `testTopShelfRefreshFails` |
| Locale change while app running | `AppRouter` listens for `NSLocale.currentLocaleDidChangeNotification`; rebuilds tabs. | `testLocaleChangeRebuilds` |

## 9. Dependencies

| Dep | Version | Why |
|---|---|---|
| Apollo iOS | 1.x | GraphQL codegen + runtime. |
| `swift-collections` | 1.x | `Deque` for the playback retry queue. |
| TVServices.framework | system | Top Shelf. |
| AVFoundation | system | Player + HDR. |

## 10. Acceptance checklist

**Build**
- [ ] `make tvos` produces a signed `.app` for tvOS Simulator.
- [ ] Apollo codegen regenerates from `shared/graphql/schema.graphql` cleanly.

**App**
- [ ] Tabs: Home, Library, Search, Settings (no others).
- [ ] Cold-launch ≤ 5 s on Apple TV 4K (UI test fails if regression).
- [ ] HDR (HLG, DV) preserved end-to-end on capable displays.
- [ ] Top Shelf shows Continue Watching from the App Group cache.

**Auth**
- [ ] Pairing via QR completes within 3 s on a single LAN.
- [ ] 401 triggers single-flight refresh.

**Tests**
- [ ] All §7 tests pass on tvOS Simulator (CI: macOS runner).

**Docs**
- [ ] `specs/epics/14-tv-apps/README.md` ticks story 14.1.

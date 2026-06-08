# Maktaba tvOS

A SwiftUI app for Apple TV (tvOS 17+). A 10-foot UI over the same
Maktaba API the web and mobile clients use.

## Layout

```
tvos/
├── Package.swift                 # structural source of truth (host-compilable core)
└── MaktabaTV/
    ├── MaktabaTVApp.swift         # @main entry; owns AuthManager + APIClient
    ├── ContentView.swift          # top tab bar: Home / Libraries / Search / Settings
    ├── Views/
    │   ├── HomeView.swift          # Continue Watching + recommendation rails
    │   ├── LibraryView.swift       # grid of libraries
    │   ├── MediaGridView.swift     # poster grid + focusable PosterCard
    │   ├── PlayerView.swift        # VideoPlayer (AVKit) HLS playback
    │   ├── SearchView.swift        # .searchable + Siri-remote dictation
    │   └── SettingsView.swift      # server URL, sign-in, language
    ├── Services/
    │   ├── APIClient.swift         # async REST client, 401→refresh→retry
    │   └── AuthManager.swift       # JWT tokens in the Keychain
    ├── Models/                     # Codable: User, Library, Media, SearchResult
    ├── Resources/Assets.xcassets/  # AppIcon.brandassets (layered tvOS icon)
    └── Info.plist                  # bundle id com.hamzalabs.maktaba.tv
```

## Design notes

- **Focus engine.** D-pad navigation is handled by SwiftUI's focus
  engine. Cards are `.focusable()` and respond to `@FocusState` with a
  scale-up + ring + shadow ("lift toward the viewer"). Lazy stacks have
  padding so the grown card isn't clipped.
- **Safe area.** Screens use ~80 px horizontal gutters for TV overscan.
- **Auth.** Access/refresh JWTs live in the Keychain
  (`kSecAttrAccessibleAfterFirstUnlock`) — the only storage tvOS won't
  evict. A 401 transparently refreshes once and retries (single-flight).
- **Playback.** `VideoPlayer` wraps `AVPlayerViewController`, giving the
  native transport bar, scrubbing, and subtitle/audio pickers for free.

## Build

### Host-side type-check / core tests (no tvOS SDK)

```bash
cd apps/tv/tvos
swift build           # compiles the app sources for the host
```

This is what CI runs — it catches model/client regressions without an
Apple TV toolchain.

### Real device / simulator build

Generate an Xcode app target around these sources and build with
`xcodebuild` (wrapped by the repo's `make tv-build-ios`):

```bash
make tv-build-ios
```

You will need: Xcode 15+, an Apple Developer team for signing, and a
tvOS 17 simulator or device. Set the signing team and the API base URL
(in-app, under Settings) before first launch.

## Next steps (not scaffolded)

- **Siri intents** — an `INSpeakableString` intents extension so
  "Hey Siri, search Maktaba for…" deep-links into `SearchView`.
- **Top Shelf** extension for the tvOS home-screen recommendation strip.
- Real `AppIcon` layered image stacks (placeholders only today).

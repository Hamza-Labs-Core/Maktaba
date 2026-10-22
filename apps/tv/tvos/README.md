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
    ├── Theme/Theme.swift           # palette, fonts, library backdrop, reading panel, focus ring
    ├── Resources/Assets.xcassets/  # AppIcon.brandassets (layered tvOS icon)
    ├── Resources/Fonts/            # Amiri-Bold + IBM Plex Sans Arabic (OFL, licences alongside)
    ├── Resources/Art/              # library-dark.jpg / library-light.jpg (Home background)
    └── Info.plist                  # bundle id com.hamzalabs.maktaba.tv
```

## Design notes

- **Theme.** `Theme/Theme.swift` is the only place colours, fonts and
  art come from. It follows the system appearance: dark "Ink & Brass" or
  light "gold on white" (gold as *text* is the deeper `#8A6212` there).
  Fonts are registered from the SwiftPM resource bundle at launch. Home
  shows the library scene behind everything; text goes on
  `.readingPanel()` (material + tint), never straight on the art.
- **Focus engine.** D-pad navigation is handled by SwiftUI's focus
  engine. Cards are `.focusable()` and respond to `@FocusState` with
  `.goldFocus(_:)`: a 3pt gold ring, scale-up and gold glow. Lazy stacks
  have padding so the grown card isn't clipped.
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

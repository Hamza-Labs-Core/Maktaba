# `apps/`: Maktaba's apps

The Player, Remote and TV apps are native and talk to the home's API
([Epic 42](../specs/epics/42-apps-player-remote-server/README.md),
[Epic 48](../specs/epics/48-native-players/README.md)). The desktop apps that carry the server
wrap the shared `web/` bundle.

| Subdir | Stack | Epic |
|---|---|---|
| [`apps/apple/`](apple/) | SwiftUI: Player (iPhone, iPad, Mac) and Remote (iPhone, iPad), on the shared `MaktabaKit`; also builds the Apple TV app | Epics 42, 48 |
| [`apps/android/`](android/) | Jetpack Compose: Player and Remote (phone, foldable, tablet), on a portable `client` | Epics 42, 48 |
| [`apps/desktop-player/`](desktop-player/) | Compose for Desktop: Player for Windows and Linux, on the Android apps' `client` | Epic 42 |
| [`apps/tv/`](tv/) | Maktaba TV: SwiftUI + AVKit (Apple TV), Compose for TV + Media3 (Android TV and Google TV), the Samsung and LG launcher (`tv/web/`), Roku (`tv/roku/`) | Epics 14, 48, 58 |
| [`apps/desktop/`](desktop/) | Tauri 2 (macOS, Windows, Linux): wraps `web/dist` and carries the server | Epics 13, 36–39 |
| [`apps/mobile/`](mobile/) | Capacitor 6 (iOS + Android): the older web-app wrapper | Epic 12 |

## Get the apps

The apps are in testing. Testers get them from TestFlight and Google Play's internal testing,
not from the public stores yet. The owner adds each tester: their Apple ID to a TestFlight
testers group in App Store Connect ([docs/testflight.md](../docs/testflight.md)), their Google
account to the testers list in Play Console ([docs/google-play.md](../docs/google-play.md)).
Every release tag uploads a new build to both (a scheduled release session tags the newest
commit of main that CI and the update rehearsal pass on as the next patch version,
[docs/updates.md](../docs/updates.md#how-a-release-is-made)), which reaches testers as an
update, and attaches the installable files to the
[GitHub Release](https://github.com/Hamza-Labs-Core/Maktaba/releases) for that tag.

| App | Platform | How to get it | Built by |
|---|---|---|---|
| **Maktaba Player** (watch) | iPhone · iPad (*Maktaba Player for iPad*) · Mac, macOS 15+ (*Maktaba Player for Mac*) | TestFlight | [`testflight.yml`](../.github/workflows/testflight.yml) |
| **Maktaba Remote** (run the home; admins only) | iPhone · iPad (*Maktaba Remote iPad*) | TestFlight | [`testflight.yml`](../.github/workflows/testflight.yml) |
| **Maktaba TV** | Apple TV (tvOS 27) | TestFlight on the Apple TV | [`testflight.yml`](../.github/workflows/testflight.yml) |
| **Maktaba Player** and **Maktaba Remote** | Android phones, foldables and tablets | Google Play internal testing (`maktaba-player-<version>.aab`, `maktaba-remote-<version>.aab`, signed with the Play upload key) | [`play-bundles.yml`](../.github/workflows/play-bundles.yml) |
| **Maktaba TV** | Android TV and Google TV | Google Play internal testing (`maktaba-tv-<version>.aab`); or sideload `maktaba-tv-<version>.apk` when a release carries it (only signed builds are attached) | [`play-bundles.yml`](../.github/workflows/play-bundles.yml), [`tv-release.yml`](../.github/workflows/tv-release.yml) |
| **Maktaba TV** (the launcher for the home's `/tv/`) | Samsung (Tizen) and LG (webOS) TVs | `maktaba-tizen-<version>.wgt` and `maktaba-webos-<version>.ipk`, attached to a tag's release when the launcher changed, and uploaded to each store by hand ([docs/stores/](../docs/stores/)) | [`tv-web-release.yml`](../.github/workflows/tv-web-release.yml) |
| **Maktaba Player** | Windows (x64), Linux (x86_64, aarch64) | `Maktaba-Player_<v>_x64.msi`, `maktaba-player_<v>_<amd64\|arm64>.deb`, `maktaba-player-<v>.<x86_64\|aarch64>.rpm` (each with a `.sig`) from the [latest release](https://github.com/Hamza-Labs-Core/Maktaba/releases/latest); it updates itself from `latest-player.json` (Help → Check for updates) | [`desktop-release.yml`](../.github/workflows/desktop-release.yml) |
| **Maktaba** (the server and the web app) | Mac, Apple silicon only (no Intel build since v0.3.15), macOS 15+ | `Maktaba_<v>_aarch64.dmg`, the whole server inside; or `Maktaba-Core_<v>_aarch64.dmg`, the same without the local language model (ollama) | [`desktop-release.yml`](../.github/workflows/desktop-release.yml) |
| **Maktaba** (the server and the web app) | Windows (x64) | NSIS `Maktaba_<v>_x64-setup.exe` with the server inside (current user, `%LOCALAPPDATA%\Maktaba`), plus a portable `.zip` of the client | [`desktop-release.yml`](../.github/workflows/desktop-release.yml) |
| **Maktaba** (the server and the web app) | Linux with a desktop (x86_64, aarch64) | `.AppImage`, `.deb`, `.rpm`, the whole server inside (ffmpeg is the distribution's) | [`desktop-release.yml`](../.github/workflows/desktop-release.yml) |
| **Maktaba** (the server) | Linux without a desktop (x86_64, aarch64) | `maktaba-linux-payload-<version>-<x86_64\|aarch64>.tar.gz`, through `install.sh --app` ([how](../deploy/packaging/README.md#linux-the-app-or-these-packages)) | [`release.yml`](../.github/workflows/release.yml) |
| **Web app** | any browser | the home's address, port `8088`; or through Maktaba Cloud. The bundle alone, to self-host: `maktaba-web-<version>.tar.gz` | [`release.yml`](../.github/workflows/release.yml) (each tag) · [`web-release.yml`](../.github/workflows/web-release.yml) (rebuild on demand) |

The server apps update themselves ([docs/auto-update.md](../docs/auto-update.md),
[docs/updates.md](../docs/updates.md)). The server binaries and container images come from
[`release.yml`](../.github/workflows/release.yml) too.

**Signing in a TV or a device by code.** A TV (or a phone or computer that chooses *With a
code*) shows a code like `ABCD-1234` and a QR code. Approve it from an app that is already
signed in: in **Maktaba Remote**, tap **Approve a TV or device** at the top of any screen; in
**Maktaba Player**, open **Profile** and tap **Approve a TV or device** (on the Mac, the
**Account** menu); or open the web app's **Sign in a device** page (`/pair`). While a TV waits,
the Remote (and the Android Player) shows a banner with **Enter code**, and the Android apps a
notification when notifications are allowed. Older apps call it **Approve a sign-in**, under the
person icon. The device is signed in as you, with your role.

**The older web-app wrapper** (`apps/mobile`, Capacitor: `maktaba-<version>.apk` and `.aab`,
and an unsigned `maktaba-<version>.ipa`, from
[`mobile-release.yml`](../.github/workflows/mobile-release.yml)) is not one of these apps.
Installed by hand, it shows a *New version available* banner with *Get update* when a newer
release exists ([docs/auto-update.md](../docs/auto-update.md)).

The user guide for every app is in the web app at `/help` (Help → Apps), in English and Arabic.

**Signing status.** The TestFlight builds are signed by Xcode's cloud signing, and the Play
bundles with the upload key. The desktop installers are signed and notarized only when the
Apple Developer ID and Windows Authenticode secrets are set; until then they are **unsigned**.
The desktop apps' **updates are signed** with the Tauri updater key: the release workflow signs
them with the repository's secret and stamps the public key (the `TAURI_UPDATER_PUBKEY`
variable) into `tauri.conf.json` at build time, so the apps take only updates signed with it
([auto-update](../docs/auto-update.md)). Android signs automatically when the
`ANDROID_KEYSTORE_BASE64` / `ANDROID_KEY_ALIAS` / `ANDROID_KEY_PASSWORD` secrets are set.
Without them the APKs are built unsigned, kept as workflow artifacts and not attached to the
Release.

## Build the apps from source

| Target | Stack | Build |
|---|---|---|
| Player (iPhone, iPad, Mac) and Remote (iPhone, iPad) | **SwiftUI** + the shared `MaktabaKit` ([`apple/`](apple/)) | `xcodebuild` (see its README) |
| Android: Player and Remote | **Jetpack Compose** on the shared `client` ([`android/`](android/)) | `./gradlew` (see its README) |
| Windows, Linux: Player | **Compose for Desktop** ([`desktop-player/`](desktop-player/)) | `./gradlew packageMsi` / `packageDeb packageRpm` |
| Apple TV | **SwiftUI + AVKit** ([`tv/tvos/`](tv/tvos/), scheme `Maktaba TV`): native 10-foot UI | `make tv-build-ios` |
| Android TV | **Compose-for-TV + Media3** ([`tv/android/`](tv/android/)): native 10-foot UI | `make tv-build-android` |
| macOS / Windows / Linux: the server app | **Tauri 2** ([`desktop/`](desktop/)): wraps `web/dist`, carries the server | `make desktop-build` |
| iOS + Android (older web-app wrapper) | **Capacitor 6** ([`mobile/`](mobile/)): wraps `web/dist` | `make mobile-build` |

```sh
make mobile-build      # web build + cap sync (needs Xcode/CocoaPods, Android SDK+JDK)
make desktop-build     # Tauri build for the host OS (needs the Rust toolchain)
make native-sync       # rebuild web/dist and cap sync it into the native platforms
make tv-build-ios      # tvOS core (swift build); device builds via xcodebuild
make tv-build-android  # Android TV debug APK (needs the Android SDK)
```

To build any client package locally, mirror CI with the `make package-*` targets
(`package-desktop`, `package-mobile-android`, `package-mobile-ios`, `package-tv-android`, or
`package-all`).

The wrappers expect `web/dist/` to exist; their build scripts call
`pnpm --filter maktaba-web build` to produce it before bundling.

The native apps use `com.hamzalabs.maktaba.player` (`.player.tablet` on iPad, `.player.mac` on
the Mac), `com.hamzalabs.maktaba.remote` (`.remote.tablet` on iPad) and
`com.hamzalabs.maktaba.tv`; the Capacitor and Tauri wrappers share `com.hamzalabs.maktaba`. All
take `maktaba://connect` links. Generated native projects (`apps/mobile/{ios,android}`,
`apps/desktop/src-tauri/{target,gen}`, the TV build outputs) and signing material are not
checked in: see each shell's README for the toolchain prerequisites and signing setup.

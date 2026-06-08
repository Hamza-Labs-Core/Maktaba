# Maktaba Android TV

A Kotlin + Jetpack Compose app for Android TV / Google TV (minSdk 28,
targetSdk 34). A 10-foot UI over the same Maktaba REST API the web and
mobile clients use.

## Layout

```
android/
├── settings.gradle.kts
├── build.gradle.kts                 # root: declares plugins (catalog versions)
├── gradle/libs.versions.toml        # version catalog (single source of versions)
├── gradle.properties
├── gradlew / gradlew.bat            # Gradle 8.7 wrapper
└── app/
    ├── build.gradle.kts             # Compose-TV, Media3, Retrofit, Coil, security-crypto
    ├── proguard-rules.pro
    └── src/
        ├── main/
        │   ├── AndroidManifest.xml  # LEANBACK_LAUNCHER + leanback feature
        │   ├── res/                 # banner, theme, strings, colors
        │   └── java/com/hamzalabs/maktaba/tv/
        │       ├── MaktabaTVApp.kt          # Application + AppContainer
        │       ├── MainActivity.kt          # single activity, sets Compose content
        │       ├── AppContainer.kt          # hand-wired DI graph
        │       ├── data/
        │       │   ├── SettingsStore.kt     # server URL + language (prefs)
        │       │   ├── api/
        │       │   │   ├── MaktabaApi.kt     # Retrofit interface
        │       │   │   ├── AuthInterceptor.kt# bearer inject + 401 refresh
        │       │   │   ├── TokenStore.kt     # JWTs in EncryptedSharedPreferences
        │       │   │   └── ApiProvider.kt    # builds OkHttp + Retrofit
        │       │   ├── models/               # Media, Library, User, SearchResult
        │       │   └── repository/MediaRepository.kt
        │       └── ui/
        │           ├── theme/                # dark TV theme + colors
        │           ├── components/           # MediaCard (focusable), TopBar
        │           ├── screens/              # Home, Library, MediaGrid, Player, Search, Settings
        │           └── navigation/NavGraph.kt
        └── test/java/.../RailItemTest.kt     # JVM unit test
```

## Design notes

- **Compose for TV.** Uses `androidx.tv:tv-material` (Material tuned for
  focus) and `androidx.tv:tv-foundation` (`TvLazyRow` /
  `TvLazyVerticalGrid`). The phone `androidx.compose.material3` is
  deliberately avoided — its components have no D-pad focus visuals.
- **Focus.** Cards are `tv-material` `Card`s, which apply the focused
  scale/border/glow automatically; `MediaCard` tracks focus only to
  brighten the title label.
- **Leanback launcher.** `CATEGORY_LEANBACK_LAUNCHER` +
  `uses-feature android.software.leanback` put the app on the TV home
  row; `touchscreen required="false"` keeps it in the TV catalog.
- **Auth.** JWTs live in `EncryptedSharedPreferences` (Keystore-backed).
  An OkHttp `Authenticator` does single-flight 401 → refresh → retry.
- **Playback.** Media3/ExoPlayer with the HLS extension; `PlayerView`
  via `AndroidView`, released in a `DisposableEffect` to avoid leaks.

## Build

```bash
cd apps/tv/android
./gradlew :app:assembleDebug      # build the APK
./gradlew test                    # JVM unit tests
```

Or open `apps/tv/android` in Android Studio (Hedgehog+). You need the
Android SDK (API 34) and an Android TV emulator or device. The repo's
`make tv-build-android` wraps the assemble task.

Set the server URL on first launch (Settings → Server URL); it defaults
to `https://demo.maktaba.app`.

## Next steps (not scaffolded)

- **Assistant voice search** — a `SearchManager` searchable config +
  content provider so "Hey Google, search Maktaba for…" deep-links in.
- A real branded `app_banner` (the committed one is a placeholder).
- A Home-screen **recommendation channel** (`androidx.tvprovider`).
- Hilt for DI in place of the hand-wired `AppContainer`.

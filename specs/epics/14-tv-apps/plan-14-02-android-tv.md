# Implementation Plan — Story 14.2 Android TV app (Kotlin / Compose for TV)

> Companion to [story-14-02-android-tv.md](story-14-02-android-tv.md).
> The story states *what* and *why*; this plan states *how*.
> Layout follows [architecture.md §6.5](../../architecture.md) and the
> tree spelled out in §12.1 under `apps/androidtv/`.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Gradle root | `apps/androidtv/build.gradle.kts` (Kotlin DSL); minSdk = 28 (Android TV 9, Pie). Compile/target SDK 34. |
| Module layout | Single `:app` module split internally by package: `io.maktaba.tv.{app,api,ui,features.{home,library,search,settings,player,pairing,channel}}`. |
| GraphQL client | Apollo Kotlin 4.x with `apollo` gradle plugin pointing at `shared/graphql/schema.graphql`. Codegen runs on Gradle build. |
| Player | ExoPlayer (media3) for HLS/DASH; HDR10/Dolby Vision passthrough via `MediaCodec` HDR config. |
| Recommendations channel | `androidx.tvprovider:tvprovider` — a `PreviewChannel` plus one `WatchNextProgram` per Continue Watching entry (mirrors Top Shelf on tvOS). The `WatchNextProgram` *class* is singular; `WatchNextPrograms` is only a content-URI helper. |
| QR pairing | Calls [Story 15.6](../15-discovery/story-15-06-pairing-api.md); same flow as tvOS. |
| Out of scope | Recommendations algorithm ([Story 14.7](story-14-07-recommendations-api.md)). |

## 1. Architecture diagram

```
┌────────────────────────────────────────┐
│ Android TV launcher                    │
│  ┌─────────────────────┐               │
│  │ Maktaba Channel     │ ←──── ChannelSyncWorker (WorkManager, 6 h)
│  │ (Continue Watching) │       writes WatchNextPrograms via TvProvider
│  └─────────────────────┘               │
└────────────────────────────────────────┘

┌────────────────────────────────────────┐
│ MaktabaActivity                        │
│  ┌────────────┐  ┌─────────────┐       │
│  │ Compose    │  │ ExoPlayer   │       │
│  │ for TV     │  │ Service     │       │
│  │ NavHost    │  │ (foreground)│       │
│  └─────┬──────┘  └──────┬──────┘       │
│        │                │              │
│        ▼                ▼              │
│   ApolloClient      MediaSourceFactory │
│   (auth interceptor)  (HLS+DRM-less)   │
└────────────────────────────────────────┘
                │
                ▼
       Maktaba server (GraphQL, HLS)
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `apps/androidtv/build.gradle.kts` | Application plugin, Apollo plugin, Compose, Hilt, media3. |
| `apps/androidtv/settings.gradle.kts` | `:app` only; pulls `shared/graphql` schema via `extraSourceDirs`. |
| `apps/androidtv/src/main/AndroidManifest.xml` | `LEANBACK_LAUNCHER` intent, `CATEGORY_LEANBACK_LAUNCHER`; foreground media service. |
| `apps/androidtv/src/main/java/io/maktaba/tv/MaktabaApp.kt` | `Application` w/ Hilt setup. |
| `apps/androidtv/src/main/java/io/maktaba/tv/MaktabaActivity.kt` | Single-activity host; `setContent { App() }`. |
| `apps/androidtv/src/main/java/io/maktaba/tv/app/AppNav.kt` | TV `NavHost` with destinations for each tab. |
| `apps/androidtv/src/main/java/io/maktaba/tv/api/GraphQL.kt` | Hilt-provided `ApolloClient` w/ `AuthInterceptor`. |
| `apps/androidtv/src/main/java/io/maktaba/tv/api/AuthInterceptor.kt` | Adds bearer; refresh on 401 (single-flight via `Mutex`). |
| `apps/androidtv/src/main/java/io/maktaba/tv/auth/AuthSession.kt` | Encrypted SharedPreferences (`EncryptedSharedPreferences`) for tokens. |
| `apps/androidtv/src/main/java/io/maktaba/tv/features/home/HomeScreen.kt` | Compose for TV `LazyColumn` of rows. |
| `apps/androidtv/src/main/java/io/maktaba/tv/features/home/RowComposable.kt` | `LazyRow` with `Modifier.focusRestorer()`. |
| `apps/androidtv/src/main/java/io/maktaba/tv/features/library/LibraryScreen.kt` | Library picker + grid. |
| `apps/androidtv/src/main/java/io/maktaba/tv/features/search/SearchScreen.kt` | Compose `TextField` + `RecognizerIntent` voice (Story 14.4). |
| `apps/androidtv/src/main/java/io/maktaba/tv/features/settings/SettingsScreen.kt` | Trimmed: Account, Playback, Subtitles, Sign Out. |
| `apps/androidtv/src/main/java/io/maktaba/tv/features/player/PlayerScreen.kt` | `AndroidView` wrapping `PlayerView`; surface for ExoPlayer. |
| `apps/androidtv/src/main/java/io/maktaba/tv/features/player/PlayerService.kt` | `MediaSessionService` so playback survives backgrounding. |
| `apps/androidtv/src/main/java/io/maktaba/tv/features/pairing/PairingScreen.kt` | Renders QR + 6-digit code; polls `GET /api/auth/pair`. |
| `apps/androidtv/src/main/java/io/maktaba/tv/features/channel/ChannelSyncWorker.kt` | WorkManager `CoroutineWorker` that updates the recommendations channel. |
| `apps/androidtv/src/main/java/io/maktaba/tv/ui/Tokens.kt` | Compose theme bound to [Story 17.1](../17-ux-design-system/story-17-01-design-tokens.md) outputs. |
| `apps/androidtv/src/main/graphql/io/maktaba/tv/*.graphql` | Operations co-located by feature. |
| `apps/androidtv/src/test/...` | JVM unit tests (Robolectric where Android-specific). |
| `apps/androidtv/src/androidTest/...` | Espresso/Compose UI tests; D-pad `KeyEvent.KEYCODE_DPAD_*`. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `Makefile` | `make androidtv` → `./gradlew :app:assembleRelease`. |
| `specs/epics/14-tv-apps/README.md` | Tick story 14.2 once landed. |

### 2.3 Build script highlights

```kotlin
// apps/androidtv/build.gradle.kts
plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    // Apollo Kotlin 4.x renamed the plugin id from `apollo3` to `apollo`;
    // see https://www.apollographql.com/docs/kotlin/migration/v4
    id("com.apollographql.apollo") version "4.0.0"
    id("dagger.hilt.android.plugin")
    id("com.google.devtools.ksp")
}

android {
    namespace = "io.maktaba.tv"
    compileSdk = 34
    defaultConfig {
        applicationId = "io.maktaba.tv"
        minSdk = 28; targetSdk = 34
        versionCode = providers.gradleProperty("buildNumber").get().toInt()
        versionName = providers.gradleProperty("versionName").get()
    }
    buildFeatures { compose = true }
}

apollo {
    service("maktaba") {
        srcDir("../../shared/graphql")    // shared schema
        packageName.set("io.maktaba.tv.api.gql")
    }
}

dependencies {
    implementation("androidx.tv:tv-foundation:1.0.0")
    implementation("androidx.tv:tv-material:1.0.0")
    implementation("androidx.media3:media3-exoplayer:1.4.0")
    implementation("androidx.media3:media3-exoplayer-hls:1.4.0")
    implementation("androidx.media3:media3-ui-leanback:1.4.0")
    implementation("androidx.media3:media3-session:1.4.0")
    implementation("androidx.tvprovider:tvprovider:1.0.0")
    implementation("androidx.security:security-crypto:1.1.0-alpha06")
    implementation("com.apollographql.apollo:apollo-runtime:4.0.0")
    implementation("com.google.dagger:hilt-android:2.51")
    ksp("com.google.dagger:hilt-android-compiler:2.51")
}
```

## 3. Auth & refresh

```kotlin
class AuthInterceptor @Inject constructor(
    private val session: AuthSession,
    private val refresh: RefreshGate,
) : ApolloInterceptor {
    override fun intercept(request: ApolloRequest<...>, chain: ApolloInterceptorChain) = flow {
        emitAll(chain.proceed(request.newBuilder().addHttpHeader("Authorization",
            "Bearer ${session.accessToken()}").build()))
    }.catch { e ->
        if (e is ApolloHttpException && e.statusCode == 401) {
            if (refresh.refreshIfNeeded()) emitAll(chain.proceed(request)) else throw e
        } else throw e
    }
}

@Singleton
class RefreshGate @Inject constructor(private val session: AuthSession) {
    private val mutex = Mutex()
    suspend fun refreshIfNeeded(): Boolean = mutex.withLock {
        // single-flight refresh; second concurrent caller sees fresh token from session.
        session.refresh()
    }
}
```

## 4. ExoPlayer + HDR

```kotlin
class PlayerScreenViewModel(...) : ViewModel() {
    val exoPlayer = ExoPlayer.Builder(ctx)
        .setRenderersFactory(DefaultRenderersFactory(ctx)
            .setEnableDecoderFallback(true))
        .build().apply {
            // HDR: media3 picks the best capable decoder; we set the
            // RendererCapabilities filter so SDR is chosen if the panel
            // is not HDR-capable. EC: misconfigured TV → fall back to SDR.
        }

    fun play(video: Video) {
        val source = HlsMediaSource.Factory(authedFactory(video.streamingJWT))
            .createMediaSource(MediaItem.fromUri(video.hlsUri))
        exoPlayer.setMediaSource(source); exoPlayer.prepare()
        exoPlayer.seekTo(video.resumeAt * 1000L)
        exoPlayer.play()
        // Default media3 retry handles transient drops; we add a 5 s grace
        // before the UI surfaces "Buffering…" beyond ExoPlayer's own toast.
    }
}
```

`authedFactory` returns a `DataSource.Factory` that injects the streaming JWT in headers and refreshes via `StreamingTokenRefresher` on 401 (mirrors tvOS).

## 5. Recommendations channel (WorkManager)

`ChannelSyncWorker` runs:

1. Every 6 hours via `PeriodicWorkRequest`.
2. Immediately on app foreground if `lastSync` > 1 hour.
3. After every `playback.changed` event (one-shot).

It calls `GET /api/recommendations?surface=tv-home` and `GET /api/me/playback-state?in_progress=true` (canonical path per architecture §9.4 / Epic 11 plan-11-02), then writes:

- A `PreviewChannel` with the brand artwork.
- A `WatchNextProgram` per Continue Watching entry, with `WATCH_NEXT_TYPE_CONTINUE` and the resume position.

Manufacturer-skin caveats are addressed by trying the `TvContractCompat` API; if it returns `null` (no provider), we silently skip and log a one-time analytics event so we can detect skins where it doesn't work and document them per the EC.

## 6. Test plan

### 6.1 Unit

| Test | What it pins |
|---|---|
| `AuthInterceptorTest` | 401 triggers refresh and a single retry; second 401 in a row signs out. |
| `RefreshGateConcurrencyTest` | 10 concurrent callers → 1 refresh API call. |
| `HDRPolicyTest` | HDR-capable display + HDR source → media3 selected variant has HDR; non-HDR display → SDR. |
| `ChannelSyncWorkerTest` | With 5 in-progress videos, writes 5 `WatchNextPrograms`; idempotent on re-run. |
| `PairingApiTest` | TV polls; on `claimed_at` flip, transitions to signed-in. |

### 6.2 UI (Compose / Espresso)

| Test | What it pins |
|---|---|
| `coldLaunchUnder6s` | Cold-launch on Chromecast emulator; `home_continue` is visible within 6 s. |
| `dpadAcrossLargeRow` | 50 cards; `KeyEvent.KEYCODE_DPAD_RIGHT` × 49 → focus reaches the last item. |
| `voiceSearchDispatch` | Inject `RecognizerIntent` result → search query state updates and request fires. |
| `backFromPlayer` | Play → BACK → focus returns to detail page, not Home. |

## 7. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Manufacturer skin (Sony, Sharp) without preview channel API | `TvContractCompat.requestChannelBrowsable` returns null; logged once; in-app rows still work. | `testChannelGracefulDegradation` |
| HDR auto-engagement fails on misconfigured TV | media3 `Renderer` falls back to SDR; one-time toast shown; flag persisted to skip retries for 24 h. | `testHDRFallbackToast` |
| Network drop mid-playback | media3 default retry; UI surfaces nothing for the first 5 s grace; then a recoverable error with retry. | `testNetworkDropGrace` |
| Voice query returns nothing | Empty results screen with "did you mean" suggestions from `/api/search/suggest` ([Story 14.4](story-14-04-voice-search.md)). | `testVoiceEmptyShowsSuggestions` |
| Locale change while running | `Configuration.locales` changed → `MaktabaActivity.recreate()` keeps state via `rememberSaveable`. | `testLocaleChangePreservesState` |
| App killed mid-playback | `MediaSessionService` keeps playing; foreground notification persists the state. | `testServiceSurvivesKill` |
| 4K HDR direct play on Chromecast 4K | `MediaCodec` HDR profile selected; passthrough; no transcode. | `testHDRPassthrough` |

## 8. Dependencies

| Dep | Version | Why |
|---|---|---|
| Apollo Kotlin | 4.0 | GraphQL codegen + runtime. |
| `androidx.tv:tv-foundation` | 1.0.0 | TV-specific Compose primitives. |
| `androidx.media3:*` | 1.4.0 | ExoPlayer + HLS + leanback UI. |
| `androidx.tvprovider` | 1.0.0 | Recommendations channel API. |
| `androidx.security:security-crypto` | 1.1.0-alpha06 | EncryptedSharedPreferences. |
| Hilt | 2.51 | DI. |

## 9. Acceptance checklist

**Build**
- [ ] `./gradlew :app:assembleRelease` produces a signed APK ≤ 25 MB.
- [ ] Apollo codegen produces classes from shared schema.

**App**
- [ ] D-pad navigation across all flows; no swipe-only paths.
- [ ] HDR10/DV passthrough on capable panels; SDR fallback otherwise.
- [ ] Recommendations channel populated with Continue Watching.

**Tests**
- [ ] All §6 tests pass.

**Docs**
- [ ] `specs/epics/14-tv-apps/README.md` ticks story 14.2.

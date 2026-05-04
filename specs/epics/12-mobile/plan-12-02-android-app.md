# Implementation Plan — Story 12.2 Android App Wrapper

> Companion to [story-12-02-android-app.md](story-12-02-android-app.md).
> Capacitor 6 wrapper; same web bundle as iOS (Story 12.1).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Framework | Capacitor 6 (`@capacitor/android`). |
| Placement | `apps/mobile/android/app/`. |
| Targets | minSdk 28 (Android 9); compileSdk/targetSdk 34. ARM64 + ARMv7. |
| Signing | Play App Signing; upload key via Fastlane keystore. |
| Distribution | Play Store internal testing. |
| Out of scope | Push (Story 12.4 + 12.10), foreground download service body (Story 12.6), native player (12.3). |

## 1. Layout

```
apps/mobile/android/
├── app/
│   ├── build.gradle.kts          # Kotlin DSL
│   ├── src/main/
│   │   ├── AndroidManifest.xml
│   │   ├── java/com/maktaba/app/
│   │   │   ├── MainActivity.kt
│   │   │   └── DownloadForegroundService.kt   # declared here, body in Story 12.6
│   │   ├── res/                  # icons, splash, themes (light/dark)
│   │   └── assets/public/        # Capacitor mounts here
└── build.gradle.kts
```

## 2. AndroidManifest.xml essentials

```xml
<manifest xmlns:android="http://schemas.android.com/apk/res/android">
  <uses-permission android:name="android.permission.INTERNET"/>
  <uses-permission android:name="android.permission.ACCESS_NETWORK_STATE"/>
  <uses-permission android:name="android.permission.FOREGROUND_SERVICE"/>
  <uses-permission android:name="android.permission.FOREGROUND_SERVICE_DATA_SYNC"/>
  <uses-permission android:name="android.permission.FOREGROUND_SERVICE_MEDIA_PLAYBACK"/>
  <uses-permission android:name="android.permission.WAKE_LOCK"/>
  <application
      android:label="@string/app_name"
      android:theme="@style/AppTheme.NoActionBar"
      android:icon="@mipmap/ic_launcher"
      android:roundIcon="@mipmap/ic_launcher_round"
      android:requestLegacyExternalStorage="false"
      android:enableOnBackInvokedCallback="true"
      android:usesCleartextTraffic="false">
    <activity
        android:name=".MainActivity"
        android:configChanges="orientation|keyboardHidden|keyboard|screenSize|smallestScreenSize|uiMode"
        android:launchMode="singleTask"
        android:exported="true"
        android:windowSoftInputMode="adjustResize">
      <intent-filter>
        <action android:name="android.intent.action.MAIN"/>
        <category android:name="android.intent.category.LAUNCHER"/>
      </intent-filter>
    </activity>
    <service
        android:name=".DownloadForegroundService"
        android:foregroundServiceType="dataSync"
        android:exported="false"/>
    <service
        android:name=".MediaPlaybackService"
        android:foregroundServiceType="mediaPlayback"
        android:exported="false"/>
  </application>
</manifest>
```

## 3. MainActivity

```kotlin
class MainActivity : BridgeActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        WindowCompat.setDecorFitsSystemWindows(window, false)
        // Edge-to-edge; CSS env() handles inset padding.
        onBackPressedDispatcher.addCallback(this) {
            if (!bridge.webView.canGoBack()) {
                AlertDialog.Builder(this@MainActivity)
                    .setMessage(R.string.quit_confirm)
                    .setPositiveButton(R.string.quit) { _, _ -> finish() }
                    .setNegativeButton(R.string.cancel, null)
                    .show()
            } else {
                bridge.webView.goBack()
            }
        }
    }
}
```

## 4. Build pipeline

`apps/mobile/scripts/build-android.ts`:

1. Build web → `web/dist`.
2. `sync-web.ts` copies into `apps/mobile/android/app/src/main/assets/public`.
3. `npx cap sync android`.
4. `./gradlew bundleRelease` (AAB).

CI uses Linux runner; `fastlane android beta` uploads to Play Store internal track.

## 5. Lifecycle bridge

Same plugin as Story 12.1; `LifecyclePlugin` Android implementation listens to `Application.ActivityLifecycleCallbacks` and emits `background`, `foreground`, `memoryPressure` (`onTrimMemory`).

## 6. Edge cases

| Case | Handling |
|---|---|
| WebView updates mid-session (System WebView background update) | App survives implicit reload via Capacitor's session restore. Visible state is rehydrated by re-running route loaders. |
| Device without Play Services | FCM silently no-ops; in-app polling fallback (Story 12.4). Downloads still work via WorkManager. |
| Sideload, no Play Store | Settings → About surfaces version skew; on `update_available`, prompts manual install via `/api/system/version`. |
| Doze mode | Foreground service for downloads exempts the process; background sockets gracefully back off. |
| Low-end device (Moto G play) | Cold launch ≤ 6 s budget; bundle code-split. |

## 7. Test cases

### 7.1 Espresso

| Test | Asserts |
|---|---|
| `cold launch reaches library on Pixel 5` | Library list visible ≤ 4 s. |
| `back from Settings returns to previous tab` | Tab restored. |
| `back at root prompts quit` | AlertDialog appears with title `quit_confirm`. |
| `rotate on watch route does not pause video` | Player state preserved. |

### 7.2 CI gating

- `gradle lint` passes on PRs.
- `assembleRelease` succeeds with R8 obfuscation enabled.
- `aab-size-budget` script asserts release bundle ≤ 25 MB.

## 8. Performance

- Cold launch ≤ 4 s on Pixel 5 (ColdStartSignpost via Macrobenchmark).
- ANR rate < 0.1% (Play Console gating).
- Bundle ≤ 25 MB.

## 9. Dependencies

- Same web bundle as Story 12.1.
- Foreground service body added by Story 12.6.
- FCM token registration by Story 12.4 + Story 12.10.

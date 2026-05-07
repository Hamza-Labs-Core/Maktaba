# Implementation Plan — Story 12.1 iOS App Wrapper

> Companion to [story-12-01-ios-app.md](story-12-01-ios-app.md).
> Capacitor 6 wrapper for the same web bundle as Epic 11.
> Native plugins live under `apps/mobile/plugins/` with their own READMEs.

## 0. First-launch server URL onboarding

Maktaba is self-hosted, so the app must learn its server URL on first
launch. Server URL onboarding is **deferred to
`plan-10-17-auth-pair.md`** (the auth-pair flow). The server URL is
captured during pair-code claim and stored in:

- iOS: Keychain (kSecClassGenericPassword, account = `server_url`,
  service = `com.maktaba.app`).
- Android: EncryptedSharedPreferences (alias = `maktaba_server`).

Until pair-code claim completes, no Capacitor `allowNavigation` host is
known, so the WebView shows the onboarding screen served from the
bundled web assets only. Once a server URL is stored, subsequent launches
read it before constructing `CapacitorConfig`.

## 0a. Scope and placement

| Concern | Decision |
|---|---|
| Framework | Capacitor 6.x (`@capacitor/core`, `@capacitor/ios`). |
| Placement | `apps/mobile/` (TypeScript root with shared scripts), `apps/mobile/ios/App/` (Xcode project). |
| Web bundle | Built via `apps/mobile/scripts/sync-web.ts` which copies the Vite `web/dist/` into `apps/mobile/dist` before `npx cap sync`. |
| Targets | iOS 16+; Universal (iPhone + iPad). |
| Signing | Provisioning profile committed under `apps/mobile/ios/build/` (gitignored secrets); Xcode automatic signing for dev. |
| Distribution | TestFlight via Fastlane lane `ios:beta`. |
| Out of scope | Push (Story 12.4), native player (12.3), downloads (12.6), background playback (12.5). |

## 1. Project layout

```
apps/mobile/
├── package.json              # capacitor + scripts
├── capacitor.config.ts       # appId com.maktaba.app, appName, webDir = "dist"
├── scripts/
│   ├── sync-web.ts           # copy web/dist → dist
│   └── build-ios.ts          # cap sync + xcodebuild
├── dist/                     # produced; gitignored
├── ios/
│   └── App/
│       ├── App.xcodeproj/
│       ├── App/
│       │   ├── AppDelegate.swift
│       │   ├── SceneDelegate.swift
│       │   ├── Info.plist
│       │   ├── Assets.xcassets/    # icons + LaunchImage; light + dark
│       │   └── public/             # Capacitor mounts here
│       └── Podfile
└── plugins/                  # (Epic-12 native plugins live here too)
```

## 2. capacitor.config.ts

```ts
const config: CapacitorConfig = {
  appId: 'com.maktaba.app',
  appName: 'Maktaba',
  webDir: 'dist',
  bundledWebRuntime: false,
  ios: {
    contentInset: 'always',
    backgroundColor: '#0F1115',           // dark; matches splash
    limitsNavigationsToAppBoundDomains: true,
  },
  // {server_url} is the user-configured server host captured during the
  // pair-code claim flow (see §0 — owned by plan-10-17-auth-pair.md).
  // At runtime the allowNavigation list is set to ['*.{server_host}', '{server_host}'].
  server: { allowNavigation: ['*.{server_host}', '{server_host}'] },
  plugins: {
    SplashScreen: { launchAutoHide: false, backgroundColor: '#0F1115' },
    StatusBar: { style: 'DEFAULT', overlaysWebView: false },
  },
};
```

## 3. Lifecycle wiring

`apps/mobile/ios/App/App/AppDelegate.swift`:

```swift
func applicationDidEnterBackground(_ application: UIApplication) {
    NotificationCenter.default.post(name: .maktabaBackgrounded, object: nil)
}
func applicationWillEnterForeground(_ application: UIApplication) {
    NotificationCenter.default.post(name: .maktabaForegrounded, object: nil)
}
func applicationDidReceiveMemoryWarning(_ application: UIApplication) {
    NotificationCenter.default.post(name: .maktabaMemoryPressure, object: nil)
}
```

The web bundle subscribes via a thin Capacitor plugin `lifecycle-bridge`:

```ts
// apps/mobile/plugins/lifecycle-bridge/web/index.ts
export const Lifecycle = registerPlugin<LifecyclePlugin>('Lifecycle');
Lifecycle.addListener('background', () => wsThrottle.toBackground());
Lifecycle.addListener('foreground', () => { wsThrottle.toForeground(); refreshVisible(); });
Lifecycle.addListener('memoryPressure', () => qc.clear());
```

`wsThrottle` (in `web/src/lib/ws.ts`) delays reconnects to ≥ 60 s when backgrounded.

## 4. Splash, theme, safe areas

- LaunchScreen.storyboard renders the brand mark on `#0F1115` matching the dark theme.
- `StatusBar.setStyle({ style: theme.effective === 'dark' ? 'DARK' : 'LIGHT' })` runs from `<ThemeProvider>` whenever `effective` changes.
- Safe-area insets exposed as CSS env vars (already used by Story 11.7's Tailwind config); `<BottomTabs>` adds `pb-safe-b`.

## 5. Build & TestFlight

`fastlane/Fastfile`:

```ruby
lane :beta do
  setup_ci
  sh("cd ../apps/mobile && npm run build && npm run sync-ios")
  build_app(scheme: "App", workspace: "apps/mobile/ios/App/App.xcworkspace", export_method: "app-store")
  upload_to_testflight(skip_waiting_for_build_processing: true)
end
```

CI (GitHub Actions) macOS runner runs `fastlane ios beta` on tag `mobile-v*`.

## 6. Edge cases

| Case | Handling |
|---|---|
| WKWebView crash | `webViewWebContentProcessDidTerminate` reloads the route after 500 ms; if it crashes again within 10 s, show native error UI. |
| iOS 16.0 quirk (WKWebView memory) | Documented; we ship the JIT-disable workaround if reproducible. |
| iPhone SE smallest screen | Layout already validated in Story 11.7 viewport matrix. |
| iPad split view | Treated as tablet by Story 11.7 breakpoints. |

## 7. Test cases

### 7.1 Manual / device

- iPhone SE 3rd gen: cold launch ≤ 3 s; no clipping.
- iPhone 13 (notch): status bar padding correct; Dynamic Island devices similarly.
- iPad Air split-view: layout matches Story 11.7 baseline.

### 7.2 Automated (XCUITest)

| Test | Asserts |
|---|---|
| `cold launch reaches Library` | Splash → library list visible ≤ 3 s on iPhone 13 sim. |
| `background → foreground refresh` | Set "background" event; library content reloads within 1 s after foreground. |
| `low-memory simulator` | Trigger `memoryPressure`; query cache cleared (verify count = 0 via JS bridge). |

### 7.3 Crash recovery (XCUITest)

- Inject a crash via `WKWebView.loadHTMLString` with malformed payload; assert error banner appears, no white screen.

## 8. Performance

- Cold launch budget ≤ 3 s on iPhone 13 (tracked by Fastlane via XCTest signposts).
- Bundle size ≤ 750 KB gzipped (Capacitor shim + web bundle).

## 9. Dependencies

- Web bundle from Epic 11 must be built first.
- `apps/mobile/plugins/lifecycle-bridge` is the first plugin; subsequent stories add `native-player`, `download-manager`, `push`, etc.

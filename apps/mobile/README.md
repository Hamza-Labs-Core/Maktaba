# Maktaba mobile (Capacitor)

iOS and Android wrappers around the shared `web/` SPA via **Capacitor 6**.
The native shells serve the exact same `web/dist` bundle as the desktop
and browser clients — they add only the native lifecycle, push, secure
storage, and deep-link surface.

- **appId / bundle id:** `com.hamzalabs.maktaba`
- **appName:** `Maktaba`
- **webDir:** `../../web/dist`

## Layout

```
apps/mobile/
├── package.json           ← Capacitor deps + scripts
├── capacitor.config.ts    ← appId, webDir, dev server URL, plugins
├── scripts/build.sh       ← build web + cap sync (the entrypoint)
├── ios/                   ← scaffolded by `npx cap add ios` (gitignored)
├── android/               ← scaffolded by `npx cap add android` (gitignored)
└── src/native-shell.ts    ← native-only bridge (lifecycle, deep links,
                              haptics, secure storage)
```

## Prerequisites

| Platform | Needs |
|---|---|
| iOS      | macOS, **Xcode** 15+, **CocoaPods** (`sudo gem install cocoapods`), an Apple Developer account for signing |
| Android  | **Android Studio**, Android SDK (set `ANDROID_HOME`), **JDK 17** |

## Bootstrap

```bash
# from repo root — the build script handles web build + platform add + sync
cd apps/mobile
pnpm install
./scripts/build.sh            # builds web/dist, adds ios+android, syncs
npx cap open ios              # opens Xcode
npx cap open android          # opens Android Studio
```

Or from the repo root via the Makefile:

```bash
make mobile-build             # = apps/mobile/scripts/build.sh
make native-sync              # cap sync only (web/dist must already exist)
```

## Dev live-reload

For fast iteration you can point the WebView at a running Vite dev server
instead of the bundled `web/dist`, so edits hot-reload on device:

```bash
# 1. start the API (:8080) and the web dev server (:5173)
make dev            # or: cd web && pnpm dev

# 2. run the app pointed at the host machine's LAN IP (NOT localhost —
#    on a phone localhost is the phone itself)
cd apps/mobile
CAP_SERVER_URL=http://192.168.1.20:5173 npx cap run ios
```

The Vite dev server proxies `/api` and `/ws` to the local Go API, so one
URL gives the WebView both the SPA and the API. `cleartext` is enabled
automatically for `http://` LAN dev URLs; production stays https / custom
scheme only.

## Plugins / native surface

| Concern | Plugin | Notes |
|---|---|---|
| Splash screen | `@capacitor/splash-screen` | hidden after first paint by the bridge (no white flash) |
| Status bar | `@capacitor/status-bar` | tint reconciled to the SPA `data-theme` (light/dark) |
| Push (FCM/APNs) | `@capacitor/push-notifications` | device registered server-side via `POST /api/devices` |
| Secure storage | `@aparajita/capacitor-secure-storage` | Keychain (iOS) / Keystore (Android) — auth refresh token |
| Network / app / haptics | `@capacitor/network` `/app` `/haptics` | lifecycle + offline banner + haptics (Story 12.8) |

## Lifecycle bridge (`src/native-shell.ts`)

`installNativeShell()` registers app / network / status-bar / deep-link /
back-button listeners and re-publishes them as `mkt:*` DOM events the SPA
already consumes (`web/src/lib/native.ts`, called from `web/src/main.tsx`):

- `mkt:appResumed` / `mkt:appBackgrounded` — resume / WS-throttle
- `mkt:networkChange` — offline banner
- `mkt:lowMemory` — drop in-memory caches
- `mkt:deepLink` — `maktaba://watch/{id}` deep-link navigation (custom
  scheme + Universal/App Links), incl. cold-launch URL replay
- `mkt:backAtRoot` — Android back button at history root → quit prompt

Storage helpers: `nativePrefs` (UserDefaults / SharedPreferences, **not**
encrypted — UI prefs only) and `nativeSecureStore` (Keychain / Keystore —
secrets / tokens).

## Native projects are NOT checked in

`ios/` and `android/` are gitignored. `npx cap add ios|android` requires
the platform toolchain (Xcode + CocoaPods / Android SDK + JDK) and signing
infra that this repo's CI does not provide. Committing empty native
projects would fake-green CI, so they are generated on demand from the
config above. The config, bridge, and web wiring are real and buildable
today; signing certs, push credentials (FCM `google-services.json`, APNs
key + entitlements), and store metadata are provisioned per-platform
outside version control.

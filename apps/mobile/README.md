# Maktaba mobile (Capacitor)

Epic 12 (Stories 12.1 / 12.2) ships an iOS and Android wrapper around
the shared `web/` SPA via Capacitor 6.

## Layout

```
apps/mobile/
├── package.json          ← Capacitor deps + scripts
├── capacitor.config.json ← appId, webDir → ../../web/dist, plugins
├── ios/                  ← scaffolded by `npx cap add ios` (not in git yet)
├── android/              ← scaffolded by `npx cap add android` (not in git yet)
└── src/                  ← native-only TypeScript bridge helpers
```

## Bootstrap

```bash
cd web && pnpm build         # produce web/dist
cd ../apps/mobile
pnpm install
npx cap add ios              # one-time
npx cap add android          # one-time
npx cap sync
npx cap open ios             # opens Xcode
npx cap open android         # opens Android Studio
```

## Lifecycle hooks (Story 12.1 / 12.2 AC)

`src/native-shell.ts` registers app, network, status-bar, deep-link and
Android back-button listeners so the JS bundle gets these DOM events:

- `mkt:appResumed` / `mkt:appBackgrounded` — resume / WS-throttle
- `mkt:networkChange` — show offline banner
- `mkt:lowMemory` — clear in-memory caches
- `mkt:deepLink` — Story 12.9 deep-link navigation (custom scheme +
  Universal/App Links), incl. cold-launch URL replay
- `mkt:backAtRoot` — Story 12.2 Android back button at history root
  (SPA shows a "Quit?" prompt before `App.exitApp()`)

The web side now actually **consumes** these. `web/src/lib/native.ts`
(`bootNative()`, called from `web/src/main.tsx`) installs the
consumers and — only inside the Capacitor shell — dynamically loads
this bridge. Previously `installNativeShell()` had no importer and
never ran; that wiring gap is closed.

Haptics: `haptic(kind)` covers tap / medium / selection / success /
warning / error, with a ≤ 1 / 100 ms throttle and OS reduce-motion +
in-app `setHapticLevel()` gating (Story 12.8).

## Native projects: deferred (no toolchain in this repo)

`ios/` and `android/` are **not in git** and are NOT generated here.
`npx cap add ios|android` requires Xcode / Android Studio, signing
infra and a device/simulator that this repository's CI does not
provide. Committing empty native projects would assert nothing and
fake-green CI, so they are intentionally omitted. The bridge and web
wiring above are real and buildable today; the native shells, native
video player (12.3), background playback (12.5), offline downloads
(12.6) and push credentials (12.4) are blocked on that toolchain —
see `specs/gap-analysis/epic-12-mobile.md`.

## Out of scope for the scaffold

- Apple/Google signing certs and provisioning profiles
- Push notification credentials (FCM, APNs)
- App Store / Play Store metadata

Those land in Stories 12.4, 12.10, and 22.x.

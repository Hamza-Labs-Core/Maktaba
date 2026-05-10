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

## Lifecycle hooks (Story 12.1 AC)

`src/native-shell.ts` registers app, network, and status-bar listeners
so the JS bundle gets:

- `mkt:appResumed` — refresh visible page
- `mkt:appBackgrounded` — throttle WS reconnect
- `mkt:networkChange` — show offline banner
- `mkt:lowMemory` — clear in-memory caches

## Out of scope for the scaffold

- Apple/Google signing certs and provisioning profiles
- Push notification credentials (FCM, APNs)
- App Store / Play Store metadata

Those land in Stories 12.4, 12.10, and 22.x.

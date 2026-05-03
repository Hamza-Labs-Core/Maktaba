# Story 12.2 — Android app wrapper

A Capacitor 6 wrapper that builds an Android APK / AAB with the same web
bundle.

**Anchors:** [`architecture.md` §6.3](../../architecture.md), §2.1.

## AC

- App targets Android 9+ (API 28); ARM64 + ARMv7.
- Cold launch to library list ≤ 4 s on a Pixel 5.
- Edge-to-edge layout with proper insets on notched / hole-punch devices.
- Back button: pops the in-app history stack; from the root, prompts
  "Quit Maktaba?".
- Foreground service for downloads
  ([Story 12.6](story-12-06-offline-downloads.md)) declared in manifest.
- Play Store internal testing track configured; AAB signing via Play App
  Signing.

## TC

- Launch on a low-end Android (Moto G play): start ≤ 6 s, no ANR.
- Tap Back from Settings: returns to the previous tab.
- Background → 1 hour → foreground: data refreshes; no stale spinner.
- Rotate device on the player route: video continues without restarting.

## EC

- WebView updated mid-session (Chrome System WebView background update):
  app survives the implicit reload.
- A device with no Google Play Services (e.g., Huawei post-2019): push
  falls back to in-app polling; downloads work via WorkManager.
- Sideloaded APK with no Play Store: in-app updater (we ship our own
  update check pinging `/api/system/version`) prompts for manual install.

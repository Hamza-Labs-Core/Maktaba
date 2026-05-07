# Epic 12 — Mobile Apps (Capacitor)

**Goal.** iOS and Android apps that wrap the same web bundle as a native
shell, with a native-player handoff plugin, background download, push
notifications, share targets, and Keychain/Keystore for refresh tokens.
The web app is the source of truth for UI and routing; native code only
exists where browser APIs are insufficient.

**Anchors:** [`architecture.md` §6.3](../../architecture.md), §2.1
(Capacitor 6).

---

## Stories

| # | Story | Status |
|---|-------|--------|
| 12.1 | [iOS app wrapper](story-12-01-ios-app.md) | spec |
| 12.2 | [Android app wrapper](story-12-02-android-app.md) | spec |
| 12.3 | [Native video player integration](story-12-03-native-player.md) | spec |
| 12.4 | [Push notifications](story-12-04-push-notifications.md) | spec |
| 12.5 | [Background playback](story-12-05-background-playback.md) | spec |
| 12.6 | [Download for offline viewing](story-12-06-offline-downloads.md) | spec |
| 12.7 | [Share / AirPlay / Chromecast support](story-12-07-share-cast.md) | spec |
| 12.8 | [Haptic feedback](story-12-08-haptics.md) | spec |
| 12.9 | [Deep linking](story-12-09-deep-linking.md) | spec |
| 12.10 | [API: device registration & push fan-out](story-12-10-device-registration-api.md) | spec (added per REVIEW §3.2) |
| 12.11 | [API: downloaded-flag sync](story-12-11-downloaded-flag-api.md) | spec (added per REVIEW §3.4) |

---

## Dependencies

- **Epic 11** (Web UI) is the source bundle — everything in 11.1–11.12
  ships inside the Capacitor shell.
- **Epic 10** (Auth) Story 10.3 (refresh tokens) must support
  Keychain/Keystore storage on the client side.
- **Epic 8** (Streaming) Story 8.1 (signed-URL middleware) issues the
  manifests consumed by Story 12.3 (native player).
- **Epic 17** (Design System) Stories 17.1, 17.2 — token outputs include
  iOS / Android JSON consumers.

## Cross-cutting checklist

- **Native plugins** are listed in `apps/mobile/plugins/`; each carries
  its own README and an Espresso/XCUITest harness.
- **Permissions** (notifications, camera, photos, network) are requested
  contextually, never on first launch except where required by the OS.
- **Tokens at rest** live in iOS Keychain / Android Keystore; never in
  `localStorage` inside the WebView.
- **Bundle size:** JS bundle ≤ 750 KB gzipped (Capacitor shim + web
  bundle, with headroom over the web target).
- **Offline:** Story 12.6 owns long-lived downloads; the PWA service
  worker (Story 11.10) still runs and remains the source of metadata
  caching.

## Out of scope

- Wear OS / watchOS companion apps.
- Carplay / Android Auto integrations.
- Maktaba as a CarPlay audio source (audio-only "podcast" view).

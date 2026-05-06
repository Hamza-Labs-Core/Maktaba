# Epic 12 — Mobile Apps (Capacitor)

> iOS and Android apps that wrap the same web bundle as a native shell, with a native-player handoff plugin, background download, push notifications, share targets, and Keychain/Keystore for refresh tokens. The web app is the source of truth for UI and routing; native code only exists where browser APIs are insufficient.

- **Spec README:** [`specs/epics/12-mobile/README.md`](../../../specs/epics/12-mobile/README.md)
- **Architecture anchors:** §6.3, §2.1 (Capacitor 6)
- **Source bundle:** [Epic 11 Web UI](epic-11-web-ui.md) — everything in 11.1–11.12 ships inside the Capacitor shell unchanged.
- **Out of scope:** Wear OS / watchOS companion apps, CarPlay / Android Auto integrations, Maktaba as a CarPlay audio source.

## Stories & Plans

| #     | Story                                                  | Plan                                                | Status |
|-------|--------------------------------------------------------|-----------------------------------------------------|--------|
| 12.1  | [iOS app wrapper](../../../specs/epics/12-mobile/story-12-01-ios-app.md) | [plan](../../../specs/epics/12-mobile/plan-12-01-ios-app.md) | spec |
| 12.2  | [Android app wrapper](../../../specs/epics/12-mobile/story-12-02-android-app.md) | [plan](../../../specs/epics/12-mobile/plan-12-02-android-app.md) | spec |
| 12.3  | [Native video player integration](../../../specs/epics/12-mobile/story-12-03-native-player.md) | [plan](../../../specs/epics/12-mobile/plan-12-03-native-player.md) | spec |
| 12.4  | [Push notifications](../../../specs/epics/12-mobile/story-12-04-push-notifications.md) | [plan](../../../specs/epics/12-mobile/plan-12-04-push-notifications.md) | spec |
| 12.5  | [Background playback](../../../specs/epics/12-mobile/story-12-05-background-playback.md) | [plan](../../../specs/epics/12-mobile/plan-12-05-background-playback.md) | spec |
| 12.6  | [Download for offline viewing](../../../specs/epics/12-mobile/story-12-06-offline-downloads.md) | [plan](../../../specs/epics/12-mobile/plan-12-06-offline-downloads.md) | spec |
| 12.7  | [Share / AirPlay / Chromecast support](../../../specs/epics/12-mobile/story-12-07-share-cast.md) | [plan](../../../specs/epics/12-mobile/plan-12-07-share-cast.md) | spec |
| 12.8  | [Haptic feedback](../../../specs/epics/12-mobile/story-12-08-haptics.md) | [plan](../../../specs/epics/12-mobile/plan-12-08-haptics.md) | spec |
| 12.9  | [Deep linking](../../../specs/epics/12-mobile/story-12-09-deep-linking.md) | [plan](../../../specs/epics/12-mobile/plan-12-09-deep-linking.md) | spec |
| 12.10 | [API: device registration & push fan-out](../../../specs/epics/12-mobile/story-12-10-device-registration-api.md) | [plan](../../../specs/epics/12-mobile/plan-12-10-device-registration-api.md) | spec (added per REVIEW §3.2) |
| 12.11 | [API: downloaded-flag sync](../../../specs/epics/12-mobile/story-12-11-downloaded-flag-api.md) | [plan](../../../specs/epics/12-mobile/plan-12-11-downloaded-flag-api.md) | spec (added per REVIEW §3.4) |

## DB tables owned

This epic ships native shells — no DB tables of its own. Stories 12.10 and 12.11 add API surface that lives logically with [Epic 07](epic-07-api-server.md):

- **Device registration (12.10):** writes to the `devices` table owned by [Epic 07 story 7.22](../../../specs/epics/07-api-server/story-07-22-devices-register.md).
- **Downloaded-flag sync (12.11):** new column / table for per-device download state on watch progress (joins to `videos` and `users`).

## API endpoints owned

| Endpoint                                  | Story  |
|-------------------------------------------|--------|
| `POST /devices/register`, `GET /devices`, `DELETE /devices/{id}` | 12.10 (story owns the contract; handler implementation in [Epic 07 story 7.22](../../../specs/epics/07-api-server/story-07-22-devices-register.md)) |
| `GET/PUT /videos/{id}/downloads/{deviceId}` | 12.11 |

## Mockups

| File | Story | Platform | UI states / contents |
|---|---|---|---|
| [`web/mockups/mobile/home.html`](../../../web/mockups/mobile/home.html) | 12.1, 12.2 | mobile | Home grid, continue-watching row, library shelves |
| [`web/mockups/mobile/video-detail.html`](../../../web/mockups/mobile/video-detail.html) | 12.1, 12.2 | mobile | Video detail with download / cast / share affordances |
| [`web/mockups/mobile/player.html`](../../../web/mockups/mobile/player.html) | 12.3, 12.5 | mobile | Native player chrome, background-playback controls |
| [`web/mockups/mobile/search.html`](../../../web/mockups/mobile/search.html) | 12.1, 12.2 | mobile | Mobile search UI |
| [`web/mockups/mobile/downloads.html`](../../../web/mockups/mobile/downloads.html) | 12.6 | mobile | Download manager: queued, in-progress, paused, complete |
| [`web/mockups/mobile/push-notification.html`](../../../web/mockups/mobile/push-notification.html) | 12.4 | mobile | Push permission prompt + sample alerts |

## Diagrams

| Diagram | Type | Coverage |
|---|---|---|
| [`client-stories.drawio`](../../../specs/diagrams/client-stories.drawio) | Story-relationship | All Epic 12 stories grouped with 11/13/17 |
| [`system-architecture.drawio`](../../../specs/diagrams/system-architecture.drawio) | System | Mobile shells in the topology |
| [`data-flow.drawio`](../../../specs/diagrams/data-flow.drawio) | Flow | Mobile → API + Streaming, plus push fan-out |
| [`auth-flow.drawio`](../../../specs/diagrams/auth-flow.drawio) | Flow | Native JWT login + refresh + Keychain/Keystore storage |

## Dependencies on other epics

- **[Epic 11](epic-11-web-ui.md)** is the source bundle — everything in 11.1–11.12 ships inside the Capacitor shell.
- **[Epic 10 story 10.3](../../../specs/epics/10-auth-security/story-10-03-native-login.md)** (refresh tokens) — must support Keychain/Keystore storage on the client side.
- **[Epic 08 story 8.1](../../../specs/epics/08-streaming/story-08-01-server-skeleton.md)** (signed-URL middleware) — issues the manifests consumed by 12.3 (native player).
- **Epic 17 stories 17.1, 17.2** — token outputs include iOS/Android JSON consumers.
- **[Epic 07 story 7.22](../../../specs/epics/07-api-server/story-07-22-devices-register.md)** — device-token store this epic registers against (12.4).

## Key decisions

- **Capacitor over React Native.** The web bundle is the canonical UI; native plugins fill the gaps. No code-fork.
- **Native player handoff.** AVPlayer (iOS) / ExoPlayer (Android) consume HLS via 8.5; the web Vidstack player is bypassed on mobile to gain background playback, AirPlay/Chromecast, and lock-screen controls.
- **Refresh tokens in Keychain / Keystore**, never in `localStorage` inside the WebView.
- **Permissions are contextual.** Notifications, camera, photos, network — never asked on first launch except where the OS requires it.
- **Long-lived downloads** (12.6) are owned by native code; the PWA service worker (11.10) still runs and remains the source of metadata caching.
- **Bundle size budget:** JS bundle ≤ **500 KB** gzipped (250 KB headroom over web for the Capacitor shim).
- **Push routing.** Server-side fan-out reads from `devices` and dispatches via APNs / FCM (12.4). Per-device tokens are revocable from `admin/sessions.html`.
- **Deep links** (12.9) reuse the existing window via single-instance lock; OS-level cold-start opens the deep target directly.

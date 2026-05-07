# Story 12.1 — iOS app wrapper

A Capacitor 6 wrapper that builds an iOS app from `web/`, launches a
native shell, and handles iOS-specific lifecycle events (background /
foreground, low memory, status bar).

**Anchors:** [`architecture.md` §6.3](../../architecture.md), §2.1.

## AC

- App targets iOS 16+; iPhone and iPad universal.
- Splash screen matches the brand; cold launch to library list ≤ 3 s on
  iPhone 13.
- Status bar style follows the active theme; safe-area insets respected
  on every notched / Dynamic Island device.
- Backgrounding pauses non-essential timers (WS reconnect throttles to
  60 s); foregrounding refreshes the visible screen.
- Memory warnings: clear in-memory caches, keep state.
- App icon, launch image, and dark-mode variants present.
- TestFlight build pipeline configured; signing via the provisioning
  profile in `apps/mobile/ios/`.

## TC

- Launch on iPhone SE (smallest current screen): no clipping, all CTAs
  accessible.
- Launch on iPad with split view: layout adapts (treats as tablet).
- Background → 30 minutes → foreground: WS reconnects, UI shows fresh
  data within 1 s.
- Low-memory simulation: caches purged, no crashes.

## EC

- WKWebView crash on a malformed video URL: native shell catches and
  reloads the route with an error banner instead of white-screening.
- App killed mid-download: see [Story 12.6](story-12-06-offline-downloads.md).
- iOS 16.0 specifically (older WKWebView quirks): tested explicitly; any
  workaround documented.

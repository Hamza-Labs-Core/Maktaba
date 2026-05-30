# Epic 12 — Mobile: Spec vs Implementation Gap Analysis

**Verdict:** Epic 12 is ~95% unimplemented. Only a Capacitor *config skeleton* (4 files, no native projects, no plugins, no web wiring) exists, plus a pre-existing **Story 7.22** device-registration endpoint that does **not** satisfy the Story 12.10 contract. Stories 12.1–12.9 are entirely missing; 12.10 is partial/divergent; 12.11 is fully missing.

Method: every AC traced to code. READ-ONLY. Verified against source, not audit/spec self-claims.

---

## What actually exists (inventory)

| Path | Lines / state | Relevance |
|---|---|---|
| `apps/mobile/package.json` | 35 | Capacitor 6 deps declared (`@capacitor/app, network, status-bar, splash-screen, preferences, haptics, push-notifications, share`). No native-player, no filesystem/background-download plugin. |
| `apps/mobile/capacitor.config.json` | 40 | appId `dev.maktaba.app`, `webDir → ../../web/dist`, SplashScreen/StatusBar/PushNotifications plugin config. |
| `apps/mobile/src/native-shell.ts` | 89 | Lifecycle→DOM-event bridge + `tapHaptic()` + `nativePrefs` (uses **`@capacitor/preferences`, NOT Keychain/Keystore**). |
| `apps/mobile/README.md` | 47 | Documents `ios/`, `android/` as *"scaffolded by npx cap add (not in git yet)"*. |
| `apps/mobile/ios/`, `apps/mobile/android/` | **absent** | No native Xcode/Gradle projects exist. |
| `apps/mobile/plugins/` | **absent** | README §cross-cutting requires `apps/mobile/plugins/`; dir does not exist. |
| `web/src/lib/native.ts` | **absent** | `native-shell.ts:4` says SPA imports this to detect Capacitor; file does not exist → bridge is never invoked. `grep -rn Capacitor\|installNativeShell web/src` → 0 hits. |
| `api/internal/handlers/devices/devices.go` | 217 | Labeled **"Story 7.22"** (line 1), mounted at `router/p6.go:101`. Schema/contract diverges from Story 12.10. |
| `shared/db/migrations/0040_devices.sql` | 36 | `devices` table — but **"Slot 0040 (Epic 7 / Story 7.22)"**, schema differs from 12.10 spec. |
| `cloud/internal/push/{push,apns,fcm,jws}.go` | — | A push *Dispatcher* exists but reads its **own** `push_devices` table (`cloud/migrations/00070001_push.sql`), unrelated to API `devices`. No domain-event consumer. |

---

## Per-story AC analysis

### Story 12.1 — iOS app wrapper — **MISSING**

| AC | Status | Evidence / gap |
|---|---|---|
| iOS 16+, iPhone+iPad universal | missing | No `apps/mobile/ios/` Xcode project; nothing sets a deployment target. |
| Splash matches brand; cold launch ≤ 3 s | unwired | `capacitor.config.json:23-30` configures SplashScreen, but no iOS project to host it; `SplashScreen.hide()` (`native-shell.ts:59`) never runs (bridge not imported). |
| Status-bar style follows theme; safe-area insets | unwired | `native-shell.ts:43-55` has the logic, but `installNativeShell()` is never called (no `web/src/lib/native.ts`). |
| Background pauses timers; WS throttle 60 s | partial/unwired | `native-shell.ts:23-25` dispatches `mkt:appBackgrounded`; no consumer in `web/src` (0 grep hits) and no 60 s throttle code. |
| Memory warnings clear caches | unwired | `native-shell.ts:39` forwards `lowmemory`→`mkt:lowMemory`; no listener; iOS shim "plumbing" described in comment (38) does not exist. |
| App icon / launch image / dark variants | missing | No iOS asset catalog. |
| TestFlight pipeline; signing profile in `apps/mobile/ios/` | missing | No `ios/`, no CI lane. README §out-of-scope explicitly defers signing. |

### Story 12.2 — Android app wrapper — **MISSING**

| AC | Status | Evidence / gap |
|---|---|---|
| Android 9+ (API 28), ARM64+ARMv7 | missing | No `apps/mobile/android/` Gradle project. |
| Cold launch ≤ 4 s Pixel 5 | missing | No Android app. |
| Edge-to-edge + insets | missing | No Android project / theme. |
| Back button pops history; root → "Quit?" prompt | missing | No `@capacitor/app` `backButton` handler anywhere (`native-shell.ts` registers only `appStateChange`/`appRestoredResult`). |
| Foreground service for downloads in manifest | missing | No `AndroidManifest.xml`; no download feature (12.6). |
| Play Store internal track; AAB Play App Signing | missing | No Android project, no CI. |

### Story 12.3 — Native video player integration — **MISSING**

| AC | Status | Evidence / gap |
|---|---|---|
| Plugin API `nativePlayer.open({...})` | missing | No `plugins/native-player/`; no `nativePlayer` symbol anywhere in repo. |
| Call `POST /api/stream/sessions` then pass direct/manifest URL | missing | No native player; no integration code. |
| AVPlayerViewController (iOS) / ExoPlayer (Android) | missing | No native source files exist. |
| Now Playing via MPNowPlayingInfoCenter / MediaSession | missing | Not implemented. |
| Subtitle switching exposed + echoed to web | missing | Not implemented. |
| Close returns to detail page w/ synced position | missing | Not implemented. |
| Position sync `POST .../progress` every 10 s | missing | No native player; no 10 s loop. |

### Story 12.4 — Push notifications — **MISSING (client)**

| AC | Status | Evidence / gap |
|---|---|---|
| Permission asked only after Queue/onboarding | missing | `@capacitor/push-notifications` declared in `package.json:24` but **never imported** (not in `native-shell.ts`, not in `web/src`). No permission-prompt logic. |
| Categories (4 types) | missing | No category model client-side. |
| Toggle each category in Settings | missing | No settings UI binding. |
| Deep-link payload `maktaba://watch/{id}` opens page | missing | No push handler; no deep-link routing (12.9 absent). |
| Client posts token to `POST /api/devices/register {token, platform, locale}` | missing | No registration call; and endpoint expects different body (see 12.10). |
| Token rotation on cold launch; server dedup | missing | No client cold-launch re-register. |
| Sound/vibration follow device defaults | unwired | Config `presentationOptions` set (`capacitor.config.json:36-38`) but no native project consumes it. |

### Story 12.5 — Background playback — **MISSING**

All ACs missing: no audio-session category, no Android `mediaPlayback` foreground service, no lock-screen controls, no headphone-unplug pause, no PiP, no background position sync. Depends on 12.3 (absent).

### Story 12.6 — Offline downloads — **MISSING**

All ACs missing: no Download action, no quality picker, no `URLSession`/WorkManager integration, no Downloads tab, no quota/LRU, no encrypted storage, no offline-source switching, no BLAKE3 checksum. No filesystem/download Capacitor plugin in `package.json`. Depends on 12.11 (absent).

### Story 12.7 — Share / AirPlay / Chromecast — **MISSING**

| AC | Status | Evidence / gap |
|---|---|---|
| Share button → native share sheet w/ deep link | missing | `@capacitor/share` declared (`package.json:25`) but **never imported/used**; no Share button code. |
| AirPlay via AVPlayer | missing | No native player (12.3). |
| Chromecast via Cast SDK | missing | No Cast SDK dependency anywhere. |
| Share to Messages/Mail/… same payload | missing | No share invocation. |
| Receiving shared link opens deep link | missing | 12.9 absent. |

### Story 12.8 — Haptic feedback — **PARTIAL / UNWIRED**

| AC | Status | Evidence / gap |
|---|---|---|
| Haptic events (tab/long-press/toggle/download/error) | stub | `native-shell.ts:65-71` exports a single `tapHaptic()` (Light impact only). No medium-impact, no selection-change, no success/warning notification haptics. Never called (no importer). |
| iOS UIImpact/UINotification; Android HapticFeedbackConstants | unwired | Relies on `@capacitor/haptics`; no native project. |
| Respect OS reduce-motion/haptics toggle | missing | No check. |
| Settings → Accessibility → Haptics (Off/Light/Full) | missing | No setting. |
| EC: throttle ≤1/100 ms | missing | No throttle in `tapHaptic`. |

### Story 12.9 — Deep linking — **MISSING**

| AC | Status | Evidence / gap |
|---|---|---|
| iOS Universal Links w/ web fallback | missing | No `apple-app-site-association` served (security.go hits are JWKS, unrelated); no iOS project assoc-domains. |
| Android App Links via `/.well-known/assetlinks.json` | missing | No `assetlinks.json` route in `api/`. |
| Custom scheme `maktaba://watch/{id}?t=` | partial-config-only | `capacitor.config.json:8` sets `iosScheme: "maktaba"` (WKWebView scheme, not a URL-handler); no `appUrlOpen` listener anywhere. |
| Routes /watch /search /library /queue /settings /collection | missing | No deep-link router. |
| Cold launch → deep-linked route | missing | No handler. |
| Preserve deep link across login | missing | No logic. |

### Story 12.10 — API: device registration & push fan-out — **PARTIAL / DIVERGENT**

Implemented as **Story 7.22**, not 12.10. Material contract mismatches:

| AC (12.10) | Status | Evidence / gap |
|---|---|---|
| `devices` table per 12.10 columns | divergent | `0040_devices.sql:11-23`: has `bundle_id` (NOT in spec); **missing** `token_hash BYTEA GENERATED`, `os_version`, `categories JSONB`, `created_at`. Spec uniqueness `UNIQUE(user_id, token_hash)`; impl uses `UNIQUE(user_id, platform, push_token)`. Index `(token_hash)` absent. |
| `POST /api/devices/register {platform, token, app_version?, os_version?, locale?, categories?}` | divergent | `devices.go:28-34` body is `{platform, push_token, bundle_id, app_version, locale}` — field is `push_token` not `token`; `bundle_id` **required** (415-style 422 if missing, `devices.go:81-87`); no `categories`, no `os_version`. |
| `200`/`201` semantics | complete | `devices.go:140-144` returns 201 created / 200 updated + `{id}`. |
| `400 invalid-token` (format vs platform) | missing | No token-format validation. |
| `409 token-claimed-by-other-user` + 24 h grace revoke | missing | Cross-user `token_hash` claim not handled (no token_hash column). |
| `PATCH /api/devices/{id} {categories?, locale?}` | missing | Only `Post/register`, `Delete/{id}`, `Get` mounted (`devices.go:55-59`). No PATCH. |
| `DELETE /api/devices/{id}` → 204 soft-revoke | complete | `devices.go:148-168` sets `revoked_at`, 204. |
| `GET /api/me/devices` (no plaintext token) | divergent | Route is `GET /api/devices` not `/api/me/devices`; **leaks plaintext `push_token`** in JSON (`devices.go:178, 190, Device.PushToken json:"push_token"` line 41) — directly violates the "plaintext tokens never returned" security AC. |
| `push-fanout` worker (job.completed/failed, library.video.added, subscription.expiring) | missing | No domain-event consumer in `api/`. `cloud/internal/push` is a generic `Dispatcher.Send` keyed off cloud-only `push_devices` table; no event mapping, no category filter, no per-user locale payload. |
| Retry 3× backoff; revoke on Unregistered/BadDeviceToken | missing | Not implemented in any consumer. |
| Batching 5+ job.completed/60 s → one push | missing | No batching/coalesce code. |
| Config `[push.apns]/[push.fcm]`, secrets masked, disable-if-unconfigured | partial | `cloud/internal/config/config.go` + `apns.go`/`fcm.go` exist (cloud side); not wired to API devices registry; "disable + still 201" path untested against this story. |
| Audit log `category='device'` | missing | No audit emit in `devices.go`. |

### Story 12.11 — API: downloaded-flag sync — **MISSING**

| AC | Status | Evidence / gap |
|---|---|---|
| `device_downloads` table + index `(video_id)` | missing | No migration; `grep device_downloads` across `api shared` → 0 hits. |
| `POST/DELETE/GET/PATCH /api/videos/{video_id}/downloaded` | missing | No such routes; no handler file. |
| `403 not-a-device-session` for non-device auth | missing | Not implemented. |
| GraphQL `Video.downloads: [DeviceDownload!]!` | missing | No `DeviceDownload` type / field in schema. |
| Revoked-device retention; metadata-only | missing | Nothing built. |

---

## AC counts by status

| Status | Count (approx ACs) |
|---|---|
| Complete | 2 (12.10 register 200/201; 12.10 DELETE 204) |
| Partial | 3 (12.8 haptic stub; 12.10 schema/config; 12.9 scheme config) |
| Missing | ~62 |
| Unwired (code exists, never invoked) | ~7 (12.1 lifecycle bridge, 12.8 tapHaptic, push/share/splash config) |
| Stub | 1 (`tapHaptic`) |

(~75 ACs across 11 stories; only 2 fully satisfied.)

---

## Top gaps by impact

1. **No native projects, no plugins, no web bridge wiring (12.1/12.2 + foundation).** `apps/mobile/ios/`, `android/`, `plugins/` all absent; `web/src/lib/native.ts` (the documented importer of `installNativeShell()`) does not exist, so even the 89-line bridge that *is* written never executes. The entire epic has no runnable artifact.

2. **12.10 GET endpoint leaks plaintext push tokens.** `devices.go:41,178,190` returns `push_token` in `GET /api/devices` JSON, directly violating the Story 12.10 security AC ("Plaintext tokens are never returned by GET endpoints"). This is a live security defect in mounted, reachable code (`router/p6.go:101-102`).

3. **No push fan-out worker (12.10 core).** No consumer of `job.completed`/`library.video.added` etc.; the cloud `Dispatcher` is generic, keyed off an unrelated `push_devices` table, with no category filter, locale payload, retry, or 5/60 s batching. Push notifications cannot be delivered for any product event.

4. **12.10 contract divergence (table + endpoints).** Implemented as Story 7.22 with `push_token`/`bundle_id` schema; missing `token_hash`, `categories`, `os_version`, `PATCH`, `409` cross-user claim, `400` invalid-token, and the `/api/me/devices` path. Clients written to the 12.10 spec will not interoperate.

5. **12.11 entirely absent** → 12.6 offline-download cross-device sync impossible; no `device_downloads` table, routes, or GraphQL field.

6. **Tokens-at-rest violation (cross-cutting).** README mandates iOS Keychain / Android Keystore; `native-shell.ts:77-88` `nativePrefs` uses `@capacitor/preferences` (plain prefs), not secure storage — refresh tokens would not be stored securely even if the shell ran.

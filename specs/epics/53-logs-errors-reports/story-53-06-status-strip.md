# Story 53.06 — A status strip in every app

> Epic 53 · Phase 1 · Depends on: 53.03, Epic 51 (51.09 capabilities, 51.11 clients; built) · Needed by: 53.07, Epic 50.11

## Why

When streaming is down, a user sees a spinning player and thinks the app is broken. When the
cloud relay is slow, the same. The web has an offline banner (`web/src/components/OfflineBanner.tsx`,
#384) and Epic 50.11 planned a cloud banner in every app. One strip, fed by both the home's
`/status.json` (53.03) and the cloud's `summary.json` (50.11), tells people the truth in one
place.

## Description

### 1. One strip, three sources, one priority

| Priority | Source | Shows | Who sees it |
|---|---|---|---|
| 1 | The device is offline (existing) | "You're offline" | everyone |
| 2 | The home can't be reached (existing error kinds) | "Can't reach {home}" | everyone |
| 3 | `/status.json`: `overall: down` | `database_down` / `api_down`: "Maktaba is restarting. Some things won't work for a moment." | everyone |
| 4 | `/status.json`: `mode: safe` | the safe-mode text (53.05) | everyone |
| 5 | `/status.json`: a component down or degraded with a viewer banner key | e.g. `streaming_down`: "Playback and Live TV are unavailable right now." | everyone |
| 6 | Epic 50.11 `summary.json`: `relay` or `api` degraded, outage, or maintenance within 24 h, **only when the app reaches the home through the cloud** | the 50.11 incident title (EN/AR from the feed) | everyone using the relay |
| 7 | `/status.json`: admin-only keys (`processing_down`, `logs` down) | "Processing is paused: {reason}" with a link to System health or the console | admins |

Only the highest one shows; a "1 more" tap shows the rest. Admins also get a **Details** link
(System health, or `/recovery` when the API is down).

**A feature that is off is not an outage.** Epic 51 already tells the apps what is switched off:
`GET /api/capabilities` (`features`: `live_tv`, `transcripts`, `semantic_search`, …, `on` or
`off`), refreshed on `capabilities_changed` from `/ws/home`, and a `409 component_off` problem
(`code`, `component`, `feature`) from a route whose component is off. Story 51.11 hides those
features from viewers and explains them to admins (`error.kind.off`, `error.kind.offAdmin`;
Android `ErrorKind.Off`, Apple `ErrorKind.off`). The strip never repeats that:

- `/status.json` reports a switched-off, held or not-installed component as `off` (53.03 §2a),
  and `off` has no banner key;
- a `409 component_off` is not "a request failed": it does not trigger an early `/status.json`
  read, and the strip shows nothing for it;
- a feature the capabilities answer says is `off` gets no strip line, even when its status group
  is `degraded` for another reason (the TV part of `streaming_down` is left out of the sentence
  when `live_tv` is `off`: `status.streamingNoTv`).

### 2. Behaviour

- Apps read `/status.json` at start, every **10 s** while in the foreground (a few hundred bytes
  answered from memory), and at once when a
  request fails with a 5xx, a timeout or a gateway error; the 50.11 feed every 5 minutes and on a
  relay failure (as 50.11 says).
- Never blocks: a failed read of either feed shows nothing new.
- Not over the video while playing; it shows on pause, on exit, and in the player's error screen
  ("Playback is unavailable right now" instead of the generic error, when streaming is down).
- Can be closed for the current state; it comes back if the state changes.
- On TV apps (tvOS, Android TV) it is a top strip that doesn't take focus.
- Right to left in Arabic; the strip's icon is mirrored where it points.

### 3. Where

| App | File |
|---|---|
| Web (home), phone shells, desktop window | `web/src/components/StatusStrip.tsx` (new), replacing the inside of `OfflineBanner.tsx`, mounted in `AppShell.tsx` |
| Cloud web (`app.maktaba.tv`) | `web/cloud/src/StatusStrip.tsx` (50.11 source only, plus the linked home's `/status.json` when a home is open) |
| Apple (Player, Remote, tvOS) | `apps/apple/MaktabaKit/Sources/MaktabaKit/StatusStrip.swift`, `HomeStatus.swift` (new) |
| Android (Player, Remote) | `apps/android/core/src/main/kotlin/com/hamzalabs/maktaba/core/ui/StatusStrip.kt`, `core/session/HomeStatus.kt` (new) |
| Android TV | `apps/tv/android/app/src/main/java/com/hamzalabs/maktaba/tv/ui/StatusStrip.kt` (new) |
| Mac app tray | The tray's status line uses the same priority (it already reads `/processes`) |

This replaces 50.11's planned `CloudStatusBanner` files: 50.11 provides the feed and its
fetcher; this story draws it.

### 4. Strings

| Key | English | Arabic |
|---|---|---|
| `status.offline` | You're offline | أنت غير متصل بالإنترنت |
| `status.unreachable` | Can't reach {home} | تعذّر الوصول إلى {home} |
| `status.restarting` | Maktaba is restarting. Some things won't work for a moment. | مكتبة تُعاد تشغيلها. قد لا تعمل بعض الميزات للحظات. |
| `status.database` | Maktaba is having trouble with its database. Playback and browsing may not work. | تواجه مكتبة مشكلة في قاعدة البيانات. قد لا يعمل التشغيل والتصفح. |
| `status.streaming` | Playback and Live TV are unavailable right now. | التشغيل والبث المباشر غير متاحين الآن. |
| `status.streamingNoTv` | Playback is unavailable right now. | التشغيل غير متاح الآن. |
| `status.search` | Search is unavailable right now. | البحث غير متاح الآن. |
| `status.processing` | Processing is paused: new videos won't be added for now. | المعالجة متوقفة مؤقتًا: لن تُضاف فيديوهات جديدة الآن. |
| `status.details` | Details | التفاصيل |
| `status.more` | {count} more | {count} أخرى |
| `status.player` | Playback is unavailable right now. Try again in a few minutes. | التشغيل غير متاح الآن. حاول مجددًا بعد دقائق. |

`{home}` is the home's name; in generic text the home is «الجهاز الرئيسي».

## Acceptance criteria

- **Given** streaming is down,
  **when** a viewer opens any app,
  **then** within 15 s of the crash the strip says "Playback and Live TV are unavailable right now", and pressing play shows the same sentence instead of a generic error.
- **Given** the API is down,
  **when** the web app loads,
  **then** the shell renders, the strip says Maktaba is restarting, and an admin sees "Details" linking to `/recovery`.
- **Given** the home is fine and the relay is under maintenance within 24 h,
  **when** the app reaches the home through the cloud,
  **then** the strip shows 50.11's maintenance title; on the LAN it shows nothing.
- **Given** a pipeline worker is down,
  **then** viewers see nothing and admins see "Processing is paused".
- **Given** an admin turned Live TV off (`PUT /api/components/livetv` with `want = installed`),
  **when** a viewer opens any app,
  **then** no strip shows (Live TV is hidden by 51.11), and a `409 component_off` from a TV route adds no strip line.
- **Given** the strip is closed,
  **when** the state changes,
  **then** it shows again.
- **Given** Arabic,
  **then** every strip text is Arabic and laid out right to left.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (web, Swift, Kotlin ×2) | fixture documents for every state | render | the priority table |
| T02 | unit | feed 404 / timeout | start | no strip, no error |
| T03 | unit | viewer vs admin | admin-only key | shown only to admins |
| T03b | unit | a `/status.json` with `off` groups; a capabilities answer with `live_tv: off`; a `409 component_off` | render | no strip; `status.streamingNoTv` when streaming is down and TV is off |
| T04 | unit | strings | en/ar parity checks on each platform | pass |
| T05 | UI (**CI only**: Apple and Android UI shards, web Playwright) | mock home serving each `/status.json` | open the app | the strip; RTL screenshot in Arabic (dark theme) |
| T06 | **CI only** (53.07) | real faults | | strip within 15 s |

## Edge cases

- **An old home without `/status.json`:** 404 → no strip (apps must work with older homes).
- **Flapping state:** 53.03 already needs two checks; the strip also waits 5 s before hiding.
- **A Remote app watching another home:** each home's own status; the strip names the home.

## Build plan

### Files to touch

See §3, plus `web/src/i18n/en.json` / `ar.json`, `apps/apple/MaktabaKit/Sources/MaktabaKit/Localizable.xcstrings`,
`apps/android/core/src/main/res/values/strings.xml` / `values-ar/strings.xml`, the TV app's
`res/values*/strings.xml`, and `web/cloud/src/i18n/*.json`.

### Tests

T01–T04 in each app's unit job; T05 in the CI UI jobs only (project rule: no local Mac or
simulator UI runs); T06 in the fault rehearsal.

### Rollout

Ships in one release across apps; with no faults it shows nothing.

### Rollback

Each app falls back to the old offline banner by reverting its file.

### Dependencies

53.03. Needed by 53.07 and by Epic 50.11 (whose banner is this strip).

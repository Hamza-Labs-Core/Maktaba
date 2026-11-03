# Story 53.14 — Crash reports from every app

> Epic 53 · Phase 3 · Depends on: 53.01, 53.10 · Needed by: 53.15

## Why

The Apple apps keep the last five MetricKit crashes on the device (#382); the Android phone apps
write `last-crash.txt` (#380); the Android TV app and the Apple TV app keep nothing; the web's
uncaught errors reach the home only if the user opted in to usage data; the desktop shell writes
panics to `app.log`. None of it reaches the home's admin. The audit's open follow-up is "a home
endpoint for client crash and log reports".

## Description

### 1. The endpoint at the home

`POST /api/diagnostics/envelope`, `Content-Type: application/x-sentry-envelope` (README D5), any
signed-in device (viewer or admin; the device's own token):

- Items accepted: `event` (a crash or uncaught error, 53.01 fields inside), `attachment`
  (the app's log ring, ≤ 512 KB), `session` is ignored, unknown types skipped (as the format
  requires).
- Limits: ≤ 1 MB per envelope, ≤ 50 per device per hour (429 with `Retry-After`).
- The API scrubs again (53.01 rules), tags `user_h`, `install_id`, `app`, `app_version`,
  `platform`, `device_model`, stores the envelope as a file under `<data>/logs/crashes/<yyyymmdd>/`
  and writes one `fatal` line (`msg: app_crashed`, `service: <app>`) into the `api` segment, so
  the index groups it (53.10, `source: app`). The group page shows the crash's stack, the app log
  around it, and how many devices.
- Not signed in (a crash at the sign-in screen): kept on the device and sent after sign-in, only
  if the person then turns crash reports on (§3).
- The endpoint accepts envelopes only from a device that has turned crash reports on: each
  envelope carries `consent: crash_reports_to_home` in its header, and the API answers `403
  crash_reports_off` without it. A device whose switch is off sends nothing at all, so the check
  is a second guard, not the main one.

### 2. Each app

| App | Crash source | New |
|---|---|---|
| iPhone, iPad, Mac Player and Remote | MetricKit `MXCrashDiagnostic` (exists, `CrashReports.swift`) | Turn each kept payload into an envelope (call stack tree + exception + termination reason + the app log ring of that launch) and send on the next launch when signed in. |
| Apple TV | none today (no MetricKit on tvOS) | **PLCrashReporter** (MIT, tvOS 12+) via SwiftPM in the tvOS target only; same envelope. |
| Android Player and Remote | `CrashLog` (exists) | Envelope from `last-crash.txt` + the log ring; on API 30+ also read `ActivityManager.getHistoricalProcessExitReasons()` at start for `REASON_ANR`, `REASON_CRASH_NATIVE`, `REASON_LOW_MEMORY` since the last run (ANR trace included, ≤ 256 KB). |
| Android TV | none | Install `CrashLog` (the core one) in `MaktabaTVApp.kt`; same ANR read. |
| Web (and the phone shells) | `errorLog.ts` (exists) | Uncaught errors and unhandled rejections (≤ 1 per fingerprint per page load) sent as envelopes to the home **only when the signed-in user turned crash reports on** (§3). This switch is separate from the usage-data opt-in, which is about **Maktaba**, not the home. |
| Desktop shell | `applog.rs` panic hook (exists) | Envelope written to disk at panic; sent by the next start through the local server, only when crash reports are on. |

Symbols: Apple stacks are unsymbolicated on the device; the release workflow keeps each build's
dSYMs as a release asset, and `tools/apple/symbolicate.py` turns a crash's addresses into names
**at Maktaba** when a report is forwarded with consent (53.16). At home, the group shows the
binary images and offsets plus the app log, which is usually enough to tell what happened.

### 3. Consent and switches (decided 2026-11-03, README §12)

- **To the home: off by default.** Nothing is sent until the person says yes. The app asks once,
  during its **initial setup** (the onboarding steps after signing in to a home), on its own
  screen with two equal buttons, "Send crash reports" and "Not now"; neither is pre-selected.
  "Not now" leaves it off. Afterwards the choice lives in **Settings → Privacy → "Send crash
  reports to {home}"**, a switch that starts off. Apps already installed before this release
  show the same screen once, on the first launch after the update, with the same two buttons
  and the switch off until the person says yes.
- **Who decides:** each person on each device (the switch is stored on the device; the web's is
  stored per user at the home, `user_prefs.crash_reports_to_home`, and asked once in the web's
  first-sign-in setup). An admin can't turn it on for someone else's device.
- **When off:** nothing leaves the device. Crashes are still kept on the device as today (last
  5 on Apple, `last-crash.txt` on Android, the desktop's `app.log`), so a person who later sends
  a problem report (53.15) can choose to attach them, seeing them in its preview first.
- **To Maktaba:** never automatic. Anything that leaves the home stays opt-in: anonymous error
  groups follow the existing usage-data opt-in (Epic 16.7); full crash reports go only inside a
  problem report the user sends with the Maktaba box ticked (53.15, 53.16).
- **Store labels:** App Store privacy "Crash Data" and "Other Diagnostic Data", optional, not
  linked to identity at Maktaba, not used for tracking; Play Data safety "Crash logs" and
  "Diagnostics", collected, optional (off until the user opts in), not shared.
  `docs/app-privacy.md` (new: the App Store and Play answers, kept with the code) changes in
  the same PR.

### 4. Strings

| Key | English | Arabic |
|---|---|---|
| `crash.onboarding.title` | Help {home} fix problems? | هل تريد مساعدة {home} في إصلاح المشكلات؟ |
| `crash.onboarding.body` | If the app crashes, it can send a crash report to {home} so its admin can fix it. It contains no videos or messages. It is off unless you turn it on. | إذا توقف التطبيق فجأة، يمكنه إرسال تقرير عطل إلى {home} ليتمكّن المشرف من إصلاحه. لا يحتوي التقرير على فيديوهات أو رسائل. هذه الميزة متوقفة ما لم تشغّلها. |
| `crash.onboarding.yes` | Send crash reports | إرسال تقارير الأعطال |
| `crash.onboarding.no` | Not now | ليس الآن |
| `crash.setting` | Send crash reports to {home} | إرسال تقارير الأعطال إلى {home} |
| `crash.setting.help` | Off by default. When on, a report goes to {home} after a crash, never to Maktaba. | متوقف افتراضيًا. عند تشغيله، يُرسل تقرير إلى {home} بعد العطل، ولا يُرسل أبدًا إلى مكتبة. |
| `crash.lastCrash.sent` | The app closed unexpectedly last time. A report was sent to {home}. | أُغلق التطبيق بشكل غير متوقع في المرة السابقة. أُرسل تقرير إلى {home}. |
| `crash.lastCrash.kept` | The app closed unexpectedly last time. The crash is kept on this device; you can attach it to a problem report. | أُغلق التطبيق بشكل غير متوقع في المرة السابقة. حُفظ العطل على هذا الجهاز، ويمكنك إرفاقه ببلاغ عن مشكلة. |

## Acceptance criteria

- **Given** a fresh install,
  **when** the person finishes initial setup without tapping "Send crash reports",
  **then** the switch is off and a crash sends nothing.
- **Given** the person tapped "Send crash reports" during setup and the Android TV app crashes,
  **when** it next starts signed in,
  **then** the home has an `app_crashed` line with `service: android.tv`, the stack, and the app log, grouped by fingerprint.
- **Given** an Android phone app had an ANR (API 30+),
  **when** it starts,
  **then** an envelope with the ANR trace reaches the home.
- **Given** the Apple TV app crashes,
  **then** PLCrashReporter's report reaches the home as an envelope on the next launch.
- **Given** the switch is off,
  **then** nothing is sent; the crash stays on the device (last 5), and `crash.lastCrash.kept` is shown.
- **Given** an app updated from a release before this story,
  **when** it first launches,
  **then** it shows the crash-report question once and the switch stays off until the person says yes.
- **Given** an envelope without the consent header,
  **then** the API answers `403 crash_reports_off` and stores nothing.
- **Given** a device sends 60 envelopes in an hour,
  **then** the 51st gets 429 and the app keeps it for later (≤ 3 retries over 7 days).
- **Given** an envelope containing a bearer token in its log attachment,
  **then** the stored file has it redacted.
- **Given** a viewer's device,
  **then** it can send envelopes but can't read any crash (the Errors pages are admin-only).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (Go) | envelopes: valid, unknown item, oversize, bad header | POST | stored / skipped / 413 / 400 |
| T02 | unit (Go) | 51 in an hour | POST | 429 + `Retry-After` |
| T03 | role test | nobody, viewer, admin | POST envelope; GET crash | 401; 202 / 403; 202 / 200 |
| T04 | unit (Swift) | MetricKit payload fixture; PLCrashReporter fixture (tvOS) | build envelope | valid, scrubbed, ≤ 1 MB |
| T05 | unit (Kotlin) | `last-crash.txt`; fake `ApplicationExitInfo` list | build envelopes | valid, only exits since last run |
| T06 | unit (TS) | uncaught error twice | send | one envelope |
| T07 | unit (Rust) | panic | next start | envelope sent |
| T08 | unit | strings | en/ar parity | pass |
| T09 | UI (**CI only**) | test-only crash trigger in Debug builds; "Send crash reports" tapped in setup | crash, relaunch | home received it |
| T10 | unit (each platform) | fresh install, setup skipped; update from an old release | read the switch; crash | off; nothing sent; question shown once |
| T11 | unit (Go) | envelope without `consent` header | POST | 403 `crash_reports_off`, nothing stored |
| T12 | UI (**CI only**) | each app's onboarding, dark theme, en and ar | reach the crash step | two equal buttons, none pre-selected; RTL screenshot |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `api/internal/handlers/diagnostics/envelope.go` (new), `shared/envelope/go/` (new) | §1. |
| `apps/apple/MaktabaKit/Sources/MaktabaKit/CrashReports.swift`, `Envelope.swift`, `CrashSender.swift` (new) | Apple §2. |
| `apps/apple/project.yml` | PLCrashReporter for the tvOS target. |
| `apps/android/core/src/main/kotlin/com/hamzalabs/maktaba/core/diagnostics/` (`CrashSender.kt`, `ExitReasons.kt`, `Envelope.kt`) | Android §2. |
| `apps/tv/android/app/src/main/java/com/hamzalabs/maktaba/tv/MaktabaTVApp.kt` | Install `CrashLog`. |
| `web/src/lib/errorLog.ts`, `web/src/lib/envelope.ts` (new) | Web §2. |
| `apps/desktop/src-tauri/src/applog.rs` | Desktop §2. |
| `.github/workflows/release.yml` (Apple job) | Keep dSYMs as an asset. |
| `docs/app-privacy.md` (new) | §3. |
| Each app's first-run setup screens (Apple apps, Android phone and TV apps, Apple TV, web first-sign-in setup) and Settings → Privacy | §3 question and switch. |
| `api/internal/handlers/me/prefs.go`, migration adding `user_prefs.crash_reports_to_home boolean NOT NULL DEFAULT false` | The web's switch. |
| String files on each platform | §4. |

### Tests

T01–T08 in unit jobs; T09 in CI UI jobs only.

### Rollout

Endpoint first (accepts and stores); apps in the next release.

### Rollback

The default is already off. The feature flag `crash_reports_to_home` (existing flags table) hides
the question and the switch and stops sending; the endpoint can answer 202 and drop.

### Dependencies

53.01 (lines, rules), 53.10 (groups). Needed by 53.15.

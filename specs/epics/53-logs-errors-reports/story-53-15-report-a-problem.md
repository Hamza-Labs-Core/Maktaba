# Story 53.15 — Report a problem

> Epic 53 · Phase 3 · Depends on: 53.08, 53.14 · Needed by: 53.16

## Why

Today a household member who hits a problem can only tell the owner in person, with no details;
the owner then searches logs by guessing the time. Plex, Jellyfin and Home Assistant send people
to forums. Emby's apps can "Send Log" (on Android TV only with debug on). Maktaba can do better:
one button in every app that sends what happened, with the right logs attached, to the person who
can fix it: the home's admin.

## Description

### 1. Where the button is

| App | Where |
|---|---|
| Web (and phone shells) | Help menu and Profile → "Report a problem"; on every error screen a "Report this" link (pre-filled with that error's reference) |
| Desktop (Mac, Windows, Linux) | Help menu → "Report a problem…"; tray menu |
| Apple Player and Remote (iPhone, iPad, Mac) | Settings → Help → Report a problem; error screens' "Report this" |
| Apple TV and Android TV | Settings → Report a problem (remote-friendly, §3) |
| Android Player and Remote | Settings → Help → Report a problem; error screens |
| Recovery console | "Download diagnostics" (the admin's version; 53.04) |

### 2. The form

1. **What happened?** (text, optional, ≤ 2,000 characters) and **a category**: Playback, Live TV,
   Search, Signing in, Something else.
2. **When?** defaults to "just now"; or pick "earlier today" with a time (the report then takes
   logs around that time).
3. **What will be sent** (always shown, expanded by tapping):
   - the app's name, version, platform, OS version, device model;
   - the app's log for the last 30 minutes (≤ 2,000 lines, ≤ 512 KB), with a **preview** of the
     lines (scrollable, already redacted);
   - the last 20 request ids and their results;
   - recent crash reports of this app kept on the device, if any (a separate tick box, on, shown
     even when crash reports to the home are off: sending a report is itself the person's choice);
   - **a screenshot** of the screen before the form (owner's decision, 2026-11-03, README §12):
     **offered, off by default**. The "Include a screenshot" box starts unticked. Ticking it shows
     the screenshot itself in the preview, full width, before Send, with "Remove" next to it;
     unticking removes it from the payload. A screenshot is never sent unless it was shown in the
     preview first. TV apps offer it too: the preview shows it on screen and the remote's select
     toggles it.
4. **Send to:** {home}'s admin (always). And **"Also send to Maktaba so the developers can look"**:
   shown only when the home's admin turned on sharing with Maktaba (53.16); unticked by default;
   ticking shows one more sentence ("Maktaba gets the same report, without your name, video
   titles or file names, and keeps it for 90 days.").
5. **Send.** The app shows "Sent to {home}" with the report's number (e.g. #27).

Reports can't be sent anonymously to the home (the admin needs to reply); the admin sees the
sender's name.

### 3. TV apps

The form is a list of categories and a single "Describe it" field using the platform's dictation
or keyboard (optional). The preview is a summary ("Your TV app's log for the last 30 minutes,
1,240 lines") with "Show" to scroll it. The screenshot box sits below it, unticked; ticking
shows the screenshot full screen before Send.

### 4. At the home

`POST /api/reports` (any signed-in user), a multipart envelope (the app's part ≤ 512 KB + an
optional screenshot ≤ 1 MB, present only when the sender ticked the box), 10 a day per device.
The API rejects a screenshot part whose envelope header lacks `screenshot_previewed: true`
(400), so a client bug can't send one silently.

The API:

1. stores the report under `<data>/logs/reports/<id>/` and a row in `logs.db`
   (`reports(id, at, user_h, app, version, category, text, status, shared_with_maktaba)`);
2. **adds the home's side:** every server line with the report's request ids, plus `warn`+ lines
   of every component from 10 minutes before to 2 minutes after the reported time, plus
   `/status.json` at that time (from events), plus the matching error groups: ≤ 5 MB compressed;
3. notifies the admins ("New problem report from {name}: {category}");
4. logs `problem_reported` with the report id.

### 5. The admin's inbox (`/admin/reports`)

- List: number, who, when, category, app, status (New, Looking, Fixed, Closed), whether shared
  with Maktaba.
- A report: the text, the device, the timeline (the app's lines and the server's lines merged by
  time, request ids linked to "Follow a request"), the error groups, crash reports, the
  screenshot; actions: set status, **write back** (a short message the sender sees in the app's
  "My reports"), **download** (the bundle), **send to Maktaba** (53.16, if the sender ticked the
  box or the admin decides to, with the admin's own consent preview).
- Viewers see **My reports** (theirs only): status and the admin's reply. Role tests: a viewer
  can't read another person's report, or the server part of their own.

### 6. Retention and deletion

Reports are kept **90 days** by default (owner's decision, 2026-11-03; README §4.2), then deleted
with their files. The admin can change it (Admin → Logs → Retention, 53.11) and reports count
toward the home's 1 GB diagnostics cap (reports' share ≤ 100 MB; over it, the oldest closed
reports go first, then the oldest). Deleting a user deletes
their reports. The sender can delete their report from My reports at any time.

### 7. Strings

| Key | English | Arabic |
|---|---|---|
| `report.title` | Report a problem | الإبلاغ عن مشكلة |
| `report.what` | What happened? | ماذا حدث؟ |
| `report.category.playback` | Playback | التشغيل |
| `report.category.live` | Live TV | البث المباشر |
| `report.category.search` | Search | البحث |
| `report.category.signin` | Signing in | تسجيل الدخول |
| `report.category.other` | Something else | شيء آخر |
| `report.when.now` | Just now | الآن |
| `report.when.earlier` | Earlier today | في وقت سابق اليوم |
| `report.sent.header` | What will be sent | ما الذي سيُرسل |
| `report.sent.log` | This app's log for the last 30 minutes ({count} lines) | سجل هذا التطبيق لآخر 30 دقيقة ({count} سطرًا) |
| `report.sent.device` | App version, device model and system version | إصدار التطبيق وطراز الجهاز وإصدار النظام |
| `report.screenshot` | Include a screenshot | إرفاق لقطة شاشة |
| `report.screenshot.help` | Off unless you tick it. You'll see the screenshot here before sending. | متوقف ما لم تحدده. سترى لقطة الشاشة هنا قبل الإرسال. |
| `report.screenshot.remove` | Remove screenshot | إزالة لقطة الشاشة |
| `report.crashes` | Include recent crashes of this app ({count}) | إرفاق الأعطال الأخيرة لهذا التطبيق ({count}) |
| `report.to.home` | Send to the admin of {home} | الإرسال إلى مشرف {home} |
| `report.to.maktaba` | Also send to Maktaba so the developers can look | الإرسال إلى مكتبة أيضًا ليطّلع عليه المطوّرون |
| `report.to.maktaba.note` | Maktaba gets the same report, without your name, video titles or file names, and keeps it for 90 days. | تتلقى مكتبة التقرير نفسه، دون اسمك أو عناوين الفيديوهات أو أسماء الملفات، وتحتفظ به 90 يومًا. |
| `report.send` | Send | إرسال |
| `report.sentTo` | Sent to {home} (#{number}) | أُرسل إلى {home} (رقم {number}) |
| `report.thisError` | Report this | الإبلاغ عن هذا |
| `report.mine` | My reports | بلاغاتي |
| `report.status.new` | New | جديد |
| `report.status.looking` | Looking into it | قيد المتابعة |
| `report.status.fixed` | Fixed | تم الإصلاح |
| `report.status.closed` | Closed | مغلق |
| `report.notify` | New problem report from {name}: {category} | بلاغ جديد عن مشكلة من {name}: {category} |
| `report.limit` | You've sent many reports today. Try again tomorrow. | أرسلت بلاغات كثيرة اليوم. حاول مجددًا غدًا. |

`{home}` is the home's name; generic text uses «الجهاز الرئيسي».

## Acceptance criteria

- **Given** a viewer on the Android Player after a failed playback,
  **when** they tap "Report this" and send,
  **then** the admin gets a notification, and the report shows the player's lines and the server's lines for that request id on one timeline.
- **Given** the form,
  **then** the exact log lines to be sent can be read before sending, already redacted.
- **Given** the home hasn't turned on sharing with Maktaba,
  **then** the Maktaba box isn't shown.
- **Given** a report from a TV app,
  **then** it can be sent with only the remote (category + Send).
- **Given** viewer A,
  **when** they ask for viewer B's report, or the server part of their own,
  **then** 403.
- **Given** the admin writes back,
  **then** the sender sees the reply and status in My reports in their language.
- **Given** the form opens,
  **then** "Include a screenshot" is unticked and no screenshot is in the payload.
- **Given** the sender ticks "Include a screenshot",
  **then** the screenshot is shown in the preview before Send, and unticking or "Remove" drops it from the payload.
- **Given** a screenshot part without `screenshot_previewed: true`,
  **then** the API answers 400 and stores nothing.
- **Given** a report is 91 days old (default retention),
  **then** it and its files are gone.
- **Given** Arabic,
  **then** the form, preview header and statuses are Arabic and right to left; log lines stay left to right.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (Go) | report + fake index | build the home part | right lines, window, ≤ 5 MB |
| T02 | role test | nobody, viewer A/B, admin | every report route | as the criteria |
| T03 | unit | limits | 11th report in a day | 429 + `report.limit` |
| T04 | unit (web, Swift ×2 incl. tvOS, Kotlin ×2 incl. TV, Rust) | form | preview, send, Maktaba box rules | right payload |
| T05 | unit | retention, admin-changed retention, reports over 100 MB | sweep | deleted at 90 d / at the admin's value; oldest closed first over the cap |
| T08 | unit (each platform) | form | open; tick screenshot; untick | unticked at open; preview shows the image; payload has it only while ticked |
| T09 | unit (Go) | screenshot without the preview flag | POST | 400 |
| T06 | unit | strings | en/ar parity on each platform | pass |
| T07 | UI (**CI only**) | each app against a mock home | send a report | received; screenshots dark, en and ar |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `api/internal/handlers/reports/` (new) | §4, §5 API, roles, retention. |
| `cmd/maktaba-server/internal/logindex/reports.go` (new) | Table, the home part's query. |
| `web/src/components/ReportProblem/` (new), `web/src/pages/Admin/Reports.tsx` (new), `web/src/pages/Profile/MyReports.tsx` (new) | Web. |
| `apps/desktop/src-tauri/src/tray.rs`, `lib.rs` (menu) | Desktop entry (opens the web form). |
| `apps/apple/MaktabaKit/Sources/MaktabaKit/ReportProblem.swift` (new), Player/Remote/tvOS settings | Apple. |
| `apps/android/core/src/main/kotlin/com/hamzalabs/maktaba/core/ui/ReportProblem.kt` (new), TV app screen | Android. |
| `apps/apple/Remote/`, `apps/android/remote/` | Admin inbox on the Remote apps (list, read, status, reply). |
| String files on every platform | §7. |
| `web/src/help/content/{en,ar}/report-a-problem.md` | Help. |

### Tests

T01–T06 in unit jobs; T07 in CI UI jobs only.

### Rollout

API and admin inbox first; the button in each app in the next release.

### Rollback

Hide the button with the `problem_reports` flag; stored reports stay readable.

### Dependencies

53.08 (request ids), 53.14 (crash reports and the envelope). Needed by 53.16.

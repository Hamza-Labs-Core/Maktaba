# Story 53.16 — Reports and errors to Maktaba

> Epic 53 · Phase 4 · Depends on: 53.15, 53.17 · Needed by: —

## Why

A problem the home's admin can't fix (a bug in Maktaba) should reach the developers with the
evidence, not as a forum post. The cloud already receives **anonymous** error groups from homes
that opted in to usage data (Epic 16.7, `telemetry_error_groups`), but nothing with enough detail
to fix a bug, and nothing a person chose to send.

## Description

### 1. Two levels, two consents

| What | Leaves the home when | Identity at Maktaba | Kept |
|---|---|---|---|
| **Anonymous error groups** (fingerprint, title, counts, versions) | the home's usage-data opt-in is on (existing, unchanged) | none (random install id) | as today |
| **Problem reports** (the 53.15 report, the home's part, crash reports inside it) | the home's admin turned on **"Let people send problem reports to Maktaba"** **and** the report was sent with the Maktaba box ticked (by the sender, or by the admin with their own preview) | the linked home (`home_h` and the cloud account that linked it, so Maktaba can answer); **never** the sender's name, email or user id | **90 days**, then deleted |

Nothing here is automatic: anything that leaves the home stays opt-in (owner's decision,
2026-11-03, README §12). The admin setting lives in Settings → Privacy, off by default, and needs the home to be linked to
Maktaba Cloud (Epic 25). Turning it off stops new sends at once; already-sent reports stay until
their 90 days or until the admin asks to delete them ("Delete what Maktaba has").

### 2. What is removed before it leaves

The 53.01 export-only rules on every file of the report: media paths under a library root →
`<library>/…`, titles removed, `user_h` replaced by `user-1`, `user-2` per report, names and
emails removed from the text field (the sender sees this in the preview), the screenshot kept only
if the sender ticked it (off by default) and saw it in the preview (53.15); an admin forwarding
a report sees the screenshot in their own preview and can drop it. A test fixture of a full report checks no title, path, name or IP is
left.

### 3. Transport

Through the home's cloud link (the relay tunnel the home already holds, Epic 25 / Epic 50), as a
request to the cloud API `POST /v1/homes/{id}/reports` (multipart, ≤ 8 MB, authenticated as the
home). If the cloud is unreachable, the home keeps it in an outbox (the `telemetry_outbox`
pattern) and retries for 7 days with backoff.

### 4. In the cloud

- Storage: a row in `problem_reports` (cloud migration at the next free slot) and the files in
  the cloud's object storage (Epic 50.09's bucket, encrypted, under `reports/`), deleted at 90
  days by the worker.
- Each report's crash stacks are symbolicated with the release's dSYMs (53.14) and its error
  groups joined to the anonymous `telemetry_error_groups` by fingerprint (README D8), so a group
  page shows "3 reports" next to its anonymous counts.
- **Service admin view** (`app.maktaba.tv/admin/reports`, the Maktaba service admin only, not home
  admins): list across homes (home, app, version, category, status, age), a report page (the same
  merged timeline as the home's inbox), status (New, Triaged, Fixed in {version}, Closed), a
  link to a GitHub issue (pasted by hand), and **reply to the home** (the home's admin sees it in
  the report, and the sender in My reports). Filters: version, app, error group, category.
- The **Errors** view in the cloud admin (53.17) lists the anonymous groups and the cloud's own
  errors together, with "reports" counts.
- Role tests: a home admin's cloud token can't read any report (not even their own home's: they
  have them at home); only the service admin role can.

### 5. Strings

| Key | English | Arabic |
|---|---|---|
| `privacy.reportsToMaktaba` | Let people send problem reports to Maktaba | السماح بإرسال بلاغات المشكلات إلى مكتبة |
| `privacy.reportsToMaktaba.help` | Reports go to Maktaba only when the person sending one chooses it. Names, video titles and file names are removed. Maktaba keeps them for 90 days. | تُرسل البلاغات إلى مكتبة فقط عندما يختار المُرسِل ذلك. تُحذف الأسماء وعناوين الفيديوهات وأسماء الملفات. تحتفظ بها مكتبة 90 يومًا. |
| `privacy.deleteAtMaktaba` | Delete what Maktaba has | حذف ما لدى مكتبة |
| `report.maktabaReplied` | Maktaba replied | ردّت مكتبة |
| `report.sentToMaktaba` | Also sent to Maktaba | أُرسل إلى مكتبة أيضًا |

## Acceptance criteria

- **Given** the admin setting is off,
  **then** no app shows the Maktaba box and the API refuses a forward (409).
- **Given** a report sent with the box ticked,
  **when** it reaches the cloud,
  **then** it contains no title, library path, user name, email or IP (the fixture check), and the service admin can read it.
- **Given** the cloud is down,
  **then** the report waits in the outbox and arrives when the link is back (within 7 days).
- **Given** the service admin replies,
  **then** the home's admin and the sender see it in their language.
- **Given** 90 days,
  **then** the cloud row and files are gone.
- **Given** the admin clicks "Delete what Maktaba has",
  **then** every report of the home is deleted in the cloud within 24 h, and the admin sees "Deleted".
- **Given** a home admin's cloud token,
  **then** `GET /v1/admin/reports` answers 403.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (Go) | full report fixture with titles, paths, names, IPs | export scrub | none left |
| T02 | unit | setting off / on, box unticked / ticked | forward | 409 / sent only when both |
| T03 | integration | fake cloud down then up | send | outbox, then delivered once |
| T04 | role test (cloud) | home admin, service admin | admin routes | 403, 200 |
| T05 | unit (cloud) | retention, delete-home request | worker | gone |
| T06 | unit | strings en/ar | | pass |
| T07 | **CI only** (50.06's rehearsal with a canary home) | send through the relay | | arrives, symbolicated |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `api/internal/handlers/reports/forward.go` (new), `api/internal/usagedata/outbox` (reuse) | Forward, outbox, settings. |
| `shared/redact/rules.json` | Export-only rules (53.01) used here. |
| `cloud/migrations/<next>_problem_reports.sql` (new) | Table. |
| `cloud/internal/reports/` (new), `cloud/internal/handlers/admin/reports.go` (new) | §4. |
| `cloud/internal/telemetry/` | Join by fingerprint. |
| `web/cloud/src/pages/admin/Reports.tsx` (new) | Service admin view. |
| `web/src/pages/Settings/Privacy*` | The setting, delete button. |
| `tools/apple/symbolicate.py` (new) | dSYM symbolication. |
| String files | §5. |

### Tests

T01–T06 in unit jobs; T07 in the cloud rehearsal (Epic 50.06).

### Rollout

Cloud side first (accepts, stores, lists); then the home setting (off by default).

### Rollback

The cloud answers 503 to new reports; homes keep them in the outbox until it expires.

### Dependencies

53.15 (reports), 53.17 (the cloud's store, groups and admin Errors view).

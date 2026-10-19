# Cloud server status

- **Route:** `/cloud/status` (no params, no query state)
- **Component:** `src/pages/Cloud/ServerStatus.tsx`; children (same file): `ConnectedView`, helper `findCloudService()`; design system `Card`, `Badge`, `EmptyState`. Does not use `ClusterSignIn` or any cluster API.
- **API calls:** `GET /api/system/health` → `{ status, services: Record<string, { status: string, checks?: Record<string, { status, reason? }>, reason?: string }> }` (server `api/internal/system/aggregator.go`; `services` is always a map there); `GET /api/system/version` → `{ version, build_sha, ... }`. Both via `api` in `src/lib/api.ts`; both are on the server's public allowlist (no auth needed). Polls both every 30 s (`REFRESH_MS = 30_000`, `setInterval`, cleared on unmount). The "cloud" service is the first key in `services` matching `/cloud|relay|tunnel/i`.
- **Access:** signed-in user (`RequireAuth` + `AppShell`). No admin check. Sidebar link `nav.cloudStatus` ("Cloud status").
- **Existing tests:** none. Gaps: everything.

## Summary

| ID     | Title                                                          | Category      | Priority | Covered by existing test? |
| ------ | -------------------------------------------------------------- | ------------- | -------- | ------------------------- |
| CST-01 | No cloud service: "not configured" and setup steps             | Empty state   | P1       | No                        |
| CST-02 | Cloud relay connected                                          | Happy path    | P1       | No                        |
| CST-03 | Cloud relay down / degraded shows "Disconnected"               | Error         | P1       | No                        |
| CST-04 | Status words and case accepted as "up"                         | Happy path    | P2       | No                        |
| CST-05 | Heartbeat text sources                                         | Happy path    | P2       | No                        |
| CST-06 | Version missing or failing: Server ID falls back               | Error         | P2       | No                        |
| CST-07 | Health endpoint fails: "Down" card                             | Error         | P1       | No                        |
| CST-08 | Health 401/403 is reported as "Down"                           | Permissions   | P2       | No                        |
| CST-09 | Polling every 30 s; recovers after an error                    | Loading       | P1       | No                        |
| CST-10 | Link goes down while the page is open                          | Error         | P1       | No                        |
| CST-11 | Polling stops when leaving the page                            | Navigation    | P2       | No                        |
| CST-12 | `services: null` / missing `services`                          | Empty state   | P1       | No                        |
| CST-13 | Cloud service with null / missing `status`, `checks`, `reason` | Empty state   | P1       | No                        |
| CST-14 | Malformed or non-JSON health response                          | Error         | P1       | No                        |
| CST-15 | Service name matching picks the right entry                    | Happy path    | P2       | No                        |
| CST-16 | Loading state                                                  | Loading       | P3       | No                        |
| CST-17 | Normal user and admin see the same page; signed out redirects  | Permissions   | P1       | No                        |
| CST-18 | Crash during render shows a fallback; Back recovers            | Error         | P1       | No                        |
| CST-19 | Nav link, Back and Forward                                     | Navigation    | P2       | No                        |
| CST-20 | Arabic / RTL                                                   | RTL           | P2       | No                        |
| CST-21 | Light and dark theme                                           | Theme         | P3       | No                        |
| CST-22 | Phone width 375px                                              | Responsive    | P3       | No                        |
| CST-23 | Status changes are announced                                   | Accessibility | P3       | No                        |

## Cases

### CST-01 — No cloud service: "not configured" and setup steps

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** signed in; `GET /api/system/health` → `{ "status": "ok", "services": { "api": { "status": "ok" }, "streaming": { "status": "ok" } } }`; `GET /api/system/version` → `{ "version": "1.9.0", "build_sha": "abcdef0123456789" }`
- **Steps:**
  1. Open `/cloud/status`.
- **Expected:** Heading "Cloud server status" and muted "Auto-refreshes every 30s". EmptyState "Cloud relay not configured" with "This server isn't connected to a Maktaba Cloud relay yet." Card "Connect to Maktaba Cloud" with an ordered list of three steps (`cloud.status.setup.step1..3`). No "Connected" badge anywhere (no false green). Also `"services": {}` gives the same.

### CST-02 — Cloud relay connected

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** health → `{ "status": "ok", "services": { "api": { "status": "ok" }, "cloud-relay": { "status": "ok", "checks": { "heartbeat": { "status": "ok", "reason": "2s ago" } } } } }`; version as CST-01
- **Steps:**
  1. Open `/cloud/status`.
- **Expected:** Four cards: "Tunnel" with success badge "Connected"; "Server ID" `abcdef012345` (first 12 chars of `build_sha`, monospace); "Last heartbeat" `2s ago`; "Bandwidth" muted "Not reported". No EmptyState, no setup card.

### CST-03 — Cloud relay down / degraded shows "Disconnected"

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** health with `"cloud-relay": { "status": "down", "reason": "dial tcp: i/o timeout" }`; second run `"status": "degraded"`; third run `"status": "unknown"`
- **Steps:**
  1. Open `/cloud/status`.
- **Expected:** "Tunnel" card with error badge "Disconnected" in all three runs. "Last heartbeat" shows the reason `dial tcp: i/o timeout` (run 1) or "Not reported". Server ID still shown. Note: the heartbeat card shows the failure reason under the label "Last heartbeat", which reads oddly (minor UX).

### CST-04 — Status words and case accepted as "up"

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** cloud service `status` each of `ok`, `OK`, `up`, `Up`, `healthy`, `READY`
- **Steps:**
  1. Render the page for each value.
- **Expected:** "Connected" for all six. Anything else (e.g. `"starting"`) shows "Disconnected".

### CST-05 — Heartbeat text sources

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** cloud service variants: (a) `checks.heartbeat.reason = "5s ago"` and `reason = "x"`; (b) no `checks`, `reason = "last seen 10s ago"`; (c) `checks: { "heartbeat": { "status": "ok" } }` and no `reason`; (d) `reason: ""`
- **Steps:**
  1. Render the page for each.
- **Expected:** (a) `5s ago` (checks win); (b) `last seen 10s ago`; (c) "Not reported"; (d) "Not reported".

### CST-06 — Version missing or failing: Server ID falls back

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** connected health as CST-02; `GET /api/system/version`: (a) 500; (b) fetch rejects; (c) `{ "version": "1.9.0", "build_sha": "" }`; (d) `{ "version": "1.9.0" }` (no `build_sha`); (e) `{ "build_sha": null }`
- **Steps:**
  1. Open `/cloud/status`.
- **Expected:** No error shown for the version failure; "Server ID" falls back to the service name `cloud-relay` in all five. The tunnel card is unaffected. Note: the label says "Server ID" but the value is a build hash or a service name, not a server id (misleading label — minor).

### CST-07 — Health endpoint fails: "Down" card

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/system/health`: (a) 500 `{ "title": "Internal Server Error", "status": 500 }`; (b) fetch rejects; (c) 502 `text/html` from a proxy
- **Steps:**
  1. Open `/cloud/status`.
- **Expected:** One card "Tunnel" with error badge "Down" (`health.status.down`) and "Could not reach the server health endpoint." No setup steps, no "Connected". There is no Retry button — the next poll retries (assert that, or add a Retry; today none).

### CST-08 — Health 401/403 is reported as "Down"

- **Priority:** P2
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** auth probe OK; `GET /api/system/health` → 401 `{ "title": "Unauthorized", "status": 401 }` (or 403)
- **Steps:**
  1. Open `/cloud/status`.
- **Expected:** Today the same "Down / Could not reach…" card as CST-07, which wrongly suggests the server is down. Desired: a 401 sends the user to `/login`, a 403 shows a permission message (currently all errors look like an outage — likely bug).

### CST-09 — Polling every 30 s; recovers after an error

- **Priority:** P1
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** fake timers; first health call rejects; second returns connected (CST-02)
- **Steps:**
  1. Open `/cloud/status`; see the "Down" card.
  2. Advance timers by 30 000 ms and flush promises.
- **Expected:** Exactly one health and one version call at mount; one more of each at 30 s (none before). After the second call the "Down" card is replaced by the connected cards. No "Loading…" flash between polls.

### CST-10 — Link goes down while the page is open

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** fake timers; poll 1 → connected (CST-02); poll 2 → cloud service `"status": "down"`; poll 3 → health fetch rejects; poll 4 → cloud service missing from `services`
- **Steps:**
  1. Open the page; advance 30 s three times, checking after each.
- **Expected:** Poll 1 "Connected"; poll 2 "Disconnected"; poll 3 the "Down / Could not reach…" card (old data not shown as green); poll 4 the "not configured" empty state and setup steps. The page never goes blank.

### CST-11 — Polling stops when leaving the page

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** fake timers; health and version mocked
- **Steps:**
  1. Open `/cloud/status`; click "Billing & license".
  2. Advance timers by 90 s.
- **Expected:** No further calls to `/api/system/health` or `/api/system/version` after leaving (interval cleared). No React "state update on unmounted component" errors. A health response that arrives after leaving does not throw.

### CST-12 — `services: null` / missing `services`

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** health variants: (a) `{ "status": "ok", "services": null }`; (b) `{ "status": "ok" }`
- **Steps:**
  1. Open `/cloud/status`.
- **Expected:** "Cloud relay not configured" empty state with setup steps (or the "Down" card); no blank screen. Today `findCloudService()` calls `Object.entries(null/undefined)` during render and throws a TypeError, crashing the page (currently unhandled — likely bug, `ServerStatus.tsx` line ~39). This is the same bug class as the Live TV `hosts: null` crash.

### CST-13 — Cloud service with null / missing `status`, `checks`, `reason`

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** `services` contains `"cloud-relay"` set to: (a) `{ "status": null }`; (b) `{}`; (c) `{ "status": "ok", "checks": null }`; (d) `{ "status": "ok", "checks": { "heartbeat": null } }`; (e) `"cloud-relay": null`
- **Steps:**
  1. Open `/cloud/status`.
- **Expected:** No crash. (a)/(b)/(e): "Disconnected" badge, heartbeat "Not reported". (c)/(d): "Connected", heartbeat "Not reported". Today (a), (b) and (e) throw in `svc.status.toLowerCase()` (or reading `.status` of null) and crash the page (currently unhandled — likely bug, `ServerStatus.tsx` line ~120).

### CST-14 — Malformed or non-JSON health response

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/system/health`: (a) 200 `application/json` body `{bad`; (b) 200 `text/html` body `<!doctype html>…` (SPA fallback from a misrouted proxy); (c) 200 `application/json` body `null`
- **Steps:**
  1. Open `/cloud/status`.
- **Expected:** (a) "Down" card (JSON parse error is caught). (b) and (c) should also show the "Down" card or the empty state. Today (b) stores the HTML string as `health`, then `Object.entries(undefined)` throws and the page crashes; (c) leaves `health` null so the page sits on "Loading…" forever (both currently unhandled — likely bugs).

### CST-15 — Service name matching picks the right entry

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** services variants: (a) `{ "Cloud": {status:"ok"} }`; (b) `{ "relay": {status:"ok"} }`; (c) `{ "tunnel": {status:"down"} }`; (d) `{ "cloud_storage": { "status": "ok" }, "relay": { "status": "down" } }`; (e) `{ "cloudflare_stream": { "status": "ok" } }`
- **Steps:**
  1. Render each.
- **Expected:** (a)-(c) match case-insensitively. (d) and (e) show the real risk: the first key that merely contains "cloud" wins, so an unrelated service (storage, the Cloudflare Stream extension) is shown as the Maktaba relay, possibly a false "Connected" (likely bug; match an exact service name instead). Record actual output.

### CST-16 — Loading state

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** health pending
- **Steps:**
  1. Open `/cloud/status`.
- **Expected:** Heading, "Auto-refreshes every 30s" and "Loading…". No empty state or "Down" card yet.

### CST-17 — Normal user and admin see the same page; signed out redirects

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** (a) `is_admin: false`; (b) `is_admin: true`; (c) `GET /api/auth/me` → 401
- **Steps:**
  1. Open `/cloud/status` directly.
- **Expected:** (a) and (b) render identically; no `common.forbidden.*` state. (c) redirect to `/login` with `state.from = "/cloud/status"`; no health/version calls and no interval started.

### CST-18 — Crash during render shows a fallback; Back recovers

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** start on `/billing`; `GET /api/system/health` → `{ "status": "ok", "services": null }` (crashes today, CST-12)
- **Steps:**
  1. From `/billing` click "Cloud status".
  2. Press browser Back.
  3. Click "Cloud status" again, then "Account".
- **Expected:** Route-level error fallback, not a blank page; nav visible. Back renders `/billing` without a refresh. "Account" renders `/account`. The 30 s interval of the crashed page is cleared (no health calls after leaving).

### CST-19 — Nav link, Back and Forward

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** connected as CST-02
- **Steps:**
  1. From `/library` click "Cloud status"; press Back; press Forward.
- **Expected:** "Cloud status" link has `aria-current="page"`. Forward mounts the page again with one fresh health + version call and one new interval (not two).

### CST-20 — Arabic / RTL

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** No
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; run CST-01, CST-02 and CST-07 data
- **Steps:**
  1. Open `/cloud/status` for each.
- **Expected:** `<html dir="rtl" lang="ar">`. Heading "حالة خادم السحابة", "يُحدَّث تلقائيًا كل ٣٠ ثانية"; empty state "مُرحّل السحابة غير مُهيّأ" with setup card "الاتصال بـ Maktaba Cloud" and three Arabic steps (list numbers on the right); cards "النفق", "متصل"/"غير متصل", "معرّف الخادم", "آخر نبضة", "النطاق الترددي", "غير متوفر"; down card "متوقفة" and "تعذّر الوصول إلى نقطة فحص صحة الخادم.". The build hash stays left-to-right.

### CST-21 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `mkt:theme` light, then dark
- **Steps:**
  1. View connected, disconnected and not-configured states in each theme.
- **Expected:** Success and error badges, muted text and cards readable; tokens only.

### CST-22 — Phone width 375px

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** viewport 375x812; long heartbeat reason (150 chars)
- **Steps:**
  1. Reach the page via "More" → "Cloud status".
- **Expected:** Cards stack (`minmax(180px, 1fr)`); header text stacks under the title; long reason wraps; no horizontal page scroll.

### CST-23 — Status changes are announced

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** fake timers; poll 1 connected, poll 2 down
- **Steps:**
  1. Use a screen reader / check ARIA while the status flips.
- **Expected:** The tunnel status sits in a live region (`role="status"`) so a change from "Connected" to "Disconnected" is announced; the "Down" card should be `role="alert"`. Today none of these cards has a live region (currently unhandled — minor a11y gap). Card labels use `<strong>` only; headings or `dt/dd` would help navigation.

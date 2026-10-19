# Settings: About tab

- **Route:** `/settings/about` (the automatic updates card has `id="updates"`, so `/settings/about#updates` is a useful deep link)
- **Component:** `src/pages/Settings.tsx` → `AboutTab` and `pollVersionUntilChanged`; child `src/pages/Settings/UpdatesSettings.tsx` (admins only).
- **API calls:**
  - `GET /api/system/version` (public, no auth) → `{ version, commit?, build_date?, channel? }`. Errors swallowed; falls back to `import.meta.env.VITE_APP_VERSION ?? "dev"`.
  - `GET /api/system/updates` → `{ available, disabled?, current_version, latest_version?, channel, release_url?, release_notes?, assets?: [{ name, url, size }], checked_at }` (`api/internal/system/updater.go`). Errors swallowed.
  - "Check now": `GET /api/system/updates?refresh=true`; error → toast with `ApiError.message` or `common.error`.
  - "Update now" (admin): `POST /api/admin/system/update` `{ confirm: true }`; then polls `GET /api/system/version` every 2 s, up to 30 times, until the version differs. A 409 with `instructions` (or `detail`) shows the Docker hint instead of failing.
  - `UpdatesSettings` (admin, `src/lib/updatesApi.ts`): `GET /api/cluster/updates` → `UpdatesOverview { current_version, watermark, policy: { auto_install, channel, window, window_overrun_minutes, min_release_age_hours, pinned_version, deferred_until, skipped_versions: string[], node_failure_policy, keep_versions }, auto_install_available, plan: { target?, newest?, held, stepping_stone, reason?, auto_install, due, next_run_at?, eligible_at?, waiting? }, run?, catalog: CatalogRelease[], last_check_ok?, last_check_error?, check_stale, time_zone }`; save `PUT /api/cluster/updates/settings` with `{ auto_install, channel, window, min_release_age_hours, pinned_version }`. Any load error → the card renders nothing.
- **Access:** signed-in user (`RequireAuth`). Version, channel, last check and "Check now" for everyone. "Update now" button only when `user.is_admin && upd.available && phase !== "done" && !dockerHint`. `UpdatesSettings` only when `user.is_admin` (server also admin only).
- **Existing tests:** `src/pages/Settings/UpdatesSettings.test.tsx` — covers `validWindow`, `compareVersions`, `pinProblem`; card shows the next planned update; bad window and pin below installed block the save, good save sends the five keys; phase A note and stale alert; renders nothing when the API fails. Gaps: nothing tests `AboutTab` itself (version block, check now, update now, Docker hint, polling, admin/non-admin), and card cases for `plan: null`, save errors, `held`/`waiting` texts, age validation, Arabic.

## Summary

| ID     | Title                                                          | Category      | Priority | Covered by existing test?        |
| ------ | -------------------------------------------------------------- | ------------- | -------- | -------------------------------- |
| SAB-01 | Shows version, commit, build date, channel, last checked       | Happy path    | P1       | Yes (`Settings.test.tsx`)        |
| SAB-02 | Version API fails: build version fallback                      | Error         | P2       | Yes (`Settings.test.tsx`)        |
| SAB-03 | Missing / "unknown" optional version fields hidden             | Empty state   | P2       | Yes (`Settings.test.tsx`)        |
| SAB-04 | Up to date / checks disabled messages                          | Happy path    | P2       | Yes (`Settings.test.tsx`)        |
| SAB-05 | Update available card with notes and release link              | Happy path    | P1       | Yes (`Settings.test.tsx`)        |
| SAB-06 | Release notes render as plain text (no HTML injection)         | Validation    | P1       | Yes (`Settings.test.tsx`)        |
| SAB-07 | Check now refreshes; busy state; error toast                   | Error         | P2       | Yes (`Settings.test.tsx`)        |
| SAB-08 | Non-admin sees no Update now and no updates card               | Permissions   | P1       | Yes (`Settings.test.tsx`)        |
| SAB-09 | Admin Update now: downloading, restarting, done                | Happy path    | P1       | Yes (`Settings.test.tsx`)        |
| SAB-10 | Update now reports success even when the version never changes | Error         | P1       | Yes (`Settings.test.tsx`)        |
| SAB-11 | Update now fails (403 / 500 / network): no retry possible      | Error         | P1       | Yes (`Settings.test.tsx`)        |
| SAB-12 | Docker / package install: 409 shows the instruction            | Error         | P2       | Yes (`Settings.test.tsx`)        |
| SAB-13 | Leaving the tab during polling                                 | Navigation    | P2       | Yes (`Settings.test.tsx`)        |
| SAB-14 | Unknown channel / bad checked_at values                        | Empty state   | P3       | No                               |
| SAB-15 | Updates card: next update texts (at / held / waiting / none)   | Happy path    | P2       | Yes (`UpdatesSettings.test.tsx`) |
| SAB-16 | Updates card: validation of window, age, pin                   | Validation    | P1       | Yes (`UpdatesSettings.test.tsx`) |
| SAB-17 | Updates card: save success and error                           | Error         | P2       | Yes (`UpdatesSettings.test.tsx`) |
| SAB-18 | Updates card: `plan: null` / missing crashes the tab           | Empty state   | P1       | Yes (`UpdatesSettings.test.tsx`) |
| SAB-19 | Updates card: null lists and policy fields                     | Empty state   | P1       | Yes (`UpdatesSettings.test.tsx`) |
| SAB-20 | Updates card hidden when the API fails                         | Error         | P2       | Yes (`UpdatesSettings.test.tsx`) |
| SAB-21 | Signed-out deep link redirects to /login                       | Permissions   | P1       | Yes (`Settings.test.tsx`)        |
| SAB-22 | Render crash shows route error fallback; Back works            | Error         | P1       | Yes (`Settings.test.tsx`)        |
| SAB-23 | Arabic: RTL, Arabic strings, versions stay LTR                 | RTL           | P2       | Yes (`Settings.test.tsx`)        |
| SAB-24 | Phone width: meta list, notes and card fit                     | Responsive    | P3       | No (e2e-only)                    |
| SAB-25 | Labels, region and live announcements                          | Accessibility | P2       | Yes (`Settings.test.tsx`)        |
| SAB-26 | Both themes readable (success, error, pre blocks)              | Theme         | P3       | No                               |

## Cases

### SAB-01 — Shows version, commit, build date, channel, last checked

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** normal user. `GET /api/system/version` → `{ "version": "1.5.0", "commit": "abcdef1234567890", "build_date": "2026-10-01", "channel": "stable" }`; `GET /api/system/updates` → `{ "available": false, "current_version": "1.5.0", "channel": "stable", "checked_at": "2026-10-19T08:00:00Z" }`.
- **Steps:**
  1. Open `/settings/about`.
- **Expected:** Description list: "Current version" **1.5.0**; "Commit" `abcdef1234` (first 10 chars); "Build date" 2026-10-01; "Update channel" "Stable"; "Last checked" the local date string of `checked_at`. Button "Check now". Text "You're up to date."

### SAB-02 — Version API fails: build version fallback

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `GET /api/system/version` → 500 (and separately fetch rejects); `VITE_APP_VERSION` unset; updates → `{ "available": false, "channel": "beta", "current_version": "1.5.0", "checked_at": "..." }`.
- **Steps:**
  1. Open `/settings/about`.
- **Expected:** "Current version" shows "dev" (or the `VITE_APP_VERSION` value when set). No commit/build rows. Channel "Beta" (from the updates answer). No error shown, no crash. Both GETs fail → version "dev", channel "Stable", no "up to date" text, "Check now" still works.

### SAB-03 — Missing / "unknown" optional version fields hidden

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** version → `{ "version": "1.5.0", "commit": "unknown", "build_date": "unknown" }`; then `{ "version": "1.5.0", "commit": null, "build_date": "" }`.
- **Steps:**
  1. Open `/settings/about`.
- **Expected:** No "Commit" or "Build date" rows. Channel falls back to the updates channel or "Stable".

### SAB-04 — Up to date / checks disabled messages

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** updates → (a) `{ "available": false, "channel": "stable", ... }`; (b) `{ "available": false, "disabled": true, ... }`; (c) `{ "available": true, ... }` without `latest_version`.
- **Steps:**
  1. Open `/settings/about` for each.
- **Expected:** (a) "You're up to date." (b) "Automatic update checks are off." and no "up to date". (c) neither message and no update card (silent; wanted: still say an update exists — low priority).

### SAB-05 — Update available card with notes and release link

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** updates → `{ "available": true, "current_version": "1.5.0", "latest_version": "1.6.0", "channel": "stable", "release_url": "https://github.com/Hamza-Labs-Core/Maktaba/releases/tag/v1.6.0", "release_notes": "- faster\n- fixes", "checked_at": "2026-10-19T08:00:00Z" }`.
- **Steps:**
  1. Open `/settings/about`; expand the notes.
- **Expected:** `region` named "Updates" with `h2` "Update available: 1.6.0". A `details` whose summary is "View release" and body `<pre>` with the notes (line breaks kept). A link "View release" to the release URL with `target="_blank"` and `rel="noreferrer"`. Note: two controls share the text "View release" (summary and link) — confusing; wanted: summary "Release notes" (`update.banner.notes` exists).

### SAB-06 — Release notes render as plain text (no HTML injection)

- **Priority:** P1
- **Category:** Validation
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `release_notes: "<img src=x onerror=alert(1)><b>bold</b>"`; `release_url: "javascript:alert(1)"`.
- **Steps:**
  1. Open `/settings/about`.
- **Expected:** Notes appear as literal text; no `img`/`b` elements are created; no script runs. Wanted: the link only renders for `http(s)` URLs; **current** any `release_url` becomes an `href` (React warns but still sets `javascript:` URLs in React 18) (currently unhandled — likely bug, low risk since the value comes from the server's GitHub check).

### SAB-07 — Check now refreshes; busy state; error toast

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** first updates → up to date; `GET /api/system/updates?refresh=true` → (a) available 1.6.0; (b) 500 `{ "title": "Internal Server Error", "status": 500, "detail": "github rate limited" }`; (c) fetch rejects; (d) pending.
- **Steps:**
  1. Click "Check now".
- **Expected:** (d) button "Checking…" and disabled. (a) the update card appears; "Last checked" updates. (b) error toast "Internal Server Error: github rate limited"; old status kept. (c) toast "Something went wrong.". Button re-enabled after each.

### SAB-08 — Non-admin sees no Update now and no updates card

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `user.is_admin = false`; updates as SAB-05.
- **Steps:**
  1. Open `/settings/about`.
- **Expected:** Update card with notes and link, but no "Update now" button. No "Automatic updates" section; no `GET /api/cluster/updates` request. Admin with the same data sees "Update now" and the card.

### SAB-09 — Admin Update now: downloading, restarting, done

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** admin; fake timers; version `1.5.0`; updates as SAB-05. `POST /api/admin/system/update` resolves after a tick; polls of `/api/system/version`: reject (server restarting), then `{ "version": "1.5.0" }`, then `{ "version": "1.6.0" }`.
- **Steps:**
  1. Click "Update now".
  2. Advance timers 2 s three times.
- **Expected:** POST once with `{ "confirm": true }`. While posting: button disabled with "Downloading…" and muted "Downloading…". After POST: "Restarting…". After the third poll: success text "Updated. Now running 1.6.0."; "Current version" shows 1.6.0; the button is gone.

### SAB-10 — Update now reports success even when the version never changes

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** admin; fake timers. (a) every poll returns `{ "version": "1.5.0" }` (update rolled back) for 30 attempts; (b) version GET failed at load (so `from` is `""`) and the first poll answers the old `1.5.0`.
- **Steps:**
  1. Click "Update now"; advance 60 s.
- **Expected (wanted):** (a) "Update failed and was rolled back." (`update.rolledBack`, unused today); (b) keep polling until the version differs from the pre-update one. **Current:** both end in phase `done` showing "Updated. Now running 1.6.0." while 1.5.0 is running; the `rolled_back` phase is never set (currently unhandled — likely bug: false success).

### SAB-11 — Update now fails (403 / 500 / network): no retry possible

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** admin; `POST /api/admin/system/update` → 500 `{ "title": "Internal Server Error", "status": 500 }`; then 403; then fetch rejects.
- **Steps:**
  1. Click "Update now".
  2. Try to click it again.
- **Expected:** Error text "Update failed." (class `mkt-error`). Wanted: the server detail shown and the button usable again for a retry. **Current:** the button stays rendered but disabled with the label "Update failed." because `phase !== "idle"`; only a reload allows a retry (currently unhandled — likely bug).

### SAB-12 — Docker / package install: 409 shows the instruction

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** admin; POST → 409 `application/problem+json` `{ "title": "Conflict", "status": 409, "instructions": "docker compose pull && docker compose up -d" }`; second run 409 with only `detail: "apt upgrade maktaba"`; third run 409 with neither.
- **Steps:**
  1. Click "Update now".
- **Expected:** Run 1: "This is a Docker install. Run:" and a `<pre>` with the command; "Update now" button hidden; no error text. Run 2: same with the `detail`. Run 3: "Update failed." (falls through to error). No polling in any run.

### SAB-13 — Leaving the tab during polling

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** admin; fake timers; POST ok; polls keep returning the old version.
- **Steps:**
  1. Click "Update now"; after 4 s click "Account" in the Settings nav; advance 60 s.
- **Expected (wanted):** polling stops on unmount. **Current:** the loop keeps calling `GET /api/system/version` up to 30 times and then sets state on an unmounted component (no cancel) (currently unhandled — likely bug, low). Coming back to About shows a fresh state (no "Restarting…").

### SAB-14 — Unknown channel / bad checked_at values

- **Priority:** P3
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** version `{ "version": "1.5.0", "channel": "nightly" }`; updates `{ "available": false, "channel": "nightly", "checked_at": "not-a-date" }`; then `checked_at: "0001-01-01T00:00:00Z"` (Go zero time).
- **Steps:**
  1. Open `/settings/about`.
- **Expected (wanted):** channel shows "nightly" as is; last checked hidden or "never". **Current:** channel shows the raw key `update.channel.nightly`; last checked shows "Invalid Date", or a year-1 date for the zero time (likely bug, low).

### SAB-15 — Updates card: next update texts (at / held / waiting / none)

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`UpdatesSettings.test.tsx`)
- **Preconditions / data:** admin; `GET /api/cluster/updates` → the test's `overview()` with `plan` variants: (a) `target` 1.6.0 + `next_run_at`; (b) `held: true`; (c) no `next_run_at`, `waiting: "release_age"`; (d) `waiting` missing; (e) `waiting: "something_new"`; (f) no `target`.
- **Steps:**
  1. Open `/settings/about`; read `data-testid="updates-next"`.
- **Expected:** (a) "Version 1.6.0 installs on <local date>." (b) "Version 1.6.0 waits for your approval (Live TV -> Changes)." (c) "Version 1.6.0 installs once it is old enough." (d) "…automatic install is off." (e) wanted a generic text; **current** shows the raw key `updates.plan.waiting.something_new`. (f) "No update planned: you are up to date."

### SAB-16 — Updates card: validation of window, age, pin

- **Priority:** P1
- **Category:** Validation
- **Covered by existing test?** Yes (`UpdatesSettings.test.tsx`)
- **Preconditions / data:** admin; default overview (installed 1.5.0, `watermark` 1.5.0).
- **Steps:**
  1. Window: `03:00-03:30`, `24:00-01:00`, `3am`, `23:00 - 01:00`.
  2. Age: ``, `-1`, `1.5`, `abc`, `0`.
  3. Pin: `latest`, `1.4.9`, `v1.5.0`, `1.6.0-rc.1`, empty.
- **Expected:** Bad windows: "Write the window as HH:MM-HH:MM, longer than 30 minutes (23:00-01:00 crosses midnight)."; the padded one is valid and saved trimmed. Bad ages: "Write a whole number of hours, 0 or more."; `0` valid. Pins: "Write a version such as 1.5.9." / "A pin cannot be below the installed version 1.5.0." / valid / valid / valid (no pin). "Save update settings" disabled while any error shows; no PUT. Errors are linked to their inputs (`aria-invalid`, `aria-describedby`).

### SAB-17 — Updates card: save success and error

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`UpdatesSettings.test.tsx`)
- **Preconditions / data:** admin. PUT → (a) `{ "section": "updates", "changed": ["window"] }`; (b) 400 `{ "title": "Bad Request", "status": 400, "detail": "bad_window" }`; (c) 403; (d) fetch rejects; (e) pending.
- **Steps:**
  1. Change the release channel to "Beta" and toggle "Install updates automatically"; click "Save update settings".
- **Expected:** Body has `channel: "beta"`, `auto_install: false`. (e) button disabled while saving. (a) toast "Update settings saved." and the card reloads (`GET /api/cluster/updates` again). (b) toast "Bad Request: bad_window"; draft kept. (d) toast "Something went wrong.". Note: if the reload after a good save fails, the whole card disappears (load error sets `unavailable`) — wanted: keep the card.

### SAB-18 — Updates card: `plan: null` / missing crashes the tab

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`UpdatesSettings.test.tsx`)
- **Preconditions / data:** admin; `GET /api/cluster/updates` → `overview({ plan: null })`; then the same with `plan` key deleted.
- **Steps:**
  1. Open `/settings/about`.
- **Expected (wanted):** the card shows "No update planned…" and the form. **Current:** `ov.plan.target` throws during render, taking down the whole About tab (blank page) (currently unhandled — likely bug).

### SAB-19 — Updates card: null lists and policy fields

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`UpdatesSettings.test.tsx`)
- **Preconditions / data:** admin; overview variants, each alone: `catalog: null`; `catalog` missing; `policy.skipped_versions: null`; `policy.pinned_version: null`; `policy.window: null`; `policy: null`; `time_zone` missing. Also `GET /api/system/updates` with `assets: null`.
- **Steps:**
  1. Open `/settings/about`.
- **Expected:** No crash in any variant. `catalog`, `skipped_versions` and `assets` are not read by the UI today, so null is harmless (keep this case so a future reader of them is covered). `pinned_version: null` → empty pin field. `window: null` → the window error shows and Save is disabled (the regex coerces null to `"null"`); React warns about a null input `value` (wanted: empty field). `policy: null` → `draftOf` throws inside the load `try`, so the card is hidden (acceptable). Missing `time_zone` → label "Quiet window (undefined), HH:MM-HH:MM" (wanted: no "undefined").

### SAB-20 — Updates card hidden when the API fails

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes — `UpdatesSettings.test.tsx` "renders nothing without the updates API"
- **Preconditions / data:** admin; `GET /api/cluster/updates` → 404 / 403 / 500 / network.
- **Steps:**
  1. Open `/settings/about`.
- **Expected:** No "Automatic updates" section; the rest of the About tab works. (Wanted for 500: an error line rather than silently hiding — low.)

### SAB-21 — Signed-out deep link redirects to /login

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `GET /api/auth/me` → 401.
- **Steps:**
  1. Open `/settings/about`.
- **Expected:** Redirect to `/login`; no `/api/system/updates` or `/api/cluster/updates` request.

### SAB-22 — Render crash shows route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** full app with `AppShell`; admin; start at `/settings/models`. `GET /api/cluster/updates` → `overview({ plan: null })` (SAB-18). A second variant for non-admins: `GET /api/system/updates` → `{ "available": true, "latest_version": "1.6.0", "release_notes": { "md": "x" }, ... }` (an object as a React child throws).
- **Steps:**
  1. Click "About".
  2. Press browser Back; click a sidebar link.
- **Expected:** A route-level error fallback (not blank). Back shows Models without a reload; other pages render.

### SAB-23 — Arabic: RTL, Arabic strings, versions stay LTR

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** `mkt:locale=ar`; admin; data as SAB-05 plus the updates overview.
- **Steps:**
  1. Open `/settings/about`.
- **Expected:** `dir="rtl"`; "الإصدار الحالي", "قناة التحديث" "مستقر", "تحقق الآن", "يتوفر تحديث: 1.6.0", "حدّث الآن"; the automatic updates card title and labels in Arabic (all `update.*` / `updates.*` keys exist in `ar.json` today; assert no English label is left). Version numbers, commit hash and the Docker command render LTR and unbroken. Last-checked date uses the locale (wanted: `formatDate` from `useI18n`; **current** `toLocaleString()` uses the browser locale, not the app locale — likely bug, low).

### SAB-24 — Phone width: meta list, notes and card fit

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** e2e 375x812; long release notes with a 200-char line; Docker hint with a long command.
- **Steps:**
  1. Open `/settings/about`; expand notes.
- **Expected:** `<pre>` blocks wrap or scroll inside themselves; page has no horizontal scroll; inputs and buttons fit.

### SAB-25 — Labels, region and live announcements

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`Settings.test.tsx`)
- **Preconditions / data:** admin; update available.
- **Steps:**
  1. Run axe; click "Update now" and watch the DOM.
- **Expected:** Update card is `role="region"` named "Updates"; the automatic updates section is labelled by its `h2`. All card inputs have labels (switch, "Release channel" combobox, three textboxes). Wanted: phase texts ("Downloading…", "Restarting…", success, error) in a live region so screen readers hear progress; **current** plain `<p>`s (a11y gap). Stale-check message is `role="alert"`.

### SAB-26 — Both themes readable (success, error, pre blocks)

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** light and dark; force success, error and the Docker hint.
- **Steps:**
  1. Screenshot each state in each theme.
- **Expected:** `.mkt-success`, `.mkt-error`, muted text and `<pre>` backgrounds readable in both themes; no hardcoded colours.

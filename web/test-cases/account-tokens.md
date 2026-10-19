# API tokens

- **Route:** `/account/tokens` (no params, no query state)
- **Component:** `src/pages/Profile/APITokens.tsx`; children (same file): `CreateTokenModal`, `RevealTokenModal`, revoke confirm `Modal`; design system `Button`, `Badge`, `Modal`, `Input`, `Select`, `Checkbox`, `EmptyState`, `ErrorState`, `Toast`. Note: the page does NOT use `src/components/CopyField.tsx` or `copyText()` from `src/lib/clipboard.ts`; it calls `navigator.clipboard.writeText` directly.
- **API calls:** `GET /api/me/tokens` → `{ items: TokenView[] }`, `TokenView = { id, name, prefix, scopes: string[], last_used_at: string|null, expires_at: string|null, created_at, revoked_at: string|null }` (never the secret); `POST /api/me/tokens` body `{ name, scopes: string[], expires_in_days: 0|30|90|365 }` → 201 `{ token, pat: TokenView }` (raw token once); `DELETE /api/me/tokens/{id}` → 204 (soft revoke). Server: `api/internal/handlers/apitokens/apitokens.go`. Via `api` in `src/lib/api.ts`. List refetched after create and after revoke.
- **Access:** signed-in user (`RequireAuth` + `AppShell`); owner-scoped on the server. No admin check (the "Admin" scope checkbox is shown to everyone; server decides). Sidebar link `nav.tokens` ("API tokens").
- **Existing tests:** none for the page. `src/lib/clipboard.test.ts` covers `copyText()` only, which this page does not call. Gaps: everything.

## Summary

| ID     | Title                                                           | Category      | Priority | Covered by existing test? |
| ------ | --------------------------------------------------------------- | ------------- | -------- | ------------------------- |
| ATK-01 | Token table renders (active and revoked)                        | Happy path    | P1       | No                        |
| ATK-02 | Create token: request body and show-once reveal                 | Happy path    | P1       | No                        |
| ATK-03 | Secret is shown only once                                       | Permissions   | P1       | No                        |
| ATK-04 | Copy button on a secure origin                                  | Happy path    | P1       | No                        |
| ATK-05 | Copy on plain http LAN (no `navigator.clipboard`)               | Error         | P1       | No                        |
| ATK-06 | Copy rejected by the browser                                    | Error         | P2       | No                        |
| ATK-07 | Reveal dialog closes by Escape / X and loses the secret         | Accessibility | P2       | No                        |
| ATK-08 | Create name validation (empty, spaces, trimming)                | Validation    | P2       | No                        |
| ATK-09 | Scopes and expiry choices are sent correctly                    | Validation    | P2       | No                        |
| ATK-10 | Create fails: error toast, dialog stays with inputs             | Error         | P1       | No                        |
| ATK-11 | Create response without `token`                                 | Error         | P2       | No                        |
| ATK-12 | Create: double click / Enter sends one POST                     | Loading       | P2       | No                        |
| ATK-13 | Create dialog Cancel / Escape / backdrop                        | Accessibility | P3       | No                        |
| ATK-14 | Revoke through the confirm dialog                               | Happy path    | P1       | No                        |
| ATK-15 | Revoke fails / double click                                     | Error         | P2       | No                        |
| ATK-16 | `items: []` shows the empty state                               | Empty state   | P2       | No                        |
| ATK-17 | `items: null` / missing `items` shows the empty state           | Empty state   | P1       | No                        |
| ATK-18 | `scopes: null` / missing on a token                             | Empty state   | P1       | No                        |
| ATK-19 | Null dates, empty name, odd prefix                              | Empty state   | P2       | No                        |
| ATK-20 | Loading state                                                   | Loading       | P3       | No                        |
| ATK-21 | List load errors and Retry                                      | Error         | P2       | No                        |
| ATK-22 | 401 mid-session; signed-out deep link                           | Permissions   | P1       | No                        |
| ATK-23 | Token name with markup is shown as text                         | Permissions   | P2       | No                        |
| ATK-24 | Crash during render shows a fallback; Back recovers             | Error         | P1       | No                        |
| ATK-25 | Back while a dialog is open                                     | Navigation    | P2       | No                        |
| ATK-26 | Arabic / RTL (incl. identical "إلغاء" buttons in revoke dialog) | RTL           | P2       | No                        |
| ATK-27 | Light and dark theme                                            | Theme         | P3       | No                        |
| ATK-28 | Phone width 375px                                               | Responsive    | P3       | No                        |
| ATK-29 | Keyboard and screen reader basics                               | Accessibility | P2       | No                        |

## Cases

### ATK-01 — Token table renders (active and revoked)

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** signed in; `GET /api/me/tokens` → `{ "items": [ { "id": "t1", "name": "CLI laptop", "prefix": "ab12cd", "scopes": [], "last_used_at": null, "expires_at": null, "created_at": "2026-10-01T09:00:00Z", "revoked_at": null }, { "id": "t2", "name": "CI", "prefix": "ef34gh", "scopes": ["read", "write"], "last_used_at": "2026-10-18T07:00:00Z", "expires_at": "2027-01-01T00:00:00Z", "created_at": "2026-09-01T09:00:00Z", "revoked_at": null }, { "id": "t3", "name": "Old", "prefix": "zz99yy", "scopes": ["read"], "last_used_at": null, "expires_at": null, "created_at": "2026-01-01T09:00:00Z", "revoked_at": "2026-02-01T09:00:00Z" } ] }`
- **Steps:**
  1. Open `/account/tokens`.
- **Expected:** Heading "API tokens", "Create token" button in the header, description "Personal access tokens authenticate scripts and the CLI on your behalf." Table named "API tokens" with headers "Name", "Prefix", "Scopes", "Last used", "Expires", "Actions". t1: prefix `ab12cd…`, scopes muted "Full access" (`tokens.scopes.full`), last used "Never" (`common.never`), expires "Never" (`tokens.noExpiry`), "Revoke" button. t2: "read, write", formatted dates, "Revoke". t3: row has class `mkt-row--muted`, badge "Revoked" (`tokens.revokedBadge`), no "Revoke" button. No raw secret anywhere.

### ATK-02 — Create token: request body and show-once reveal

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** list as ATK-01; `POST /api/me/tokens` → 201 `{ "token": "mkt_pat_ab12cd_SECRETVALUE", "pat": { "id": "t4", "name": "Scripts", "prefix": "ab12cd", "scopes": ["read"], "last_used_at": null, "expires_at": "2026-11-18T09:00:00Z", "created_at": "2026-10-19T09:00:00Z", "revoked_at": null } }`; next `GET` includes t4
- **Steps:**
  1. Click "Create token".
  2. In dialog "Create API token", type "Scripts" in "Name", tick "Read", choose "30 days" in "Expiry".
  3. Click the dialog's "Create token".
- **Expected:** One `POST /api/me/tokens` with body `{ "name": "Scripts", "scopes": ["read"], "expires_in_days": 30 }` and the CSRF header. Toast "Token created." Create dialog closes; dialog "Copy your new token" opens with the alert (`role="alert"`) "This is the only time the token is shown. Copy it now and store it safely.", the full token in a `<code>` element, a "Copy" button and a "Close" button. List refetched and shows "Scripts" with prefix `ab12cd…` only.

### ATK-03 — Secret is shown only once

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** as ATK-02
- **Steps:**
  1. Create a token; click "Close" in the reveal dialog.
  2. Navigate to `/account` and back to `/account/tokens`; reload the page.
- **Expected:** After close, the raw token text `mkt_pat_ab12cd_SECRETVALUE` is not in the DOM, not in `localStorage`/`sessionStorage`, not in the URL. Only the prefix is shown. No later `GET` returns or shows it.

### ATK-04 — Copy button on a secure origin

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** as ATK-02; `navigator.clipboard.writeText` mocked to resolve; `window.isSecureContext = true`
- **Steps:**
  1. In the reveal dialog click "Copy".
- **Expected:** `writeText` called once with exactly `mkt_pat_ab12cd_SECRETVALUE`. Button text becomes "Copied!" (`tokens.copied`) and a toast "Copied!" appears. Dialog stays open.

### ATK-05 — Copy on plain http LAN (no `navigator.clipboard`)

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** as ATK-02; `navigator.clipboard` undefined and `window.isSecureContext = false` (Maktaba opened at `http://192.168.1.20:8780`); `document.execCommand` mocked to return `true`
- **Steps:**
  1. In the reveal dialog click "Copy".
- **Expected:** The token is copied through the selection fallback (`copyText()` in `src/lib/clipboard.ts` uses `execCommand("copy")`), button shows "Copied!". If even that fails, the token text stays selected so the user can press Ctrl+C. Today the page calls `navigator.clipboard.writeText` directly, which throws on http, so the user only sees "Couldn't copy to clipboard." and nothing is selected (currently unhandled — likely bug, `APITokens.tsx` `RevealTokenModal.copy`; should use `copyText` / `CopyField`).

### ATK-06 — Copy rejected by the browser

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** secure context; `navigator.clipboard.writeText` rejects `DOMException("NotAllowedError")`
- **Steps:**
  1. Click "Copy".
- **Expected:** Error toast "Couldn't copy to clipboard." (`tokens.copyFailed`); button stays "Copy"; token still visible and selectable; dialog stays open.

### ATK-07 — Reveal dialog closes by Escape / X and loses the secret

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** reveal dialog open as ATK-02
- **Steps:**
  1. Click the backdrop.
  2. Press Escape (fresh run: click the header close button `aria-label="Close"`).
- **Expected:** Backdrop click does not close it (`dismissable={false}`). Escape and the X do close it and the secret is gone for good. Since this is the only chance to see the token, closing without copying should at least be deliberate (e.g. Escape ignored, or a "Did you copy it?" check) (currently Escape closes silently — UX risk, decide).

### ATK-08 — Create name validation (empty, spaces, trimming)

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** No
- **Preconditions / data:** list loaded; `POST` mocked
- **Steps:**
  1. Open the create dialog; leave "Name" empty.
  2. Type three spaces.
  3. Type `  CLI  `, then submit.
- **Expected:** (1) and (2): the dialog's "Create token" is disabled; Enter in the name field sends no request. (3): enabled; today the untrimmed value `"  CLI  "` is sent. The server trims it (`strings.TrimSpace` in `apitokens.go`), so assert the list shows "CLI" after the refetch. Low risk; trimming on the client too would be cleaner.

### ATK-09 — Scopes and expiry choices are sent correctly

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** No
- **Preconditions / data:** `POST` mocked
- **Steps:**
  1. Create with no scope ticked and expiry left at default.
  2. Create ticking "Read", "Write", "Admin", then unticking "Write", expiry "365 days".
- **Expected:** Hint text "Leave all unchecked to inherit your full permissions." under "Scopes". Expiry options in order: "Never", "30 days", "90 days", "365 days"; default "Never". (1) body `{ "name": …, "scopes": [], "expires_in_days": 0 }`. (2) body `scopes: ["read", "admin"]`, `expires_in_days: 365`. Values are numbers, not strings.

### ATK-10 — Create fails: error toast, dialog stays with inputs

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** variants for `POST /api/me/tokens`: (a) 400 `{ "title": "Bad Request", "status": 400, "detail": "name required" }`; (b) 403 `{ "title": "Forbidden", "status": 403, "detail": "admin scope not allowed" }`; (c) 500 no detail; (d) fetch rejects.
- **Steps:**
  1. Fill the create dialog and submit.
- **Expected:** Error toast with (a) "name required", (b) "admin scope not allowed", (c) the problem title, (d) "Something went wrong.". The create dialog stays open with name, scopes and expiry unchanged; button not stuck busy. No reveal dialog; list not refetched.

### ATK-11 — Create response without `token`

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** `POST /api/me/tokens` → 201 `{ "pat": { … } }` (no `token`), or `{ "token": "" }`, or 204
- **Steps:**
  1. Create a token.
- **Expected:** The user must be told something went wrong (error toast, or a reveal dialog saying the secret is unavailable). Today the success toast "Token created." shows, the create dialog closes, and the reveal dialog never opens because `created` is falsy — the token exists but its secret is lost silently (currently unhandled — likely bug).

### ATK-12 — Create: double click / Enter sends one POST

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** `POST` resolves after a delay
- **Steps:**
  1. Fill the name, click "Create token" twice quickly.
  2. Fresh run: press Enter in "Name" twice quickly.
- **Expected:** One POST in each run. The footer button is `aria-busy` and disabled while pending. Today the form's `onSubmit` checks `valid` but not `busy`, so two quick Enters can send two POSTs and create two tokens (likely bug).

### ATK-13 — Create dialog Cancel / Escape / backdrop

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** list loaded
- **Steps:**
  1. Open the create dialog, type a name; click "Cancel".
  2. Reopen; press Escape.
  3. Reopen; click the backdrop.
- **Expected:** All three close without a request (backdrop closes because this dialog is dismissable). Reopening starts with an empty name, no scopes, "Never". Focus returns to the header "Create token" button.

### ATK-14 — Revoke through the confirm dialog

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** No
- **Preconditions / data:** list as ATK-01; `DELETE /api/me/tokens/t2` → 204; next `GET` returns t2 with `"revoked_at": "2026-10-19T09:00:00Z"`
- **Steps:**
  1. Click "Revoke" on "CI".
  2. Check the dialog; click "Cancel"; reopen and click the dialog's "Revoke".
- **Expected:** Dialog "Revoke token" with text Revoke “CI”? Any client using it will stop working. (`tokens.revoke.confirm` with `name`). Cancel sends nothing. Confirm sends one `DELETE /api/me/tokens/t2` (id URL-encoded). Toast "Token revoked."; dialog closes; after refetch "CI" is greyed with the "Revoked" badge and no "Revoke" button. Backdrop click does not close the dialog.

### ATK-15 — Revoke fails / double click

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** (a) `DELETE` → 404 `{ "title": "Not Found", "status": 404, "detail": "token not found" }`; (b) fetch rejects; (c) `DELETE` slow, for the double click
- **Steps:**
  1. Confirm a revoke for each variant; for (c) click the dialog's "Revoke" twice quickly.
- **Expected:** (a) toast "token not found", (b) "Something went wrong."; dialog stays open, row unchanged. (c) one DELETE only; today the dialog's "Revoke" has no busy state so two DELETEs go out (likely bug).

### ATK-16 — `items: []` shows the empty state

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/me/tokens` → `{ "items": [] }`
- **Steps:**
  1. Open `/account/tokens`.
- **Expected:** EmptyState "No API tokens" with "Create a token to access the API from scripts or the CLI."; the header "Create token" button is still there and works.

### ATK-17 — `items: null` / missing `items` shows the empty state

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** variants: (a) `{ "items": null }`; (b) `{}`; (c) 200 `text/html`; (d) 200 `application/json` body `null`.
- **Steps:**
  1. Open `/account/tokens`.
- **Expected:** (a)-(c): EmptyState "No API tokens", no crash. (d): ErrorState "Something went wrong." with Retry (the throw happens inside `.then` and is caught). Never blank.

### ATK-18 — `scopes: null` / missing on a token

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/me/tokens` → one token with `"scopes": null`, another with the `scopes` key missing (other fields as t1)
- **Steps:**
  1. Open `/account/tokens`.
- **Expected:** Both rows render with "Full access" in Scopes. Today `tok.scopes.length` throws a TypeError during render and the whole page crashes (currently unhandled — likely bug, `APITokens.tsx` line ~128; server guards against nil today, but the client should too).

### ATK-19 — Null dates, empty name, odd prefix

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** No
- **Preconditions / data:** tokens with (a) `"name": ""`; (b) `"prefix": ""` / `null`; (c) `last_used_at` and `expires_at` keys missing; (d) `"revoked_at"` key missing.
- **Steps:**
  1. Open the page; open the revoke dialog for (a).
- **Expected:** (a): row renders with an empty name; dialog text reads Revoke “”? (acceptable, but a fallback like the prefix would be clearer). (b): shows only "…". (c): "Never" in both columns (falsy check). (d): `revoked_at !== null` is true for `undefined`, so a missing key marks the token as revoked and hides "Revoke" (likely bug; should treat missing as active).

### ATK-20 — Loading state

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** `GET /api/me/tokens` pending
- **Steps:**
  1. Open the page.
- **Expected:** "Loading…" under the description; "Create token" is already usable.

### ATK-21 — List load errors and Retry

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** first `GET`: (a) 500 `{ "title": "Internal Server Error", "status": 500, "detail": "db down" }`; (b) 404 `{ "title": "Not Found", "status": 404 }`; (c) fetch rejects; (d) 200 `application/json` `{bad`. Second `GET` → list.
- **Steps:**
  1. Open the page; click "Try again".
- **Expected:** ErrorState with (a) "db down", (b) "Not Found", (c)/(d) "Something went wrong."; Retry renders the table.

### ATK-22 — 401 mid-session; signed-out deep link

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** (a) auth probe OK, `GET /api/me/tokens` → 401; (b) `GET /api/auth/me` → 401
- **Steps:**
  1. Open `/account/tokens` directly.
- **Expected:** (a) no blank page; today ErrorState "Unauthorized"; desired redirect to `/login` (currently no 401 redirect — likely bug). (b) redirect to `/login` with `state.from = "/account/tokens"`, no tokens request.

### ATK-23 — Token name with markup is shown as text

- **Priority:** P2
- **Category:** Permissions
- **Covered by existing test?** No
- **Preconditions / data:** token `"name": "<img src=x onerror=alert(1)>"`
- **Steps:**
  1. Open the page; open its revoke dialog.
- **Expected:** The name shows as literal text in the row and in the dialog; no `<img>` element is created; no script runs.

### ATK-24 — Crash during render shows a fallback; Back recovers

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** No
- **Preconditions / data:** start on `/account/sessions`; `GET /api/me/tokens` → `{ "items": [ { "id": "t1", "name": "x", "prefix": "p", "scopes": null, "last_used_at": null, "expires_at": null, "created_at": "2026-10-01T09:00:00Z", "revoked_at": null } ] }` (crashes today, see ATK-18)
- **Steps:**
  1. From `/account/sessions` click sidebar "API tokens".
  2. Press browser Back.
  3. Click "API tokens" again, then "Billing & license".
- **Expected:** Route-level error fallback, not a blank page; nav still visible. Back renders `/account/sessions` without a refresh. The nav link renders `/billing`.

### ATK-25 — Back while a dialog is open

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No
- **Preconditions / data:** list loaded
- **Steps:**
  1. Open the create dialog; press browser Back.
  2. Forward; create a token; with the reveal dialog open press Back.
- **Expected:** Leaving the page removes the dialog portal, unlocks body scroll and leaves no inert background behind (the next page is clickable). Coming back with Forward shows no dialog and no secret.

### ATK-26 — Arabic / RTL (incl. identical "إلغاء" buttons in revoke dialog)

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** No
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; list as ATK-01
- **Steps:**
  1. Open the page; open the create dialog; open a revoke dialog; create a token to see the reveal dialog.
- **Expected:** `<html dir="rtl" lang="ar">`. Heading "رموز الـ API", button "إنشاء رمز", headers "الاسم", "البادئة", "النطاقات", "آخر استخدام", "تنتهي". Create dialog "إنشاء رمز API" with "قراءة"/"كتابة"/"مسؤول", expiry "أبداً", "30 يوماً". Reveal "انسخ رمزك الجديد", "نسخ". Token and prefix (Latin) stay left-to-right inside the RTL layout. Bug to catch: in the revoke dialog both buttons read "إلغاء" (Cancel = `common.cancel`, Revoke = `tokens.revoke`), so the user cannot tell them apart — expect different labels (currently identical — i18n bug in `src/i18n/ar.json`).

### ATK-27 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `mkt:theme` light, then dark
- **Steps:**
  1. View the table (incl. a revoked row), create and reveal dialogs in each theme.
- **Expected:** Readable text; revoked row dimmed but legible; the warning `.mkt-alert` (error background, hardcoded `#fff` text in `src/styles/app.css`) has enough contrast in both themes.

### ATK-28 — Phone width 375px

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** viewport 375x812; token of 60+ chars
- **Steps:**
  1. Reach the page via "More" → "API tokens"; open create and reveal dialogs.
- **Expected:** No horizontal page scroll (table scrolls inside itself). Header stacks (title then button). Long token wraps (`word-break: break-all`) inside the dialog; Copy and Close buttons visible.

### ATK-29 — Keyboard and screen reader basics

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** No
- **Preconditions / data:** list loaded
- **Steps:**
  1. Keyboard only: open create, fill name, Space on checkboxes, choose expiry, submit, Tab to Copy, Enter, Tab to Close.
- **Expected:** Focus moves into each dialog and is trapped there; Tab order inside create: Name, Read, Write, Admin, Expiry, Cancel, Create token (plus the X). Checkboxes grouped in a `fieldset` with legend "Scopes". The reveal warning is announced (`role="alert"`). "Copied!" change is announced via the toast live region. Focus returns to "Create token" in the header after the reveal dialog closes.

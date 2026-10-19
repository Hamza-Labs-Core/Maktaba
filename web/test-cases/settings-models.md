# Settings: Models tab

- **Route:** `/settings/models`
- **Component:** `src/pages/Settings/ModelsSection.tsx` (`ModelsSection`, `ModelCard`); uses `@ds` `Badge`, `Button`, `Card`, `Input`, `ProgressBar`, `Select`.
- **API calls (direct `api.*` from `src/lib/api.ts`; server in `api/internal/handlers/models/models.go`):**
  - `GET /api/models` → a bare array `Model[]`, `Model = { id, type: "stt"|"embedding"|"diarization", name, size, platform, status: "active"|"downloaded"|"downloading"|"available", progress, active, last_test?: { ok, latency_ms, detail? } }`.
  - `POST /api/models/{id}/download` (202), `DELETE /api/models/{id}`, `PATCH /api/models/{id}/activate`, `POST /api/models/{id}/test`. After each, the list is reloaded.
  - Polling: while any model has `status: "downloading"`, `GET /api/models` every 600 ms; stops when none is downloading or on unmount.
  - OpenAI API key: only saved to `localStorage["mkt:openai_api_key"]`; never sent to the server.
- **Access:** signed-in user (`RequireAuth`). No admin gate in the UI and no admin check visible in the handler `Mount` (all users see Download / Delete / Activate). Server returns "pipeline offline" errors on mutations when no pipeline client is wired.
- **Existing tests:** none. Gaps: all below.

Errors are shown in a `<p role="alert">` above the controls: `ApiError.message` (`"<title>: <detail>"`) or "Could not load models." (`settings.models.loadError`). Use fake timers for polling cases.

## Summary

| ID     | Title                                                        | Category      | Priority | Covered by existing test?                                       |
| ------ | ------------------------------------------------------------ | ------------- | -------- | --------------------------------------------------------------- |
| SMD-01 | Groups models into STT / Embedding / Diarization cards       | Happy path    | P1       | Yes (`ModelsSection.test.tsx`)                                  |
| SMD-02 | Buttons per status (available / downloaded / active / cloud) | Happy path    | P1       | Yes (`ModelsSection.test.tsx`)                                  |
| SMD-03 | Download starts polling and shows progress                   | Happy path    | P1       | Yes (`ModelsSection.test.tsx`)                                  |
| SMD-04 | Polling stops when the download finishes and on unmount      | Loading       | P1       | Yes (`ModelsSection.test.tsx`)                                  |
| SMD-05 | Activate from card and from the backend select               | Happy path    | P1       | Yes (`ModelsSection.test.tsx`)                                  |
| SMD-06 | Backend select with no active STT model                      | Empty state   | P2       | Yes (`ModelsSection.test.tsx`; it.fails: no placeholder option) |
| SMD-07 | Delete a model                                               | Happy path    | P2       | Yes (`ModelsSection.test.tsx`)                                  |
| SMD-08 | Test a model shows pass / fail                               | Happy path    | P2       | Yes (`ModelsSection.test.tsx`)                                  |
| SMD-09 | Loading: no "empty" flash before the list arrives            | Loading       | P2       | No (it.fails: bug)                                              |
| SMD-10 | Empty list `[]`                                              | Empty state   | P2       | Yes (`ModelsSection.test.tsx`)                                  |
| SMD-11 | `GET /api/models` returns `null`: crash                      | Empty state   | P1       | Yes (`ModelsSection.test.tsx`)                                  |
| SMD-12 | Response is an object, not an array                          | Empty state   | P1       | Yes (`ModelsSection.test.tsx`)                                  |
| SMD-13 | Missing / null optional fields on a model                    | Empty state   | P2       | Yes (`ModelsSection.test.tsx`)                                  |
| SMD-14 | Load fails (401 / 403 / 500 / network / bad JSON)            | Error         | P2       | Yes (`ModelsSection.test.tsx`)                                  |
| SMD-15 | Action fails (pipeline offline 503, 404, network)            | Error         | P2       | Yes (`ModelsSection.test.tsx`)                                  |
| SMD-16 | Polling error does not loop forever silently                 | Error         | P2       | Yes (`ModelsSection.test.tsx`)                                  |
| SMD-17 | OpenAI API key save                                          | Validation    | P2       | Yes (`ModelsSection.test.tsx`)                                  |
| SMD-18 | Non-admin sees model mutations                               | Permissions   | P1       | Yes (ModelsSection.test.tsx)                                    |
| SMD-19 | Signed-out deep link redirects to /login                     | Permissions   | P1       | Yes (`ModelsSection.test.tsx`)                                  |
| SMD-20 | Render crash shows route error fallback; Back works          | Error         | P1       | Yes (`ModelsSection.test.tsx`)                                  |
| SMD-21 | Arabic: RTL and Arabic strings                               | RTL           | P2       | Yes (`ModelsSection.test.tsx`)                                  |
| SMD-22 | Phone width: cards stack, controls fit                       | Responsive    | P3       | No (e2e-only)                                                   |
| SMD-23 | Labels, progress bar and alerts are accessible               | Accessibility | P2       | Yes (`ModelsSection.test.tsx`)                                  |
| SMD-24 | Error colour and badges in both themes                       | Theme         | P3       | No                                                              |

## Cases

### SMD-01 — Groups models into STT / Embedding / Diarization cards

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`)
- **Preconditions / data:** `GET /api/models` → `[{ "id": "whisper-small", "type": "stt", "name": "Whisper small", "size": "466 MB", "platform": "cpu", "status": "active", "progress": 100, "active": true }, { "id": "whisper-large", "type": "stt", "name": "Whisper large", "size": "2.9 GB", "platform": "cuda", "status": "available", "progress": 0, "active": false }, { "id": "e5", "type": "embedding", "name": "E5 base", "size": "420 MB", "platform": "cpu", "status": "downloaded", "progress": 100, "active": false }]`.
- **Steps:**
  1. Open `/settings/models`.
- **Expected:** `h2` "Model Management". `h3`s "Speech-to-Text", "Embedding", "Diarization". STT has 2 cards, Embedding 1, Diarization shows "No models in this category." Each card: bold name, status badge ("Active", "Available", "Downloaded"), size text, platform badge.

### SMD-02 — Buttons per status (available / downloaded / active / cloud)

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`)
- **Preconditions / data:** SMD-01 data plus `{ "id": "openai", "type": "stt", "name": "OpenAI Whisper", "size": "", "platform": "cloud", "status": "downloaded", "progress": 0, "active": false }` and a `downloading` model.
- **Steps:**
  1. Open `/settings/models`; inspect each card.
- **Expected:** available (local): only "Download". downloaded (not active): "Activate", "Test", "Delete". active: "Test", "Delete" (no "Activate"). cloud downloaded: "Activate", "Test", no "Delete", no "Download", no size text. downloading: no buttons, a progress bar.

### SMD-03 — Download starts polling and shows progress

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`)
- **Preconditions / data:** fake timers. `POST /api/models/whisper-large/download` → 202. `GET /api/models` sequence for that model: `downloading` progress 10, then 55, then `downloaded` 100.
- **Steps:**
  1. Click "Download" on "Whisper large".
  2. Advance timers by 600 ms twice.
- **Expected:** POST once to `/api/models/whisper-large/download`; then GET reload. Card shows badge "Downloading" and a `progressbar` named "Downloading" with `aria-valuenow` 10, then 55. After the third GET the bar is gone, badge "Downloaded", "Activate"/"Test"/"Delete" appear.

### SMD-04 — Polling stops when the download finishes and on unmount

- **Priority:** P1
- **Category:** Loading
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`)
- **Preconditions / data:** fake timers; one model `downloading`.
- **Steps:**
  1. Open the tab; advance 600 ms (GET count +1).
  2. Make GET return `downloaded`; advance 3 s.
  3. In a second run, navigate to `/settings/about` while downloading; advance 3 s.
- **Expected:** After it settles no more `GET /api/models` calls. After leaving the tab no more calls and no React "state update on unmounted" warning.

### SMD-05 — Activate from card and from the backend select

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`)
- **Preconditions / data:** two downloaded STT models, `whisper-small` active, `whisper-medium` downloaded; `PATCH` → 200.
- **Steps:**
  1. Check the `combobox` "Active STT backend".
  2. Select "Whisper medium" in it.
  3. In a second run, click "Activate" on the "Whisper medium" card.
- **Expected:** Select options are only downloaded/active STT models; value `whisper-small`. Each action sends `PATCH /api/models/whisper-medium/activate` once, then reloads. After reload (server says medium active) the select shows "Whisper medium" and the card badge "Active".

### SMD-06 — Backend select with no active STT model

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`; it.fails: no placeholder option)
- **Preconditions / data:** (a) no STT models downloaded; (b) one downloaded STT model, none `active`.
- **Steps:**
  1. Open `/settings/models`; in (b), try to choose the only option.
- **Expected:** (a) select is empty and does not crash. (b) Wanted: a placeholder option ("None") so nothing looks active and the only model can be chosen. **Current:** `value=""` matches no option, so the browser shows the first model as if it were active, and picking it fires no `change` (no PATCH) (currently unhandled — likely bug).

### SMD-07 — Delete a model

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`)
- **Preconditions / data:** `e5` downloaded; `DELETE /api/models/e5` → 204; reload returns it `available`.
- **Steps:**
  1. Click "Delete" on "E5 base".
- **Expected:** One `DELETE /api/models/e5`; card now "Available" with "Download". Wanted: a confirm step for deleting a multi-GB model; **current** deletes at once (note).

### SMD-08 — Test a model shows pass / fail

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`)
- **Preconditions / data:** `POST /api/models/whisper-small/test` → 200; reload returns `last_test: { "ok": true, "latency_ms": 412 }`; second run `last_test: { "ok": false, "latency_ms": 0, "detail": "CUDA missing" }`.
- **Steps:**
  1. Click "Test" on "Whisper small".
- **Expected:** Text "Test passed (412 ms)" (class `mkt-models__test-ok`), or "Test failed". Note: `detail` is never shown (wanted: show it).

### SMD-09 — Loading: no "empty" flash before the list arrives

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** No (it.fails: bug)
- **Preconditions / data:** `GET /api/models` pending.
- **Steps:**
  1. Open `/settings/models`.
- **Expected (wanted):** a loading indicator. **Current:** state starts as `[]`, so all three groups say "No models in this category." until the list arrives (currently unhandled — likely bug, misleading).

### SMD-10 — Empty list `[]`

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`)
- **Preconditions / data:** `GET /api/models` → `[]`.
- **Steps:**
  1. Open `/settings/models`.
- **Expected:** Three groups, each "No models in this category."; select has no options; API key field still usable; no polling.

### SMD-11 — `GET /api/models` returns `null`: crash

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`)
- **Preconditions / data:** `GET /api/models` → `null` (200, JSON).
- **Steps:**
  1. Open `/settings/models`.
- **Expected (wanted):** same as `[]` (SMD-10). **Current:** `setModels(null)`, then `models.some(...)` throws during render: blank page (currently unhandled — likely bug).

### SMD-12 — Response is an object, not an array

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`)
- **Preconditions / data:** `GET /api/models` → `{ "items": [ ... ] }` and, separately, `{ "models": null }`.
- **Steps:**
  1. Open `/settings/models`.
- **Expected (wanted):** empty state or an error message, no crash. **Current:** `models.some is not a function` during render (currently unhandled — likely bug). Field list for the null-list rule: the top-level array itself is the only list field.

### SMD-13 — Missing / null optional fields on a model

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`)
- **Preconditions / data:** `[{ "id": "x", "type": "stt", "name": "X", "status": "downloading" }, { "id": "y", "type": "diarization", "name": null, "size": null, "platform": null, "status": "weird", "progress": null, "active": false, "last_test": null }, { "id": "z", "type": "ocr", "name": "Z", "status": "available" }]`.
- **Steps:**
  1. Open `/settings/models`.
- **Expected:** No crash. X: progress bar with no value treated as 0. Y: card renders; unknown status should show a neutral badge (wanted) — **current** badge text is the raw key `settings.models.status.weird` and tone `undefined`. Z (unknown type) is not shown anywhere (note: silently dropped).

### SMD-14 — Load fails (401 / 403 / 500 / network / bad JSON)

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`)
- **Preconditions / data:** `GET /api/models` → 500 problem+json `{ "title": "Internal Server Error", "status": 500, "detail": "pipeline offline" }`; 403 `{ "title": "Forbidden", "status": 403 }`; 401; fetch rejects; 200 with body `not json` and `Content-Type: application/json`.
- **Steps:**
  1. Open `/settings/models`.
- **Expected:** `role="alert"` with "Internal Server Error: pipeline offline" / "Forbidden" / "Could not load models." (network and bad JSON, which are not `ApiError`). Groups show their empty text. No crash. Wanted: a retry button (none today).

### SMD-15 — Action fails (pipeline offline 503, 404, network)

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`)
- **Preconditions / data:** `POST /api/models/whisper-large/download` → 503 `{ "title": "Service Unavailable", "status": 503, "detail": "pipeline offline" }`; `DELETE` → 404; `PATCH` → network reject.
- **Steps:**
  1. Click "Download", "Delete", "Activate" in turn.
- **Expected:** Alert shows the API message (or "Could not load models." for network — note the wording says "load" for a failed action, a copy bug). The list is not reloaded after a failed action; cards keep their state. A later successful load clears the alert. Note: buttons are not disabled while an action runs, so double clicks send two requests (wanted: disable while pending).

### SMD-16 — Polling error does not loop forever silently

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`)
- **Preconditions / data:** fake timers; one model `downloading`; next GETs reject (500).
- **Steps:**
  1. Advance 6 s.
- **Expected:** Alert shows the error. Note: the model stays `downloading` in state, so polling keeps going every 600 ms while the server is down (10 calls in 6 s). Wanted: back off or stop after errors. Record the call count.

### SMD-17 — OpenAI API key save

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`)
- **Preconditions / data:** empty storage; second run with `mkt:openai_api_key = "sk-old"`.
- **Steps:**
  1. Type `sk-test-123` in "OpenAI API key"; click "Save".
  2. Reload the tab.
- **Expected:** Input is `type="password"`, `autocomplete="off"`, placeholder "sk-…". After Save, `localStorage["mkt:openai_api_key"] === "sk-test-123"`; after reload the field is prefilled. Wanted: feedback on save and the key used by the server; **current** no toast, no validation (empty or "abc" saved as is), and the key is never sent to any API (currently unhandled — likely bug: control has no effect, and a secret sits in plain localStorage).

### SMD-18 — Non-admin sees model mutations

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (ModelsSection.test.tsx; server gate in models_test.go)
- **Preconditions / data:** `user.is_admin = false`; SMD-01 data; mutation endpoints → 403 `{ "title": "Forbidden", "status": 403 }`.
- **Steps:**
  1. Open `/settings/models` as the normal user; click "Download".
- **Expected (wanted):** model management is admin-only: buttons hidden or disabled, or the tab wrapped in `AdminGate` ("Admins only"). **Current:** every user sees Download / Delete / Activate / Test; the server handler has no visible admin check (security: confirm on the server; currently unhandled — likely bug). With a 403 the alert says "Forbidden".

### SMD-19 — Signed-out deep link redirects to /login

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`)
- **Preconditions / data:** `GET /api/auth/me` → 401.
- **Steps:**
  1. Open `/settings/models`.
- **Expected:** Redirect to `/login`; no `/api/models` call.

### SMD-20 — Render crash shows route error fallback; Back works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`)
- **Preconditions / data:** full app with `AppShell`; start at `/settings`. `GET /api/models` → `null` (see SMD-11).
- **Steps:**
  1. Click "Models".
  2. Press browser Back; click "Rules" in the Settings nav; click a sidebar link.
- **Expected:** A route-level error fallback (not blank). Back shows the Account tab without a reload. Other links render. No polling timer left running.

### SMD-21 — Arabic: RTL and Arabic strings

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`)
- **Preconditions / data:** `mkt:locale=ar`; SMD-01 data.
- **Steps:**
  1. Open `/settings/models`.
- **Expected:** `dir="rtl"`; "إدارة النماذج", "تحويل الكلام إلى نص", "التضمين", "فصل المتحدثين"; badges "نشط", "متاح", "مُنزَّل"; buttons "تنزيل", "تفعيل", "اختبار", "حذف"; "نجح الاختبار (412 مللي ثانية)". Progress bar fills from the right. The API key input stays LTR-friendly.

### SMD-22 — Phone width: cards stack, controls fit

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** e2e 375x812.
- **Steps:**
  1. Open `/settings/models`.
- **Expected:** Cards in one column; select and API key row wrap; no horizontal page scroll.

### SMD-23 — Labels, progress bar and alerts are accessible

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`ModelsSection.test.tsx`)
- **Preconditions / data:** SMD-02 data.
- **Steps:**
  1. Run axe; query by role.
- **Expected:** `combobox` "Active STT backend" and textbox "OpenAI API key" are labelled. `progressbar` has `aria-valuemin/max/now`. Errors are `role="alert"`. Wanted: card buttons name their model (e.g. "Delete E5 base"); **current** several identical "Delete"/"Test" names on one page.

### SMD-24 — Error colour and badges in both themes

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** light and dark; force the load error.
- **Steps:**
  1. Check the alert text colour.
- **Expected:** Readable in both themes. Note: `.mkt-models__error` uses `var(--color-danger, #c0392b)` and `--color-danger` does not seem to be defined in the tokens, so the hardcoded `#c0392b` is used in dark mode too (check contrast; likely bug).

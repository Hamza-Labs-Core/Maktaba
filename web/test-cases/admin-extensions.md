# Admin: Extensions

- **Route:** `/admin/extensions`; deep links with a hash to one card, e.g. `/admin/extensions#dizquetv` (linked from the queue overview, see `QueueOverview.test.tsx`). Card section ids = extension `name`.
- **Component:** `src/pages/Admin/Extensions.tsx` (`AdminExtensions`, `ExtensionCard`); children: `src/pages/Admin/PlexSignin.tsx` (Plex card only), `src/pages/Admin/CloudflareRemote.tsx` (`CloudflareRemote` on the `cloudflare_stream` card, `RemoteTestLines` for any test result with `lines`), `src/pages/Admin/RemoteUsage.tsx` (inside CloudflareRemote). Uses `useAction` (`src/components/queue/useAction.ts`).
- **API calls:** `queueApi` in `src/lib/queueApi.ts`:
  - `GET /api/extensions` via `queueApi.extensions()` → `{ extensions: Extension[] }`, `Extension = { name, label, description, version, enabled, configured, settings_schema: [{key,label,type:"text"|"url"|"secret"|"number"|"boolean"|"select",required?,help?,options?:[{value,label}]}], settings: Record<string, string|number|boolean|null>, secrets_set: string[] }`
  - `PUT /api/extensions/{name}` via `saveExtension(name, {enabled}|{settings})` → `Extension`
  - `POST /api/extensions/{name}/test` via `testExtension(name)` → `{ ok, message, lines? }`
  - Plex: `POST /api/extensions/plex/signin` → `{id, code, auth_url}`; `GET /api/extensions/plex/signin/{id}` (polled every 2 s, 15 min timeout) → `{state:"waiting"|"done", servers?:[{name,uri}]}`; `POST /api/extensions/plex/signin/{id}/use` `{name}` → `Extension`
  - Cloudflare card: `GET /api/tv/remote` via `tvRemoteApi.status()` → `{ integration: RemoteIntegration }` (with `last_test.lines`), `PUT /api/tv/remote/enabled`, `DELETE /api/tv/remote/integration` → `{ deleted_inputs: string[], errors: string[] }`.
- **Access:** signed-in (RequireAuth). NOT wrapped in `AdminGate`. Server: `GET /api/extensions` is open to any signed-in user; PUT, test and Plex sign-in call `requireAdmin` (403 for non-admins) (`api/internal/handlers/queues/extensions.go`, `plexsignin.go`). Sidebar link only for admins.
- **Existing tests:**
  - `src/pages/Admin/Extensions.test.tsx` — covers: list with version/configured/schema form; secrets masked + write-only and only sent when typed; Save sends settings and shows "Settings saved."; toggle sends `{enabled:true}`; Test connection failure text; Plex sign-in: popup + poll + auto-pick one server, choose among several, 15-min timeout, stop polling on unmount.
  - `src/pages/queue/a11y.test.tsx` "extensions page has no violations" — axe on the happy path.
  - `src/components/AppShell.test.tsx` "links the Extensions page for admins".
  - `src/pages/Admin/CloudflareRemote.test.tsx` — Cloudflare card (token write-only, test lines, enable after green test, token problem, remove with confirm).
  - Gaps: load error, empty list, null fields, toggle/save/test errors, non-admin, number/select fields, hash deep link, Plex error paths, crash fallback, RTL/theme/responsive.

## Summary

| ID     | Title                                                     | Category      | Priority | Covered by existing test?         |
| ------ | --------------------------------------------------------- | ------------- | -------- | --------------------------------- |
| EXT-01 | Lists extension cards with status and generated form      | Happy path    | P1       | Yes (`Extensions.test.tsx`)       |
| EXT-02 | Secrets are masked and write-only                         | Happy path    | P1       | Yes (`Extensions.test.tsx`)       |
| EXT-03 | Loading state                                             | Loading       | P3       | No                                |
| EXT-04 | Load failure shows error banner                           | Error         | P2       | Yes (`Extensions.cases.test.tsx`) |
| EXT-05 | No extensions installed                                   | Empty state   | P2       | Yes (`Extensions.cases.test.tsx`) |
| EXT-06 | `GET /api/extensions` null/missing list fields            | Empty state   | P1       | Yes (`Extensions.cases.test.tsx`) |
| EXT-07 | Toggle enabled: success and failure                       | Happy path    | P1       | Yes (`Extensions.cases.test.tsx`) |
| EXT-08 | Toggling discards unsaved form edits                      | Validation    | P2       | Yes (`Extensions.cases.test.tsx`) |
| EXT-09 | Save failure shows error, keeps edits                     | Error         | P1       | Yes (`Extensions.cases.test.tsx`) |
| EXT-10 | Field types: number, select, boolean, url, help, required | Validation    | P2       | Yes (`Extensions.cases.test.tsx`) |
| EXT-11 | Test connection: ok, failed, request error, with lines    | Happy path    | P2       | Yes (`Extensions.cases.test.tsx`) |
| EXT-12 | Plex sign-in happy paths                                  | Happy path    | P1       | Yes (`Extensions.test.tsx`)       |
| EXT-13 | Plex sign-in error paths                                  | Error         | P2       | Yes (`Extensions.cases.test.tsx`) |
| EXT-14 | Cloudflare card null list fields                          | Empty state   | P1       | Yes (`Extensions.cases.test.tsx`) |
| EXT-15 | Non-admin user                                            | Permissions   | P1       | Yes (`Extensions.cases.test.tsx`) |
| EXT-16 | Signed-out user redirected to /login                      | Permissions   | P1       | Yes (`Extensions.cases.test.tsx`) |
| EXT-17 | Hash deep link scrolls to the card                        | Navigation    | P2       | Yes (`Extensions.cases.test.tsx`) |
| EXT-18 | "Back to the queues" link and browser Back                | Navigation    | P2       | Yes (`Extensions.cases.test.tsx`) |
| EXT-19 | Page crash shows route error fallback; Back works         | Navigation    | P1       | Yes (`Extensions.cases.test.tsx`) |
| EXT-20 | Phone width (375px)                                       | Responsive    | P3       | No                                |
| EXT-21 | Arabic / RTL                                              | RTL           | P3       | No                                |
| EXT-22 | Light and dark theme                                      | Theme         | P3       | No                                |
| EXT-23 | Keyboard, labels, live regions                            | Accessibility | P2       | Yes (`Extensions.cases.test.tsx`) |

## Cases

### EXT-01 — Lists extension cards with status and generated form

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `Extensions.test.tsx` "lists extensions with version, status and a form generated from the schema"
- **Preconditions / data:** admin; `GET /api/extensions` → `{ extensions: extensionsFixture }` (`src/test/fixtures/queues.ts`: plex enabled+configured, dizquetv disabled+not configured).
- **Steps:**
  1. Open `/admin/extensions`.
- **Expected:** h1 "Extensions", intro `queue.ext.intro`. One section per extension labelled by its h2 ("Plex", "dizqueTV"); "version 1.2.0"; "Configured" / "Not configured"; switch "Enabled" checked for Plex, unchecked for dizqueTV; "Server address" prefilled; "Also mirror channels" checkbox; buttons "Save settings" and "Test connection". Plex card also has "Sign in with Plex".

### EXT-02 — Secrets are masked and write-only

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `Extensions.test.tsx` "shows secrets masked and write-only"
- **Preconditions / data:** fixture; PUT → fixture[0].
- **Steps:**
  1. Check the Token input; Save without typing; type "new-token" and Save.
- **Expected:** `type="password"`, empty value, placeholder "•••••••• Saved. Type to replace it." (or "Not set" when not in `secrets_set`). First PUT has no `token`; second includes `token:"new-token"`. "Settings saved." shown.

### EXT-03 — Loading state

- **Priority:** P3
- **Category:** Loading
- **Covered by existing test?** No
- **Preconditions / data:** GET never resolves.
- **Steps:**
  1. Open page.
- **Expected:** `role="status"` "Loading…"; no cards; header and "Back to the queues" link visible.

### EXT-04 — Load failure shows error banner

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Extensions.cases.test.tsx`)
- **Preconditions / data:** GET → (a) 500 problem; (b) 401; (c) fetch rejects; (d) 200 `application/json` body `{bad`.
- **Steps:**
  1. Open page.
- **Expected:** `role="alert"` "Could not load the extensions."; no endless "Loading…". Wanted: a "Try again" button (currently none — reload is the only way; minor gap). For 401 wanted: redirect to `/login`.

### EXT-05 — No extensions installed

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (`Extensions.cases.test.tsx`)
- **Preconditions / data:** GET → `{ "extensions": [] }`.
- **Steps:**
  1. Open page.
- **Expected:** "No extensions are installed." text; no cards.

### EXT-06 — `GET /api/extensions` null/missing list fields

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`Extensions.cases.test.tsx`)
- **Preconditions / data:** every list/array field read from this endpoint, run separately:
  - top-level `extensions: null`; `extensions` key missing (`{}`)
  - `extensions[0].settings_schema: null`; key missing
  - `extensions[0].secrets_set: null`; key missing (with a `secret` field in the schema)
  - `extensions[0].settings_schema[i].options: null` on a `select` field
  - `extensions[0].settings: null` (object, not list, but read with spread)
- **Steps:**
  1. Open page.
- **Expected:** `extensions` null/missing → "No extensions are installed." Currently it stays on "Loading…" forever because `list` is set to null (currently unhandled — likely bug). `settings_schema` null/missing → card renders with no form fields; currently `ext.settings_schema.map` throws and blanks the page (likely bug). `secrets_set` null/missing → secret placeholder "Not set"; currently `.includes` throws (likely bug). `options: null` → empty select (handled). `settings: null` → empty inputs (handled).

### EXT-07 — Toggle enabled: success and failure

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes (`Extensions.cases.test.tsx`)
- **Preconditions / data:** fixture. (a) PUT `/api/extensions/dizquetv` → `{...fixture[1], enabled:true}`; (b) PUT → 403 problem `{ "title":"forbidden","status":403,"detail":"admin only" }`; (c) PUT → 500 / fetch rejects.
- **Steps:**
  1. Click the dizqueTV "Enabled" switch.
- **Expected:** switch disabled while the request runs. (a) switch checked. (b)/(c) switch stays unchecked and the card shows `role="alert"` with the error ("forbidden: admin only" / "Failed to fetch"). Other cards are not affected.

### EXT-08 — Toggling discards unsaved form edits

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`Extensions.cases.test.tsx`)
- **Preconditions / data:** fixture; PUT `{enabled:false}` → fixture[0] with `enabled:false`.
- **Steps:**
  1. On Plex, change "Path map" to "/x=Y:/" and type a token; do NOT save.
  2. Toggle Plex "Enabled" off.
- **Expected:** wanted: typed values stay in the form. Currently `onSaved` replaces `ext`, and the `useEffect([ext])` resets `values` and `secrets`, so the edits are lost silently (currently unhandled — likely bug). Same after a Plex sign-in finishes.

### EXT-09 — Save failure shows error, keeps edits

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (`Extensions.cases.test.tsx`)
- **Preconditions / data:** PUT `/api/extensions/plex` → (a) 400 problem `{ "title":"bad request","status":400,"detail":"url: required" }`; (b) 403; (c) fetch rejects.
- **Steps:**
  1. Clear "Server address", Save.
- **Expected:** `role="alert"` in the Plex card with the error text (e.g. "bad request: url: required"); no "Settings saved."; field values kept; Save usable again. Pressing Enter in a text field also submits (form onSubmit).

### EXT-10 — Field types: number, select, boolean, url, help, required

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (`Extensions.cases.test.tsx`)
- **Preconditions / data:** one extension `{ name:"demo", settings_schema:[{key:"port",label:"Port",type:"number",required:true},{key:"mode",label:"Mode",type:"select",options:[{value:"a",label:"A"},{value:"b",label:"B"}]},{key:"on",label:"On",type:"boolean"},{key:"url",label:"URL",type:"url",help:"http://host:port"}], settings:{port:8080,mode:"b",on:true,url:"http://x"}, secrets_set:[] , ...}`; PUT echoes.
- **Steps:**
  1. Check initial values. 2. Change Port to 9090, Mode to A, uncheck On. Save. 3. Clear Port entirely, Save.
- **Expected:** Port input `type="number"` = 8080; Mode = B; On checked; URL `type="url"` with help text; Port label shows "(Required)". Save body `{ settings:{ port:9090, mode:"a", on:false, url:"http://x" } }` (port as a number). Step 3: wanted: empty number is sent as empty/null or blocked as required; currently `Number("")` sends `port: 0` (likely bug). Settings keys not in the schema are never sent.

### EXT-11 — Test connection: ok, failed, request error, with lines

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (`Extensions.cases.test.tsx`)
- **Preconditions / data:** POST `/api/extensions/plex/test` → (a) `{ ok:true, message:"Plex 1.40" }`; (b) `{ ok:false, message:"401 from Plex" }`; (c) 500 problem / fetch rejects; (d) `{ ok:true, message:"ok", lines:[{key:"token",ok:true,detail:"valid"}] }`; (e) `{ ok:true, message:"ok", lines:null }`.
- **Steps:**
  1. Click "Test connection".
- **Expected:** button loading while pending. (a) `role="status"` "Connection works: Plex 1.40"; (b) "Connection failed: 401 from Plex"; (c) `role="alert"` error, no status line; (d) a list named "Connection checks" with one line; (e) no list, no crash (guarded by `Array.isArray`).

### EXT-12 — Plex sign-in happy paths

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `Extensions.test.tsx` "opens plex.tv, polls, and auto-picks the only server", "lets the user choose when the account has several servers", "stops polling on unmount"
- **Preconditions / data:** see existing tests.
- **Steps:**
  1. Click "Sign in with Plex"; complete with one server; then with two.
- **Expected:** popup opened inside the click; "Waiting for you to sign in to Plex in the new window…" with app.plex.tv link; one server auto-used; several → radio group "Choose the Plex server to use" + "Use this server"; "Connected to Plex server {name}."; Server address updated.

### EXT-13 — Plex sign-in error paths

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (`Extensions.cases.test.tsx`)
- **Preconditions / data:** run each:
  - (a) `window.open` returns `null` (popup blocked)
  - (b) `POST /api/extensions/plex/signin` → 502 problem `{ "title":"bad gateway","status":502,"detail":"plex.tv unreachable" }`
  - (c) poll → 500 on 2nd call
  - (d) poll → `{ state:"done", servers: [] }`, and `{ state:"done", servers: null }`, and `{ state:"done" }` (missing)
  - (e) `.../use` → 409 problem
  - (f) click "Sign in with Plex" twice quickly
- **Expected:** (a) waiting text with the clickable app.plex.tv link (`queue.ext.plexPopup`); polling continues. (b) popup closed; `role="status"` error "bad gateway: plex.tv unreachable". (c) polling stops; error shown; button usable. (d) "Signed in, but this Plex account has no reachable servers." for all three (null list handled). (e) back to the server picker with the error shown. (f) only the latest sign-in keeps polling (no double polling of the first id).

### EXT-14 — Cloudflare card null list fields

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (`Extensions.cases.test.tsx`)
- **Preconditions / data:** extensions list includes `{ name:"cloudflare_stream", ... }`. Run separately:
  - `GET /api/tv/remote` → `integration.last_test.lines: null`; and `lines` missing
  - `DELETE /api/tv/remote/integration` → `{ deleted_inputs: null, errors: null }`; and `{}`
  - Test connection on this card → `{ ok:true, message:"ok", lines:null }`
- **Steps:**
  1. Open page; then click "Remove integration" and confirm.
- **Expected:** card shows the last test result with no lines list (not a blank page); remove shows the removed message with 0 inputs. Currently `RemoteTestLines` calls `result.lines.map` and remove reads `r.errors.length` / `r.deleted_inputs.length` without guards — would throw (currently unhandled — likely bug).

### EXT-15 — Non-admin user

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Extensions.cases.test.tsx`)
- **Preconditions / data:** `/api/auth/me` → `is_admin:false`; GET `/api/extensions` → fixture (server allows this read); PUT/test/signin → 403 problem `{ "title":"forbidden","status":403 }`.
- **Steps:**
  1. Open `/admin/extensions` directly.
- **Expected:** wanted: the `common.forbidden.*` ErrorState "Admins only" like the other admin pages, with no forms. Currently the page has no `AdminGate`: a non-admin sees every card with editable settings and Save/Test/Sign-in buttons that fail with 403 (currently unhandled — likely bug). Sidebar has no Extensions link for non-admins (AppShell).

### EXT-16 — Signed-out user redirected to /login

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (`Extensions.cases.test.tsx`)
- **Preconditions / data:** `/api/auth/me` → 401.
- **Steps:**
  1. Open `/admin/extensions#plex`.
- **Expected:** redirect to `/login` with `state.from === "/admin/extensions"`; no `/api/extensions` request. (Note: the hash is lost on return — acceptable or not, record it.)

### EXT-17 — Hash deep link scrolls to the card

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`Extensions.cases.test.tsx`)
- **Preconditions / data:** fixture; GET resolves after a delay (e.g. 300 ms).
- **Steps:**
  1. Open `/admin/extensions#dizquetv` (as linked from the queue overview). Also `#unknown`.
- **Expected:** after load the dizqueTV card is scrolled into view (and ideally focused/highlighted). Currently nothing scrolls once cards appear after the async load, since the browser only jumps to the hash before the element exists (currently unhandled — likely bug). `#unknown` → page renders normally at the top.

### EXT-18 — "Back to the queues" link and browser Back

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Yes (`Extensions.cases.test.tsx`)
- **Preconditions / data:** fixture.
- **Steps:**
  1. From `/queue`, click an extension badge link to `/admin/extensions#plex`.
  2. Click "Back to the queues". 3. Browser Back. 4. Browser Back again.
- **Expected:** link goes to `/queue`; Back returns to Extensions (refetches, renders cards); Back again to `/queue`. Plex polling from a started sign-in stops when leaving the page.

### EXT-19 — Page crash shows route error fallback; Back works

- **Priority:** P1
- **Category:** Navigation
- **Covered by existing test?** Yes (`Extensions.cases.test.tsx`)
- **Preconditions / data:** full app router; GET `/api/extensions` → `{ extensions:[{ ...fixture[0], settings_schema:null }] }` (throws today), or mock `PlexSignin` to throw.
- **Steps:**
  1. Start on `/queue`, click sidebar "Extensions".
  2. Press browser Back; then click another nav link.
- **Expected:** route-level error fallback in the content area, shell still visible. Back shows `/queue` without reload; other links work.

### EXT-20 — Phone width (375px)

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** 375x812; fixture plus a long URL value and long help text.
- **Steps:**
  1. Scroll through cards; open Plex server picker with two servers.
- **Expected:** no horizontal page scroll; inputs fit the width; Save/Test buttons wrap; header link reachable; nav reachable.

### EXT-21 — Arabic / RTL

- **Priority:** P3
- **Category:** RTL
- **Covered by existing test?** No
- **Preconditions / data:** locale `ar`.
- **Steps:**
  1. Open page; save; test; start Plex sign-in.
- **Expected:** `<html dir="rtl">`; h1 "الإضافات"; all `queue.ext.*` strings Arabic (no raw keys); labels and switches mirrored; URLs and versions stay LTR-readable; ✓/✕ markers on the correct side.

### EXT-22 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `data-theme` light / dark.
- **Steps:**
  1. View configured / not configured status, error banner, ok/bad test result.
- **Expected:** `mkt-q-status--ok/off/bad` and `mkt-q-banner` readable in both; colour is not the only signal (✓ / – / ✕ text present).

### EXT-23 — Keyboard, labels, live regions

- **Priority:** P2
- **Category:** Accessibility
- **Covered by existing test?** Yes (`Extensions.cases.test.tsx`)
- **Preconditions / data:** fixture.
- **Steps:**
  1. Tab through a card: switch, Sign in, fields, Save, Test. Toggle the switch with Space.
  2. Trigger save success, test failure, an error.
  3. Run axe with the error banner, test lines and Plex picker visible.
- **Expected:** every input labelled (`htmlFor` = `ext-{name}-{key}`); switch named "Enabled"; result messages in `role="status"`, errors in `role="alert"`; focus visible; Plex radios in a fieldset with legend. Axe: no violations in these states.

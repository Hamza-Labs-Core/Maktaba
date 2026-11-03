# Story 53.04 — The recovery console

> Epic 53 · Phase 1 · Depends on: 53.02, 53.03 · Needed by: 53.07

## Why

When the API or Postgres is down, the web app shows "Maktaba is restarting" and reloads every
5 seconds, for as long as it takes. If it never comes back, the admin has no way in except a
terminal, which the project rules out: recovery must be automatic or one click, never a
terminal. Home Assistant's recovery mode is the model: the UI, the logs and restore keep
working when the core can't start.

## Description

### 1. Where it lives

`/recovery` on the web port, served by the supervisor from a small page embedded in its binary
(server-rendered HTML with one small script, the shared page theme, dark by default), **not**
the SPA and **not** through the API. It loads in ≤ 1 s with every child down.

The existing "Maktaba is restarting" page (for `/api/` HTML requests while the API is down,
`cmd_serve.go:696`) gains a line under its spinner: what is down (from 53.03) and, after 60 s, a
**"Open the recovery page"** button for admins.

### 2. Sign-in (README D4)

Two ways, both without Postgres or the API:

1. **A recovery pass.** The web's sign-in is a server-side session (an opaque id in the
   `HttpOnly` session cookie, checked against Postgres), so it can't be checked with Postgres
   down. Instead, while the API is up, it gives every signed-in **admin** a recovery pass: a JWT
   signed with the API's own key (`aud: "maktaba-recovery"`, `sub`, `is_admin: true`, `jti`,
   **7 days**), set as a cookie `maktaba_recovery` with `Path=/recovery`, `HttpOnly`,
   `SameSite=Strict`, `Secure` when the home serves HTTPS. The SPA asks for it after sign-in and
   every 12 h (`POST /api/auth/recovery-pass`, admins only; viewers get 403 and no cookie).
   Sign-out clears it. The Remote apps (which hold JWT access tokens, `api/internal/auth/jwt`)
   can send their access token as `Authorization: Bearer` instead.
   The supervisor verifies either against the API's JWKS public keys, which it caches on disk
   (`<data>/state/jwks.json`, refreshed every 10 minutes while the API is up), as streaming
   already does (`streaming/internal/auth/jwks.go`): signature, audience, unexpired `exp`,
   `is_admin: true`. **Revocation without Postgres:** when an admin is demoted, deleted or signs
   out everywhere, the API appends their user id and the time to
   `<data>/state/recovery-revoked.json`, which the supervisor reads; any pass issued before that
   time for that user is refused.
2. **A one-time link from the Mac app.** The tray's "Open recovery page" asks the supervisor's
   loopback admin port (`127.0.0.1:9103`, already only reachable from the Mac) for a link with a
   random 32-byte code, valid **2 minutes**, single use, for this Mac's browser only.

A viewer's token, an expired token or a token from another home is refused (401), and the page
says how to get in (use the Mac, or wait for the home to come back and sign in). Five failures a
minute from one address lock it for 5 minutes.

Every action on the console is written to the supervisor event log and as a 53.01 line
`recovery_action` (who: `user_h` or `tray`; what), and copied into `audit_log` by the API when it
next starts.

### 3. What it shows

- The 53.03 components with their admin detail: state, restarts in the last hour, last exit code
  and time, crash loop, the last `error` line of each.
- **Logs:** the last 15 minutes of `warn`+ lines of every component (53.02's segment reader),
  filter by component and level, a text box that searches the loaded window, "load 15 more
  minutes". No index needed.
- **Disk:** free space of the data volume and the log segments' size.

### 4. What it can do (one click each, with a confirm)

| Action | Does | Available when |
|---|---|---|
| Restart a component | Supervisor stop + start of that role, resetting its crash-loop count | always |
| Restart everything | Supervisor restarts all roles in order | always |
| Start in safe mode | Writes the safe-mode flag and restarts (53.05) | always |
| Leave safe mode | Removes the flag and restarts | in safe mode |
| Roll back to the previous version | The launcher's existing rollback to the kept payload (`payload-override`), as the boot-loop rollback does today | a previous version is kept |
| Restore a backup | Opens the existing restore flow (`cmd_restore.go` via the supervisor's admin call), listing backups on the data disk | Postgres down or in safe mode |
| Download diagnostics | A `.tar.gz`: the last 2 h of segments of every component, `status.json`, supervisor events, system info, redacted (53.01); ≤ 50 MB | always |

No action deletes data. Restore asks twice and says what will be replaced, as the existing restore
does.

### 5. Strings

| Key | English | Arabic |
|---|---|---|
| `recovery.title` | Maktaba recovery | استعادة مكتبة |
| `recovery.intro` | Part of Maktaba isn't working. You can see what happened and fix it here. | جزء من مكتبة لا يعمل. يمكنك هنا معرفة ما حدث وإصلاحه. |
| `recovery.signin.mac` | Open this page from the Maktaba menu on the Mac that runs the home. | افتح هذه الصفحة من قائمة مكتبة على جهاز الماك الذي يشغّل الجهاز الرئيسي. |
| `recovery.signin.denied` | Only an admin of this home can open the recovery page. | يمكن لمشرفي هذا الجهاز الرئيسي فقط فتح صفحة الاستعادة. |
| `recovery.action.restart` | Restart | إعادة التشغيل |
| `recovery.action.restartAll` | Restart everything | إعادة تشغيل الكل |
| `recovery.action.safeMode` | Start in safe mode | التشغيل في الوضع الآمن |
| `recovery.action.leaveSafe` | Leave safe mode | الخروج من الوضع الآمن |
| `recovery.action.rollback` | Go back to version {version} | الرجوع إلى الإصدار {version} |
| `recovery.action.restore` | Restore a backup | استعادة نسخة احتياطية |
| `recovery.action.diagnostics` | Download diagnostics | تنزيل ملف التشخيص |
| `recovery.logs.title` | Recent problems | المشكلات الأخيرة |
| `restarting.openRecovery` | Open the recovery page | افتح صفحة الاستعادة |

The page follows the browser's language (Arabic right to left), like the server's other own
pages.

## Acceptance criteria

- **Given** Postgres is stopped and won't start,
  **when** an admin who was signed in opens `/recovery`,
  **then** the page loads, signs them in with their recovery pass, shows `database: down` with its last error line, and the last 15 minutes of logs.
- **Given** the API is crash-looping,
  **when** the admin clicks "Go back to version 0.3.44" and confirms,
  **then** the launcher rolls back as its boot-loop rollback does, and the console shows the result after the restart.
- **Given** a viewer's valid access token, or a pass issued before the admin was demoted,
  **when** it is sent to the console,
  **then** the answer is 401 and no component detail or log line is returned.
- **Given** the tray's "Open recovery page",
  **when** the link is opened twice,
  **then** the first works and the second is refused; after 2 minutes the first is refused too.
- **Given** any console action,
  **when** the API is back,
  **then** `audit_log` has a row for it.
- **Given** the browser asks for Arabic,
  **then** the page is in Arabic, right to left.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | JWKS cache + passes and Bearer tokens (admin, viewer, expired, wrong audience, other home, revoked after issue) | verify | only the valid, unrevoked admin passes |
| T01b | role test (API) | `POST /api/auth/recovery-pass` as nobody, viewer, admin | | 401, 403 (no cookie), 200 with the cookie |
| T02 | unit | one-time codes, fake clock | use twice; use after 2 min | single use; expiry |
| T03 | unit | 6 bad tries in a minute | try | locked 5 min |
| T04 | integration | supervisor, API stopped | GET `/recovery`, actions | page 200 in ≤ 1 s; restart works |
| T05 | role test (API side) | `audit_log` import | API start after actions | rows present, attributed |
| T06 | unit | strings | en/ar parity | every key has Arabic |
| T07 | **CI only** (53.07) | each fault scenario | sign in and read logs | works in every scenario |

## Edge cases

- **The JWKS cache is missing** (fresh install that never ran the API): only the tray link works;
  the page says so.
- **The pass expired while the home was down** (more than 7 days without the API): sign in from
  the Mac's tray. The page says so; passes are never extended without the API.
- **A stolen pass:** it opens only `/recovery`, for at most 7 days, is refused after a revocation,
  and every action is logged with the user; restore and rollback still ask to confirm twice.
- **Remote access through the relay while the API is down:** the relay reaches the web port, so
  the console works remotely with a valid pass or admin token. Actions over the relay ask for a second
  confirm.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/recovery/` (new): `handler.go`, `auth.go`, `jwks.go`, `actions.go`, `page.go`, `strings.go`, `assets/` | §1–§5. |
| `cmd/maktaba-server/cmd_serve.go` | Route `/recovery`; the restarting page's new line and button. |
| `cmd/maktaba-server/internal/supervisor/` | Restart one role / all on request; events. |
| `cmd/maktaba-launcher/` | Expose the existing rollback as an order the supervisor can give. |
| `api/internal/handlers/auth/recovery_pass.go` (new), `api/internal/auth/users/` (demotion and delete hooks) | Issue the pass; write `recovery-revoked.json`. |
| `web/src/lib/auth.ts` (or where the SPA signs in) | Ask for the pass after sign-in and every 12 h. |
| `apps/desktop/src-tauri/src/tray.rs`, `shell.rs` | "Open recovery page" (asks for the one-time link). |
| `api/internal/handlers/audit/` (or where `audit_log` is written) | Import supervisor console events at start. |
| `web/src/help/content/en/recovery.md`, `ar/recovery.md` (Epic 41) | Help page. |

### Tests

T01–T06 in unit jobs; T07 in the fault rehearsal.

### Rollout

Supervisor and launcher change: `update-rehearsal` on the exact commit. The tray item ships in the
same release as the console.

### Rollback

Revert; `/recovery` returns 404 and the restarting page loses its button.

### Dependencies

53.02 (segment reader), 53.03 (component states). Needed by 53.07.

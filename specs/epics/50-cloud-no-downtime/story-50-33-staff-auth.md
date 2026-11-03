# Story 50.33 — Staff sign-in for the operator console: MFA, short sessions, re-authentication

> Epic 50 · Phase 1 · Depends on: 50.32 (the admin host and listener) · Needed by: the launch
> gate (README §11), 50.24 and 50.25 (their admin actions use the re-authentication here)

## Why

Today a service admin is a **customer account** with `users.role = 'service_admin'`, granted
from the CLI and signed in with an email and password like any customer, with a 30-day refresh
session (`cloud/docs/accounts-billing-admin.md`). The docs list what is missing: no admin
step-up, no MFA. A customer account can also sign in with Google or Apple and reset its password
by email, so whoever takes over that inbox or that Google account becomes an operator.

The console is signed into with **Maktaba's own auth** (README §15 decision), so that auth has to
be strong enough for staff on its own: separate staff accounts, MFA that can't be skipped, short
sessions, a fresh check before the actions that hurt, lockout, a full audit trail, and an
optional network allow-list. Every rule is enforced by the API, on every request, with role
tests (hiding UI is never a rule).

## Description

### 1. Staff accounts

- **A separate table**, `staff_accounts` (`id`, `email` unique, `password_hash` argon2id,
  `roles text[]`, `status active|disabled`, `created_at`, `last_signin_at`), with
  `staff_passkeys`, `staff_totp` (secret encrypted with AES-256-GCM under
  `MAKTABA_CLOUD_STAFF_SECRET_KEY`), `staff_recovery_codes` (argon2id, one use) and
  `staff_sessions`. A staff account is **not** a cloud account: it owns no homes, has no plan,
  no OAuth identity and no email reset.
- **Roles:** `service_admin`, which every `/v1/admin/*` route requires (the route list is today's,
  `cloud/internal/handlers/admin`, `metrics`, `privacy`). A staff account may hold no role (just
  created, or a role revoked); it can sign in and see its own sessions, nothing else. New roles
  (for example a moderator for 50.24's queue) are added by name to the route's requirement, each
  with its role test. **The plugin store's reviewers and store admins are not staff roles**: the
  store is a separate service with its own accounts and auth (Epic 54).
- **Created from the CLI only**, on the leader over SSH:

  ```sh
  maktaba-cloud staff add     --email ops@example.com --role service_admin   # prints a one-time enrolment link (24 h)
  maktaba-cloud staff grant   --email … --role service_admin
  maktaba-cloud staff revoke  --email … --role service_admin
  maktaba-cloud staff disable --email …        # ends every session at once
  maktaba-cloud staff reset-mfa --email …      # removes every factor; prints a new enrolment link
  maktaba-cloud staff list
  ```

  Each writes an audit row with actor `cli`. No HTTP route creates staff or changes roles.
- **Enrolment.** The link opens `admin.maktaba.tv/enroll#<token>` (the token stays in the
  fragment). The person sets a password (14–256 characters, the cloud's breached-password check),
  then **must** register at least one MFA factor: a passkey, a TOTP app, or both (recommended).
  They get 10 recovery codes, shown once. Until enrolment finishes the account can't sign in.
- **Replacing today's role.** A migration resets `users.role` to `user` everywhere (no production
  data exists yet), `RequireServiceAdmin` stops reading it, and `maktaba-cloud admin grant |
  revoke | list` are removed (the command prints a pointer to `staff add`). A later migration
  drops the column.

### 2. Signing in

All on `admin.maktaba.tv`, all through the admin listener (50.32), all JSON:

| Step | Route | Body | Answer |
|---|---|---|---|
| 1 | `POST /v1/staff/login` | `{email, password, turnstile_token}` | `200 {mfa_token, methods:["passkey","totp","recovery"]}`; any wrong email, password or disabled account: the same `401 invalid_credentials`, in the same time; `403 mfa_required` if the account has no factor; `403 turnstile_failed` (50.30); `429 too_many_attempts`; `423 account_locked` |
| 2a | `POST /v1/staff/mfa/passkey/begin`, `/finish` | `{mfa_token}`, then the WebAuthn assertion | session (below) |
| 2b | `POST /v1/staff/mfa/totp` | `{mfa_token, code}` | session |
| 2c | `POST /v1/staff/mfa/recovery` | `{mfa_token, code}` | session; the code is spent, and an alert tells the owner (50.10) |
| — | `POST /v1/staff/refresh` | — (cookie) | a new access token, the cookie rotates |
| — | `POST /v1/staff/logout`, `POST /v1/staff/sessions/revoke-all`, `GET /v1/staff/me`, `GET /v1/staff/sessions` | — | — |

- The `mfa_token` lasts 5 minutes, is bound to the account and the client IP, takes at most 5
  MFA attempts, and is not an access token: no admin route accepts it.
- **Passkeys:** WebAuthn with `rpId = admin.maktaba.tv`, origin `https://admin.maktaba.tv`,
  `userVerification: required`, attestation `none`; the signature counter is checked and a
  counter that goes backwards disables that passkey and alerts. Library: `go-webauthn/webauthn`.
- **TOTP:** RFC 6238, SHA-1, 6 digits, 30 s, ±1 step for clock drift; a code for a step already
  used is refused (no replay inside its window).
- **The session.** Success sets `__Host-maktaba_staff` (50.32 §4) and returns a 10-minute access
  token signed with `MAKTABA_CLOUD_STAFF_TOKEN_SECRET` (a different key from customer tokens) with
  `aud = "admin.maktaba.tv"`, `sub = staff id`, `sid`, `amr` (`["pwd","hwk"]` or
  `["pwd","otp"]`) and `auth_time`. The admin UI keeps the access token in memory only.

### 3. What every admin request checks, in order

1. **Host** is `admin.maktaba.tv` and the request came on the admin listener, else **404**
   (50.32).
2. **IP allow-list**, when set (§7), else `403 ip_not_allowed`.
3. **A staff access token** (signature, `aud`, expiry) whose session is alive (not revoked,
   not idle, not past 8 h) and whose account is `active`, else **401** `no_session`. A
   customer's token fails the signature and `aud` check: **401**.
4. **MFA in the token's `amr`** (`hwk` or `otp`), else **403 `mfa_required`**. Sign-in can't
   produce such a token, so this catches a bug or a forged signing path.
5. **The route's role**, read from the database on each request (so a revoke takes effect at
   once), else **403 `admin_access_denied`**.
6. For a sensitive action, **re-authentication** (§5), else **401 `reauth_required`**.
7. **CSRF** on state-changing requests (50.32 §4), else `403 bad_origin`.

Every refusal from step 2 on writes an audit row.

### 4. Short sessions

- Access token: **10 minutes**. Refresh: through the cookie, rotating; a rotated cookie presented
  again revokes every session of that account (the cloud's replay rule).
- **Idle timeout: 15 minutes.** `staff_sessions.last_active_at` moves on each admin request; a
  refresh after 15 minutes without one is refused and the session ends. The UI warns at 13
  minutes.
- **Absolute cap: 8 hours** from the MFA sign-in, whatever the activity.
- A password change, `staff disable`, `staff reset-mfa` and a role change end every session of
  the account.

### 5. Re-authentication for sensitive actions

These need an MFA check **within the last 5 minutes** (`auth_time`, updated by
`POST /v1/staff/reauth/passkey/*` or `/v1/staff/reauth/totp`; a password alone never counts):

| Action | Route |
|---|---|
| Suspend, unsuspend an account | `POST /v1/admin/accounts/{id}/suspend`, `/unsuspend` |
| Set a plan by hand | `POST /v1/admin/accounts/{id}/plan` |
| Block, unblock | `POST /v1/admin/abuse/block`, `/unblock` |
| Revoke, unrevoke a license | `POST /v1/admin/licenses/{id}/revoke`, `/unrevoke` |
| Delete a user's data | `DELETE /v1/admin/privacy/users/{id}` |
| Take down, restore, kill switch (when 50.24 and 50.25 land) | their admin routes |

The set is one list in code (`staffauth.Sensitive`), and a test fails when a mutating admin
route is neither in it nor marked `reauth: not needed` with a reason. Revoking a plugin is the
store's own action (Epic 54), not this console's.

### 6. Lockout and rate limits

- **Per account:** 5 failed steps (password or MFA) in 15 minutes lock the account for 30
  minutes (`423 account_locked`, `Retry-After`); a lockout alerts the owner (50.10,
  `staff_lockout`). Unlocking early is `maktaba-cloud staff unlock --email …`.
- **Per IP:** `/v1/staff/login`, `/v1/staff/mfa/*` and `/v1/staff/reauth/*` burst 5, then 1 per
  12 s (`429 too_many_attempts`), under 50.30's edge rule R1.
- **Per `mfa_token`:** 5 attempts, then it is spent.
- **Timing:** an unknown email runs the same argon2id work as a known one.

### 7. The optional IP allow-list

- `admin.ip_allowlist`: a list of CIDRs, **empty by default (off)**. When set, every request on
  the admin listener from a client IP (50.31's trusted rule) outside it is `403 ip_not_allowed`
  and audited; 50.30's custom rule 5 mirrors it at the edge.
- Changed by a config deploy. If the owner locks themselves out, they change it on the leader
  over SSH and restart the api role (the runbook in 50.12).

### 8. Audit

Every staff sign-in event and every admin request writes an `audit_log` row with
`actor_kind = 'staff'` (new column; `user` and `cli` for the others), the staff id, the client IP
and the user agent:

- `staff.signin.password_ok | password_failed | mfa_ok | mfa_failed | recovery_used | locked |
  unlocked | turnstile_failed | ip_denied`, `staff.enrolled`, `staff.reauth`, `staff.signout`,
  `staff.session.idle_ended | capped`;
- every `/v1/admin/*` request, reads included: `admin.request` with the method, the route
  pattern, the target and the status (today only mutations, account-detail views and denials
  are audited);
- the existing named actions (`admin.account.set_plan`, `admin.license.revoke`, …) keep their
  rows.

The console's *Audit* page filters by actor kind; *Staff sign-ins* is a new view.

### 9. Strings

| Key | English | Arabic |
|---|---|---|
| `staff.signIn.title` | Staff sign-in | تسجيل دخول الفريق |
| `staff.signIn.submit` | Continue | متابعة |
| `staff.signIn.failed` | Email or password is wrong. | البريد الإلكتروني أو كلمة المرور غير صحيحة. |
| `staff.mfa.title` | Two-step check | التحقق بخطوتين |
| `staff.mfa.passkey` | Use your passkey | استخدم مفتاح المرور |
| `staff.mfa.totp` | Enter the 6-digit code from your authenticator app | أدخل الرمز المكوّن من 6 أرقام من تطبيق المصادقة |
| `staff.mfa.recovery` | Use a recovery code | استخدم رمز استرداد |
| `staff.mfa.failed` | That code didn't work. Try again. | لم يعمل هذا الرمز. حاول مجددًا. |
| `staff.mfa.required` | This account needs two-step sign-in set up first. Ask the owner for an enrolment link. | يحتاج هذا الحساب إلى إعداد التحقق بخطوتين أولًا. اطلب رابط التسجيل من المالك. |
| `staff.enroll.title` | Set up your staff account | إعداد حساب الفريق |
| `staff.enroll.passkey` | Add a passkey | أضف مفتاح مرور |
| `staff.enroll.totp` | Scan this code with your authenticator app | امسح هذا الرمز بتطبيق المصادقة |
| `staff.enroll.recoveryCodes` | Keep these recovery codes somewhere safe. Each one works once. | احفظ رموز الاسترداد هذه في مكان آمن. كل رمز يعمل مرة واحدة. |
| `staff.enroll.linkExpired` | This enrolment link has expired or was already used. | انتهت صلاحية رابط التسجيل هذا أو سبق استخدامه. |
| `staff.reauth.title` | Confirm it's you | أكّد هويتك |
| `staff.reauth.body` | This action needs a fresh check with your passkey or code. | يحتاج هذا الإجراء إلى تحقق جديد بمفتاح المرور أو الرمز. |
| `staff.session.idleWarning` | You'll be signed out in 2 minutes unless you do something. | سيتم تسجيل خروجك بعد دقيقتين ما لم تقم بأي إجراء. |
| `staff.session.idle` | You were signed out after 15 minutes without activity. | تم تسجيل خروجك بعد 15 دقيقة دون نشاط. |
| `staff.session.capped` | Your session ended after 8 hours. Sign in again. | انتهت جلستك بعد 8 ساعات. سجّل الدخول من جديد. |
| `staff.locked` | Too many failed attempts. This account is locked for 30 minutes. | محاولات فاشلة كثيرة. هذا الحساب مقفل لمدة 30 دقيقة. |
| `staff.ipNotAllowed` | The console can't be used from this network. | لا يمكن استخدام اللوحة من هذه الشبكة. |
| `staff.forbidden` | Your staff account doesn't have this permission. | لا يملك حساب الفريق الخاص بك هذه الصلاحية. |
| `staff.sessions.title` | Signed-in devices | الأجهزة المسجّل دخولها |
| `staff.sessions.signOutAll` | Sign out everywhere | تسجيل الخروج من كل الأجهزة |
| `staff.audit.signins` | Staff sign-ins | عمليات تسجيل دخول الفريق |

## Acceptance criteria

- **Given** any `/v1/admin/*` route on the admin host,
  **when** it is called **with no session**,
  **then** **401** `no_session`.
- **Given** a customer's access token, or a staff account with no `service_admin` role,
  **when** it calls any admin route,
  **then** **401** for the customer token (not a staff session) and **403**
  `admin_access_denied` for the staff account without the role, and the attempt is audited.
- **Given** a staff account with `service_admin` but no MFA factor,
  **when** it signs in,
  **then** **403 `mfa_required`** and no session; **and given** a staff token minted without an
  MFA method in `amr` (a test signer), **when** it calls any admin route, **then** **403
  `mfa_required`**.
- **Given** any admin route,
  **when** it is called on a user host (`api.maktaba.tv`, `app.maktaba.tv`) with any credential,
  **then** **404** (50.32).
- **Given** a service admin signed in 20 minutes ago,
  **when** they suspend an account,
  **then** **401 `reauth_required`**; after a passkey or TOTP re-check, the same request
  succeeds and both events are audited.
- **Given** a staff session with no admin request for 15 minutes,
  **when** the UI refreshes,
  **then** the refresh is refused and the console shows `staff.session.idle`.
- **Given** 5 wrong TOTP codes in 15 minutes,
  **when** the sixth step comes,
  **then** **423 `account_locked`** for 30 minutes, an audit row and an owner alert.
- **Given** `admin.ip_allowlist = ["198.51.100.0/24"]`,
  **when** a request comes from 203.0.113.9 through the Tunnel,
  **then** **403 `ip_not_allowed`**, audited.
- **Given** any admin request that succeeds,
  **when** it completes,
  **then** an `admin.request` audit row names the staff account, the route and the status.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | role test (go, integration) | `chi.Walk` over the admin router; anon, a customer token, staff without role, staff `service_admin` without MFA (test signer, `amr=["pwd"]`), staff `service_admin` with MFA | every route | 401; 401; 403; 403 `mfa_required`; served (or `401 reauth_required` for sensitive routes until re-checked) |
| T02 | role test (go) | the same five, on the user router | every admin path | 404 ×5 (50.32 T01) |
| T03 | unit (go) | sign-in | right password + no factor; wrong password; unknown email; disabled | 403 `mfa_required`; 401; 401 (same timing ±20 %); 401 |
| T04 | unit (go) | TOTP | right code; previous step; same step twice; 2 steps old | ok; ok; second refused; refused |
| T05 | unit (go) | WebAuthn with a virtual authenticator | register; assert; wrong rpId; counter backwards; `uv=false` | ok; ok; refused; refused and disabled; refused |
| T06 | unit (go) | sessions, fake clock | 14 min idle then a request; 16 min idle then refresh; 8 h 1 min with activity | ok; refused `idle`; refused `capped` |
| T07 | unit (go) | re-auth | every route in `staffauth.Sensitive` with `auth_time` 6 min old, then 1 min old | 401 `reauth_required`; served |
| T08 | unit (go) | sensitive list | a mutating admin route neither in `Sensitive` nor marked with a reason | the test fails, naming it |
| T09 | unit (go) | lockout and limits | 5 failures; 6th; per-IP burst of 6; 6 attempts on one `mfa_token` | locked 423; 423; 429; token spent |
| T10 | unit (go) | IP allow-list | empty; set, inside; set, outside | served; served; 403 audited |
| T11 | integration (go) | audit | sign-in, a read, a mutation, a denial | rows with `actor_kind = staff` for each |
| T12 | unit (go) | CLI | `staff add`, `grant`, `revoke`, `disable`, `reset-mfa`, `unlock` | rows changed; sessions ended where §4 says; audit rows with actor `cli`; `admin grant` prints the pointer |
| T13 | unit (go) | migration | an old `users.role = service_admin` row | becomes `user`; it can't reach the admin router |
| T14 | unit (vitest) | admin UI | sign-in, MFA, enrolment, re-auth dialog, idle warning, locked, ip-not-allowed | the §9 strings in English and Arabic; RTL snapshots |
| T15 | unit (vitest) | i18n | every `staff.*` key | in `en.json` and `ar.json` |

## Edge cases

- **The only service admin loses their passkey and phone.** Recovery codes; if those are gone
  too, the owner runs `staff reset-mfa` over SSH, which needs the server's SSH key. There is no
  email or support reset.
- **Clock drift on a TOTP phone.** ±1 step covers 30 s each way; beyond that the code fails and
  the passkey is the way in.
- **A staff member is also a customer.** Two accounts, two hosts, two cookies (50.32 §4); the
  same email may appear in both tables.
- **The console is used from a shared network** (a hotel) and the IP allow-list is on. Sign-in
  is refused; that is the setting's purpose. The owner can widen it by a deploy.
- **Turnstile is down.** Staff sign-in fails closed (`503 turnstile_unavailable`, 50.30 §4); an
  owner who must act runs the CLI over SSH.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cloud/migrations/<next free slots>_staff_accounts.sql`, `_users_role_reset.sql`, `_audit_actor_kind.sql` (new) | The tables, the reset, the audit column. |
| `cloud/internal/staffauth/` (new) | Passwords, TOTP, WebAuthn, recovery codes, sessions, tokens, the `Sensitive` list, lockout. |
| `cloud/internal/handlers/staff/` (new) | `/v1/staff/*`. |
| `cloud/internal/auth/middleware/middleware.go` | `RequireStaff(role)`, replacing `RequireServiceAdmin`; the §3 order. |
| `cloud/internal/stores/staff.go` (new), `cloud/internal/audit/` | Data access; `actor_kind`; `admin.request` rows. |
| `cloud/cmd/maktaba-cloud/staff.go` (new), `admin.go` | The CLI; the old `admin` command's pointer. |
| `cloud/internal/config/config.go`, `cloud/configs/cloud.example.toml` | `admin.ip_allowlist`; secrets `MAKTABA_CLOUD_STAFF_TOKEN_SECRET`, `MAKTABA_CLOUD_STAFF_SECRET_KEY`. |
| `cloud/cmd/maktaba-cloud/admin_integration_test.go` | T01–T02 replace `TestAdminRoutesRequireServiceAdmin`'s user-role cases. |
| `web/admin/src/pages/StaffSignIn.tsx`, `Enroll.tsx`, `Reauth.tsx`, `Sessions.tsx`, `StaffSignins.tsx` (new), `web/admin/src/i18n/{en,ar}.json` | The screens and strings. |
| `cloud/README.md`, `cloud/docs/accounts-billing-admin.md` | Staff accounts replace the role; the *Still open* line on step-up and MFA is closed. |

### Tests

T01–T13 in the cloud Go unit and integration jobs; T14–T15 in the web unit job. No test signs in
through a real browser against production; the owner's first enrolment is the live check.

### Rollout

1. Merge; production has no staff account until the owner runs `staff add` (README §15.7).
2. The owner enrols with a passkey and a TOTP app and keeps the recovery codes.
3. 50.10 alerts (`staff_lockout`, recovery-code use, passkey counter) go live with it.

### Rollback

Reverting the code would bring back the customer-account role, which the migration has
cleared, so nobody would be an operator until `admin grant` is run again. That is the safe
direction; it is never rolled back to a password-only console in production.

### Dependencies

50.32. Used by 50.24 and 50.25, whose admin actions join the `Sensitive` list.

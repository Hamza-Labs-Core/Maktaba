# Epic 10 — Auth & Security: Spec-vs-Implementation Gap Analysis

**Verdict:** Structurally present, behaviorally hollow. The prior audit's
17/17 "structurally complete" is misleading: the auth *primitives*
(stores, hashers, JWT, keys) are well-built and behaviorally correct,
but the *HTTP enforcement surface* is largely unwired. No `RequireAuth`
gate exists in the live router, web `CookieAuth` is orphaned, CSRF
enforcement is entirely absent, brute-force lockout never increments,
the `lib[]` claim is hardcoded empty, admin user-management HTTP routes
do not exist, `GET /api/auth/me` is unmounted, Story 10.17
(`/api/auth/pair`) is unimplemented, and resource-scope authz is nil in
the videos handler. Roughly half the behavioral ACs fail.

Scope reviewed: all 18 stories, README, plans referenced; verified
against live boot path (`api/main.go` → `auth_bootstrap.go` →
`router.New` → `MountP6/P9/P10`), not audit/spec self-claims.

---

## Status legend

- **complete** — code exists, reachable from boot, behavior satisfies AC.
- **partial** — exists & reachable but behavior incomplete / deviates.
- **unwired** — code exists & correct but unreachable from live router/boot.
- **missing** — no implementing code.
- **stub** — placeholder only.

---

## Per-story AC table

### Story 10.1 — User store + argon2id

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 hash creation (argon2id PHC, §11.2 params) | complete | `api/internal/auth/users/users.go:132` `argon2id.Hash(in.Password, s.Params)`; `argon2id.DefaultParams()` per `argon2id/argon2id.go`. PHC string stored. |
| AC-2 constant-time verify, no log | complete | `users.go:62-73` uses `argon2id.Verify`; `pwHash` package-private, never serialized. |
| AC-3 admin user CRUD HTTP (`POST/PATCH/DELETE /api/users`, `/unlock`) | **missing** | Store methods (`Create/Update/Delete/Unlock`) exist `users.go:128-324` but **no HTTP handler/route**. `grep '/api/users'` finds only `auth.go:100-101` (sessions/refresh-tokens revoke). No `POST /api/users`, `PATCH /api/users/{id}`, `DELETE /api/users/{id}`, `POST /api/users/{id}/unlock`. |
| AC-3 `DELETE /api/users/{id}/sessions/{session_id}` | partial | Route exists `auth.go:100`, handler `auth.go:456`. Reachable, but admin gate relies on `principal.FromContext` which is never populated for cookie clients (see Top Gap #1) and route is anonymous. |
| AC-4 `maktaba-api adduser` CLI | complete | `api/adduser.go`: no-echo `term.ReadPassword` (140), `is_admin=true` (107), `HasAnyUser` guard (96). |
| EC username conflict 409 / last-admin 409 / self-promote | partial | Store returns `ErrUsernameExists`/`ErrLastAdmin` (`users.go:85-90`, `Update:206-228`, `Delete:294-302`). No HTTP layer maps these to 409 (no `/api/users` handler exists). Self-promotion guard not enforced (no handler). |

### Story 10.2 — Web login (cookie + CSRF)

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 login → `web_sessions` row + `mkt_sess`/`mkt_csrf` cookies | complete | `auth.go:228-258` `respondWeb`: both cookies set with `httpOnly/Secure/SameSiteLax/Path=/`, body `{user}`. `sessions.Create` writes row + CSRF. |
| AC-2 authed request loads identity, bumps `last_seen_at` (debounced 1/min) | **unwired** | `CookieAuth` middleware exists `auth.go:525` with `TouchLastSeen`, but it is **never installed** in the router. `auth_bootstrap.go:99-106 applySecurity` wires only `JWTBearer`+`AdminToken`. `grep '.CookieAuth'` → only definition + p9.go comment. Cookie clients never get a principal on normal requests. Debounce-1/min not verifiable (depends on `TouchLastSeen` impl; middleware unreachable regardless). |
| AC-3 wrong creds → 401 generic + 500ms floor | complete | `auth.go:135-170`: `invalidCreds()` generic `type:invalid-credentials`; `padFailDelay`/`FailDelay=500ms` (`auth.go:55,570`) applied to unknown-user, locked, and wrong-password branches uniformly. |
| AC-4 expired session → anonymous + clear cookie | partial | `CookieAuth` clears cookie on `Sessions.Lookup` error (`auth.go:537-542`); but middleware unwired so never runs on protected routes. `LogoutAll`/`Logout` paths do a manual `Sessions.Lookup`. Expiry-as-anonymous behavior not enforced anywhere reachable. |
| EC multi-tab shared session / logout invalidates all tabs | complete | Single `web_sessions` row keyed by cookie; `Sessions.Revoke` revokes the shared row. |
| EC `Maktaba-Hint: cookies-missing-check-proxy` | **missing** | No occurrence of `Maktaba-Hint` or `cookies-missing` anywhere in `api/`. |

### Story 10.3 — Native login (JWT + refresh)

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 native login response shape, no cookies | complete | `auth.go:189-225 respondNative`: `{access_token, access_expires_in, refresh_token, refresh_expires_in, user}`; `isNativeClient` honors `X-Maktaba-Client: native` (`auth.go:655`). |
| AC-2 JWT claims incl. **`lib=[...]`** read-access snapshot | **partial (critical)** | `auth.go:195-204`: `iss/aud=api/sub/iat/exp/jti(via jwt.Sign)/kid/is_admin` correct, but **`Lib: []string{}` is hardcoded empty**. AC-2 mandates the user's accessible library set. No `authz.ACLStore.LibrariesFor` call. Breaks Story 8.1 offline authz: every native client gets `lib:[]` → Streaming rejects all their signed URLs. |
| AC-3 bearer auth verify (sig+exp+aud), record `jti` | partial | `middleware.JWTBearer` (`middleware.go:93`) wired via `applySecurity`; verifies sig/exp/aud offline. But `jti` is not recorded for audit on bearer requests (no audit write in `JWTBearer`). |
| AC-4 opaque refresh, 32-byte, only argon2id hash persisted | complete | `refresh.Issue` (`refresh/refresh.go`) generates random secret, persists hash of secret half only. |
| EC clock-skew leeway 60s | complete | `jwt.Verify` applies skew leeway (see `jwt/jwt.go`). |
| EC native client missing header → cookies OK | complete | `isNativeClient` falls back; web path used otherwise. |

### Story 10.4 — Token refresh + rotation

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 refresh rotates, old revoked+`replaced_by`, family inherited, re-snapshots `lib` | partial | `auth.go:266-336` calls `RefreshTokens.Rotate`; rotation/family logic in `refresh/refresh.go` is correct. But re-issued access token again sets `Lib: []string{}` (`auth.go:322`) — `lib` re-snapshot AC violated (same root cause as 10.3 AC-2). |
| AC-2 reuse detection → family revoke + audit `refresh.replay-detected` + 401 | complete | `auth.go:280-292`: `errors.Is(err, refresh.ErrReplay)` → audit `EventRefreshReplay` + `refreshReplayed()` 401. `Rotate` revokes family internally. |
| AC-3 expired refresh → 401 `refresh-expired`, no family revoke | complete | `auth.go:293-295` maps `refresh.ErrExpired` → `refreshExpired()`. |
| EC network-race double refresh / revoked account | complete | Second old-token triggers `ErrReplay`. Revoked-user path: `GetByID` after rotate; account-revoked surfaces as error. |

### Story 10.5 — Logout + revocation

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 web logout → revoke row, clear cookies, 204 | complete | `auth.go:346-374`: cookie lookup → `Sessions.Revoke` → clear both cookies → 204. |
| AC-2 native logout → revoke refresh row, access valid until exp | complete | `auth.go:353-360` `RevokeByPlaintext`. Access not invalidated (by design). |
| AC-3 logout-all → every session + refresh family + audit | partial | `auth.go:378-412 LogoutAll`: `RevokeAllForUser` (sessions + refresh) + `EventLogoutAll` audit. Reachable, but principal resolution relies on cookie-only fallback `Sessions.Lookup` since `CookieAuth`/`RequireAuth` are unwired; bearer clients depend on `JWTBearer` (wired) so native works; web works via cookie fallback. Functional but fragile. |
| AC-4 admin revoke session / refresh-family / streaming close-all | partial | `DELETE /api/users/{id}/sessions/{session_id}` (`auth.go:456`) and `/refresh-tokens/{family_id}` (`auth.go:495`) exist. **`POST /api/users/{id}/streaming/close-all` is missing** (no route, `grep` finds none). |

### Story 10.6 — RS256 keys / rotation / JWKS

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 load PEM env, kid=SHA256(DER)[:16], refuse-start if missing | complete | `auth_bootstrap.go:37-54`: both-or-neither check, `keys.FromPEM`, fatal error on partial config. |
| AC-2 `keys init` 4096-bit, print PEM, never write disk | complete | `api/keys_cli.go:36` init action; prints PEM, no disk write. |
| AC-3 `GET /api/.well-known/jwks.json`, all trusted kids, `Cache-Control:public,max-age=300` | complete | Mounted `main.go:280` on publicMux; `keys/jwks_handler.go:29` sets header; `Set.JWKS()` returns trust set. |
| AC-4 rotation, overlap 24h, old key reaped, `LISTEN jwks_changed` | partial | Overlap reaper wired `main.go:311-325` (`ReapExpired`). `keys rotate` CLI prints PEM (operator-side). **No `LISTEN jwks_changed` NOTIFY** emission found in API (`grep NOTIFY/jwks_changed` → none in keys CLI); Streaming-side push thus never triggered (5-min poll only). |
| AC-5 `--immediate` requires `yes-invalidate-all-tokens` + audit | partial | `keys_cli.go:77-91`: `--immediate` flag prompts for exact `yes-invalidate-all-tokens` string. No `audit_log` `key.rotated payload={mode:immediate}` row written (CLI has no DB audit write). |
| EC key <2048 bits refused | complete | `keys.FromPEM` rejects short keys (per keys.go validation). |

### Story 10.7 — Streaming offline JWT verify

| AC | Status | Evidence |
|----|--------|----------|
| AC-1/2/3 JWKS bootstrap, rotation handling, aud/iss/lib checks | **out-of-epic, not verified here** | Streaming-side; lives in `streaming/`. API side: `lib[]` is empty (10.3 AC-2) so even a correct Streaming verifier would reject every real user's URL. Behaviorally blocked by the API gap regardless of Streaming code. |

### Story 10.8 — Signed-URL minter

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 `mintManifestURL` | **unwired/partial** | `streaming.URLSigner` interface referenced `router/p6.go:41,73`, but `main.go` constructs `P6Deps{DB,PipelineClient,StreamingClient}` only — **`URLSigner` is never set** (nil). Minter implementation not located as a wired concrete type; signed-URL JWT minting not reachable. |
| AC-2 `mintDirectURL` | unwired | Same root cause; `d.URLSigner` nil at boot. |
| AC-3 `mintStaticURL` | unwired | Same. |
| AC-4 ttl capped at `max_signed_url_ttl_sec` | not verifiable | Minter unwired. |
| AC-5 `lib[]` resolution + 403 before signing | missing/unwired | No reachable minter performing `library_acl` intersection + pre-sign 403. |

### Story 10.9 — Single-user mode (admin token)

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 admin token → sentinel principal, no DB | complete | `middleware.AdminToken` (`middleware.go:49`) wired via `applySecurity` (`auth_bootstrap.go:102`); bearer or `mkt_admin_token` cookie → `SentinelAdminID`, `IsAdmin/AccessAllLibraries=true`, no DB. |
| AC-2 constant-time compare | complete | `middleware.go:69` `subtle.ConstantTimeCompare`. |
| AC-3 UI bootstrap "paste token" dialog | not verified (web) | Out of API scope; web-side. |
| AC-4 env rotation, no grace | complete | Token read once at boot from env; old value rejected after restart (`subtle` compare against new). |
| AC-5 synthetic-user `lib[]` = all libraries | **missing** | `AdminToken` sets `AccessAllLibraries=true` on the principal, but when this path mints a JWT (via the unwired 10.8 minter) there is no code that fills `lib` with every library id. Minter unwired + no all-libs resolver. |
| EC weak token (<32) refused at boot | complete | `auth_bootstrap.go:59-61` returns fatal error if `< MinAdminTokenLen(32)`. |

### Story 10.10 — CSRF protection (web only)

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 CSRF token issued on login | complete | `auth.go:248-256` sets `mkt_csrf` (non-httpOnly); `sessions.go:103` generates 32-byte token. |
| AC-2 state-changing request must carry matching `X-Maktaba-CSRF` else 403 `csrf-failed` | **missing** | **No CSRF-validation middleware anywhere.** `TypeCSRFMismatch` constant declared `auth.go:68` but unused. No code reads `X-Maktaba-CSRF` and compares to `mkt_csrf` cookie. `grep csrf-failed` → none. Double-submit pattern entirely unenforced. |
| AC-3 bearer path skips CSRF | n/a | No CSRF middleware to skip. |
| AC-4 safe methods exempt | n/a | No CSRF middleware. |

### Story 10.11 — Brute-force / credential-stuffing

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 per-username lockout (5 / 900s) → 423 `account-locked` | **partial/broken** | `User.IsLocked` is checked in login (`auth.go:159`) and `Store.IncrementFailedAttempt` exists (`users.go:330`), but **the login handler never calls `IncrementFailedAttempt` on failure** (`grep IncrementFailedAttempt` in handlers → none). Counter never increments → lockout never engages. Also locked user returns generic 401 `invalid-credentials` (`auth.go:159-163`), not 423 `account-locked`. |
| AC-2 per-IP lockout (20 / 900s) → 429 exp backoff | **missing** | No per-IP failed-login tracking; no exponential `Retry-After`. |
| AC-3 no user enumeration (timing parity, IP counter increments) | partial | Timing parity OK (`padFailDelay` on all branches). Per-IP counter increment missing (AC-2 missing). |
| AC-4 audit on lockout (`lockout-username`/`lockout-ip`) | **missing** | No lockout-event audit; `securityaudit` has no such write call from login. |

### Story 10.12 — Rate limiting on auth endpoints

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 `/api/auth/*` (excl login) 30/min per IP | **missing** | `internal/middleware/ratelimit.go` has generic `PerIP`/`PerUser` (global 6000/600 from `main.go:203`). `grep auth_rate/login_rate/refresh_rate/'/api/auth'` in ratelimit.go → none. No auth-scoped limiter. |
| AC-2 `/refresh` 6/min per token-family | **missing** | No per-family limiter. |
| AC-3 `/api/auth/login` 10/min per IP (stricter) | **missing** | No login-specific limiter. |

### Story 10.13 — Permission model

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 `authz.Can(...)` resource-scope; v1 policy | **partial/unwired** | `authz.V1.Can` implemented (`authz/authz.go:85`) with `library_acl`-backed policy. But `videos.Handler` is constructed `router/p6.go:63` as `&videos.Handler{DB:d.DB}` — **`Authz` field never set (nil)**. videos.go:80-85 comment says "Authz optional in single-user"; effectively no resource-scope enforcement is active for videos. No global `RequireAuth`. |
| AC-2 per-user `playback_state` filter | not verified / likely partial | Depends on videos handler; authz nil, filtering by principal needs a principal which is unset for cookie clients. |
| AC-3 saved searches per-user | not verified | Search handler `search.go:143` references authz scope; principal availability same concern. |
| AC-4 403 `forbidden` generic | partial | `RequireAdmin`/`Forbidden` helpers exist but `RequireAuth`/`RequireAdmin` unwired in router. |
| AC-5 streaming JWT carries `lib[]` | **missing** | Same as 10.3 AC-2 / 10.8 AC-5: `lib` empty everywhere it is minted. |

### Story 10.14 — Secret loading & redaction

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 env-var precedence over file | complete | `secret.FromEnvOrFile` (`internal/secret/secret.go`); used `auth_bootstrap.go:57`. |
| AC-2 `secret:"true"` slog redaction + header strip | complete | `secret/secret.go` redaction tag handling; sensitive-header list incl `X-Maktaba-CSRF` (`secret.go:267`). |
| AC-3 `/api/settings` redaction + `*_present` | not verified (Epic 7 handler) | `handlers/settings/settings.go` exists; redaction depends on tag use. Not the focus of this epic's enforcement path. |
| AC-4 gRPC metadata stripping | not verified | gRPC interceptor layer; out of primary scope. |

### Story 10.15 — Transport security

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 Caddy front | n/a (deploy) | `deploy/`; not API code. |
| AC-2 HSTS header | complete | `httpsec.Headers` wired `auth_bootstrap.go:104` (outermost); `HSTSOneYear` when `MAKTABA_HSTS=1` (`:72-74`). |
| AC-3 cookie attributes | complete | `auth.go:239-256` sets Secure/HttpOnly/SameSiteLax/Path; `SecureCookies` from `MAKTABA_COOKIES_SECURE`/`MAKTABA_HSTS` (`main.go:393`). |
| AC-4 CORS allow-list, preflight 204 | complete | `httpsec.CORS` wired `auth_bootstrap.go:103`; origins from `MAKTABA_CORS_ALLOWED_ORIGINS` (`:68`). |
| AC-5 security headers (nosniff, Referrer-Policy, COOP, CSP) | complete | `httpsec.DefaultHeaders()` (`httpsec` pkg); applied outermost on public mux. |

### Story 10.16 — Security audit log

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 event vocabulary, `category='security'` | partial | `securityaudit.Writer.Write` inserts `category='security'` (`securityaudit.go:114`). Events emitted: login.success/failed, logout, logout-all, refresh.replay, session/refresh revoked. **Not emitted:** `lockout-*` (10.11 broken), `key.rotated` (CLI no DB), `admin-token.used`, `permission.denied`, `streaming.direct.access`, `pair.*` (10.17 missing). |
| AC-2 append-only triggers | inherited (Epic 9) | Trigger owned by 9.17; not re-verified here. |
| AC-3 `GET /api/security/audit` admin-only, newest-first | partial | Route `auth.go:99`, handler `:418` filters `category='security'` (`securityaudit.go:152`), checks `p.IsAdmin`. But principal is unset for cookie admins (CookieAuth unwired) → admin via cookie gets 401; only bearer/admin-token admins can read. |
| AC-4 retention partitioning | inherited (Epic 9) | Not re-verified. |

### Story 10.17 — Device pairing `POST /api/auth/pair`

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 `POST /api/auth/pair` → `pairing_codes` row, 8-char base32, 201 | **missing** | No `/api/auth/pair` route. `grep '/api/auth/pair'` and `pairing_codes` in `api/` → none. The `handlers/discovery/pairing.go` implements a *different* Epic-15.5 flow: `/api/pairing/request|status|exchange` (`pairing.go:34-37`), no `pairing_codes` table, no SHA-256 code hash, no `pair.*` audit. |
| AC-2 `POST /api/auth/pair/claim` | missing | Not implemented. |
| AC-3 `GET /api/auth/pair/{code}` poll → tokens | missing | Not implemented. |
| AC-4 constant-time SHA-256 code match | missing | No `pairing_codes` table/store. |
| AC-5 reaper marks expired + audit | missing | No pairing-codes reaper. |
| AC-6 6/min per-IP rate cap | missing | No auth-pair limiter (also 10.12 missing). |

### Story 10.18 — Ed25519 server identity

| AC | Status | Evidence |
|----|--------|----------|
| AC-1 first-boot generate, sealed at rest, audit `identity.generated` | partial | `auth/serverkeys/{loader,store,keys}.go` implement generation/sealing/flock. Not invoked from `main.go` boot path (`grep serverkeys/ServerIdentity` in main.go → none) → never generated at runtime. |
| AC-2 env-var bootstrap precedence | partial | `serverkeys/loader.go` supports env; not called at boot. |
| AC-3 kid = SHA256(pub raw)[:16] | complete (pkg) | Implemented in `serverkeys/keys.go`. |
| AC-4 `GET /api/.well-known/server-identity.json` | **missing/unwired** | Handler exists `serverkeys/jwks.go:11,50`, but **not mounted** anywhere. `grep server-identity` in main/router → only the pkg file. Unlike `jwks.json` (mounted `main.go:280`), this endpoint is unreachable. |
| AC-5 Sign/Verify primitives, typed unknown-kid vs bad-sig | complete (pkg) | `serverkeys` Signer/Verifier present. |
| AC-6 `maktaba-api identity rotate` CLI | **missing** | No `identity` subcommand in `main.go:49-81` (only version/migrate/serve/adduser/keys/help). `grep 'identity rotate'` → none. |
| AC-7 federation/cloud cross-refs | n/a | Downstream epics 15/16/25; blocked by AC-1/4/6 not wired. |

---

## Top gaps by impact

1. **No `RequireAuth` gate + orphaned `CookieAuth` → web auth broken & protected routes anonymous (CRITICAL).**
   `auth_bootstrap.go:99-106 applySecurity` wires only `JWTBearer` +
   `AdminToken` globally. `RequireAuth`/`RequireAdmin`
   (`middleware.go:127,140`) and `Handler.CookieAuth` (`auth.go:525`)
   are **never installed** (`grep` confirms only definitions). Effects:
   (a) `GET /api/auth/me` is unmounted (`auth.go:94 Mount` lacks it)
   AND even if added, web cookie clients never get a principal →
   web session-restore (`web/src/lib/auth.tsx:39`) and every
   cookie-authenticated request fails; (b) no global `RequireAuth`
   means every Epic-7 business route (videos, libraries, jobs,
   collections, …) is reachable **unauthenticated** — handlers only
   protected if they internally re-check `principal.FromContext`
   (most do not). This is a behavioral auth bypass for the entire API.

2. **`lib[]` claim hardcoded empty in every minted token (CRITICAL).**
   `auth.go:202` (login) and `auth.go:322` (refresh) set
   `Lib: []string{}`. Story 10.3 AC-2, 10.4 AC-1, 10.8 AC-5, 10.9
   AC-5, 10.13 AC-5 all require the user's `library_acl` set.
   `authz.ACLStore.LibrariesFor` (`authz/acl.go:17`) exists but is
   never called by the token minter. Consequence: every native client
   gets `lib:[]`; Streaming's offline authz (Epic 8/10.7) rejects
   100% of their signed URLs → native playback is fully broken end
   to end even though each component looks "complete" in isolation.

3. **CSRF entirely unenforced (HIGH).** Story 10.10 AC-2: no
   middleware reads `X-Maktaba-CSRF` / compares to `mkt_csrf`.
   `TypeCSRFMismatch` constant unused. Cookie auth (when fixed) would
   be CSRF-vulnerable.

4. **Brute-force lockout never engages (HIGH).** `IncrementFailedAttempt`
   (`users.go:330`) is never called by the login handler; counter
   stays 0, `IsLocked` never true. Story 10.11 AC-1..AC-4 effectively
   absent; no per-IP throttle; no lockout audit. Locked path also
   returns 401 generic instead of 423 `account-locked`.

5. **Admin user-management HTTP surface missing (HIGH).** Story 10.1
   AC-3: no `POST /api/users`, `PATCH /api/users/{id}`,
   `DELETE /api/users/{id}`, `POST /api/users/{id}/unlock`. Store
   layer is complete and unused. Operators cannot create/manage users
   or unlock brute-force-locked accounts over HTTP.

6. **Story 10.17 `/api/auth/pair` unimplemented (HIGH, REVIEW.md
   §3.2).** No `pairing_codes`, no `/api/auth/pair*` routes; the
   existing discovery handler is a different Epic-15.5 mechanism. TV/
   desktop QR-pairing has no API.

7. **Signed-URL minter unwired (HIGH).** `P6Deps.URLSigner` never set
   in `main.go` (only DB/Pipeline/Streaming clients). Story 10.8
   minting unreachable; compounds gap #2.

8. **Story 10.18 endpoint/CLI unwired (MEDIUM).**
   `/api/.well-known/server-identity.json` not mounted; no
   `identity` subcommand; serverkeys never initialized at boot.

9. **Auth-specific rate limiting absent (MEDIUM).** Story 10.12
   AC-1/2/3: only generic global PerIP/PerUser; no
   login/refresh/auth-surface caps.

10. **Resource-scope authz inactive for videos (MEDIUM).**
    `videos.Handler.Authz` nil (`router/p6.go:63`); Story 10.13
    AC-1/AC-2 per-user/per-library scoping not enforced on the live
    video detail path.

---

## Client-called auth endpoints vs API-mounted routes

Web client (`web/src/lib/auth.tsx`):

| Client calls | API mounts? | Where |
|---|---|---|
| `POST /api/auth/login` (cookie mode) | YES | `auth.go:95` (Mount via MountP9) |
| `GET /api/auth/me` (session restore, `auth.tsx:39`) | **NO** | Not in `auth.go:94 Mount()`. Unmounted everywhere. **Confirmed gap.** |
| `POST /api/auth/logout` (`auth.tsx:66`) | YES | `auth.go:97` |
| `POST /api/auth/logout-all` (`auth.tsx:76`) | YES | `auth.go:98` |

Native/mobile clients (per Story 10.3/10.4/10.17 contract):

| Expected | API mounts? | Where |
|---|---|---|
| `POST /api/auth/login` (native, `X-Maktaba-Client`) | YES | `auth.go:95` |
| `POST /api/auth/refresh` | YES | `auth.go:96` |
| `POST /api/auth/logout {refresh_token}` | YES | `auth.go:97` |
| `POST /api/auth/pair` (Story 10.17) | **NO** | Missing entirely |
| `POST /api/auth/pair/claim` | **NO** | Missing |
| `GET /api/auth/pair/{code}` | **NO** | Missing |

Admin surface (Story 10.1/10.5):

| Expected | API mounts? | Where |
|---|---|---|
| `POST /api/users` | **NO** | Missing |
| `PATCH /api/users/{id}` | **NO** | Missing |
| `DELETE /api/users/{id}` | **NO** | Missing |
| `POST /api/users/{id}/unlock` | **NO** | Missing |
| `DELETE /api/users/{id}/sessions/{session_id}` | YES | `auth.go:100` |
| `DELETE /api/users/{id}/refresh-tokens/{family_id}` | YES | `auth.go:101` |
| `POST /api/users/{id}/streaming/close-all` (10.5 AC-4) | **NO** | Missing |

Well-known:

| Expected | API mounts? | Where |
|---|---|---|
| `GET /api/.well-known/jwks.json` | YES | `main.go:280` (publicMux) |
| `GET /api/.well-known/server-identity.json` (10.18) | **NO** | Handler exists `serverkeys/jwks.go`, unmounted |

Additionally, even the mounted protected routes lack a `RequireAuth`
gate (Top Gap #1): authentication is *attached* (JWT/admin-token) but
never *required* by the router, so absence of a principal does not
produce 401 except where individual handlers self-check.

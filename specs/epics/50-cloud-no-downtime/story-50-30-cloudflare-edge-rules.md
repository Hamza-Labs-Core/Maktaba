# Story 50.30 — Cloudflare in front: TLS, WAF, rate limits and bot protection

> Epic 50 · Phase 1 · Depends on: 50.01 (names), 50.34 (deploys) · Needed by: the launch gate
> (README §11), 50.32, 50.33

## Why

The user portal (`app.maktaba.tv`, `maktaba.tv/developers`, `api.maktaba.tv`) and the operator
console (`admin.maktaba.tv`) sit behind the Cloudflare proxy (README §15). The proxy only helps
if its settings are right and stay right: TLS on, the WAF's managed rules on, rate limits on the
routes attackers hammer (sign-in, sign-up, password reset, the API), and a bot check on the auth
pages. Settings changed by hand in a dashboard drift and leave no review trail, so this story
keeps them **in the repo**, checks them offline on every pull request, and has CI apply them
(50.34).

The API's own limits (per-IP buckets, the per-account lockout, `cloud/internal/ratelimit`,
`abuse/`) stay. The edge's limits stop floods before they reach the origin; the API's limits are
the ones that know accounts.

## Description

### 1. The zone file

`deploy/cloudflare/zone.maktaba.tv.json` (new) is the one description of the zone's security
settings. It names the plan it is written for (`"plan": "pro"`, README §15.7) and holds:

| Part | What |
|---|---|
| `settings` | `ssl: strict` (only matters for 50.31's fallback; Tunnel names need no origin certificate), `min_tls_version: 1.2`, `tls_1_3: on`, `always_use_https: on`, `automatic_https_rewrites: on`, `opportunistic_encryption: on`, `browser_check: on`, `security_level: medium` |
| `managed_rules` | the Cloudflare managed ruleset (Pro) or the Free managed ruleset (Free), action default; the OWASP core ruleset at paranoia level 1, score threshold *medium*, in **log** mode for its first 14 days, then **block** (a dated field, `owasp_block_from`) |
| `custom_rules` | §2 |
| `rate_limits` | §3 |
| `exceptions` | skip rules, each with a reason (§2, last row) |

`tools/cloudflare/zone.mjs` (new, Node 22, no dependencies beyond `undici`) reads the file and:

- `check` (offline, every pull request): validates it against
  `deploy/cloudflare/zone.schema.json`, checks the rule counts against the plan's limits
  (a table in the script: Free 5 custom / 1 rate limit, Pro 20 / 2, with the date it was
  checked), parses each expression with a small grammar for the fields it uses, and fails on an
  unknown field, a missing reason or a rule that would match a relay name (relay names are DNS
  only and never reach the proxy; a rule naming them is a mistake);
- `plan` (main, before applying): fetches the zone's current rulesets and settings and prints
  the difference;
- `apply` (main): writes them through the Rulesets API (`http_request_firewall_custom`,
  `http_ratelimit`, `http_request_firewall_managed` phases) and the zone settings API, then
  reads them back and fails if they differ.

### 2. Custom rules

| # | Name | Expression (abridged) | Action |
|---|---|---|---|
| 1 | Admin paths only on the admin host | `http.host ne "admin.maktaba.tv" and (starts_with(http.request.uri.path, "/v1/admin/") or starts_with(http.request.uri.path, "/v1/staff/"))` | block, custom **404** JSON (§5) |
| 2 | Admin host serves only its own paths | `http.host eq "admin.maktaba.tv" and not (starts_with(http.request.uri.path, "/v1/admin/") or starts_with(http.request.uri.path, "/v1/staff/") or http.request.uri.path in {"/healthz" "/readyz"} or not starts_with(http.request.uri.path, "/v1/"))` | block, custom 404 |
| 3 | Methods | `http.request.method in {"TRACE" "CONNECT" "TRACK"}` | block |
| 4 | Oversized auth bodies | `starts_with(http.request.uri.path, "/v1/auth/") and http.request.body.size gt 16384` (Pro field; Free drops this rule) | block |
| 5 | Admin IP allow-list mirror (off by default) | `http.host eq "admin.maktaba.tv" and not ip.src in $maktaba_admin_allow` | block, custom 404; generated only when `admin.ip_allowlist` is set (50.33 §7) |
| — | Exception: Maktaba's own Workers | `cf.worker.upstream_zone eq "maktaba.tv"` | skip managed rules; **added only if** a managed rule is seen challenging the broadcast edge's calls (README §15.5); the API still checks the edge's signature |

Rules 1 and 2 repeat at the edge what the origin already enforces (50.32), so a mistake in one
place is caught by the other.

The plugin store's rules (Epic 54, its own hostname) go in the same file when the store is
built, because they share the zone's rule counts; this story leaves room for them (Pro: 15 of
20 custom rules free after the rows above).

### 3. Rate limits at the edge

Counted per client IP (`ip.src`), with Cloudflare's own counter. Starting values, tuned from the
first month's logs (50.10):

| Rule | Matches | Limit | Action |
|---|---|---|---|
| R1 auth | `POST` to `/v1/auth/login`, `/v1/auth/register`, `/v1/auth/password/forgot`, `/v1/auth/password/reset`, `/v1/auth/verify-email/resend`, and on `admin.maktaba.tv` `/v1/staff/login`, `/v1/staff/mfa/*`, `/v1/staff/reauth` | **20 requests a minute** per IP (Pro period 1 min; on Free 5 per 10 s) | block for 10 min (Pro) or 10 s (Free), custom 429 JSON (§5) |
| R2 API | every request to `api.maktaba.tv` and `/v1/*` on `app.maktaba.tv` | **1,200 requests a minute** per IP | block for 1 min, custom 429 (Pro only; on Free this rule is dropped and the API's limiter is the only one) |

Why these numbers: R1 is about four times what a person retyping a password could send, and far
below what a credential-stuffing run needs. R2 is generous because one public IP can carry a
household or a carrier-grade NAT, and homes call the API every minute (license, heartbeats).
The API's own limits stay tighter per account (burst 30, 5/s; 10 failed logins lock the account
for 30 minutes; 50.33 adds stricter ones for staff).

### 4. Bot protection on the auth pages: Turnstile

- The web sign-in, register, forgot-password and staff sign-in pages render a Turnstile widget
  (managed mode). Its token goes with the form as `turnstile_token`.
- The API verifies it server-side (`POST https://challenges.cloudflare.com/turnstile/v0/siteverify`
  with `TURNSTILE_SECRET_KEY`, the client IP from 50.31, and the expected hostname) on
  `POST /v1/auth/register`, `/v1/auth/login`, `/v1/auth/password/forgot` and
  `/v1/staff/login`, **when the request carries a web `Origin`** (one of the cloud's web origins).
  A failed or missing token is `403 turnstile_failed`. A token is used once.
- **Native apps** (no web `Origin`) aren't asked for a token: Turnstile has no native SDK. They
  still pass R1, the API's per-IP buckets and the account lockout. App attestation (App Attest,
  Play Integrity) for native sign-up is noted for later, not built here.
- `turnstile.enabled` (default `true` in production, `false` in dev and CI) and
  `turnstile.site_key` are config; the secret comes from the environment only. With the secret
  missing in production, the api role refuses to start.
- **If siteverify can't be reached** (timeout 3 s): sign-in and staff sign-in fail closed with
  `503 turnstile_unavailable` and `Retry-After: 30`; register and forgot-password fail closed
  too. The outage is a 50.10 alert, not a silent bypass.
- **Why not Bot Fight Mode:** it covers the whole zone and can't be skipped on Free, so it would
  challenge homes and native apps on `api.maktaba.tv`. Super Bot Fight Mode (Pro) stays **off**
  until a check on staging shows it can be kept off the API paths; the zone file has the field,
  set to `off`, with that note.

### 5. The edge's own answers, in English and Arabic

Custom responses for the block and rate-limit actions are JSON, the shape the API uses, with
both languages so any client can show them:

```json
{"error":"rate_limited","message":"Too many requests. Wait a moment and try again.","message_ar":"طلبات كثيرة. انتظر قليلًا ثم حاول مجددًا."}
{"error":"not_found","message":"Not found.","message_ar":"غير موجود."}
```

The web apps map these codes to their own strings:

| Key | English | Arabic |
|---|---|---|
| `errors.rateLimited` | Too many requests. Wait a moment and try again. | طلبات كثيرة. انتظر قليلًا ثم حاول مجددًا. |
| `errors.notFound` | Not found. | غير موجود. |
| `auth.turnstile.failed` | We couldn't check that you're a person. Reload the page and try again. | تعذّر التحقق من أنك إنسان. أعد تحميل الصفحة وحاول مجددًا. |
| `auth.turnstile.unavailable` | Sign-in is briefly unavailable. Try again in a minute. | تسجيل الدخول غير متاح مؤقتًا. حاول مجددًا بعد دقيقة. |

Keys go in `web/cloud/src/i18n/{en,ar}.json` and the admin bundle's files (50.32); the existing
i18n tests fail on a key missing in one language.

### 6. HSTS and headers

HSTS (`max-age=31536000`, no `includeSubDomains` or `preload` until every `maktaba.tv` name is
HTTPS-only and checked) is set by the two web Workers and the API on every response. The CSP for
each host is 50.32's.

## Acceptance criteria

- **Given** the zone file,
  **when** a pull request changes it,
  **then** `zone.mjs check` runs offline and fails on a schema error, an unknown field, a rule
  without a reason, a rule naming a relay host, or more rules than the named plan allows.
- **Given** a push to main that changes the zone file and the Cloudflare secrets are set,
  **when** `cloudflare-zone.yml` runs,
  **then** it prints the plan, applies it, reads it back, and fails if anything differs.
- **Given** the applied zone,
  **when** a client sends `GET https://api.maktaba.tv/v1/admin/accounts`,
  **then** the edge answers `404` with the JSON body in §5 and the request never reaches the
  origin.
- **Given** one IP,
  **when** it sends 21 `POST /v1/auth/login` in one minute,
  **then** the 21st gets `429 rate_limited` with English and Arabic messages.
- **Given** a web client,
  **when** it registers without a Turnstile token, or with a used one,
  **then** the API answers `403 turnstile_failed` and creates nothing.
- **Given** the Android app (no web `Origin`),
  **when** it signs in,
  **then** no Turnstile token is asked for, and the per-IP and per-account limits still apply.
- **Given** siteverify times out,
  **when** a web client signs in,
  **then** `503 turnstile_unavailable` with `Retry-After`, and an alert fires (50.10).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (node:test) | `zone.mjs check` on fixtures | valid file; unknown field; rule without reason; rule naming `x.relay.maktaba.tv`; 21 custom rules on Pro; 2 rate limits on Free | pass; fail ×5, each naming the rule |
| T02 | unit | `zone.mjs plan` against a recorded Rulesets API response (fixture, no network) | one rule changed | the diff shows exactly that rule |
| T03 | unit | `zone.mjs apply` against a local fake API (`undici` MockAgent) | apply then read back a different value | exits non-zero |
| T04 | unit (go) | `turnstile.Verifier` with a fake siteverify | success; `success:false`; hostname mismatch; reused token; timeout | ok; 403; 403; 403; 503 + `Retry-After` |
| T05 | integration (go) | api role, fake siteverify | register / login / forgot with web `Origin` and no token; same with no `Origin` | 403 `turnstile_failed`; passes to the normal checks |
| T06 | unit (go) | config | production, `turnstile.enabled`, no secret | the api role refuses to start, naming the setting |
| T07 | unit (vitest) | web sign-in page | `403 turnstile_failed`, `429 rate_limited`, `503 turnstile_unavailable` | the §5 strings, in English and in Arabic (RTL) |
| T08 | unit (vitest) | i18n | every new key | present in `en.json` and `ar.json` |

No test calls Cloudflare. The live rules are checked once by the owner after the first apply
(the rollout below), and then by 50.10's outside probe (`GET api.maktaba.tv/v1/admin/accounts`
expects 404).

## Edge cases

- **A carrier-grade NAT** sends many users through one IP. R2's limit is high for that reason;
  R1 can still trip for a crowd signing in at once (a classroom). The 429 says to wait; the
  API's per-account lockout isn't touched by an edge 429.
- **Homes behind the same NAT as their users** call the API every minute; they are counted in
  R2 and sit far below it.
- **The managed rules block a legitimate request** (a false positive, often on large JSON
  bodies). The Security Events log names the rule; the fix is an exception in the zone file,
  with a reason, through a pull request. During the first 14 days OWASP only logs, for this
  reason.
- **The plan changes** (Pro to Free). `check` fails until the file is rewritten for Free's
  counts, so the downgrade can't silently drop rules.
- **A Turnstile widget in Arabic.** Turnstile follows the page's `lang`; the pages set
  `language: "auto"` and pass `ar` when the UI is Arabic.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `deploy/cloudflare/zone.maktaba.tv.json`, `zone.schema.json` (new) | The zone file and its schema. |
| `tools/cloudflare/zone.mjs`, `zone.test.mjs`, `fixtures/` (new) | `check`, `plan`, `apply`, and their tests. |
| `.github/workflows/cloudflare-zone.yml` (new, 50.34) | `check` on pull requests; `plan` + `apply` on main. |
| `.github/ci-paths.yml` | A `cloudflare` filter: `deploy/cloudflare/**`, `tools/cloudflare/**`, the workflow. |
| `cloud/internal/turnstile/` (new) | Verifier, with an interface for the fake. |
| `cloud/internal/handlers/auth/` | Require the token on the four routes for web origins. |
| `cloud/internal/config/config.go`, `cloud/configs/cloud.example.toml` | `turnstile.enabled`, `turnstile.site_key`; secret from `MAKTABA_CLOUD_TURNSTILE_SECRET`. |
| `web/cloud/src/pages/SignIn.tsx`, `AuthFlows.tsx`, `web/cloud/src/Turnstile.tsx` (new) | The widget and the error strings. |
| `web/cloud/src/i18n/{en,ar}.json` | §5 keys. |
| `cloud/docs/accounts-billing-admin.md` | Turnstile and the edge limits in *Rate limits*. |

### Tests

T01–T03 in `cloudflare-zone.yml`'s check job (path-filtered). T04–T06 in the cloud's Go unit and
integration jobs. T07–T08 in the web unit job.

### Rollout

1. Merge with `managed_rules` in log mode and R1/R2 in **log** (`action: log`) for 7 days.
2. Read the Security Events log for false positives; fix with exceptions.
3. Switch to block through a pull request; OWASP blocks from `owasp_block_from`.

### Rollback

A pull request that reverts the zone file; `apply` writes the previous rules. In an emergency
the owner can disable a rule in the dashboard; the next `plan` shows the drift and the next
`apply` restores the file's state, so the fix must also land in the file.

### Dependencies

50.01 (names), 50.34 (the workflow and token). 50.32 and 50.33 add the admin paths these rules
name.

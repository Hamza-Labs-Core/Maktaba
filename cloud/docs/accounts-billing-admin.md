# Maktaba Cloud — accounts, billing and the service-admin console

The account side of Maktaba Cloud (Epic 25 stories 25.2–25.5, 25.11–25.15,
25.20–25.21, 25.24–25.26): who can sign in, what they pay for, and what the
operator can do about it. The relay, server linking and TLS live elsewhere
(`relay/`, `handlers/servers`, `tls/`).

## Who is who

| Principal | Where it lives | What it can do |
|---|---|---|
| **Cloud account** (`users.role = 'user'`) | cloud `users` table | its own profile, sessions, billing, linked servers |
| **Service admin** (`users.role = 'service_admin'`) | cloud `users` table | everything under `/v1/admin/*` |
| **Home admin / viewer** | a home server's own database | nothing on the cloud — a home role is never a cloud role |

A service admin is granted only from the operator CLI
(`maktaba-cloud admin grant --email …`, see below); no HTTP endpoint can
grant or revoke the role, and neither an email domain nor a home role
confers it. Every `/v1/admin/*` route checks the role in the database on
each request (so a revocation takes effect immediately) and answers a
normal account `403 {"error":"admin_access_denied"}`; the denied attempt
is written to the audit log.

## Auth — `/v1/auth/*`

All bodies are JSON. Errors are `{"error": "<code>", "message": "…"}`.

| Method & path | Body | Success | Errors |
|---|---|---|---|
| `POST /v1/auth/register` | `{email, password, display_name?}` | `202 {"status":"pending_verification"}` — a verification mail goes out; the same 202 comes back if the email is already registered (the owner gets a "someone tried to sign up" mail instead), so registration does not reveal which addresses have accounts | `400 invalid_email`, `400 weak_password` |
| `POST /v1/auth/verify-email` | `{token}` | `200 {"status":"verified"}` | `400 invalid_token` |
| `POST /v1/auth/verify-email/resend` | `{email}` | `202` always | — |
| `POST /v1/auth/login` | `{email, password}` | `200 {access_token, expires_in, user}` + `maktaba_refresh` cookie | `401 invalid_credentials`, `403 email_not_verified` (with `resend_url`), `403 account_suspended`, `429 too_many_attempts` (`Retry-After: 1800`) |
| `POST /v1/auth/refresh` | — (cookie) | same as login, the refresh cookie rotates | `401 no_session`; a rotated token presented again revokes every session of the account and records `refresh_token_replay` |
| `POST /v1/auth/logout` | — (cookie) | `204` | — |
| `POST /v1/auth/password/forgot` | `{email}` | `202` always | — |
| `POST /v1/auth/password/reset` | `{token, password}` | `204`, every session revoked | `400 invalid_token`, `400 weak_password` |
| `GET /v1/auth/oauth/google` | — | `302` to Google (state + PKCE + nonce cookies) | `501 google_disabled` |
| `GET /v1/auth/oauth/google/callback` | — | `302` to `{web_url}/signed-in` with the refresh cookie set | `302` to `{web_url}/sign-in?error=<code>` |
| `GET /v1/auth/oauth/apple` | — | `302` to Apple (`response_mode=form_post`) | `501 apple_disabled` |
| `POST /v1/auth/oauth/apple/callback` | form `code, state` | as Google | as Google |
| `GET /v1/auth/providers` | — | `200 {"google":bool,"apple":bool}`, plus `turnstile_site_key` while Turnstile is on | — |

`user` is `{id, email, email_verified, display_name?, plan, role}`.
Access tokens last 15 minutes; refresh sessions 30 days. The refresh
cookie is host-only (no `Domain`), so it never reaches a home server on a
relay subdomain. Every bearer request also checks that its session is still
alive and the account is not suspended or blocklisted. A password
reset, a password change, a suspension and an account deletion all
revoke every session.

Password policy: 10–256 characters, no leading/trailing whitespace, not in
the embedded breached-password list.

Rate limits: register, login, refresh, verify, resend, forgot, reset and
the OAuth starts are limited per client IP (burst 30, 5/s; the mail-sending
ones burst 5, 1 per 12 s). Ten failed logins for one account in 15
minutes lock it for 30 minutes. A blocklisted IP or account gets `403
blocked`. The client IP is `CF-Connecting-IP` only when the TCP peer is in
`server.trusted_proxies` (CIDRs: in production the local `cloudflared`,
`127.0.0.1/32` and `::1/128`) and the header holds one valid IP; otherwise it is
the TCP peer. `X-Forwarded-For` is never read. The rate limits, the blocklist,
the lockout, the audit log and the access log all use it (Epic 50 story 50.31).
The old `server.trust_proxy_headers` is refused at start.

**Turnstile (Epic 50 story 50.30).** With `[turnstile] enabled = true`, register, login and
`password/forgot` need a `turnstile_token` in the body when the request comes from the web app
(its `Origin` is one of `turnstile.web_origins`, by default `web_url`). The api checks it with
Cloudflare's siteverify (3 s timeout), with the client IP and the expected hostname. A missing,
used or wrong token gets `403 turnstile_failed`; siteverify down gets `503
turnstile_unavailable` with `Retry-After: 30`. Native apps send no `Origin` and are not asked.
`GET /v1/auth/providers` adds `turnstile_site_key` while it is on. The secret comes only from
`MAKTABA_CLOUD_TURNSTILE_SECRET` (a `turnstile.secret` in the file is refused); on with no secret,
the api refuses to start. Off by default.

**At the edge (50.30).** In production Cloudflare also rate-limits the auth routes per IP
(20 a minute) and the API (1,200 a minute), answering `429 {"error":"rate_limited"}` itself.
Both rules start in `log` mode (counted, not blocked) and are switched to `block` in the zone
file after a week of traffic; `deploy/cloudflare/zone.maktaba.tv.json` holds the rules.

## Account — `/v1/account/*` (bearer token)

| Method & path | Body | Success |
|---|---|---|
| `GET /v1/account/me` | — | `{id, email, email_verified, display_name?, locale, avatar_url?, plan, role, created_at, identities:[{provider,email,created_at}], has_password, deletion?: {requested_at, purge_after}}` |
| `PATCH /v1/account/me` | `{display_name?, locale?, avatar_url?}` | the `me` view; `400 display_name_too_long` (>80), `400 invalid_locale`, `400 invalid_avatar_url` (https only) |
| `POST /v1/account/password` | `{current_password, new_password}` | `204` (sessions revoked); `401 wrong_password`, `400 weak_password` |
| `GET /v1/account/sessions` | — | `{"sessions":[{id, user_agent, ip, created_at, last_used_at, expires_at, current}]}` |
| `DELETE /v1/account/sessions/{id}` | — | `204` (`404` if not yours) |
| `POST /v1/account/sessions/revoke-all` | — | `204` |
| `GET /v1/account/export` | — | JSON download of everything the cloud holds about the account |
| `DELETE /v1/account/me` | `{password?}` (required when the account has a password) | `204` — sessions revoked, 30-day hold; `401 wrong_password` |
| `POST /v1/account/deletion/cancel` | — | `204` |

Signing in during the 30-day hold is allowed and the account page offers
"Cancel deletion"; after the hold the worker purges the account.

## Billing — `/v1/billing/*`

| Method & path | Auth | Success |
|---|---|---|
| `GET /v1/billing/plans` | public | `{payments_enabled, plans:[{id, name, price_cents, relay, relay_cap_kbps, max_relay_streams, fair_use_bytes_per_month, allowance_bytes_per_month, pay_as_you_go, payg_price_per_gb_cents, extra_streams, extra_stream_price_cents, max_servers, purchasable, home_tier}]}`: `free`, `basic`, `pro` (Epic 50 story 50.19). A null number is a value not set yet (TBD); a plan without a price can't be bought. `home_tier`: the tier the license gives the account's homes, see Licenses |
| `GET /v1/billing/me` | bearer | `{plan, status, interval?, current_period_end?, cancel_at_period_end, has_customer}` |
| `POST /v1/billing/checkout` | bearer | body `{plan:"basic"\|"pro"}` → `{checkout_url, session_id}`; `422 bad_plan`, `409 already_subscribed` (use the portal), `503 payments_not_set_up` |
| `POST /v1/billing/portal` | bearer | `{portal_url}`; `409 no_customer`, `503 payments_not_set_up` |
| `GET /v1/billing/usage` | bearer | `{month, plan, limit_bytes, used_bytes, servers:[{id, name, bytes_in, bytes_out}]}`: `limit_bytes` is Pro's full-quality allowance or Basic's fair use (0: none). Past it nothing stops; the relay slows (story 50.19) |
| `GET /v1/billing/payg` | bearer | `{enabled, cap_cents, spent_cents, price_per_gb_cents}`: Pro's pay-as-you-go (story 50.19 §5); `price_per_gb_cents` null until the owner sets it |
| `PATCH /v1/billing/payg` | bearer | body `{enabled, cap_cents}`: on needs a cap (`422 cap_required`); adds or removes item 2 at the provider; `409 needs_pro`, `409 price_not_set`, `503 payments_not_set_up` |
| `PATCH /v1/billing/extra-streams` | bearer | body `{count}` (0–100) → `202`: item 3's quantity at the provider (prorated); its webhook puts the count on the account's homes, and the relay allows them within a second |
| `GET /.well-known/maktaba-entitlement-key` | public | `{fingerprint, public_key}` (base64 Ed25519): the license-signing key, whose public half release builds of the home embed |
| `POST /v1/billing/webhook/{provider}` | the provider's signature | `200`; `400 invalid_signature`; `404` unless `{provider}` is the configured provider and it takes payments |

**Payment provider: TBD.** Payments go through the
`billing.Provider` interface (`internal/billing/provider.go`): open a
checkout, open the customer portal, verify and translate a webhook into a
provider-neutral event. `[billing] provider` (env
`MAKTABA_CLOUD_BILLING_PROVIDER`) picks it, and config validation refuses
any provider the cloud does not ship. The only one shipped is `none`, the
default: `payments_enabled` is false, no plan is `purchasable`, checkout and
the portal answer `503 payments_not_set_up` (the web app shows paid plans
with a disabled "Payments coming soon" button), every webhook route is a
404, and a service admin sets plans by hand (`POST /v1/admin/accounts/{id}/plan`,
below).

Webhook handling (once a provider ships): the provider verifies the
delivery, the event is recorded in `provider_events` (keyed by provider and
event id) and applied in the same transaction, so a retried event id is a
no-op and a failed apply is retried by the provider. Events older than the
subscription's last applied event are ignored (delivery order is not
guaranteed). Neutral events: `checkout_completed` (links the provider's
customer), `subscription_started` / `subscription_changed` /
`subscription_cancelled` (plan, status, period end; `canceled`/`unpaid`/
`incomplete_expired` fall back to free), `payment_succeeded` /
`payment_failed` (invoice ledger; failure marks the subscription
`past_due` and mails the payer), `payment_disputed` (suspends the account,
records an abuse signal). A delivery that fails verification records a
`billing_webhook_forgery` abuse signal. Billing rows carry the `provider`
that wrote them (`manual` for a plan set by hand). The effective plan is mirrored onto
`users.plan` and onto the plan of every server the account owns, which is
what the relay's bandwidth gate reads.

## The plans on the relay — Epic 50 story 50.19

The relay decides each forwarded request's mode from the home's plan and this month's bytes by
mode (`billing.ModeFor`, `relay_usage_monthly`), and counts the request's bytes in it:

| Mode | When | What the relay does |
|---|---|---|
| `refused` | Free (no relay) | `402 relay_needs_plan` without forwarding, always; the tunnel stays (direct access, entitlements, push) |
| `full` | Pro within its allowance or pay-as-you-go cap; any paid home while `relay.plans.enforce` is off | forwards untouched |
| `capped` | Basic; Pro past its allowance and cap | adds `X-Maktaba-Relay-Cap-Kbps: 3000` (a client's own copy is removed) and paces media at the cap + 20 % |
| `capped_slow` | past 500 GB of fair use in capped mode | the same with `1500` |

- **Pacing** applies to media under `/stream/` (segments, a live stream, a file), never to
  playlists, subtitles, artwork or the API. It slows, never stops.
- **Streams at once.** A media stream is a session (`/stream/{session}/…`), or a channel or clip
  with the client's address. A new one past the plan's limit (Basic 2; Pro N plus its extra
  streams) is `429 stream_limit`; the ones playing go on. Counted in `relay_streams` (both relay
  colours agree), touched once a minute, and swept by the worker after 2 minutes of silence. A
  counting error admits the stream.
- **Pay-as-you-go.** Pro past its allowance plays at full quality while the account's
  pay-as-you-go is on and its cost this month (every home's `bytes_payg` at the GB price) is
  under the owner's cap; then capped. The worker queues each home's whole GB every hour in
  `billing_usage_outbox` (id `<server_id>:<month>:<hour>`, the provider's idempotency key),
  clamped to the cap's worth, and sends the queue; a provider outage leaves it queued. It
  emails at 80 % and 100 % of the cap, and reconciles daily (`billing_usage_mismatch` over
  1 GB).
- **Switches.** `relay.plans.enforce` (default false): capping, pacing and the stream limit.
  `relay.stream_limit.enabled` (default true): the limit within them. Free is refused either
  way.

## Licenses — Epic 16

A paid plan becomes a **license** its homes verify: Ed25519-signed JSON
`{"license": {license_id, tier, seats, issued_at, expires_at, features?, server_id?, holder?}, "signature"}`,
signed with the entitlement key (`entitlement.private_key_path`). It is the
one license format: the home (`api/internal/subscriptions`) verifies exactly
these bytes, and both modules pin the same golden vector in their tests. The
user-scoped entitlement token (`GET /v1/billing/entitlement`) is gone.

| Cloud plan | Home tier | Seats on the home |
|---|---|---|
| `free` | none (the home's Free: one user, LAN only) | 1 |
| `basic` | `home` (homes released before story 50.19 refuse a tier they don't know, so `basic` keeps the wire name `home` until homes read it) | 4 |
| `pro` | `pro` | unlimited |

| Method & path | Auth | Success |
|---|---|---|
| `GET /v1/servers/{id}/license` | the home's agent (basic auth `id:secret`) | `200 {license, manage_url}`: the owner's plan license for that home (`license_id` `cloud-<server id>`, `server_id` set, issued at the start of the UTC day, valid 31 days); `404 no_license` (free plan); `403 license_revoked` (the id is revoked, or the account is suspended, e.g. a disputed payment); `503 licensing_disabled` (no key) |
| `GET /v1/licenses/revocations` | public | `{revocations:{issued_at, license_ids}, signature}`, signed with the same key; homes fetch it daily |
| `GET /v1/admin/licenses?limit=&offset=` | service admin | `{licenses:[{license_id, kind plan\|offline, user_id?, server_id?, tier, seats, holder, issued_at, expires_at, revoked_at?, revoked_reason?}], total}` |
| `POST /v1/admin/licenses/{id}/revoke` | service admin | `{reason}` → `204`; audited (`admin.license.revoke`) |
| `POST /v1/admin/licenses/{id}/unrevoke` | service admin | `{reason}` → `204`, `404` when not revoked; audited |

The home's relay agent asks for its license every minute on loopback
(`GET 127.0.0.1:8765/admin/cloud-link/license`, passed through with the
server secret only the agent holds), so an upgrade reaches the home within a
minute of the payment provider's webhook (or of a plan set by hand), a cancelled plan returns it to Free, and a
revocation locks it at once. A home cut off from the cloud keeps its license
until it expires (31 days, then 72 hours of clock-skew grace): the 30-day
offline grace.

Offline licenses (for a home not linked to the cloud) come from the CLI:

```sh
# signed with the key piped in (never written to disk); nothing recorded
security find-generic-password -s maktaba-license-signing -w \
  | maktaba-cloud license issue --key-stdin --tier pro --days 365 --holder "Name"
# or with the cloud's own key and database (recorded in licenses_issued)
maktaba-cloud license issue --config cloud.toml --tier home --holder "Name"
maktaba-cloud license pubkey --key-stdin          # the PEM release builds embed
maktaba-cloud license revoke   --id lic_… --reason "…" --config cloud.toml
maktaba-cloud license unrevoke --id lic_… --reason "…" --config cloud.toml
```

## Service admin — `/v1/admin/*` (bearer token + `service_admin`)

The console that calls these is its own web bundle, `web/admin` (`pnpm dev:admin`,
`pnpm build:admin` → `dist-admin/`), for `admin.maktaba.tv` only (Epic 50 story 50.32). The
user app (`web/cloud`) has no admin route, link, string or module, and CI fails a user build
that carries one (`tools/lint/no-admin-in-user-bundle.mjs`).

| Method & path | Body | Success |
|---|---|---|
| `GET /v1/admin/accounts?q=&status=&plan=&limit=&offset=` | — | `{accounts:[{id, email, display_name?, plan, status, role, created_at, last_login_at?, servers}], total}` |
| `GET /v1/admin/accounts/{id}` | — | `{account, subscription, servers:[{id,name,slug,plan,version,online,last_seen_at}], usage:{month, used_bytes, limit_bytes}, sessions_active, abuse:[…], audit:[…]}` |
| `POST /v1/admin/accounts/{id}/suspend` | `{reason}` | `204` — status `suspended`, sessions revoked; `400 reason_required`, `409 cannot_suspend_admin` (self or another service admin) |
| `POST /v1/admin/accounts/{id}/unsuspend` | `{reason}` | `204` |
| `POST /v1/admin/accounts/{id}/plan` | `{plan, reason}` | `204`; sets the plan by hand (subscription provider `manual`, mirrored onto the account and its servers, audited `admin.account.set_plan`, not counted as revenue); `422 bad_plan`, `409 provider_managed` when a payment provider is billing the plan |
| `GET /v1/admin/fleet` | — | `{users_total, users_suspended, servers_total, servers_online, servers_stale, tunnels_live?}` |
| `GET /v1/admin/servers?status=online\|offline&limit=&offset=` | — | `{servers:[{id, name, slug, owner_email, plan, version, online, last_seen_at, last_heartbeat?, relay_latency_ms?}], total}` |
| `GET /v1/admin/revenue` | — | `{currency:"usd", mrr_cents, arr_cents, active_paid, past_due, cancelling, breakdown:[{plan, users, active, mrr_cents}]}` |
| `GET /v1/admin/abuse?limit=` | — | `{signals:[{id, subject_kind, subject, kind, severity, created_at}], blocklist:[{subject_kind, subject, reason, blocked_at, expires_at?}]}` |
| `POST /v1/admin/abuse/block` | `{subject_kind, subject, reason, expires_in_hours?}` | `204` |
| `POST /v1/admin/abuse/unblock` | `{subject_kind, subject, reason}` | `204` |
| `GET /v1/admin/audit?actor=&action=&target=&limit=&offset=` | — | `{entries:[{id, actor_id?, actor_email?, action, target_kind?, target_id?, detail, ip?, created_at}], total}` newest first, ≤ 50 per page |
| `GET /v1/admin/usage?days=30` | — | anonymous usage data (Epic 16.7): installs, versions, platforms, apps and features in use, top error groups, web-vitals p75 — see [usage-data.md](usage-data.md) |

Every admin mutation, every account-detail view and every denied attempt
writes an `audit_log` row (`actor`, `action`, `target`, `detail`, `ip`).
The Epic 30 relay-analytics routes (`/v1/admin/metrics/*`,
`/v1/admin/privacy/*`) sit behind the same role check.

### Granting the role

```sh
maktaba-cloud admin grant  --email ops@example.com   # account must exist and be verified
maktaba-cloud admin revoke --email ops@example.com
maktaba-cloud admin list
```

Each writes an audit row with actor `cli`.

## Mail

Verification, reset and notice mails go through `mail.Mailer`. The only
driver today is `log` (the default): it writes the message, including the
link, to the api log. No mail provider is wired; that needs a provider
account and is a separate decision.

## Web UI

`web/cloud/` is the cloud's own single-page app (same React/Vite stack and
design system as the home web app, separate bundle): sign-in, register,
verify, forgot/reset password, account (profile, sessions, export,
delete), billing (plans, checkout, portal, usage) and, for service
admins, the operator console (accounts, account detail with
suspend/unsuspend, servers, revenue, abuse, audit). `pnpm dev:cloud`
serves it on :5174 and proxies `/v1` and `/.well-known` to the api role at
`CLOUD_API_URL` (default `http://localhost:8090` — :8080 is the home
server's API on a machine that runs Maktaba).

## Audit against Epic 25 (2026-10-26)

What the account side had before this change, and what it has now.

### Broken (fixed)

| What | Effect | Fix |
|---|---|---|
| Admin gate was "email domain = `hamzalabs.com`" with no email verification | anyone registering `x@hamzalabs.com` was an operator; with an empty domain, an address without `@` matched | `users.role = 'service_admin'`, checked in the DB per request, granted only from the CLI |
| `sessions.Create` was passed the request id as the client IP (`::inet` cast) | every register/login/refresh failed with a 500 | the resolved client IP is stored |
| `r.Use` (rate limiter) after `NewRouter` had registered routes | chi panics: the api role could not start | app routes live in a group with their middleware |
| Google `id_token` verifier was never wired (`nil`) | Google sign-in always failed | JWKS verifier (RS256/ES256) checking iss, aud, exp, nonce; PKCE |
| Apple `id_token` decoded without any check; no `state` on the Apple callback; `email_verified` parsed as bool (Apple sends a string) | login CSRF; parse failures | same verifier, state + nonce cookies (`SameSite=None` for Apple's form_post) |
| OAuth callbacks answered JSON to a browser redirect | the browser showed a JSON blob | callbacks set the refresh cookie and redirect to the web app |
| Refresh cookie was scoped to `.maktaba.tv` (found in review) | every home server behind `*.relay.maktaba.tv` received it: account takeover | host-only cookie |
| Rate-limit key was `X-Forwarded-For` or `RemoteAddr` with its port | a client picked its own key; every new connection was a new bucket | TCP peer without port, proxy headers only when `trust_proxy_headers` (since 50.31: only from a peer in `trusted_proxies`) |
| Webhook: `checkout.session.completed` mapped to plan `free`; subscription events carried no user id; duplicate events re-applied; no ordering | a successful checkout downgraded the payer; later events were dropped | metadata `user_id` on the subscription, customer-id fallback, event + apply in one transaction, `last_event_at` ordering |
| Suspension (`status`) was never checked | an account the old `BlockUser` blocked could sign straight back in | login, refresh, OAuth and every bearer request check it |
| CORS did not allow `PATCH` | the profile update failed cross-origin | added |

### Missing (added)

Email verification and resend (25.2), password reset (25.2), per-account
login lockout with `Retry-After` (25.2), refresh-token replay detection
(25.2), timing-equalised unknown-email login (25.2), profile validation
(25.5), sessions/devices list and revoke (25.5), data export (25.5),
deletion with password confirm and cancel (25.5), customer portal through
the payment provider (25.13/25.15), invoice ledger and payment-failed notice (25.14), dispute
suspension (25.14), usage endpoint (25.11), entitlement issuing and
public key (25.26), the whole operator console API and UI (25.20/25.21),
audit log, blocklist management (25.25), sign-up flood check (25.25), a
manual plan change for service admins while no payment provider is set up,
and the cloud web app.

### Still open (not in this change)

- **Payment provider.** Not chosen yet (TBD). It plugs in behind
  `billing.Provider`; until then `[billing] provider = "none"` and plans are
  set by hand.
- **Mail provider.** Mails go to the log (`mail.driver = "log"`). A real
  provider needs an account and a decision; until then verification links
  must be read from the log.
- **Email change** (25.5): needs the mail provider (verify new address,
  notify old one).
- **Avatar upload** (25.5 `413 avatar_too_large`): only an https URL can be
  set.
- **Admin step-up re-auth** (25.20 `acr` freshness) and **admin SSO**: the
  console uses the normal session plus the role. Specced in Epic 50 stories
  50.32–50.33: the console moves to `admin.maktaba.tv` with separate staff
  accounts, mandatory MFA, short sessions and re-authentication for
  sensitive actions.
- **Force-disconnect a server / tunnel revoke frames** (25.20, 25.5): owned
  by the relay work.
- ~~**Server-scoped entitlements** and the home pulling them~~: done as
  plan licenses (Licenses, above; Epic 16).
- **Webhook offload to a worker and nightly reconciliation** (25.14):
  webhooks are applied inline; a 7-day failed-invoice downgrade job is not
  written.
- **JWKS for access tokens** (25.2 says the JWT validates against
  `/.well-known/jwks.json`): access tokens are still HMAC-signed and only
  the cloud verifies them.
- **Redis-backed rate limits** (25.24): the limiter is per process.
- **Native app sign-in** (Sign in with Apple from the iOS SDK posting an
  identity token): only the web flow exists.
- **Paths**: the stories say `/api/...`; the code serves `/v1/...`.

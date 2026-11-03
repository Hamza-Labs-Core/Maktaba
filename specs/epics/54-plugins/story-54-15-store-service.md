# Story 54.15 — The store service, store accounts and linking a home

> Epic 54 · Phase 2 · Depends on: 54.14 · Needed by: 54.16, 54.17, 54.18, 54.19

## Why

Someone has to hold the catalog, the packages, the developer and buyer accounts and the review
queue. The store is **its own service, independent of Maktaba Cloud** (decided 2026-11-03): its
own accounts and sign-in, its own database, its own deploy, its own hostname, and (Phase 3) its
own payment setup. It runs entirely on Cloudflare (Workers, D1, Durable Objects, R2, Queues),
so no VPS is ever needed for it.

### Why independent

- **Different people.** Cloud accounts are home owners paying for remote access. Store
  accounts are plugin developers, reviewers and plugin buyers, many of whom never use the
  cloud. A developer should not need a relay subscription's account to publish a free plugin.
- **Different failure.** A cloud outage must not stop a home from fetching the revocation list,
  verifying a paid plugin's entitlement or installing a plugin, and a store incident (a
  compromised developer account, a flood of uploads) must not touch the relay. Separate data,
  sessions and deploys keep each one's blast radius to itself.
- **Different money.** Cloud billing (Epic 50.19) sells Maktaba's own plans. The store
  (Phase 3) resells third-party plugins with an 85/15 split, payouts and refunds to other
  people; that needs its own seller-of-record choice and provider account. Mixing them would
  put developer payouts and tax under the cloud's billing setup.
- **Smaller to secure.** The store holds the online signing key every home trusts (54.14). It
  is safer behind its own sign-in, with MFA required for everyone who can publish, than as
  one more set of routes in the cloud API.
- **Nothing shared means nothing to unwind later.** The store can change hosting, provider or
  team without a cloud migration.

## Description

### 1. Names and where it runs

| Name | What | Served by | Cloudflare |
|---|---|---|---|
| **`plugins.maktaba.tv`** | the store: listing pages, buyer accounts, the developer console, the store API (`/v1/…`), linking a home (`/link`), and the store-admin pages (`/admin/…`, §7) | Worker `store-web` (Workers custom domain; static assets for the console) | proxied |
| **`plugins-cdn.maktaba.tv`** | public signed files: packages `/{sha256}.mkplugin`, icons, screenshots, `catalog-<api major>.json`, `revoked.json`, `revoked-head.json`, `store-keys.json`; paid packages under `/paid/…` | R2 bucket `maktaba-plugins` as an R2 custom domain (cache hits never reach R2 or a Worker); Worker `store-cdn` on the route `/paid/*` only | proxied |

- Both names are proxied by Cloudflare, so the TLS certificate, WAF and DDoS protection are
  Cloudflare's. Neither points at Hetzner.
- `plugins.maktaba.tv` is on `maktaba.tv` because users touch it (the domain rule: maktaba.tv
  is for users, maktaba.dev for developers' docs). Developer docs stay on
  **maktaba.dev/plugins**. maktaba.app is not ours and is never used.
- Code lives in a new top-level folder **`store/`** (TypeScript Workers, `wrangler.toml` per
  Worker), with nothing imported from `cloud/`. Shared logic with the home is shared through
  files, not code: the manifest JSON schema (`shared/plugins/manifest.schema.json`), signature
  test vectors (`shared/plugins/testdata/signatures/`) and reason-code word lists, so the
  TypeScript signer and the Go and Rust verifiers are checked against the same vectors.

| Piece | Cloudflare product | Notes |
|---|---|---|
| Console, listing pages, store API, accounts, sessions | Workers (`store-web`) | server-rendered listing pages for search engines; the console is a React app served as Workers static assets |
| Accounts, homes, listings, versions, submissions, reviews, ratings, reports, revocations, products, purchases, entitlements | **D1** (SQLite) | split into four databases from the start (§3) so no one database nears the size limit |
| Strongly consistent per-entity state | **Durable Objects** | `AccountGuard` (sign-in attempts and lockout per account), `IpBucket` (rate limits per IP and per /64), `PairingCode` (one per active link code), `CatalogWriter` (one writer that serialises catalog and revocation regeneration) |
| Packages, source archives, icons, signed lists, nightly exports | **R2** | public bucket `maktaba-plugins`; private bucket `maktaba-plugins-private` (source archives, check reports, exports) never on a public domain |
| Background work | **Queues** + Cron Triggers | `store-checks` (54.16), `store-catalog` (regenerate lists), `store-mail`; crons for the revocation re-sign, the monthly DNS prefix check, nightly exports |
| Upload checks | Queue consumer Worker (static checks) and **Cloudflare Containers** (sandbox run), with a CI-runner fallback | 54.16 §1 |
| Email | Cloudflare Email Service (sending), or a non-Stripe provider such as Amazon SES or Postmark | owner decision (README §14 item 10); every message in English and Arabic |
| Payment webhooks (Phase 3) | Worker route `POST /v1/payments/webhook/<provider>` | 54.19 |

**No shared tables, sessions or cookies with the cloud.** The store never reads the cloud
database, never accepts a cloud session or token, and never calls `api.maktaba.tv`. Its cookie
is `__Host-mks_session` (host-only, `Secure`, `HttpOnly`, `SameSite=Lax`), so it is never sent
to another `maktaba.tv` name and no other name can set it. A request carrying a cloud cookie or
cloud bearer token is treated as signed out (T08).

**It works when the cloud is down**, and the cloud works when the store is down: nothing on
either path calls the other.

### 2. Limits, stated honestly

Cloudflare's published limits as read on 2026-11-03 (Workers Paid). They are rechecked when
the store is built; the design keeps headroom on each.

| Limit | Value | What the store does about it |
|---|---|---|
| D1 database size | 10 GB per database | four databases (§3); the largest, `store-installs`, stays under 1 GB at 5 million home × plugin rows; old `seen` rows are rolled up monthly |
| D1 writes | one writer per database; simple writes in the low thousands per second | the heaviest write path is the daily `seen` report (§5.4): 50,000 homes is under 1 write a second on average, spread with jitter; catalog regeneration is one serialised job (`CatalogWriter`) |
| D1 queries per Worker invocation | 1,000 | batch statements; regeneration runs in a Queue consumer in pages |
| D1 row / value size | 2 MB | manifests and check reports over 256 KB go to R2 with the key in D1 |
| Worker request body | 100 MB | uploads never pass through a Worker: `mkp submit` gets **presigned R2 multipart upload URLs** (50 MB package, 200 MB source archive) and the Worker only records the result |
| Worker CPU per request | 30 s default, up to 5 min on Paid | passkey (WebAuthn ES256 / EdDSA through WebCrypto) and TOTP (HMAC-SHA1) checks take well under 5 ms; Ed25519 signing of a catalog is milliseconds; password hashing is the one heavy step (Argon2id in Wasm, about 50–150 ms, `cpu_ms` limit 1 s on the sign-in route) |
| WebCrypto PBKDF2 | capped at 100,000 iterations in Workers | not used for passwords; Argon2id (m = 19 MiB, t = 2, p = 1, the OWASP minimum) |
| Worker memory | 128 MB | Argon2id at 19 MiB fits; package inspection streams from R2 |
| Durable Object | single-threaded per object | one object per account, per IP bucket, per pairing code: contention is per entity by design |
| Queues message size | 128 KB | messages carry ids, never package bytes |
| D1 Time Travel | 30 days of point-in-time restore | plus nightly exports (§9) |
| Cloudflare Containers | public beta from June 2025; its maturity, instance sizes (a few vCPU, about 12 GiB at most in the beta) and concurrent instances per account are **to be checked at build time**, and whether the plugin sandbox profile applies inside one needs a spike | 54.16 §1: a CI-runner fallback with no secrets and no network |

### 3. Data (D1)

| Database | Tables |
|---|---|
| `store-accounts` | `accounts` (id, email, email_verified_at, display_name, password_hash?, status, created), `account_mfa` (account, kind `totp` \| `passkey`, credential id, public key or TOTP secret encrypted with a Worker secret, added, last used), `recovery_codes` (hashed, single use), `sessions` (id hash, account, created, last seen, mfa_at, user agent summary), `email_tokens`, `roles` (account, role, granted by, at), `audit` (actor, action, target, at, ip prefix, result) |
| `store-catalog` | `developers` (account, verified prefixes, agreement version accepted, seller fee paid, payout ref (Phase 3), status), `developer_keys` (developer, key id, public key, added, revoked), `plugins` (id, developer, names and summaries en/ar, category, tags, status `draft` \| `listed` \| `delisted` \| `removed`), `versions` (plugin, version, sha256, size, manifest R2 key, capabilities, maktaba range, plugin API, review level, status `submitted` \| `checking` \| `in_review` \| `approved` \| `rejected` \| `withdrawn`, release notes en/ar), `submissions` (version, uploaded by, source archive R2 key, checks report R2 key, reviewer, decision, reasons), `revocations`, `ratings`, `reports` (54.18) |
| `store-installs` | `homes` (§5), `installs` (home, plugin, version, first/last seen), `install_counts` (monthly roll-up) |
| `store-commerce` (Phase 3) | `seller_fees`, `products`, `purchases`, `entitlements`, `payouts`, `provider_events` (event id unique, for idempotent webhooks) (54.19) |

Migrations in `store/migrations/<db>/NNNN_*.sql`, applied by the deploy workflow before the new
Worker version takes traffic; additive only, so the previous version keeps working during a
deploy.

### 4. Store accounts

One account system for everyone who uses the store. Roles are store-side and enforced in the
store API on every route, never only in the UI:

| Role | Who | Gets it by | MFA |
|---|---|---|---|
| `buyer` | anyone with a verified email | signing up | offered (TOTP or passkey) |
| `developer` | a buyer who accepted the developer agreement | the console's **Become a developer** / «كن مطوّرًا» | **required** before any submit, key or listing change: TOTP or a passkey |
| `store_reviewer` | people named by the owner to review | granted by a `store_admin` | **passkey required** (phishing-resistant) |
| `store_admin` | the store's operators | granted by another `store_admin`; the first one by a one-time bootstrap token the owner sets as a Worker secret, consumed on use and then deleted | **passkey required** |

- **Sign-up and email verification.** Email and password (Argon2id), or a passkey alone. A
  single-use link valid 24 hours verifies the email; nothing but browsing works until it is
  verified. Messages: "Check your email to finish signing up" / «تحقّق من بريدك الإلكتروني
  لإكمال التسجيل». Changing the email verifies the new address and notifies the old one.
- **MFA.** TOTP (RFC 6238, 30 s, ±1 step, a used code refused a second time) and passkeys
  (WebAuthn, user verification required, several per account). Ten single-use recovery codes
  are shown once when the first factor is added. A passkey with user verification counts as
  two factors on its own.
- **Re-authentication for sensitive actions.** These need a factor confirmed in the last
  5 minutes (`sessions.mfa_at`), else `401 reauth_required` and the console asks again
  ("Confirm it's you" / «أكّد أنّك أنت»): adding or revoking a signing key; changing email,
  password or MFA factors; accepting a new agreement version; submitting a version;
  withdrawing a plugin; payout and price changes (Phase 3); linking or unlinking a home;
  deleting the account; every reviewer decision and every store-admin action (granting roles,
  revocations, suspensions).
- **Lockout and rate limits** (Durable Objects): 5 failed sign-ins on an account lock it for
  15 minutes, doubling up to 24 hours, with an email to the owner of the account; per-IP and
  per-/64 limits on sign-up, sign-in, email sends and pairing-code entry. A lockout never
  reveals whether an email has an account.
- **Sessions.** 30 days idle for buyers, 12 hours for developers, 8 hours for reviewers and
  store admins; listed in the account page with **Sign out everywhere** / «تسجيل الخروج من
  كل الأجهزة»; all sessions end on a password or factor change.
- **Account recovery.** With a recovery code, as a sign-in. Without any factor: a
  `store_admin` can reset MFA only after a 7-day hold, emailing every address on the account
  at the start and the end; a developer's signing keys are suspended until a new factor is
  added (a stolen inbox alone cannot publish).
- **Signing keys belong to the developer account** (54.14): added from
  `mkp keys export-public`, revoked from the console, at least one active key to submit. A
  suspended or deleted developer account suspends its keys.
- **Id prefix:** prove a domain with a DNS TXT record `maktaba-developer=<token>` (checked by
  the store over DNS-over-HTTPS, re-checked monthly by a Cron Trigger), or take a community
  prefix `tv.maktaba.community.<handle>`.
- **Agreement:** versioned; a new version must be accepted before the next submission.
- Every role and sensitive action writes an `audit` row.

### 5. Linking a home to a store account

A home needs a store account only to buy and install paid plugins and to rate. Free plugins
install without one (the catalog and packages are public on the CDN).

1. **Start.** In the home, Settings → Plugins → **Store account** → **Link this home** /
   «اربط هذا الجهاز الرئيسي» (home admin only, role-tested). On the first link the home makes
   an Ed25519 **home store key** and keeps the private half in its secrets store. It calls
   `POST plugins.maktaba.tv/v1/homes/pair {public_key, server_version, platform}` and gets
   `{device_code, user_code, verification_uri, expires_in: 600, interval: 5}` (the
   device-authorisation shape of RFC 8628, as the TV pairing in plan 10-17 does).
2. **Show.** The home shows the 8-character `user_code` (`KQ7M-4TXP`, no look-alike letters)
   and a QR code for `https://plugins.maktaba.tv/link`. The code is never in the URL.
3. **Confirm.** The admin signs in to their store account on any device, opens `/link`, types
   the code, sees the home's name, version and platform, and confirms (re-auth applies). The
   `PairingCode` Durable Object allows 5 wrong entries per code, then kills it.
4. **Finish.** The home polls `POST /v1/homes/pair/poll {device_code}` every 5 s and gets
   `{home_id}`. The store records `homes (id, account, public_key, name, linked_at)`.
5. **Use.** Afterwards the home signs each store request with its home store key
   (`Authorization: MKS-Home id=…, ts=…, sig=…` over method, path, timestamp and body hash;
   ±5 minutes; a nonce cache in the `IpBucket` object stops replays). There is no bearer
   secret to leak.
6. **Unlink** from the home or from the account's Homes page. An account can link up to 10
   homes; a home links to one account at a time.

The home id here is the store's own id, not the cloud's home id. A home linked to the cloud
and to the store has two unrelated ids, and neither service learns the other's.

What a linked home sends (§10): `seen` once a day (installed plugin ids and versions, server
version, platform), only when telemetry is on; entitlement refreshes (54.19); rating
eligibility checks (54.18).

### 6. The developer console

`plugins.maktaba.tv/developers`: plugins, versions with their check reports and review status,
keys, the agreement, stats (installs by version, ratings, reports summary), MFA and sessions,
and (Phase 3) seller fee, prices and payouts. English and Arabic, RTL in Arabic, dark and light
themes.

### 7. Store admin and reviewer pages: on the store host

Review and store-admin pages are at **`plugins.maktaba.tv/admin/…`**, not on
`admin.maktaba.tv`. `admin.maktaba.tv` is the cloud's operator console (Epic 25.20), signed in
through the cloud's own SSO and served from the cloud's nodes; putting store review there
would tie the store to cloud accounts and cloud uptime, the two things this decision removes.
On the store host the pages share the store's one account system, one deploy and one audit
trail. Same-origin risk is handled server-side:

- every `/v1/admin/*` route checks `store_reviewer` or `store_admin` on the server, with role
  tests (§8, T01); hiding the menu is not the guard;
- passkey-only MFA for both roles and re-auth within 5 minutes for every decision;
- a separate JavaScript bundle loaded only after the role check, a strict CSP, `noindex`,
  `Cache-Control: no-store`, and a Cloudflare WAF rate-limit rule on `/admin` and `/v1/admin`;
- **no Cloudflare Access**: sign-in is the store's own.

### 8. Store API

| Route | Who | What |
|---|---|---|
| `POST /v1/accounts`, `/v1/sessions`, `/v1/mfa/*`, `/v1/reauth` | public / signed in | §4 |
| `POST /v1/developers` | buyer, verified email | accept the agreement, become a developer |
| `POST /v1/developers/keys`, `DELETE /v1/developers/keys/{id}` | developer (MFA, re-auth) | signing keys |
| `POST /v1/submissions` | developer (MFA, re-auth) | starts a submission; answers presigned R2 upload URLs |
| `POST /v1/submissions/{id}/uploaded` | developer | records the uploads, queues checks (54.16) |
| `GET /v1/submissions/{id}` | the developer, reviewer | status and report |
| `POST /v1/plugins/{id}/withdraw` | the developer (re-auth) | delist own plugin |
| `POST /v1/homes/pair`, `/v1/homes/pair/poll` | a home (no account) | §5 |
| `POST /v1/homes/link` | buyer (re-auth) | confirm a `user_code` |
| `DELETE /v1/homes/{id}` | the account, or the home (signed) | unlink |
| `POST /v1/homes/{id}/seen` | the home (signed) | installed plugins, for counts and rating eligibility |
| `/v1/admin/*` | `store_reviewer`, `store_admin` | review queue, decisions, revocations, roles, suspensions |
| `plugins-cdn.maktaba.tv/catalog-{api-major}.json`, `/revoked.json`, `/revoked-head.json`, `/store-keys.json`, `/{sha256}.mkplugin` | public (CDN) | signed files |
| `plugins-cdn.maktaba.tv/paid/{sha256}.mkplugin?t=…` | a home with a valid entitlement (54.19) | short-lived signed URL, checked by `store-cdn` |

The catalog: `{version, issued, expires (+7 d), api_major, plugins: [{id, developer, names,
summaries, icon, category, tags, rating avg/count, installs (rounded), price, versions:
[{version, sha256, size, url, maktaba, plugin_api, platforms, capabilities, review_level,
released}]}]}`, signed by S (54.14). Only `approved` versions of `listed` plugins appear; one
catalog per plugin API major. `CatalogWriter` regenerates the catalog and revocation files on
every change and on a timer, writes them to R2 and purges their cache keys, so homes keep
working from the CDN while the Workers or D1 are down.

### 9. Backups and restore

- **D1 Time Travel**: any of the four databases can be restored to any minute of the last
  30 days.
- **Nightly export**: a Cron Trigger exports each database (D1 export) to
  `maktaba-plugins-private/exports/<db>/<date>.sql.gz`, encrypted with a key held outside
  Cloudflare by the owner, kept 90 days by an R2 lifecycle rule.
- **R2** objects are content-addressed and never overwritten; the bucket has object versioning
  on signed lists.
- **Restore drill** in CI weekly: the newest export fixture format is imported into a local D1
  (Miniflare) and the store's integration tests run against it. No live Cloudflare call; a
  live restore is a runbook step (`docs/runbooks/store-restore.md`).
- **Signing keys** are not in the exports. The online key S is a Worker secret, with an
  offline copy held by the owner; a lost S is handled by the rotation drill (54.14 §5).

### 10. Privacy

The store keeps, for linked homes only, the store's home id, the plugin ids and versions
installed, the server version and the platform. It never receives libraries, titles, plugin
data or logs. `seen` reports are skipped when the owner turned telemetry off; then counts and
rating eligibility use only downloads made with the home's signature. Unlinked homes fetch
public files from the CDN and send nothing identifying.

### 11. Deploy and cost

- **Deploy** with GitHub Actions only: `store-deploy.yml` runs on a tag `store-v*` after the
  `store` job passes on that commit. It applies D1 migrations, then `wrangler deploy` for each
  Worker with gradual deployment (10 % → 100 % over 30 minutes, auto-rollback on an error-rate
  rise), using the org secrets `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID`. The token
  used is a separate one scoped to the store's Workers, D1 databases, R2 buckets, Queues,
  Durable Objects and Containers; it cannot edit DNS or the cloud's Workers. Nothing is
  deployed from a laptop.
- **Cost (estimate, list prices read on 2026-11-03, to recheck):**

| | Launch (≤ 1,000 linked homes, ≤ 50 plugins, ≤ 200 checked versions a month) | Growth (50,000 homes, 2,000 plugins, 1,000 checked versions a month, 500 GB in R2) |
|---|---|---|
| Workers Paid base (includes 10 M requests, 30 M CPU-ms) | $5 | $5 (about 6 M requests a month; public files are CDN cache hits, not Worker requests) |
| D1 (included: 25 B rows read, 50 M rows written, 5 GB) | $0 | $0 (about 3 M rows written, 2 GB) |
| Durable Objects, Queues | $0 | about $1 |
| R2 ($0.015 / GB-month, no egress fee) | $0 (under 10 GB free) | about $7.50 |
| Upload checks on Containers | about $0–2 | about $45 (1,000 runs × 15 min × 2 vCPU, 4 GiB) |
| Email (if not Cloudflare's) | about $0–1 | about $5 (50,000 messages) |
| **Total** | **about $5–8 a month** | **about $60–65 a month** |

  Cloudflare's Workers Paid base is per account, so if Maktaba's account already pays it for
  Epic 50's broadcast edge, the store adds only usage. With the CI-runner fallback for checks
  (54.16), the Containers line becomes GitHub Actions minutes. Payment provider fees
  (Phase 3) are separate and taken from each sale.

## Acceptance criteria

- **Given** a developer without MFA,
  **when** they try to submit,
  **then** `403 mfa_required` («يلزم التحقق بخطوتين»).
- **Given** a developer whose last factor check was 10 minutes ago,
  **when** they add a signing key,
  **then** `401 reauth_required`, and after confirming a factor it succeeds.
- **Given** an unverified email,
  **when** the account tries to become a developer, buy or rate,
  **then** `403 email_unverified`.
- **Given** developer A,
  **when** they call any route on developer B's plugin,
  **then** 403, and the attempt is audited.
- **Given** a `store_reviewer` with only a TOTP factor,
  **when** they open the review queue,
  **then** `403 passkey_required`.
- **Given** a valid Maktaba Cloud session cookie or bearer token,
  **when** sent to any store route,
  **then** the store treats the request as signed out.
- **Given** a home showing a link code,
  **when** an admin confirms it on `/link`,
  **then** the home's poll returns its store home id within 10 s, and later requests signed
  with the home store key are accepted while a replayed one is refused.
- **Given** a link code entered wrongly 5 times,
  **when** the right code is entered,
  **then** it is refused (`410 code_expired`) and the home must start again.
- **Given** an approved version,
  **when** the catalog regenerates,
  **then** it appears with its sha256 and review level, and the catalog verifies with 54.14.
- **Given** the store Workers and D1 unavailable (and, separately, the cloud API down),
  **when** a home fetches the catalog, the revocation list and a free package,
  **then** all come from the CDN and verify.
- **Given** a prefix claimed by DNS and the TXT record removed,
  **when** the monthly check runs,
  **then** new submissions under it are blocked and the developer is emailed; listed plugins
  stay.
- **Given** 5 failed sign-ins,
  **when** the sixth is tried with the right password,
  **then** it is refused until the lockout ends, and the account's email is told.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | role test (ts, Miniflare) | every store route × no one, buyer, unverified, developer of another plugin, the developer, reviewer, store admin, home signature | call | per §4 and §8; refusals change nothing |
| T02 | integration (ts, Miniflare D1 + R2) | submit → approve (54.16 stubbed) | regenerate | catalog contains it; signature valid with the Go verifier (shared vectors) |
| T03 | integration (go home + fixture CDN) | store Workers and D1 stopped | home fetch | catalog, revocation list and free package work |
| T04 | unit (ts) | DNS TXT check (fake DoH) | verify / lapse | per AC |
| T05 | integration (ts) | `seen` with a bad, an old and a replayed home signature | POST | 401 each |
| T06 | unit (web) | console, `/link` and account pages en/ar | render | RTL, every string in both, dark snapshots |
| T07 | CI (`store`) | full flow with test keys in Miniflare, no live calls | e2e | pass |
| T08 | integration (ts) | cloud cookie, cloud JWT, a `Domain=.maktaba.tv` cookie | call store routes | signed out; never read |
| T09 | unit (ts) | TOTP window, reuse, passkey assertion (fixture authenticator), recovery code single use | verify | per §4 |
| T10 | integration (ts) | re-auth table: each sensitive route with `mfa_at` 4 and 6 minutes old | call | 200 / 401 `reauth_required` |
| T11 | integration (ts, Durable Objects) | lockout and pairing-code limits | brute force | locked; code killed after 5 |
| T12 | integration (go home + ts store) | full device-code link, unlink, relink | flow | per §5; home role test: no one / viewer / admin → 401 / 403 / 200 |
| T13 | CI weekly | export fixture into local D1 | restore drill | integration tests pass |
| T14 | CI (`store`) | dependency check | grep `store/` | no import from `cloud/`, no `api.maktaba.tv` call |

## Edge cases

- **Plugin id squatting** (`dev.example.plex`): ids containing trademarks of others are flagged
  for review (a reviewer-maintained list); the agreement covers removal.
- **Developer deletes their account**: plugins are delisted; keys revoked for new uploads;
  installed copies keep working; paid entitlements stay valid until their end (54.19).
- **A home's secrets are lost** (reinstall without backup): the home links again with a new
  key; the account's Homes page shows the old one, which the admin unlinks; purchases move
  with 54.19's move rule.
- **Cloudflare itself is down**: homes keep running every installed plugin; entitlements are
  valid offline until `valid_until` (31 days rolling); revocation lists expire after 48 hours
  and only warn (H10).

## Build plan

### Files to touch

| File | Change |
|---|---|
| `store/web/` (new) | Worker `store-web`: routes, sessions, MFA, re-auth, pairing, admin, listing pages, console static assets. |
| `store/cdn/` (new) | Worker `store-cdn`: `/paid/*` signed URLs. |
| `store/workers/catalog/`, `store/workers/mail/` (new) | Queue consumers and crons. |
| `store/objects/` (new) | Durable Objects `AccountGuard`, `IpBucket`, `PairingCode`, `CatalogWriter`. |
| `store/signing/` (new) | Ed25519 signer (54.14 vectors). |
| `store/migrations/{accounts,catalog,installs,commerce}/` (new) | D1 schema. |
| `store/wrangler.*.toml` (new) | Bindings; no secrets in files. |
| `api/internal/plugins/storelink/` (new) | Home side of §5: key, pairing, signed requests; admin-only routes. |
| `web/src/pages/Admin/Plugins/StoreAccount.tsx` (new) | Link / unlink, code and QR; en + ar. |
| `.github/workflows/store.yml`, `store-deploy.yml` (new) | T07, T13, T14; deploy. |
| `docs/runbooks/store-restore.md` (new) | Live restore steps. |

### Tests

- Role T01, T12; integration T02, T03, T05, T08, T10–T12; unit T04, T06, T09; CI T07, T13,
  T14. No live payment, DNS, email or Cloudflare calls: Miniflare and fakes only.

### Rollout

Deployed dark (no listed plugins, sign-up closed) by `store-deploy.yml`. Opening the store to
developers is an owner decision after the root keys exist (54.14), the first `store_admin` is
bootstrapped, the email provider is chosen and the reviewers are named.

### Rollback

`wrangler rollback` to the previous Worker version (migrations are additive); D1 Time Travel
for data. Delisting everything empties the catalog; homes keep their installed plugins.

### Dependencies

54.14. No dependency on Epic 25 or Epic 50: the store uses neither cloud accounts nor cloud
infrastructure.

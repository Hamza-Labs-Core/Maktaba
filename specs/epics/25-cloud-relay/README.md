# Epic 25 — Maktaba Cloud: Relay, Accounts & Subscriptions

> **Status:** spec. **Source:** `specs/epics/25-cloud-relay/`.
> **Anchors:** [`architecture.md` §13](../../architecture.md#13-cloud-relay-architecture).

## Goal

Maktaba Cloud is the hosted SaaS layer operated by **HamzaLabs** that turns
the self-hosted Maktaba Server (Epics 01–24) into a remotely-reachable,
cross-network product. It is the difference between "an app on my LAN" and
"my library, on every screen, anywhere I am" — the same value Plex Cloud
Relay provides for Plex.

The cloud has **five jobs** and one non-job:

1. **Identity.** A single user account that follows the user across every
   client (web, iOS, Android, tvOS, Android TV, desktop). Sign up with
   email + password, Google, or Apple.
2. **Linking.** A user binds one or more self-hosted Maktaba Servers to
   their account using a short-lived **claim token** generated on the
   server. After linking, the cloud knows which server belongs to whom.
3. **Reachability.** Each linked server holds a persistent outbound
   **WebSocket tunnel** to the cloud. The cloud advertises a public DNS
   name (`username.maktaba.app`) and TLS termination; client traffic
   enters at the edge, is multiplexed onto the tunnel, and reaches the
   server **without the user opening a port, configuring DDNS, or
   buying a static IP**.
4. **Push.** The cloud is the only entity that holds APNs/FCM credentials
   and the registration tokens for each device. Servers POST push events
   to the cloud, the cloud fans out to APNs/FCM. Servers never see the
   raw device tokens, and APNs/FCM keys never leave the cloud.
5. **Billing.** Stripe-hosted checkout and customer portal; the cloud is
   the system-of-record for entitlements (free / pro / family). Servers
   pull a signed entitlement blob and gate cloud-only features against
   it. Local features (LAN streaming, transcription, library mgmt) are
   **never gated by the cloud**.

The non-job: the cloud **never stores user media**, **never sees
unencrypted media bytes** for direct-play traffic, and **does not
transcribe, index, or analyze** content. The relay is a TLS-passthrough
proxy. This is what makes the cloud cheap to run and the privacy story
defensible.

## How Maktaba Cloud differs from the local server

| Aspect              | Self-hosted Server (Epics 01–24)              | Maktaba Cloud (Epic 25)                            |
|---------------------|-----------------------------------------------|----------------------------------------------------|
| Operator            | The user                                      | HamzaLabs                                          |
| Hosting             | User's NAS / Mac / Linux box                  | Hetzner (`eu-central`) + Cloudflare edge           |
| Stores media        | Yes (the whole point)                         | **No.** Relays bytes, never persists payload       |
| Stores credentials  | Local users + signed URL keys                 | Email/OAuth identities + Stripe customer ID        |
| Auth model          | Bearer + signed URLs (Epic 10)                | OAuth 2.1 (email + Google + Apple) + sessions      |
| DB                  | Postgres / SQLite local                       | Postgres on Hetzner (managed, daily snapshots)     |
| Runtime             | Go API, Go Streaming, Python Pipeline         | Single Go binary (`maktaba-cloud`)                 |
| Required for use?   | **Yes** (no cloud, no app)                    | **No.** Free tier = LAN only; cloud is opt-in      |
| What pays for it    | Nothing (user runs it)                        | Stripe subscriptions; free tier subsidized by paid |

The cloud is **not the API server**. It does not implement library CRUD,
search, or streaming. It implements identity, relay, push, and billing —
nothing else. All product logic stays in the Epic 07 API service running
on the user's box.

## Stories

| #     | Story                                                   | Summary |
|-------|---------------------------------------------------------|---------|
| 25.1  | [Cloud API service bootstrap](story-25-01-cloud-api-bootstrap.md) | Single Go binary, Postgres on Hetzner, Cloudflare in front, structured logs, health endpoints. |
| 25.2  | [Email + password registration](story-25-02-email-registration.md) | argon2id, email verification, account lockout, password reset. |
| 25.3  | [Google OAuth sign-in](story-25-03-google-oauth.md)     | OIDC code flow with PKCE; account-link if email already exists. |
| 25.4  | [Apple OAuth (Sign in with Apple)](story-25-04-apple-oauth.md) | Apple OIDC + private-relay email handling; required for App Store. |
| 25.5  | [Account profile & deletion](story-25-05-account-profile.md) | Display name, avatar, locale, timezone; GDPR delete + data export. |
| 25.6  | [Server claim token flow](story-25-06-server-claim-token.md) | 8-char token generated on server, redeemed on cloud, TLS-pinned. |
| 25.7  | [WebSocket relay tunnel — server side](story-25-07-relay-tunnel-server.md) | Server holds outbound WSS, reconnects with backoff, frames a multiplexed stream. |
| 25.8  | [WebSocket relay tunnel — cloud side](story-25-08-relay-tunnel-cloud.md) | Cloud accepts tunnels, registers them in a connection table, demuxes per-server. |
| 25.9  | [HTTP relay proxy](story-25-09-http-relay-proxy.md)     | Edge HTTP server tunnels client → server requests over the WS frames. |
| 25.10 | [Direct-connection probe & LAN fallback](story-25-10-direct-connection-fallback.md) | Clients try LAN first; relay is the fallback path, not the default. |
| 25.11 | [Bandwidth metering & accounting](story-25-11-bandwidth-metering.md) | Per-server, per-day byte counts; rolled up to invoices. |
| 25.12 | [Tier enforcement (concurrent streams + caps)](story-25-12-tier-enforcement.md) | Free: 0 relay GB. Pro: 100 GB/mo. Family: 500 GB/mo + 5 streams. |
| 25.13 | [Stripe checkout session](story-25-13-stripe-checkout.md) | `POST /api/billing/checkout` returns a Stripe-hosted URL. |
| 25.14 | [Stripe webhook handler](story-25-14-stripe-webhook.md) | Idempotent processing of `customer.subscription.*`; signed entitlements. |
| 25.15 | [Plan comparison & subscription UI](story-25-15-plan-comparison-ui.md) | Public marketing page; in-app upgrade CTAs; change-plan flow. |
| 25.16 | [Server status dashboard](story-25-16-server-status-dashboard.md) | Online/offline, last-seen, version, software updates available. |
| 25.17 | [Push notification ingest](story-25-17-push-notification-ingest.md) | Server POSTs `{user_id, payload}`; cloud authenticates the server. |
| 25.18 | [APNs dispatcher](story-25-18-apns-dispatcher.md)       | iOS / iPadOS / tvOS device tokens; team key + JWT; `BadDeviceToken` cleanup. |
| 25.19 | [FCM dispatcher](story-25-19-fcm-dispatcher.md)         | Android + web push; Firebase Admin SDK; topic-less per-device send. |
| 25.20 | [Admin: user & server fleet](story-25-20-admin-fleet.md) | HamzaLabs-only console; search users, see linked servers, last-seen. |
| 25.21 | [Admin: revenue dashboard](story-25-21-admin-revenue.md) | MRR, ARR, churn, LTV, plan mix; pulled from Stripe. |
| 25.22 | [Subdomain provisioning (`username.maktaba.app`)](story-25-22-subdomain-provisioning.md) | Username uniqueness, DNS via Cloudflare API, name reservations. |
| 25.23 | [TLS at the edge (wildcard ACME)](story-25-23-tls-edge.md) | `*.maktaba.app` issued via Let's Encrypt DNS-01; rotation; cert pinning posture. |
| 25.24 | [Rate limiting & quota](story-25-24-rate-limiting.md)   | Per-IP, per-user, per-server; redis-backed sliding window; 429 with Retry-After. |
| 25.25 | [Abuse detection & response](story-25-25-abuse-detection.md) | Anomalous traffic, port-scan-via-relay, hot link, bot signups; suspend playbook. |
| 25.26 | [Cloud-server entitlement signing](story-25-26-entitlement-signing.md) | Ed25519-signed JSON the server caches; reuses Epic 16 license-validation pattern. |

### Server distribution & installation (Phase 6)

The cloud is half the product; the other half is **getting Maktaba
Server installed**. These stories cover the per-platform packaging,
auto-update, first-run, and uninstall paths so a user goes from
"clicked download" to "scanning my library" with no manual config.
The cloud-side is the value proposition; the installer is the
acquisition funnel.

| #     | Story                                                   | Summary |
|-------|---------------------------------------------------------|---------|
| 25.27 | [macOS installer](story-25-27-macos-installer.md)       | Signed + notarized DMG; Homebrew cask; LaunchAgent; menu bar; Sparkle auto-update. |
| 25.28 | [Windows installer](story-25-28-windows-installer.md)   | EV-signed MSI + NSIS; Windows Service; tray; firewall rule; UAC + per-user fallback. |
| 25.29 | [Linux packages](story-25-29-linux-packages.md)         | `.deb` + `.rpm` repos, Snap, Flatpak, AppImage, systemd unit, `maktaba` user. |
| 25.30 | [Official Docker image](story-25-30-docker-image.md)    | Multi-arch (amd64+arm64) `ghcr.io` image; reference compose with Postgres + secrets. |
| 25.31 | [NAS support](story-25-31-nas-support.md)               | Synology SPK, QNAP QPKG, TrueNAS app, Unraid template; vendor-correct UID/GID. |
| 25.32 | [Raspberry Pi & ARM builds](story-25-32-raspberry-pi-arm.md) | Pi 4/5 + Jetson + RK3588 profiles; CPU Whisper; SD-card warnings; setup script. |
| 25.33 | [One-click cloud-VPS deploy](story-25-33-one-click-cloud-deploy.md) | DigitalOcean Marketplace, Hetzner Cloud App, Railway template, generic shell. |
| 25.34 | [Auto-update mechanism](story-25-34-auto-update.md)     | Signed manifest, channels (stable/beta), per-platform path, rollback on failure. |
| 25.35 | [First-run setup wizard](story-25-35-first-run-setup-wizard.md) | Hardware probe → profile → libraries → engine → cloud-link; resumable, bilingual. |
| 25.36 | [Cross-platform uninstaller](story-25-36-uninstaller.md) | Universal contract: always remove binaries; prompt for data; never touch library files. |

## Key technical decisions

- **One Go binary, three roles.** `maktaba-cloud` runs as `--role=api`,
  `--role=relay`, or `--role=worker` (Stripe webhooks, push fanout). One
  codebase, one Postgres, one Helm/Compose unit. Roles are CPU-scaled
  independently behind a single LB. **Rationale:** the operations team is
  one person; multi-binary microservices are not free in headcount.

- **Hosting: Hetzner for compute, Cloudflare for edge.** Hetzner gives
  us 80% of the cost-per-TB-egress story; Cloudflare in front gives us
  DDoS protection, anycast, and TLS termination at PoPs near the user.
  Tunneled traffic *bypasses* Cloudflare (Cloudflare doesn't allow
  arbitrary HTTP-over-WS at scale on a free plan); we terminate TLS for
  tunnels at Hetzner edge.

- **Relay is HTTP-over-WSS, not WireGuard.** Plex relay tunnels HTTP. We
  do the same: client makes HTTPS request to `username.maktaba.app`,
  cloud edge multiplexes the request onto a frame on the WS tunnel,
  server demultiplexes and dispatches to its local API. This keeps the
  client surface unchanged from LAN — the same `fetch()` works.
  WireGuard would require a client-side VPN install on every device,
  which the iOS App Store and TV platforms make impractical.

- **TLS posture.** Cloud edge presents a Cloudflare-issued or
  Let's-Encrypt-issued cert for `*.maktaba.app`. The server's local
  Caddy presents a self-signed or local-CA cert for `maktaba.local`.
  Relay traffic is **TLS to the cloud, then plaintext over the
  WS tunnel** (the tunnel itself is TLS). End-to-end TLS from client to
  user's box is documented as **not** offered in v1; the cloud is in
  the trust boundary by design (it has to inspect the Host header to
  route). Users who want true E2E tunnel-bypass run the LAN path.

- **Bandwidth accounting on the cloud only.** Servers don't report
  byte counts; the cloud counts what it relays. **Rationale:** can't
  trust client-supplied counters for billing.

- **Concurrent stream limits enforced at the relay layer.** When a
  client opens a streaming HTTP request through the relay, we count it
  against the user's `streams_in_flight` gauge. Direct-LAN streams are
  not counted (cloud doesn't see them).

- **Entitlements: signed JSON, not API calls.** The cloud signs an
  entitlement blob (Ed25519, 24h expiry) for each server. The server
  caches it; gates cloud-only features against it; refreshes daily.
  Pattern reused from Epic 16 license validation. **Rationale:** the
  server must not have to call the cloud on every request.

- **Push tokens never leave the cloud.** When a client registers a
  device token (APNs / FCM) with the cloud, the token is stored
  encrypted at rest and **never exposed to user-facing or
  server-facing APIs**. Servers POST `{user_id, payload}`; the cloud
  resolves which devices belong to that user and dispatches.

- **Identity is OAuth 2.1.** Sessions are 30-day refresh tokens stored
  HttpOnly + SameSite=Lax; access tokens are 1h JWTs with `kid`
  rotation (RS256). Mobile apps use the same flow via `ASWebAuthenticationSession`
  / `Custom Tabs`; no embedded webviews.

- **Account-link on collision.** If a user registers email-and-password
  and later signs in with Google using the same email, the second flow
  prompts to merge accounts (single-user-id, multiple identity
  providers). Same for Apple's private-relay aliases.

- **Apple Sign-In is mandatory.** App Store rules: any app offering
  third-party sign-in must offer Sign in with Apple. We support it
  even if no user picks it.

- **Subdomain reservation list.** `admin`, `api`, `auth`, `billing`,
  `support`, `maktaba`, plus 200 reserved-words list (`www`, `mail`,
  etc.) cannot be claimed. Username is 3–32 chars, `[a-z0-9-]`,
  cannot start/end with `-`. Username changes are allowed at most
  once every 90 days; old subdomain 301-redirects for 30 days then
  becomes free.

- **Free tier carries no relay.** A free user can register, link a
  server, see its status, and get push notifications. Free users
  **cannot stream through the relay**; remote streaming is the gate
  to the paid tier. **Rationale:** push and status are cheap to run;
  bandwidth is what costs money.

- **GDPR-class delete.** "Delete my account" within 30 days purges
  PII, OAuth identities, billing references (anonymized for
  accounting), and severs all server links. Audit log retained for
  90 days post-delete with PII redacted.

- **One Docker image, many wrappers.** All NAS packages, the
  one-click VPS deploys, and the generic Linux compose path use
  the same `ghcr.io/hamza-labs-core/maktaba` image. Per-platform
  packaging (Synology SPK, QNAP QPKG, TrueNAS Helm, Unraid
  template, DO Marketplace) is *vendor glue*, not a parallel
  build pipeline. **Rationale:** keeping every distribution
  channel converging on a single artifact lets one CI matrix
  validate them all.

- **macOS / Windows are native, not Docker.** On Mac and Windows,
  Docker Desktop is a heavy ask; we ship a native `.app` (Mac)
  and Windows Service (PC) that supervises Go + Python venv
  subprocesses directly. **Rationale:** the user who installs
  Maktaba on their Mac mini doesn't want to know what Docker is.

- **Signed releases, signed updates.** Every artifact is signed:
  Apple notarization (Mac), EV cert (Windows), GPG (apt/rpm/Snap),
  cosign (Docker), EdDSA (manifest). The auto-updater (25.34)
  refuses anything not signed by our keys. **Rationale:**
  supply-chain integrity is the cost of distributing binaries.

## API surface (cloud)

```
# Identity
POST   /api/auth/register
POST   /api/auth/login
POST   /api/auth/logout
POST   /api/auth/refresh
POST   /api/auth/verify-email
POST   /api/auth/forgot-password
POST   /api/auth/reset-password
GET    /api/auth/oauth/{provider}/start          # google, apple
GET    /api/auth/oauth/{provider}/callback
POST   /api/auth/oauth/{provider}/link

# Account
GET    /api/me
PATCH  /api/me
DELETE /api/me
GET    /api/me/export                           # GDPR data export ZIP

# Servers
POST   /api/servers/claim                       # redeem claim token
GET    /api/servers
GET    /api/servers/{server_id}
DELETE /api/servers/{server_id}
GET    /api/servers/{server_id}/status

# Relay (the public-facing virtual host)
ANY    https://{username}.maktaba.app/*         # routed to user's server

# Tunnel (server-to-cloud only)
GET    /tunnel/v1/connect                       # WSS upgrade with X-Server-Token
POST   /tunnel/v1/heartbeat                     # fallback for restricted networks

# Push
POST   /api/push/devices                        # register device token (client-side)
DELETE /api/push/devices/{token_id}
POST   /api/push/dispatch                       # server-only; X-Server-Token

# Entitlements
GET    /api/entitlements                        # for the calling user
GET    /api/servers/{server_id}/entitlement     # signed blob

# Billing
POST   /api/billing/checkout                    # returns Stripe URL
POST   /api/billing/portal                      # returns Stripe URL
POST   /api/billing/webhook                     # Stripe → cloud
GET    /api/billing/plans
GET    /api/billing/subscription                # current state

# Admin (HamzaLabs only)
GET    /api/admin/users
GET    /api/admin/users/{user_id}
POST   /api/admin/users/{user_id}/suspend
GET    /api/admin/servers
GET    /api/admin/servers/{server_id}
POST   /api/admin/servers/{server_id}/suspend
GET    /api/admin/revenue
GET    /api/admin/abuse-events
```

## DB schema (cloud Postgres)

The cloud Postgres is a *separate database* from the on-prem
`api/migrations/`; the `cloud_` prefix used in spec prose maps to
unprefixed table names in `cloud/migrations/` because the
namespacing is already provided by the DB boundary. The canonical
table names below match `cloud/migrations/`:

| Table                | Purpose |
|----------------------|---------|
| `users`              | id, email (unique), password_hash (nullable for OAuth-only), email_verified, locale, plan (free/pro/family), status, display_name, avatar_url, created_at, last_login_at |
| `oauth_links`        | (user_id, provider, subject) — Google/Apple identity linkages; unique on `(provider, subject)` |
| `sessions`           | id, user_id, refresh_token_hash, ip, user_agent, created_at, expires_at, revoked_at |
| `email_verifications`| token_hash, user_id, purpose, expires_at, used_at — single-use verification + password-reset tokens |
| `email_change_requests` | token_hash, user_id, new_email, expires_at, used_at — email change confirmation |
| `account_deletions`  | user_id, requested_at, purge_after, cancelled_at — GDPR delete hold |
| `servers`            | id, owner_user_id, name, slug (subdomain prefix), server_secret_hash, plan, version, public_key (Ed25519), last_seen_at, direct_ip, direct_port |
| `server_claims`      | token_hash, code, user_id, expires_at, used_at, used_server_id — 8-char 10-min claim codes |
| `server_health`      | server_id (PK), online, last_heartbeat, relay_latency_ms, cpu_pct, mem_pct, storage_pct |
| `server_endpoints`   | server_id, kind (lan/relay/direct), candidates_sealed, observed_at — direct-connect probe state (25.10 sub-migration) |
| `push_devices`       | id, user_id, platform (ios/android/web), token (sealed), app_version, last_seen_at — unique on (platform, token_hash) |
| `push_dispatch_log`  | id, user_id, platform, topic, status, error, sent_at |
| `subscriptions`      | user_id (PK), plan (free/pro/family), interval (monthly/yearly, nullable for free), stripe_customer_id, stripe_subscription_id, status, current_period_end, cancel_at_period_end, seats |
| `stripe_events`      | event_id (PK), type, received_at, payload — webhook idempotency ledger |
| `family_members`     | (owner_user_id, member_user_id) — family-plan seats; invitations |
| `bandwidth_samples`  | server_id, bucket_start, bytes_in, bytes_out — 5-min samples, source of truth for billing |
| `bandwidth_monthly`  | server_id, month (DATE), bytes_in, bytes_out, over_limit_at |
| `subdomains`         | slug (PK), server_id, provisioned_at, cert_renewed_at |
| `reserved_slugs`     | slug (PK), reason — operator/abuse reservations (`admin`, `api`, `www`, …) |
| `audit_events`       | id, occurred_at, actor_user_id (nullable), is_admin, category, action, target_kind, target_id, ip, user_agent, reason, payload (JSONB), error_id — written by 25.20 (slot 0002 sub-migration) |
| `abuse_signals`      | id, subject, subject_kind (user/server/ip), kind, severity, detail (JSONB), created_at |
| `blocklist`          | (subject_kind, subject) PK, reason, blocked_at, expires_at |
| `rate_overrides`     | (subject_kind, subject) PK, scope, limit_per_minute, reason, expires_at — 25.24 sub-migration |
| `entitlement_keys`   | fingerprint (PK), public_key, created_at, revoked_at, active — private bytes never live in DB |
| `entitlement_grants` | id, user_id, server_id, plan, issued_at, expires_at, fingerprint, revoked_at |

## Migrations claimed by this epic

The cloud has its own migration sequence (separate database, separate
goose dir at `cloud/migrations/`). Files are named `00<slot><seq>_<topic>.sql`;
the leading four digits are the slot, the next four are sequence
within that slot. Canonical slot allocation (matches
[`cloud/migrations/README.md`](../../../cloud/migrations/README.md)):

| Slot | Story | File | Tables |
|------|-------|------|--------|
| `0001` | 25.1  | `00010001_system.sql`     | `cloud_system` (bootstrap settings) |
| `0002` | 25.2–25.4 | `00020001_identity.sql` | `users`, `oauth_links`, `sessions`, `email_verifications` |
| `0003` | 25.5  | `00030001_account.sql`    | `email_change_requests`, `account_deletions` |
| `0004` | 25.6  | `00040001_servers.sql`    | `servers`, `server_claims`, `server_health` |
| `0005` | 25.11 | `00050001_bandwidth.sql`  | `bandwidth_samples`, `bandwidth_monthly` |
| `0006` | 25.13/25.14 | `00060001_billing.sql` | `subscriptions`, `stripe_events`, `family_members` |
| `0007` | 25.17 | `00070001_push.sql`       | `push_devices`, `push_dispatch_log` |
| `0008` | 25.22 | `00080001_subdomains.sql` | `subdomains`, `reserved_slugs` |
| `0009` | 25.25 | `00090001_abuse.sql`      | `abuse_signals`, `blocklist` |
| `0010` | 25.26 | `00100001_entitlement.sql`| `entitlement_keys`, `entitlement_grants` |

Stories that ALTER existing tables instead of creating new ones add a
sub-sequence file (`00<slot><seq>_<topic>.sql` with seq ≥ 0002) in the
target slot. The admin console (25.20) extends slot 0002 with
`audit_events` and trigram indexes; the rate limiter (25.24) extends
slot 0009 with `rate_overrides`; direct-connect fallback (25.10)
extends slot 0004 with `server_endpoints`.

## Threat model (summary)

| Threat                                  | Mitigation |
|-----------------------------------------|------------|
| Stolen claim token → server hijack      | 8-char token, 10-min TTL, single-use, TLS-pinned to cloud cert |
| Stolen server bearer token → impersonation | Tokens hashed at rest; rotation on demand from server UI; `last_used_at` tracking surfaces anomalies |
| Account takeover via OAuth email reuse  | Email collision triggers explicit merge prompt; never auto-link |
| Relay used for arbitrary HTTP proxying  | `Host` header must match `{subdomain}.maktaba.app`; non-Maktaba paths 404; abuse score |
| Bandwidth fraud (free user transfers TB)| Free tier hard-cap = 0 GB; Pro/Family caps + circuit breaker at 110% |
| Stripe webhook replay                   | `stripe_events.event_id` PK = idempotency |
| Push token leak                         | AES-GCM at rest with KMS-managed key; never returned in any response |
| Subdomain enumeration / squatting       | Reserved-words list, abuse-score signals (10 sign-ups/min from same IP block → CAPTCHA) |
| GDPR right-to-erasure failure           | Delete job traverses 9 tables, audited, idempotent, 30-day grace before final purge |
| Compromised admin account               | All admin endpoints require fresh re-auth (≤ 5 min); SSO via Google Workspace `@hamzalabs.com`; audit on every admin action |

## Cost model (HamzaLabs-internal)

Per active paying user, monthly:

| Line item                          | Pro tier | Family tier |
|------------------------------------|----------|-------------|
| Hetzner CCX23 share (relay node)   | $0.40    | $0.80       |
| Postgres (Hetzner managed share)   | $0.05    | $0.05       |
| Cloudflare bandwidth (free tier)   | $0       | $0          |
| Hetzner egress @ €1.20/TB          | $0.12 (100GB) | $0.60 (500GB) |
| APNs/FCM                           | $0       | $0          |
| Stripe fees (2.9% + 30¢)           | $0.59 ($9.99 plan) | $1.06 ($24.99 plan) |
| **Total cogs / user / month**      | **$1.16**| **$2.51**   |
| **List price**                     | **$9.99**| **$24.99**  |
| **Gross margin**                   | **88%**  | **90%**     |

Numbers are best-case assumptions for v1 sizing; the [admin revenue
dashboard (25.21)](story-25-21-admin-revenue.md) reports the real
numbers monthly.

## Dependencies

- **Epic 10** Story 10.6 (signing keys), 10.18 (Ed25519 server identity) —
  the server identity used to authenticate to the cloud is the same
  long-term Ed25519 key.
- **Epic 15** Story 15.1 (LAN mDNS discovery) — clients prefer LAN;
  cloud relay is the fallback path. (Story 15.2's "global discovery"
  v0 stub is superseded by 25.07–25.10.)
- **Epic 16** all stories — entitlement-signing pattern reused;
  Stripe customer portal already chosen; cloud is the *server side* of
  the existing local-license check.
- **Epic 21** Story 21.6 (audit log) — cloud writes audit events; admin
  dashboard reads them.
- **Epic 23** all stories — rate limiting, abuse posture, security
  headers established locally are mirrored at the cloud edge.

## Out of scope (v1)

- Cloud-side transcoding (the cloud never has the bytes for long enough
  to transcode; would require persisting media, which we explicitly
  refuse).
- Cloud-side metadata mirror or backup (defer to a future Epic 26).
- Multi-server-per-user federation in the cloud — **out for v1, in for
  v2.** A v1 user with two boxes gets two subdomains and two server
  rows; the cloud doesn't merge their libraries.
- Family member sub-accounts inside one subscription (each member has
  a separate cloud user; the family plan is a payer-of-record + 5
  invitees, not a household ID).
- Apple/Google in-app purchases. App Store rules force this for some
  IAP cases; v1 routes everything through Stripe portal and accepts
  the App Store revenue split as a future risk.
- Self-hosted "private cloud" deployment of the cloud relay (can be
  open-sourced later for users who don't want HamzaLabs in the loop;
  not v1).
- **Mac App Store / Microsoft Store / Linux app-store first-party
  distribution** (sandboxing breaks our pipeline; v1 ships direct
  downloads).
- **Custom Maktaba OS image** (turnkey Pi-imager image is feasible
  but niche; defer).
- **Delta updates** (full installs only; v2 candidate).
- **Staged rollout / canary** of releases (v1 = binary stable/beta).

## See also

- [Epic 10 — Auth & Security](../10-auth-security/README.md)
- [Epic 15 — Discovery & Networking](../15-discovery/README.md)
- [Epic 16 — Subscriptions](../16-subscriptions/README.md)
- [Epic 21 — Observability](../21-observability/README.md)
- [Epic 23 — Security Hardening](../23-security/README.md)
- [`architecture.md` §13](../../architecture.md#13-cloud-relay-architecture)

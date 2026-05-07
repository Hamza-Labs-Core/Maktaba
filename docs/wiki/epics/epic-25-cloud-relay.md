# Epic 25 — Maktaba Cloud: Relay, Accounts & Subscriptions

> **Status:** spec. **Source:** `specs/epics/25-cloud-relay/`.
> **Anchors:** [`architecture.md` §13](../../../specs/architecture.md#13-cloud-relay-architecture).

## Goal

Maktaba Cloud is the hosted SaaS layer (operated by HamzaLabs) that turns the
self-hosted Maktaba Server into a remotely-reachable, cross-network product.
It is the difference between "an app on my LAN" and "my library, on every
screen, anywhere I am" — the same value Plex Cloud Relay provides for Plex.
The cloud is the revenue engine; without it, the local server is great but
unmonetizable.

The cloud has five jobs: **identity** (sign-up, OAuth, sessions),
**linking** (claim-token-based server↔account binding), **reachability**
(WSS-tunneled HTTP relay at `{username}.maktaba.app`), **push** (server →
cloud → APNs/FCM fanout), and **billing** (Stripe-hosted checkout, signed
entitlement blobs). It explicitly does **not** store media, transcribe,
or analyze content. Self-hosted features never depend on the cloud.

This epic also covers **server distribution**: native installers for
macOS, Windows, Linux, NAS, ARM, and VPS — the path from `maktaba.app`
download to scanning library.

## Stories & Plans

### Cloud relay & accounts (Phases 1–5)

| #     | Story | Summary |
|-------|-------|---------|
| 25.1  | [Cloud API service bootstrap](../../../specs/epics/25-cloud-relay/story-25-01-cloud-api-bootstrap.md) | Single Go binary, Postgres on Hetzner, structured logs, health endpoints. |
| 25.2  | [Email + password registration](../../../specs/epics/25-cloud-relay/story-25-02-email-registration.md) | argon2id, email verification, lockout, password reset, refresh-token rotation. |
| 25.3  | [Google OAuth sign-in](../../../specs/epics/25-cloud-relay/story-25-03-google-oauth.md) | OIDC + PKCE, account-merge prompt on email collision. |
| 25.4  | [Apple OAuth (Sign in with Apple)](../../../specs/epics/25-cloud-relay/story-25-04-apple-oauth.md) | App Store-mandatory; private email relay handling. |
| 25.5  | [Account profile, deletion & export](../../../specs/epics/25-cloud-relay/story-25-05-account-profile.md) | GDPR-class delete (30-day grace), data export ZIP, avatar handling. |
| 25.6  | [Server claim token flow](../../../specs/epics/25-cloud-relay/story-25-06-server-claim-token.md) | 8-char base32 token, 10-min TTL, single-use, pubkey-bound. |
| 25.7  | [WSS relay tunnel — server side](../../../specs/epics/25-cloud-relay/story-25-07-relay-tunnel-server.md) | Persistent outbound WSS, framed multiplex, exponential reconnect. |
| 25.8  | [WSS relay tunnel — cloud side](../../../specs/epics/25-cloud-relay/story-25-08-relay-tunnel-cloud.md) | Per-pod registry, last-write-wins on reconnect, idle timeouts. |
| 25.9  | [HTTP relay proxy](../../../specs/epics/25-cloud-relay/story-25-09-http-relay-proxy.md) | Subdomain → server lookup, header sanitization, WS pass-through. |
| 25.10 | [Direct-connection probe & LAN fallback](../../../specs/epics/25-cloud-relay/story-25-10-direct-connection-fallback.md) | Clients race LAN candidates 1s; pin 5min; relay is fallback. |
| 25.11 | [Bandwidth metering & accounting](../../../specs/epics/25-cloud-relay/story-25-11-bandwidth-metering.md) | Redis hash counters, 60s flush to Postgres, monthly rollup. |
| 25.12 | [Tier enforcement (concurrent + caps)](../../../specs/epics/25-cloud-relay/story-25-12-tier-enforcement.md) | Free=0 GB; Pro=100/2 streams; Family=500/5 streams. |
| 25.13 | [Stripe checkout session](../../../specs/epics/25-cloud-relay/story-25-13-stripe-checkout.md) | Idempotency-keyed, customer-portal-deferred. |
| 25.14 | [Stripe webhook handler](../../../specs/epics/25-cloud-relay/story-25-14-stripe-webhook.md) | Idempotent processing, NOTIFY tier_changed, daily reconciliation. |
| 25.15 | [Plan comparison & subscription UI](../../../specs/epics/25-cloud-relay/story-25-15-plan-comparison-ui.md) | Public marketing + in-app upgrade; bilingual EN/AR. |
| 25.16 | [Server status dashboard](../../../specs/epics/25-cloud-relay/story-25-16-server-status-dashboard.md) | Live online/offline, version, MTD GB, update available. |
| 25.17 | [Push notification ingest](../../../specs/epics/25-cloud-relay/story-25-17-push-notification-ingest.md) | Fixed-shape envelope, locale templates, dedup + TTL. |
| 25.18 | [APNs dispatcher](../../../specs/epics/25-cloud-relay/story-25-18-apns-dispatcher.md) | HTTP/2, ES256 JWT, BadDeviceToken auto-revoke. |
| 25.19 | [FCM dispatcher](../../../specs/epics/25-cloud-relay/story-25-19-fcm-dispatcher.md) | Service-account OAuth, per-device send, web push via FCM. |
| 25.20 | [Admin: user & server fleet](../../../specs/epics/25-cloud-relay/story-25-20-admin-fleet.md) | Workspace SSO, search, suspend, force-disconnect, audit. |
| 25.21 | [Admin: revenue dashboard](../../../specs/epics/25-cloud-relay/story-25-21-admin-revenue.md) | MRR, ARR, churn, LTV; Stripe sync; cost-per-user model. |
| 25.22 | [Subdomain provisioning](../../../specs/epics/25-cloud-relay/story-25-22-subdomain-provisioning.md) | Wildcard DNS, 200-word reserved list, 30-day grace on release. |
| 25.23 | [TLS at the edge (wildcard ACME)](../../../specs/epics/25-cloud-relay/story-25-23-tls-edge.md) | Let's Encrypt DNS-01, ZeroSSL backup, monthly rotation. |
| 25.24 | [Rate limiting & quota](../../../specs/epics/25-cloud-relay/story-25-24-rate-limiting.md) | Redis sliding window, fail-closed; per-IP/user/server. |
| 25.25 | [Abuse detection & response](../../../specs/epics/25-cloud-relay/story-25-25-abuse-detection.md) | 9 detectors, score-and-decay, auto-suspend at threshold 50. |
| 25.26 | [Cloud→server entitlement signing](../../../specs/epics/25-cloud-relay/story-25-26-entitlement-signing.md) | Ed25519-signed JCS, 24h TTL, 7-day offline grace. |

### Server distribution & installation (Phase 6)

| #     | Story | Summary |
|-------|-------|---------|
| 25.27 | [macOS installer](../../../specs/epics/25-cloud-relay/story-25-27-macos-installer.md) | Signed/notarized DMG, Homebrew cask, LaunchAgent, menu bar, Sparkle. |
| 25.28 | [Windows installer](../../../specs/epics/25-cloud-relay/story-25-28-windows-installer.md) | EV-signed MSI + NSIS, Windows Service, tray, firewall rule. |
| 25.29 | [Linux packages](../../../specs/epics/25-cloud-relay/story-25-29-linux-packages.md) | apt + dnf repos, Snap, Flatpak, AppImage, systemd unit. |
| 25.30 | [Official Docker image](../../../specs/epics/25-cloud-relay/story-25-30-docker-image.md) | Multi-arch, cosign-signed, < 1.5 GB; reference compose with PG. |
| 25.31 | [NAS support](../../../specs/epics/25-cloud-relay/story-25-31-nas-support.md) | Synology SPK, QNAP QPKG, TrueNAS Helm, Unraid template. |
| 25.32 | [Raspberry Pi & ARM builds](../../../specs/epics/25-cloud-relay/story-25-32-raspberry-pi-arm.md) | Pi 4/5/Jetson/RK3588 profiles; CPU Whisper; SD-card warnings. |
| 25.33 | [One-click cloud-VPS deploy](../../../specs/epics/25-cloud-relay/story-25-33-one-click-cloud-deploy.md) | DigitalOcean Marketplace, Hetzner Cloud, Railway, generic shell. |
| 25.34 | [Auto-update mechanism](../../../specs/epics/25-cloud-relay/story-25-34-auto-update.md) | Signed manifest, channels, per-platform path, rollback. |
| 25.35 | [First-run setup wizard](../../../specs/epics/25-cloud-relay/story-25-35-first-run-setup-wizard.md) | Hardware probe → profile → libraries → engine → cloud-link. |
| 25.36 | [Cross-platform uninstaller](../../../specs/epics/25-cloud-relay/story-25-36-uninstaller.md) | Universal contract: never touch library files; prompt for data. |

## Key technical decisions

- **One Go binary, three roles** (`api | relay | worker`). Same
  codebase, same Postgres, same Redis. Roles scale independently
  on CPU.
- **HTTP-over-WSS, not WireGuard.** Clients keep using `fetch()`
  unchanged; tunnels traverse hotel/corp/airplane networks.
  4-byte length + 1-byte type frames; per-stream 64 KiB
  windows; PINGs every 25 s.
- **LAN-first, relay-as-fallback.** Clients race LAN candidates
  for 1 s; relay only when off-LAN. Saves user latency and
  HamzaLabs bandwidth.
- **Free tier = zero relay GB.** Push and status are free; the
  gate to monetization is bandwidth.
- **Bandwidth counted cloud-side only.** Server-supplied counts
  cannot be billing-authoritative.
- **Entitlements: Ed25519-signed JCS JSON, 24h TTL, daily
  refresh, 7-day offline grace.** Same shape as Epic 16
  license-validation; the cloud signs automatically on tunnel
  handshake.
- **Wildcard DNS + wildcard TLS.** No per-subdomain provisioning.
  Let's Encrypt DNS-01 against Cloudflare, ZeroSSL backup CA.
- **Push tokens never leave the cloud.** Sealed AES-GCM at rest;
  servers POST `{user_id, payload}`, the cloud handles fanout.
- **Apple Sign-In is mandatory** (App Store rules); App Store
  in-app purchases are explicitly out of v1 (Stripe only).
- **GDPR-class delete.** 30-day soft-delete grace, then PII
  hard-purge across 9 tables; audit retained 90 days post-delete
  with PII redacted.
- **One Docker image, many wrappers.** All NAS packages, VPS
  one-clicks, and Linux compose deploys converge on
  `ghcr.io/hamza-labs-core/maktaba`. macOS and Windows ship
  native (not Docker).
- **Signed releases everywhere.** Apple notarization (Mac), EV
  cert (Windows), GPG (apt/rpm/Snap), cosign (Docker), EdDSA
  (auto-update manifest). The auto-updater refuses anything not
  signed by our keys.

## API endpoints (cloud)

70+ endpoints across identity (`/api/auth/*`), accounts (`/api/me/*`),
servers (`/api/servers/*`), relay (`https://{user}.maktaba.app/*`),
tunnel (`/tunnel/v1/connect`), push (`/api/push/*`), entitlements
(`/api/entitlements`), billing (`/api/billing/*`), and admin
(`/api/admin/*`). Full list in
[the epic README](../../../specs/epics/25-cloud-relay/README.md#api-surface-cloud).

## Migrations claimed by this epic

The cloud has its own migration sequence (separate database).
Slots `0001`–`0010` (cloud-side):

| Slot | Story | Tables |
|------|-------|--------|
| `0001` | 25.1  | `cloud_users`, `cloud_identities`, `cloud_sessions` |
| `0002` | 25.6  | `cloud_servers`, `cloud_server_tokens`, `cloud_claim_tokens` |
| `0003` | 25.11 | `cloud_bandwidth_daily`, `cloud_streams_active` |
| `0004` | 25.13 | `cloud_subscriptions`, `cloud_invoices`, `cloud_webhook_events` |
| `0005` | 25.17 | `cloud_devices`, `cloud_push_outbox`, `cloud_push_templates` |
| `0006` | 25.20 | `cloud_audit`, `cloud_revenue_snapshots` |
| `0007` | 25.22 | `cloud_subdomains`, `cloud_subdomain_reserved` |
| `0008` | 25.24/25.25 | `cloud_rate_limit_overrides`, `cloud_abuse_events` |
| `0009` | 25.5  | indexes for GDPR-export queries |
| `0010` | 25.26 | entitlement-signing key history (rotation) |

## Cost model (HamzaLabs-internal)

Per active paying user, monthly:

| Line                        | Pro tier  | Family tier |
|-----------------------------|-----------|-------------|
| Hetzner CCX23 share         | $0.40     | $0.80       |
| Postgres (managed share)    | $0.05     | $0.05       |
| Cloudflare bandwidth        | $0        | $0          |
| Hetzner egress @ €1.20/TB   | $0.12     | $0.60       |
| APNs / FCM                  | $0        | $0          |
| Stripe fees (2.9% + 30¢)    | $0.59     | $1.06       |
| **Total cost / user / mo**  | **$1.16** | **$2.51**   |
| **List price**              | **$9.99** | **$24.99**  |
| **Gross margin**            | **88%**   | **90%**     |

Margins are healthy *because* the cloud deliberately doesn't store
media. The [admin revenue dashboard (25.21)](../../../specs/epics/25-cloud-relay/story-25-21-admin-revenue.md)
reports the real numbers.

## Dependencies

- **Epic 10** Story 10.6 (signing keys), 10.18 (Ed25519 server
  identity) — server-to-cloud auth reuses the local long-term
  Ed25519 key.
- **Epic 15** Story 15.2 (LAN discovery) — clients prefer LAN;
  relay is the fallback path.
- **Epic 16** all stories — entitlement-signing pattern reused;
  Stripe customer portal already chosen; the cloud is the
  *server side* of the existing local-license check.
- **Epic 21** Story 21.6 (audit log) — cloud writes audit events;
  admin dashboard reads them.
- **Epic 23** all stories — rate limiting, abuse posture,
  security headers established locally are mirrored at the
  cloud edge.

## Out of scope (v1)

- Cloud-side transcoding (cloud never has bytes for long enough).
- Cloud-side metadata mirror or backup (defer to Epic 26).
- Multi-server-per-user federation in the cloud (v2).
- Family member sub-accounts inside one subscription.
- Apple/Google in-app purchases.
- Self-hosted "private cloud" deployment.
- Mac App Store / Microsoft Store / Linux app-store distribution.
- Custom Maktaba OS image.
- Delta updates.
- Staged rollout / canary releases.

## See also

- [Epic 10 — Auth & Security](epic-10-auth-security.md)
- [Epic 15 — Discovery & Networking](epic-15-discovery.md)
- [Epic 16 — Subscriptions](epic-16-subscriptions.md)
- [Epic 21 — Observability](epic-21-observability.md)
- [Epic 23 — Security Hardening](epic-23-security.md)
- [`architecture.md` §13](../../../specs/architecture.md#13-cloud-relay-architecture)

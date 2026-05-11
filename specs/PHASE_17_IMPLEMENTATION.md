# Phase 17 — Cloud Relay implementation report

Closes Epic 25 (stories 25.1 → 25.36). The implementation lives under
the new `cloud/` Go module. The on-prem `api/` module is untouched.

## What landed

### Phase 1 — Bootstrap + Identity (25.1–25.5)

- `cloud/cmd/maktaba-cloud/main.go` — single binary with subcommands
  `serve`, `migrate`, `version` and `--role api|relay|worker`.
- `internal/config` — TOML loader + env override + role-specific
  validator. Tests cover defaults, file-driven values, env override,
  and validation errors.
- `internal/middleware` — request id (UUIDv7), structured access log,
  panic recovery, CORS with hard allow-list. Tests cover request-id
  mint/propagate behaviour.
- `internal/db` — `*sql.DB` pool + goose migrator with the embedded
  migration FS.
- `migrations/` — slots 0001-0010 reserved + filled (system, identity,
  account, servers/claims/health, bandwidth, billing/Stripe events,
  push, subdomains, abuse, entitlement keys).
- Identity: argon2id (PHC strings), password policy (NIST 800-63B + a
  small embedded leaked-corpus check), refresh tokens hashed at rest,
  HMAC-SHA256 access tokens.
- Google OAuth (auth-code flow) and Apple OAuth (form_post; ES256
  client_secret signed by hand).
- `handlers/account` — `GET/PATCH /v1/account/me`, password change
  (revokes all sessions), email change kickoff, reversible 30-day
  deletion hold.

### Phase 2 — Server linking + relay (25.6–25.10)

- 8-char base32 claim tokens with 10-minute TTL, hashed at rest. The
  server agent calls `POST /v1/servers/claims/redeem` and receives a
  long-lived `server_secret`.
- `internal/relay/protocol.go` — binary multiplexed frame format
  (REQUEST_HEAD/BODY, RESPONSE_HEAD/BODY, AUTH, AUTH_OK/FAIL, PING/PONG,
  HEARTBEAT, CLOSE_STREAM). 16 MiB per-frame cap.
- `internal/relay/{tunnel,registry,ws}.go` — gorilla/websocket-based
  WS tunnel. Registry deduplicates concurrent reconnects.
- `handlers/relay/proxy.go` — extracts slug from Host header, routes
  through the matching tunnel, strips hop-by-hop headers, adds
  X-Forwarded-For + X-Relayed-By.
- Direct-connection signalling fields persisted on `servers`
  (`direct_ip`, `direct_port`) and surfaced through the dashboard for
  the client to LAN-fallback against.

### Phase 3 — Billing (25.11–25.15)

- `internal/billing/plans.go` — Free/Pro/Family catalog (5 GiB / 100 GiB /
  500 GiB per month, server + concurrent-stream caps).
- `internal/billing/meter.go` — in-memory counters + 30s flush to
  Postgres. `Allow()` is the tier gate consulted by the relay before
  each proxied request.
- `internal/billing/stripe.go` — hand-rolled minimal Stripe client
  (CheckoutSession only) + webhook HMAC-SHA256 verification with
  freshness window. Tests cover the webhook signature path.
- `handlers/billing` — `/v1/billing/{plans,me,checkout,webhook}`.
  Webhook is dedupe'd via `stripe_events` PK.

### Phase 4-6 — Ops + Distribution (25.16–25.36)

- Dashboard: `/v1/dashboard/servers` joins `servers` and
  `server_health` for a poll-friendly payload.
- Push notifications: cross-platform `push/Dispatcher` over
  `push/APNsDriver` (ES256 provider token, 45-min cache) and
  `push/FCMDriver` (RS256 service-account JWT bearer grant). A
  `RetryDriver` wraps both with exponential backoff.
- Admin: `/v1/admin/fleet`, `/v1/admin/revenue`,
  `/v1/admin/users/{id}/block`, gated on
  `[admin].allowed_email_domain`.
- Subdomain provisioning: `subdomains` table + reserved-slug guard,
  check + claim endpoints.
- TLS/ACME: `HostPolicy` that admits the apex hostnames + any
  subdomain present in the `subdomains` table — Let's Encrypt quota
  is protected from random-host attacks.
- Rate limiting: in-memory token-bucket limiter keyed by IP/user,
  mounted on `/v1/auth/login` and `/v1/auth/register`.
- Abuse detection: signal store + rolling severity check + persisted
  blocklist.
- Entitlement signing: Ed25519 signer + verifier, fingerprint-as-`kfp`
  claim, expiry enforced. Public keys persisted to `entitlement_keys`
  so on-prem agents can pin the trusted set.
- Installer scaffolding: macOS DMG build script (codesign + notarize),
  Windows MSI PowerShell script (signtool + WiX), Linux nfpm config
  for .deb/.rpm + systemd unit + pre/post install hooks, Dockerfile
  (multi-stage, distroless), Synology .spk INFO + QNAP .qpkg.cfg,
  Raspberry Pi pi-gen notes, DigitalOcean App Spec + Hetzner
  Terraform + cloud-init for one-click cloud VPS.
- Auto-update mechanism documented (signed JSON manifest, channels,
  per-platform delivery, rollback).
- First-run wizard documented (TTY + web fallback, claim-code
  exchange, secret persistence to OS keychain or 0600 file).
- Uninstaller documented (cloud notification, service teardown,
  data-preserved default, optional `--purge`).

## Testing

```
$ go test ./...
ok  github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/argon2id
ok  github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/password
ok  github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/token
ok  github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing
ok  github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config
ok  github.com/Hamza-Labs-Core/Maktaba/cloud/internal/entitlement
ok  github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/relay
ok  github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/servers
ok  github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware
ok  github.com/Hamza-Labs-Core/Maktaba/cloud/internal/ratelimit
ok  github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay
ok  github.com/Hamza-Labs-Core/Maktaba/cloud/internal/tls
```

All cloud unit tests pass. The on-prem `api/` module still builds
green; no shared code was modified.

## Out of scope for this PR

- Live integration tests against Postgres + Stripe + APNs + FCM —
  these need real credentials and live infra. CI matrix entries land
  separately.
- DNS provisioning side of subdomain claim (Cloudflare API call).
  Persistence is done; the worker that drives the API call lands when
  Cloudflare creds are vaulted.
- Actual binaries for the installers — the build scripts, signing
  hooks, and CI matrix entries exist; the per-platform CI runners are
  spun up in a follow-up infrastructure PR.

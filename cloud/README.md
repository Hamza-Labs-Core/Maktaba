# Maktaba Cloud

The cloud-side companion to the on-prem `api/` server. Implements
Epic 25 (stories 25.1 → 25.36): hosted identity, a WebSocket relay for
NAT-traversal HTTPS to home servers, bandwidth metering and Stripe
billing, push notification fanout, an operator admin surface, and
installer/distribution scaffolding for the on-prem agent.

## Layout

```
cloud/
  cmd/maktaba-cloud/      # single binary, --role api|relay|worker
  internal/
    abuse/                # signal capture + blocklist
    auth/                 # argon2id, password policy, sessions, JWT, OAuth (Google/Apple)
    billing/              # plan catalog, bandwidth meter, Stripe client
    clock/                # injectable time source
    config/               # cloud.toml loader + role-specific validator
    db/                   # *sql.DB pool + goose migrator
    entitlement/          # Ed25519 signing for offline feature gates
    handlers/
      account/            # /v1/account/me
      admin/              # /v1/admin/*
      auth/               # /v1/auth/{register,login,refresh,logout,oauth/*}
      billing/            # /v1/billing/{plans,me,checkout,webhook}
      health/             # /v1/dashboard/servers
      push/               # /v1/push/{devices,dispatch}
      relay/              # subdomain-routed HTTP proxy
      servers/            # /v1/servers + claim/redeem + subdomain claim
    middleware/           # request id, logging, recover, CORS
    push/                 # APNs + FCM drivers + RetryDriver
    ratelimit/            # in-memory token bucket
    relay/                # WS framing + registry + tunnel
    server/               # chi router + health probes + graceful shutdown
    stores/               # data access layer
    tls/                  # ACME HostPolicy backed by subdomain table
  migrations/             # goose-managed SQL slots 0001–0010
  installers/             # macOS/Windows/Linux/Docker/NAS/RPi/cloud-VPS + auto-update + wizard + uninstaller
  configs/                # cloud.example.toml
```

## Running locally

```sh
# Provision a local Postgres:
docker run --rm -d --name maktaba-cloud-pg -e POSTGRES_HOST_AUTH_METHOD=trust -p 5433:5432 postgres:16-alpine

# Boot api role:
MAKTABA_CLOUD_DB_URL='postgres://postgres@localhost:5433/postgres?sslmode=disable' \
MAKTABA_CLOUD_TOKEN_SECRET='development-secret-must-be-at-least-32-bytes-long' \
MAKTABA_CLOUD_OAUTH_GOOGLE_SECRET='dev' \
  go run ./cmd/maktaba-cloud serve --role api --config configs/cloud.example.toml

curl http://localhost:8080/healthz
```

## Role wiring

| Role   | Binds                       | Provides |
|--------|-----------------------------|----------|
| api    | 0.0.0.0:8080                | identity, servers, billing, admin, dashboard, push, account |
| relay  | 0.0.0.0:8080                | WS tunnel at `/v1/relay/ws`, HTTP proxy on every other host |
| worker | 127.0.0.1:9090 (control)    | cleanup ticks, account-deletion purge |

Each role shares the migrator, pool, config validator, and `/healthz`
+ `/readyz` probes. The `/readyz` probe gates on both DB ping AND
`migrations_at_head` so an LB doesn't route to a pod whose schema is
behind its binary.

## Deployment

A turnkey **relay** deployment for a single VPS (relay + Postgres +
Redis + Caddy auto-TLS, behind systemd) lives in
[`deploy/cloud-relay/`](../deploy/cloud-relay/) — see its
[README](../deploy/cloud-relay/README.md) for DNS, TLS, and operations.
The image is built from [`cloud/Dockerfile`](Dockerfile) and published
to `ghcr.io/hamza-labs-core/maktaba-cloud` (cosign-signed) by the
[release workflow](../.github/workflows/release.yml).

### Secrets

Two runtime secrets are injected via the environment (never the TOML
file), layered into `Config` by [`config.Load`](internal/config/config.go):

- **`MAKTABA_CLOUD_TOKEN_SECRET`** — HMAC secret for access tokens,
  ≥ 32 bytes. Required by the `api` role. Generate with
  `openssl rand -base64 48`.
- **`MAKTABA_CLOUD_RELAY_PUBLIC_HOST`** — the relay's public apex
  domain (drives per-server subdomain routing).

### Entitlement key provisioning

The `api` role signs offline feature-gate **entitlement tokens** with an
Ed25519 private key, loaded from `entitlement.private_key_path` (TOML)
by [`entitlement.LoadSignerFromFile`](internal/entitlement/entitlement.go).
The loader accepts the key as raw 32-byte seed / 64-byte expanded form,
**or** base64 / hex of either. Because an Ed25519 seed is just 32
uniformly random bytes, no PEM/DER tooling is needed:

```sh
# 32-byte seed, base64-encoded:
openssl rand 32 | base64 > entitlement.key
```

Mount it read-only (`0600`, in a `0700` dir) and point
`entitlement.private_key_path` at it. **Back it up** — the public key is
derived deterministically from the seed, so rotating the key
invalidates every entitlement token signed with the old one. The VPS
[`setup.sh`](../deploy/cloud-relay/setup.sh) generates this key
automatically.

## Story coverage

| Story | Surface | Where |
|---|---|---|
| 25.1  | bootstrap | cmd/maktaba-cloud, internal/{config,server,db,middleware,clock} |
| 25.2  | email/password | handlers/auth, auth/{argon2id,password,sessions,token} |
| 25.3  | Google OAuth | auth/oauth/google.go |
| 25.4  | Apple OAuth | auth/oauth/{apple,jws}.go |
| 25.5  | account mgmt | handlers/account |
| 25.6  | server claim | stores/servers.go, handlers/servers |
| 25.7  | relay tunnel (server side) | docs/server-agent-side (see api/) |
| 25.8  | relay tunnel (cloud side) | relay/{protocol,tunnel,registry,ws}.go |
| 25.9  | HTTP relay proxy | handlers/relay/proxy.go |
| 25.10 | direct-connection probe | servers.direct_ip/port + dashboard payload |
| 25.11 | bandwidth meter | billing/meter.go |
| 25.12 | tier enforcement | billing/{plans,meter}.go + handlers/relay/proxy.go |
| 25.13 | Stripe checkout | billing/stripe.go + handlers/billing |
| 25.14 | Stripe webhook | handlers/billing/webhook |
| 25.15 | plan comparison | handlers/billing/plans |
| 25.16 | dashboard | handlers/health |
| 25.17 | push ingest | handlers/push |
| 25.18 | APNs dispatcher | push/apns.go |
| 25.19 | FCM dispatcher | push/fcm.go |
| 25.20 | admin fleet | handlers/admin/fleet |
| 25.21 | admin revenue | handlers/admin/revenue |
| 25.22 | subdomain provisioning | handlers/servers/subdomain.go |
| 25.23 | TLS/ACME edge | tls/acme.go |
| 25.24 | rate limiting | ratelimit/ |
| 25.25 | abuse detection | abuse/ |
| 25.26 | entitlement signing | entitlement/ |
| 25.27 | macOS DMG | installers/macos |
| 25.28 | Windows MSI | installers/windows |
| 25.29 | Linux .deb/.rpm | installers/linux |
| 25.30 | Docker image | installers/docker |
| 25.31 | NAS pkgs | installers/nas |
| 25.32 | RPi image | installers/rpi |
| 25.33 | one-click cloud VPS | installers/cloud-vps |
| 25.34 | auto-update | installers/auto-update.md |
| 25.35 | first-run wizard | installers/first-run-wizard.md |
| 25.36 | uninstaller | installers/uninstaller.md |

## Test coverage (this PR)

Unit tests live next to their packages and exercise:

- argon2id PHC round-trip, mismatch handling, length cap.
- password policy (length, whitespace, leaked-corpus check).
- access token sign/verify, expiry, signature mismatch.
- billing tier ordering + month-start truncation.
- Stripe webhook signature (HMAC-SHA256, replay protection).
- relay frame round-trip, oversize rejection.
- HTTP slug extraction from Host header.
- ratelimit burst + refill semantics.
- entitlement Ed25519 sign/verify, expiry, wrong-key rejection.
- TLS HostPolicy (apex allow-list + subdomain resolution).
- config TOML loader + env override + role-specific validation.
- request-id middleware (mint v7, propagate valid, reject non-v7).

Integration tests requiring a live Postgres are out of scope for this
bootstrap PR; the schema migrations are exercised by `goose up` in
each CI matrix entry that boots the binary.

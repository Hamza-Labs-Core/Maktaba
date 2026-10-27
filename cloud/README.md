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
    abuse/                # signal capture, blocklist, login lockout
    audit/                # audit log (admin actions, account events)
    auth/                 # argon2id, password policy, sessions, JWT, OAuth (Google/Apple)
    billing/              # plan catalog, bandwidth meter, Stripe client
    clock/                # injectable time source
    config/               # cloud.toml loader + role-specific validator
    db/                   # *sql.DB pool + goose migrator
    entitlement/          # Ed25519 signing for offline feature gates
    mail/                 # account mails (log driver)
    handlers/
      account/            # /v1/account/{me,password,sessions,export,deletion}
      admin/              # /v1/admin/* (service-admin console)
      auth/               # /v1/auth/{register,verify-email,login,refresh,logout,password/*,oauth/*}
      billing/            # /v1/billing/{plans,me,checkout,portal,usage,entitlement,webhook}
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
  migrations/             # goose-managed SQL slots 0001–0012
  installers/             # macOS/Windows/Linux/Docker/NAS/RPi/cloud-VPS + auto-update + wizard + uninstaller
  configs/                # cloud.example.toml
```

## Running locally

```sh
# Provision a local Postgres:
docker run --rm -d --name maktaba-cloud-pg -e POSTGRES_HOST_AUTH_METHOD=trust -p 5433:5432 postgres:16-alpine

# Migrate and boot the api role on :8090 (:8080 is the home server's API
# on a machine that runs Maktaba):
export MAKTABA_CLOUD_DB_URL='postgres://postgres@localhost:5433/postgres?sslmode=disable'
export MAKTABA_CLOUD_TOKEN_SECRET='development-secret-must-be-at-least-32-bytes-long'
export MAKTABA_CLOUD_LISTEN_ADDR=127.0.0.1:8090
export MAKTABA_CLOUD_PUBLIC_URL=http://localhost:5174   # the web app proxies /v1
go run ./cmd/maktaba-cloud migrate up
go run ./cmd/maktaba-cloud serve --role api

curl http://localhost:8090/healthz

# The cloud web app (account, billing, operator console) on :5174:
pnpm -C ../web dev:cloud

# Make an account a service admin (after it verified its email; the
# verification link is in the api log — mail.driver is "log"):
go run ./cmd/maktaba-cloud admin grant --email you@example.com
```

Stripe stays in test mode: set `MAKTABA_CLOUD_STRIPE_SECRET` to an
`sk_test_` key and the price ids (`MAKTABA_CLOUD_PRICE_PRO`,
`MAKTABA_CLOUD_PRICE_FAMILY`); an `sk_live_` key is refused unless
`[stripe] live_mode = true`. Accounts, billing and the console are
described in [docs/accounts-billing-admin.md](docs/accounts-billing-admin.md);
the anonymous usage data homes share in [docs/usage-data.md](docs/usage-data.md).

Integration tests (real Postgres; each test creates and drops its own
database):

```sh
MAKTABA_CLOUD_DB_URL=postgres://… go test -tags integration ./...
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
| 25.2  | email/password, verification, reset, lockout | handlers/auth, auth/{argon2id,password,sessions,token}, stores/tokens.go |
| 25.3  | Google OAuth | auth/oauth/google.go |
| 25.4  | Apple OAuth | auth/oauth/{apple,jws}.go |
| 25.5  | account mgmt (profile, devices, export, delete) | handlers/account |
| 25.6  | server claim | stores/servers.go, handlers/servers |
| 25.7  | relay tunnel (server side) | docs/server-agent-side (see api/) |
| 25.8  | relay tunnel (cloud side) | relay/{protocol,tunnel,registry,ws}.go |
| 25.9  | HTTP relay proxy | handlers/relay/proxy.go |
| 25.10 | direct-connection probe | servers.direct_ip/port + dashboard payload |
| 25.11 | bandwidth meter | billing/meter.go |
| 25.12 | tier enforcement | billing/{plans,meter}.go + handlers/relay/proxy.go |
| 25.13 | Stripe checkout + portal | billing/stripe.go + handlers/billing |
| 25.14 | Stripe webhook | handlers/billing/webhook.go |
| 25.15 | plan comparison | handlers/billing/plans |
| 25.16 | dashboard | handlers/health |
| 25.17 | push ingest | handlers/push |
| 25.18 | APNs dispatcher | push/apns.go |
| 25.19 | FCM dispatcher | push/fcm.go |
| 25.20 | admin console (accounts, suspend, servers, abuse, audit) | handlers/admin, auth/middleware.RequireServiceAdmin, web/cloud |
| 25.21 | admin revenue | handlers/admin (Revenue) |
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
| 42.7  | linked accounts: home assertions, home list | homeassert/, handlers/homes, stores/homelinks.go (home side: api/internal/auth/cloudlink) |

## The relay and linked accounts (Epic 25, Epic 42 story 42.7)

**Tunnel.** A home's agent (`maktaba-cloudlink`, run by the home's
supervisor) dials `wss://<relay>/v1/relay/ws`, authenticates with the
server id and secret it got at claim (`AUTH`, argon2id at rest), and
carries every request for `<slug>.<relay host>` to the home's **web port**
on loopback (the UI, `/api`, and `/stream/` behind its sign-in gate).
Frames: `relay/protocol.go`. Per-stream credit flow control
(`WINDOW_UPDATE` 0x0c, 1 MiB initial window) is used when both ends
announce `"flow":1` in AUTH / AUTH_OK; one slow client never stalls the
tunnel, and no frame is ever dropped (a stream is failed instead). Only
the wait for the home's response head is bounded (60s): a video plays as
long as its client reads. The agent pings every 25s and redials with
backoff when the cloud is silent for 35s.

**Claim.** `POST /v1/servers/claims` (owner, user auth) mints an 8-char
code; the agent redeems it at `POST /v1/servers/claims/redeem` (no user
token: the code is the credential), in one transaction with the server
row (a reserved or taken slug answers 409 and leaves the code usable).
The answer carries the cloud's **assertion public key**, which the home
pins (`cloudlink.identity.json` beside the sealed credentials).

**Linked accounts.** The cloud never grants anything inside a home:

- `GET /v1/homes`: the homes a cloud account is linked to (reported by
  each home, by cloud user id only: `PUT /v1/servers/{id}/links`, server
  secret) or owns.
- `POST /v1/homes/{id}/assertion` `{purpose}`: a one-use, one-minute
  Ed25519 assertion naming the cloud account and the home (`homeassert`).
  `signin` only for a home that linked the account; `link` for any home,
  bound to the nonce the home user's own sign-in started.
- `GET /v1/homes/{id}/link?nonce=&return=` (public): where a home's Account
  page sends the browser to link. It answers a redirect to this cloud's own
  web app (`server.web_url`, its `/link-home` page) and nowhere else; that
  page signs the person in, asks them to confirm, asks for the `link`
  assertion and goes back to `return` with it in the fragment
  (`#cloud-link=…`), which no server sees.
- The home verifies it with its pinned key, finds the home user it
  linked, and issues its own tokens with that user's role capped by the
  link's scope (`play` by default; `manage` only set by an admin, on the
  home, for their own link). Every role rule is enforced by the home's
  API (`POST /api/auth/cloud`, `/api/me/cloud-link`).

The assertion key is derived from the entitlement key
(`entitlement.private_key_path`), so no second secret is provisioned;
without it the api role answers 503 to assertion requests.

**Metering.** `billing.Meter` counts each home's relayed bytes while a
response streams (every MiB), gates on the plan's monthly allowance, and
flushes to `bandwidth_samples` / `bandwidth_monthly` every 30s and on
shutdown. **TLS:** the edge's on-demand ask is `/v1/relay/tls-ask`
(claimed slugs only).

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

Integration tests against a live Postgres (`go test -tags=integration
./...` with `MAKTABA_CLOUD_DB_URL`) cover the claim, agent auth, home links
and assertions (`handlers/homes`), the meter's flush, relay metrics and the
privacy purge. The relay's end to end test (`handlers/relay/e2e_test.go`)
runs a real tunnel and agent behind the relay role's middleware.

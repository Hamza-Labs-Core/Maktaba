# Maktaba Cloud Relay — VPS deployment

Deploy `maktaba-cloud --role relay` to a single VPS: the relay binary,
Postgres, Redis, and Caddy (reverse proxy + automatic Let's Encrypt
TLS), wired together with Docker Compose and managed by systemd.

The relay is the public ingress that self-hosted Maktaba servers dial
out to. Each registered server is reachable at
`<slug>.<your-domain>`; the relay terminates the client connection and
tunnels it back over a persistent WebSocket to the origin server (see
[`role_relay.go`](../../cloud/cmd/maktaba-cloud/role_relay.go)).

```
            ┌────────── VPS ──────────────────────────────┐
 client ──▶ │ Caddy :443  ──HTTP:8443──▶ maktaba-cloud     │
 (https)    │   auto-TLS                  (role=relay)      │
            │                              │   │            │
            │                       postgres   redis        │
            └──────────────────────────────────────────────┘
                              ▲ outbound WSS
                  self-hosted server (origin)
```

## Prerequisites

- A VPS running **Ubuntu 22.04 or 24.04** with a public IPv4 address.
- A domain you control, with **two DNS records** pointing at the VPS:
  - apex: `relay.example.com  A  <vps-ip>`
  - wildcard: `*.relay.example.com  A  <vps-ip>`
- Ports **80** and **443** reachable from the internet (Let's Encrypt
  HTTP-01 + client traffic). The setup script's firewall opens exactly
  these plus SSH.

## Quick start

```bash
# On the VPS, as a sudo-capable user:
git clone https://github.com/Hamza-Labs-Core/Maktaba.git
cd Maktaba/deploy/cloud-relay
sudo ./setup.sh                 # installs Docker, generates secrets, etc.

# Edit the two values the script can't guess, then start:
sudo -e /opt/maktaba-relay/.env   # set RELAY_DOMAIN + ACME_EMAIL
sudo systemctl enable --now maktaba-relay
```

`setup.sh` is idempotent — re-run it any time. On the first run it:

1. installs Docker Engine + the compose plugin,
2. creates the `maktaba` system user (in the `docker` group),
3. stages the stack in `/opt/maktaba-relay`,
4. **generates** the token secret, Postgres password, and Ed25519
   entitlement key (only if not already set),
5. caps container log growth (`json-file`, 10 MB × 3),
6. configures UFW to allow only SSH + 80 + 443,
7. installs the `maktaba-relay` systemd unit,
8. pulls images and starts the stack — *unless* `RELAY_DOMAIN` /
   `ACME_EMAIL` still hold template values, in which case it stops and
   tells you to finish `.env` first.

## Configuration

All runtime config is environment-driven — see
[`.env.example`](.env.example) for the full annotated list. The relay
role needs only:

| Variable | Purpose |
|---|---|
| `RELAY_DOMAIN` | Your apex relay domain (drives TLS + subdomain routing) |
| `ACME_EMAIL` | Let's Encrypt account email |
| `MAKTABA_CLOUD_DB_URL` | Postgres DSN (host = `postgres`) |
| `MAKTABA_CLOUD_REDIS_URL` | Redis DSN (host = `redis`) |
| `MAKTABA_CLOUD_PUBLIC_URL` | Public `https://` base URL |
| `MAKTABA_CLOUD_TOKEN_SECRET` | Access-token HMAC secret (≥ 32 bytes) |
| `POSTGRES_PASSWORD` | Must match the password in `MAKTABA_CLOUD_DB_URL` |

`MAKTABA_CLOUD_RELAY_PUBLIC_HOST` is set automatically from
`RELAY_DOMAIN` by the compose file.

### Manually generating secrets

`setup.sh` does this for you, but if you provision secrets out of band:

```bash
# Access-token HMAC secret:
openssl rand -base64 48

# Postgres password (hex = no URL-encoding needed in the DSN):
openssl rand -hex 24
```

## Entitlement key provisioning

The cloud signs user/server **entitlement tokens** with an Ed25519
private key (see
[`entitlement.go`](../../cloud/internal/entitlement/entitlement.go)).
This is used by the **`api` role**; a pure relay does not sign
entitlements, but the key is pre-provisioned so the same stack can run
`--role api` unchanged.

An Ed25519 *seed* is just 32 uniformly random bytes. The loader
(`LoadSignerFromFile`) accepts the seed as raw 32/64 bytes, **base64**,
or hex — so generating one needs no PEM/DER tooling:

```bash
# 32-byte seed, base64-encoded — what setup.sh writes to
# /opt/maktaba-relay/secrets/entitlement.key
openssl rand 32 | base64 > entitlement.key
```

The file is mounted read-only into the container at
`/etc/maktaba/cloud/secrets/entitlement.key` (referenced by
`ENTITLEMENT_KEY_PATH`), owned by the `maktaba` user with `0600`
permissions, in a `0700` directory. **Back it up** — rotating it
invalidates every entitlement token signed with the old key. The
matching public key is derived deterministically from the seed, so
verifiers stay in sync automatically once they load the same key.

## Running the `api` role (full cloud)

The image runs any role. To run `--role api` you must supply the
id/path-based settings that have no env override (OAuth client IDs,
Apple OAuth, APNs/FCM key paths, entitlement key path). Drop a
`cloud.toml` next to the compose file (it is mounted read-only at
`/etc/maktaba/cloud/cloud.toml`) — start from
[`cloud/configs/cloud.example.toml`](../../cloud/configs/cloud.example.toml) —
place key material under `./secrets/`, and change the service `command`
to `["serve", "--role", "api"]`.

## Wildcard TLS

The default Caddyfile uses **on-demand TLS**: a Let's Encrypt cert is
issued (via HTTP-01) the first time each `<slug>.<RELAY_DOMAIN>` is
requested. This works with the stock `caddy:2-alpine` image and your
wildcard DNS record, with no DNS-provider plugin.

Issuance is gated by an `ask` to the relay's `/healthz`, which permits
any host. Because only `*.RELAY_DOMAIN` resolves to your VPS and Let's
Encrypt rate-limits issuance, this is acceptable for most deployments.
To lock it down, either:

- point `ask` at a real allow-list endpoint that returns `200` only for
  known server slugs, or
- switch to a **DNS-01 wildcard certificate** — build a Caddy image
  with your DNS provider module
  ([caddy-dns](https://github.com/caddy-dns)) and replace the catch-all
  block with a `*.{$RELAY_DOMAIN}` site using `tls { dns <provider> }`.

## CI/CD

The relay has its own pipeline at
[`.github/workflows/relay.yml`](../../.github/workflows/relay.yml),
separate from the repo-wide `ci.yml` so it can build, publish, sign, and
roll out the `maktaba-cloud` image on its own triggers.

**Triggers**

| Event | What runs |
|---|---|
| PR touching `cloud/**` or `deploy/cloud-relay/**` | `lint` + `test` + `build` |
| Push to `main` (same paths) | the above, then `docker` build + push (`:edge`, `:sha-<short>`) |
| Tag `v*` | the above with `:vX.Y.Z` + `:latest`, then `sign` (cosign) and `deploy` |
| Manual `workflow_dispatch` | `deploy` only, of a chosen (or `latest`) tag |

**Jobs**

- **lint** — `golangci-lint` over the `cloud` module.
- **test** — `go test ./...` against a Postgres service container
  (`MAKTABA_CLOUD_DB_URL` points at it); runs the unit then the
  integration-tagged tier.
- **build** — cross-compiles the relay binary for `linux/amd64` and
  `linux/arm64` with the reproducibility envelope.
- **docker** — builds [`cloud/Dockerfile`](../../cloud/Dockerfile) as a
  **multi-arch** image (amd64 + arm64, so it runs on x86 droplets *and*
  arm64 Ampere/Graviton VPSes) and pushes to
  `ghcr.io/hamza-labs-core/maktaba-cloud`. Push events only.
- **sign** — keyless [cosign](https://docs.sigstore.dev/) over the pushed
  image digest, via the workflow's OIDC identity (no key to provision).
  Tags only. Verify with:
  ```bash
  cosign verify ghcr.io/hamza-labs-core/maktaba-cloud:vX.Y.Z \
    --certificate-identity-regexp 'https://github.com/.+/Maktaba/.github/workflows/relay.yml@.*' \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com
  ```
- **deploy** — runs [`deploy.sh`](deploy.sh) against the VPS. Gated by the
  `production` GitHub Environment (a manual approval gate — configure
  required reviewers in **Settings → Environments → production**). Runs
  on a `v*` tag or a manual dispatch.

The repo's [`release.yml`](../../.github/workflows/release.yml) *also*
builds + signs the `cloud` binary and image as part of the full release
matrix (single-arch image, multi-arch binaries); `relay.yml` is the
relay-focused, multi-arch, deploy-capable leg.

### Required GitHub secrets & variables

Set these under **Settings → Secrets and variables → Actions** (the SSH
secrets belong to the `production` environment so only approved deploys
can read them):

| Name | Kind | Purpose |
|---|---|---|
| `RELAY_SSH_HOST` | secret | VPS hostname or IP for the deploy SSH |
| `RELAY_SSH_USER` | secret | SSH login user (must be in the `docker` group) |
| `RELAY_SSH_KEY` | secret | Private key **contents** (PEM) for that user |
| `REGISTRY_TOKEN` | secret | *Optional* — only if pushing to a non-GHCR registry; otherwise the auto-provisioned `GITHUB_TOKEN` is used |
| `MAKTABA_REGISTRY` | variable | *Optional* — override the default `ghcr.io/hamza-labs-core` registry |
| `RELAY_PUBLIC_URL` | variable | *Optional* — shown as the deployment URL on the environment |

### Deploying by hand

`deploy.sh` is the single source of truth — CI and a laptop run the same
script. Roll out a specific tag:

```bash
RELAY_SSH_HOST=relay.example.com \
RELAY_SSH_USER=maktaba \
MAKTABA_CLOUD_TAG=v1.2.3 \
make relay-deploy
```

It SSHes in, pulls the image, runs migrations in a throwaway container,
swaps the stack (`docker compose up -d --wait`), verifies health, and
**rolls back to the previous tag if the health check fails**. Override the
target dir with `DEPLOY_DIR=` and add a public probe with
`HEALTH_URL=https://relay.example.com/readyz` if desired.

Run the PR gates locally before pushing:

```bash
make relay-ci      # lint + test + build (mirrors relay.yml)
```

## Operations

```bash
# Status / logs
systemctl status maktaba-relay
cd /opt/maktaba-relay && docker compose logs -f
docker compose logs -f maktaba-cloud      # just the relay

# Health
curl -fsS https://relay.example.com/healthz   # liveness
curl -fsS https://relay.example.com/readyz    # readiness (db + migrations)

# Upgrade to a pinned release
sudo -e /opt/maktaba-relay/.env               # set MAKTABA_CLOUD_TAG=vX.Y.Z
sudo systemctl reload maktaba-relay           # pulls + recreates, waits healthy

# Stop / start
sudo systemctl stop maktaba-relay
sudo systemctl start maktaba-relay

# Backups (do these regularly)
docker compose exec postgres pg_dump -U maktaba maktaba > backup.sql
cp /opt/maktaba-relay/secrets/entitlement.key entitlement.key.bak
```

Database migrations run automatically on relay start (the binary
applies pending migrations before serving — see
[`main.go`](../../cloud/cmd/maktaba-cloud/main.go)); `/readyz` reports
`migrations_behind` until they complete.

## Troubleshooting

- **Cert not issued / TLS errors** — confirm both the apex and wildcard
  A records resolve to the VPS and that ports 80/443 are open
  (`sudo ufw status`). Watch issuance with
  `docker compose logs -f caddy`.
- **`readyz` returns `db_unreachable`** — check `MAKTABA_CLOUD_DB_URL`
  uses host `postgres` and the password matches `POSTGRES_PASSWORD`.
- **Relay exits on boot** — `docker compose logs maktaba-cloud`; a
  missing/short `MAKTABA_CLOUD_TOKEN_SECRET` (api role) or empty
  `MAKTABA_CLOUD_DB_URL` fails validation at startup.

# Story 50.02 — One wildcard certificate on every node

> Epic 50 · Phase 1 · Depends on: 50.01 · Needed by: 50.07

## Why

Today Caddy issues **one Let's Encrypt certificate per home**, the first time someone opens
`<slug>.relay.maktaba.tv` (on-demand TLS, `deploy/cloud-relay/Caddyfile`), and keeps them in
the `caddy_data` volume on that one machine. A standby node starts with none. Let's Encrypt
allows **50 new certificates per registered domain per week**
([rate limits](https://letsencrypt.org/docs/rate-limits/)), so after a failover with more than
about 50 homes, most homes would have no certificate for days. Failover would fail on TLS.

One wildcard certificate for `relay.maktaba.tv` and `*.relay.maktaba.tv` covers every home,
can sit on both nodes before it is needed, and costs two issuances every 60 days.

## Description

### 1. DNS-01 wildcard through Cloudflare

- Caddy gets the `caddy-dns/cloudflare` module. The stock `caddy:2-alpine` image has no DNS
  modules, so `relay.yml` builds our own image with `xcaddy`, pinned:
  `ghcr.io/hamza-labs-core/maktaba-caddy:<caddy version>-cf<module version>`, multi-arch,
  cosign-signed like the cloud image.
- The Caddyfile has one site for `relay.maktaba.tv, *.relay.maktaba.tv` with
  `tls { dns cloudflare {env.CF_API_TOKEN} }`. The on-demand catch-all goes away after the
  transition (§3).
- `api.maktaba.tv` and `app.maktaba.tv` sit behind the Cloudflare proxy; their origin
  certificate is the same kind of DNS-01 certificate (Cloudflare "Full (strict)").
  **Updated by 50.31:** by default they reach the origin through the Cloudflare Tunnel and need
  no origin certificate; this one is needed only in 50.31's fallback mode (per-hostname
  Authenticated Origin Pulls on Caddy).
- The token is a Cloudflare API token with **Zone → DNS → Edit on `maktaba.tv` only**, stored as
  `/opt/maktaba-cloud/secrets/cloudflare.token` (0600) on each node.

### 2. Each node issues its own

- Node A and node B each get their own wildcard certificate. That is 2 issuances of the same
  name set every ~60 days. Let's Encrypt allows **5 duplicate certificates (same exact names)
  per week**; 2 is well inside, and a reinstalled node adds 1.
- Why not share one certificate through Postgres or a shared disk? A shared copy adds a
  dependency at exactly the moment things fail (the replica is read-only, a shared disk is a
  single point). Two independent certificates fail independently.
- The witness W checks both nodes' certificates daily (`openssl s_client` against each node's
  own private address with SNI `x.relay.maktaba.tv`) and alerts at **21 days** to expiry (50.10).
- **Fallback CA:** if Let's Encrypt fails, Caddy tries ZeroSSL (Caddy's `issuer acme` list:
  Let's Encrypt, then ZeroSSL with an EAB key), matching story 25.23. CAA records allow both
  (50.01).

### 3. Transition from on-demand certificates

1. The new Caddyfile keeps the on-demand catch-all **below** the wildcard site for one release,
   still gated by `/v1/relay/tls-ask`, so any name outside the wildcard (none expected) keeps
   working.
2. After one release with no on-demand issuance (metric from Caddy logs: zero
   `certificate obtained` for on-demand names in 7 days), the catch-all and the `ask` block are
   removed. `/v1/relay/tls-ask` stays in the relay (cheap, and 50.15's direct names may reuse it).

### 4. The single-VPS deploy

`deploy/cloud-relay/` gets the same Caddy image and Caddyfile, so the one-VPS setup the owner
starts with already has the wildcard. A `CF_API_TOKEN` line is added to `.env.example`;
`setup.sh` refuses to start Caddy while it holds the template value.

## Acceptance criteria

- **Given** a node with the Cloudflare token,
  **when** Caddy starts,
  **then** it holds a valid certificate for `relay.maktaba.tv` and `*.relay.maktaba.tv`
  within 2 minutes, and no per-home certificate is requested.
- **Given** the standby node B has never served traffic,
  **when** a client connects to B's address with SNI `anyhome.relay.maktaba.tv`,
  **then** B presents the wildcard certificate (before any failover).
- **Given** Let's Encrypt answers errors for issuance,
  **when** Caddy renews,
  **then** it gets a ZeroSSL certificate instead, and an alert says which CA issued it.
- **Given** a certificate with less than 21 days left on any node,
  **when** the witness's daily check runs,
  **then** the owner is alerted with the node and the expiry date.
- **Given** a name outside the wildcard during the transition,
  **when** it is requested,
  **then** it still gets an on-demand certificate only if `/v1/relay/tls-ask` approves it.
- **Given** the Cloudflare token is missing or wrong,
  **when** Caddy starts,
  **then** Caddy logs the DNS-01 failure, keeps the old certificate if it has one, and the
  node's readiness check (50.07) reports `tls_missing` so it is never made leader without a
  certificate.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | CI (image) | `relay.yml` builds `maktaba-caddy` | `caddy list-modules` | contains `dns.providers.cloudflare` |
| T02 | CI | Caddyfile | `caddy validate` with test env | valid |
| T03 | rehearsal | 50.06 `cert-on-standby`: Pebble (Let's Encrypt's test CA) + a fake DNS-01 provider (`caddy-dns/acmedns` against a local acme-dns) | start both nodes; connect to B with SNI | wildcard presented by B; Pebble saw 2 issuances, 0 per-home |
| T04 | rehearsal | Pebble set to fail | renew | falls to the second issuer (a second Pebble standing in for ZeroSSL) |
| T05 | unit (shell) | expiry check script | cert with 20 days left | exit 1 with node + date |

## Edge cases

- **Wildcards cover one level only.** `a.b.relay.maktaba.tv` isn't covered; slugs can't contain
  dots (25.22 regex), so it never happens. Phase 2's `*.r3.relay.maktaba.tv` needs its own
  wildcard per relay (50.16).
- **Token leak.** The token can edit `maktaba.tv` DNS only. Rotation: make a new token, update
  both nodes' secret file, reload Caddy, revoke the old one (runbook in 50.12).
- **Clock skew on a node** breaks ACME; chrony is part of the node setup (50.07).
- **Rate-limit math in a disaster.** Rebuilding both nodes and the staging pair in one week is
  4 duplicate issuances: still under 5. The runbook warns against more.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `deploy/caddy/Dockerfile` (new) | `xcaddy build` with `github.com/caddy-dns/cloudflare`, pinned versions. |
| `.github/workflows/relay.yml` | Build, push and sign `maktaba-caddy` (multi-arch) on changes to `deploy/caddy/**`; T01, T02. |
| `deploy/cloud-relay/Caddyfile` | Wildcard site with DNS-01; issuer list LE → ZeroSSL; on-demand block kept below for one release. |
| `deploy/cloud-relay/docker-compose.yml` | Caddy image → `maktaba-caddy`; `CF_API_TOKEN` from the secret file. |
| `deploy/cloud-relay/.env.example`, `deploy/cloud-relay/setup.sh` | Token placeholder; refuse to start with the template value. |
| `deploy/cloud-ha/caddy/Caddyfile` (new, used by 50.07) | Same site, plus `api.`/`app.` origin. |
| `deploy/cloud-ha/witness/check-certs.sh` (new) | Daily expiry check against A and B. |
| `deploy/cloud-relay/README.md` | Replace "Wildcard TLS" section. |

### Tests

- T01, T02 in `relay.yml`. T03, T04 as rehearsal scenario **`cert-on-standby`** (50.06). T05 in
  the witness scripts' test.

### Rollout

1. Merge; `relay.yml` publishes the Caddy image.
2. The owner adds the Cloudflare token when the first VPS exists (README §12).
3. One release later, remove the on-demand block.

### Rollback

Switch the compose file back to `caddy:2-alpine` and the old Caddyfile: on-demand issuance
resumes for each home (fine below ~50 homes; above that, restore the old `caddy_data` volume).

### Dependencies

50.01 (the names and CAA records).

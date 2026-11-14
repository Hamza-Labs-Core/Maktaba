# Maktaba Cloud: names and DNS

The one list of the cloud's public names: what each points at, its TTL, whether Cloudflare
proxies it, and who changes it (Epic 50, story 50.01). The owner creates these records when
the nodes exist (Epic 50 README §12); nothing in the repo calls a DNS API for them.

- **maktaba.tv** is for users. **maktaba.dev** is for development, the wiki and staging.
- **maktaba.app is not ours.** `tools/lint/no-foreign-domains.sh` fails a pull request that
  adds a host name under it. Reverse-DNS app identifiers (`com.maktaba.app`, `io.maktaba.app`)
  are not host names and stay.

## The names

| Name | Points at | Cloudflare | TTL | Used by | Phase |
|---|---|---|---|---|---|
| `maktaba.tv`, `www.maktaba.tv` | marketing site | proxied | auto | people | — |
| `api.maktaba.tv` | the Cloudflare Tunnel (`<tunnel-id>.cfargotunnel.com`, 50.31) | **proxied** | auto | apps, homes (claim, entitlements, home list) | 1 |
| `app.maktaba.tv` | the user web Worker; `/v1/*` through the Tunnel (50.32) | proxied | auto | the cloud web app | 1 |
| `admin.maktaba.tv` | the admin web Worker; `/v1/*` through the Tunnel (50.32) | proxied | auto | the operator console (staff only, 50.33) | 1 |
| `plugins.maktaba.tv` | the plugin store's Workers (Epic 54), on Cloudflare | proxied | auto | the plugin store | 54 |
| `plugins-cdn.maktaba.tv` | the store's signed files (Epic 54), on Cloudflare | proxied | auto | homes downloading plugin files | 54 |
| `ingest.maktaba.tv` | the broadcast edge's Worker (50.21), a Workers Custom Domain, **on Cloudflare, not on Hetzner** | proxied | auto | homes pushing broadcast segments (outbound only) | B |
| `live.maktaba.tv` | the broadcast edge's Worker (50.21): playlists and segments from R2 and the edge cache | proxied | auto | viewers' players | B |
| `watch.maktaba.tv` | the broadcast edge's Worker (50.21): the watch site and its API (50.26, 50.27), guide blobs | proxied | auto | viewers | B |
| `staging-ingest.maktaba.dev`, `staging-live.maktaba.dev`, `staging-watch.maktaba.dev` | the broadcast edge's staging Worker (50.21) | proxied | auto | test publishers, never users | B |
| `relay.maktaba.tv` | the Floating IP | **DNS only** | 300 s | homes' tunnel (`/v1/relay/ws`) | 1 |
| `*.relay.maktaba.tv` | the Floating IP | **DNS only** | 300 s | apps reaching a home: `<slug>.relay.maktaba.tv` | 1 |
| `canary.relay.maktaba.tv` | covered by the wildcard | DNS only | — | the canary home (50.10); a reserved slug | 1 |
| `status.maktaba.tv` | GitHub Pages (`hamza-labs-core.github.io`), **off Hetzner** (50.11) | DNS only | 300 s | people, the apps' banner | 1 |
| `witness.maktaba.tv` | the witness's primary IPv4 | DNS only | 300 s | the outside probe's pushes and the status page's view of the witness, behind a token (50.10, 50.11) | 1 |
| `r<N>.relay.maktaba.tv` | relay N's own primary IPv4 | DNS only | 60 s | homes' tunnel to an assigned relay | 2 |
| `*.r<N>.relay.maktaba.tv` | relay N's own primary IPv4 | DNS only | 60 s | relay hints for apps (50.16) | 2 |
| `relay.maktaba.tv`, `*.relay.maktaba.tv` | round robin over the healthy relays | DNS only | **60 s** | the stable names in Phase 2 | 2 |
| `staging.relay.maktaba.dev`, `*.staging.relay.maktaba.dev`, `staging-api.maktaba.dev` | the staging pair (50.14) | DNS only | 60 s | load tests, never users | 1 |

## The rules

- **Relayed video never goes through the Cloudflare proxy** (Cloudflare's terms forbid video
  on non-Enterprise plans), so every relay name is **DNS only**.
- **The API, the web app and the operator console never point at an address.** They reach the
  origin only through the Cloudflare Tunnel, whose DNS records CI writes (50.34); no inbound
  port serves them.
- **No name points at a node's own address in Phase 1**, only at the Floating IP, so failover
  never changes DNS. The 300 s TTL matters only on the manual zone-disaster path (50.09).
- **Phase 2 relay names use 60 s**, because the witness removes a dead relay's record.
- **Reserved slugs.** No home can take `canary`, `status`, `api`, `app`, `admin`, `www`,
  `plugins`, `plugins-cdn`, `r1`…`r99`, `direct`, `staging`, `ingest`, `live` or `watch` (and the
  operator names of story 25.22): the cloud answers `409 subdomain_reserved`
  (`cloud/migrations/00500001_reserved_infra_slugs.sql`, `00500015_reserved_broadcast_slugs.sql`).
- **The broadcast edge is not on Hetzner** (50.21). `ingest`, `live` and `watch` are one Worker
  with R2 and Durable Objects on Maktaba's Cloudflare account; they have no origin, so their
  custom domains own their records. Broadcast video is served from R2 through the Worker,
  never through the proxy from an origin (README §15.5).
- **The plugin store is not on Hetzner.** `plugins` and `plugins-cdn` are Cloudflare-hosted
  (Epic 54); a Hetzner outage doesn't touch them and failover never moves them. The store's
  Cloudflare deploy token is its own.
- **Cookies are host-only.** No cloud cookie is set with `Domain=.maktaba.tv`, so a cloud
  session never reaches a home on `*.relay.maktaba.tv`, the store or the operator console.
- **Direct names are never under `maktaba.tv`.** They live on 3–4 separate registered domains
  used only for direct names, each delegated by `NS` to the cloud's own DNS responder on nodes A
  and B (`--role=dns`, UDP/TCP 53), as `1-2-3-4.<home-id>.<direct-domain>` → `1.2.3.4`. The list
  is configuration (`direct.domains`, story 50.15 §4).

## CAA records

On `maktaba.tv`, so only our two certificate authorities can issue (50.02):

```
maktaba.tv.  CAA 0 issue "letsencrypt.org"
maktaba.tv.  CAA 0 issue "sectigo.com"
```

(`sectigo.com` is ZeroSSL's CA, the fallback.)

## The direct-name domains (story 50.15)

Direct remote access gives each home its own HTTPS names under one of several **separate
registered domains, never `maktaba.tv`** (`direct.domains` in `cloud.toml`): each domain has its
own Let's Encrypt allowance of 50 new certificates a week, and `maktaba.tv`'s certificates are
never at risk. A home's names are `<ip as a label>.<home id>.<direct domain>`
(`81-2-3-4.<home id>.direct-a.example`, `2001-db8--1.<home id>.direct-a.example`), answered by
the cloud's own name server (`maktaba-cloud serve --role dns` on nodes A and B), which computes
the address from the name: an address change needs no record anywhere.

For each direct-name domain, at its registrar:

| Record | Value | Why |
|---|---|---|
| `NS` | `ns1.<domain>`, `ns2.<domain>` | the domain is answered by the cloud's name server |
| glue `A` for `ns1` / `ns2` | node A's and node B's primary IPv4 | the delegation's own names |

The name server answers the apex's `NS`, `SOA` and `CAA` itself (`CAA 0 issue` for
`letsencrypt.org`, `sectigo.com` for ZeroSSL and `pki.goog` for Google Trust Services), each
home's `A` / `AAAA`, and the ACME `_acme-challenge` TXT values from Postgres. It refuses any name
under no configured domain (it is no open resolver) and rate-limits each source.

**Adding a domain** is configuration only: register it, add the two `NS` records and the glue,
append it to `direct.domains` on A and B and restart the roles. New homes start using it at once.

**On the nodes** (`deploy/cloud-ha`):
- **The service.** The name server is `maktaba-cloud-dns.service` on A and B, always (not only on the leader): each node is one of the two `NS`. It needs `[direct] domains`, `ns1` and `ns2` in `/etc/maktaba/cloud.toml`, and `DIRECT_DNS=yes` in `cloud-ha.env`, before `setup.sh` runs.
- **What `setup.sh` changes.** It opens 53/udp and 53/tcp, and turns off systemd-resolved's stub listener, which would otherwise hold port 53. The node then resolves through resolved's upstream servers.
- **Deploys.** Each deploy (`deploy.sh` step 5, `bin/dns-update`) updates the standby's name server, then the leader's, so one always answers. A release whose name server doesn't stay up goes back to the previous binary on that node, and the deploy stops before the other node.

## Who changes what

| What | Who | Where |
|---|---|---|
| The records above | the owner | Cloudflare dashboard (or the CI token for the Tunnel records, 50.34) |
| The Floating IP a name points at | nobody: it moves between nodes by itself (50.07) | — |
| A dead Phase 2 relay's record | the witness, through the Cloudflare API (50.16) | — |
| The reserved slugs | a cloud migration | `cloud/migrations/` |
| The foreign-domain list | a pull request | `tools/lint/no-foreign-domains.sh` (`FOREIGN`) |

The rehearsal in CI (50.06) uses these names under a test zone (`*.relay.test`).

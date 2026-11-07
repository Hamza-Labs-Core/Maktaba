# The cloud's Cloudflare configuration

Epic 50, section 15 (stories 50.30, 50.31, 50.34). Everything Cloudflare does in front of the
cloud (`app.maktaba.tv`, `api.maktaba.tv`, `admin.maktaba.tv`) is described here, in the
repository. CI checks it offline on every pull request and deploys it from `main` only. **Nothing
here is deployed from a laptop**, and no pull request calls Cloudflare.

The dashboard is for reading and for emergencies. An emergency change there must be copied back
into these files, because the next deploy shows it as drift and writes the files' state over it.

## What is here

| File | What | Story |
| --- | --- | --- |
| `zone.maktaba.tv.json`, `zone.schema.json` | The zone's TLS settings, WAF managed rules, custom rules and rate limits | 50.30 |
| `tunnel.maktaba.tv.json` | The Tunnel's ingress: each name to its loopback listener, everything else 404, never a relay name | 50.31 |
| `ips-v4.txt`, `ips-v6.txt` | Cloudflare's published IP ranges, for the fallback origin mode's Caddy. A weekly job opens a pull request when they change | 50.31, 50.34 |

The tools are in `tools/cloudflare/` (Node 22, no dependency):

| Tool | What |
| --- | --- |
| `token-check.mjs` | Runs first in every deploy job: the token is active and can do what the piece needs (the table below), or the job fails naming the missing permission, before any write |
| `smoke.mjs` | After a deploy: the live hosts answer as they should (the app's marker and CSP; an admin path's 404 on a user name and 401 on the admin name; `readyz` through the Tunnel; the edge's own 404 JSON) |
| `ips.mjs` | Fetches Cloudflare's published ranges and checks each line |
| `zone.mjs`, `tunnel.mjs` | `check` (offline), `plan`, `apply` (50.30, 50.31) |

Their tests run against a local fake of Cloudflare's API (`node --test 'tools/cloudflare/*.test.mjs'`).

## The Tunnel (50.31)

`tunnel.maktaba.tv.json` names the tunnel (`maktaba-cloud-production`, which the owner
creates), its mode and each name's loopback listener. `tunnel.mjs check` refuses a relay name, a
wildcard, a service that is not `http://127.0.0.1:<port>` or `http://[::1]:<port>`, a missing
`api`, `app` or `admin`, a rule without a reason, and an unknown field. What it writes:

- the ingress: each name to its listener with its own `Host` header, then `http_status:404` for
  everything else (the tool adds it, so no file can leave it out);
- each name's DNS record: a proxied `CNAME` to `<tunnel id>.cfargotunnel.com` (the ingress first,
  so a name never reaches a tunnel that does not route it). The web Workers (50.32) therefore
  attach by Workers Routes on these proxied names, not by Custom Domains.

Before the owner creates the tunnel, `plan` and `apply` write nothing and say so with a notice.

**The fallback, `"mode": "aop"`** (50.31 §3), switched by a pull request and never automatically:
`tunnel.mjs apply` uploads Maktaba's own client certificate (`CLOUDFLARE_AOP_CERT`,
`CLOUDFLARE_AOP_KEY` in the `production` environment), turns per-hostname Authenticated Origin
Pulls on for each name, and then points the names at `aop.origin_ipv4` (the Floating IP) as
proxied `A` records. The nodes must already run Caddy's aop sites (`ORIGIN_MODE=aop` in
`/etc/maktaba/cloud-ha.env`, `server.origin_mode = "aop"`; `deploy/cloud-ha/README.md`).

## The workflows

| Workflow | Pull request | Push to `main` (or by hand) |
| --- | --- | --- |
| `ci.yml`, job `cloudflare` (`_cloudflare.yml`) | The tools' tests; `zone.mjs check`; `tunnel.mjs check` | — |
| `cloudflare-zone.yml` | — | The same checks, then the token check, the zone's plan and apply, the Tunnel's plan and apply (each read back), and a smoke check of the edge |
| `cloud-web-deploy.yml` | — | For each of `maktaba-cloud-web` (`web/packages/cloud-web`) and `maktaba-admin-web` (`web/packages/admin-web`), once its `wrangler.toml` exists: build, test, token check, `wrangler deploy`, smoke check. By hand with `rollback: true`: `wrangler rollback` |
| `cloudflare-ips.yml` | — | Weekly: Cloudflare's ranges, and a pull request when they change |

Every deploy job:

- runs in the GitHub `production` environment, so its rules (required reviewers, if set) apply;
- runs one at a time per workflow, never cancelled in progress;
- skips with a notice naming the missing secret when `CLOUDFLARE_API_TOKEN` or
  `CLOUDFLARE_ACCOUNT_ID` is empty, so `main` stays green before they are set;
- masks the account id, and never prints a token.

## The token

One org secret, `CLOUDFLARE_API_TOKEN`, holds the union of the rows below. It is limited to this
account and the two zones. A piece may have its own token instead:
`CLOUDFLARE_API_TOKEN_ZONE`, `CLOUDFLARE_API_TOKEN_TUNNEL` or `CLOUDFLARE_API_TOKEN_WORKERS`.
Each workflow uses its piece's own token when it is set, and the shared one otherwise.

| Piece | Account permissions | Zone permissions |
| --- | --- | --- |
| Developer portal (existing) | Workers Scripts: Edit | `maktaba.dev`: Workers Routes: Edit, Zone: Read, DNS: Edit |
| User and admin web Workers | Workers Scripts: Edit | `maktaba.tv`: Workers Routes: Edit, Zone: Read |
| Zone security (WAF, managed rules, rate limits, settings) | — | `maktaba.tv`: Zone WAF: Edit, Zone Settings: Edit, Zone: Read |
| Tunnel ingress and DNS records | Cloudflare Tunnel: Edit | `maktaba.tv`: DNS: Edit |
| Per-hostname AOP certificate (50.31's fallback only) | — | `maktaba.tv`: SSL and Certificates: Edit |
| Broadcast edge (50.21, later) | Workers Scripts: Edit, Workers R2 Storage: Edit | `maktaba.tv`: Workers Routes: Edit, DNS: Edit |

Adding **API Tokens: Read** lets `token-check.mjs` compare the token's own policy with this
table. Without it, the check makes one read call per permission instead. That proves the token
reaches each resource, and Cloudflare checks the write itself when the deploy applies it.

**Not in the CI token:**

- the nodes' DNS-01 token (50.02: DNS Edit on `maktaba.tv` only, kept on the nodes);
- the Tunnel's own token (`CLOUDFLARE_TUNNEL_TOKEN`: it lets one `cloudflared` connect and
  nothing else; it is in the `production` environment and on the nodes, written by 50.08's
  deploy);
- the plugin store's deploy token (Epic 54's own).

## What the owner does

These steps are on Epic 50's go-live list (README §15.7).

1. Add the token's permissions above, or make a token per piece and set it as
   `CLOUDFLARE_API_TOKEN_ZONE`, `…_TUNNEL` or `…_WORKERS`. `CLOUDFLARE_API_TOKEN` and
   `CLOUDFLARE_ACCOUNT_ID` already exist for the developer portal.
2. In the repository's settings (Actions → General → Workflow permissions), allow GitHub Actions
   to create pull requests. The weekly IP-ranges job needs this. Without it, the job fails when
   the ranges change, and the next run tries again.
3. Optionally, add required reviewers to the `production` environment, so each Cloudflare deploy
   waits for a person.

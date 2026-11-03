# Story 50.31 — The origin only through Cloudflare: the Tunnel and the trusted client IP

> Epic 50 · Phase 1 · Depends on: 50.01 (names), 50.06 (rehearsal), 50.07 (two nodes) ·
> Needed by: 50.32, the launch gate (README §11)

## Why

A WAF, rate limits and a bot check at Cloudflare (50.30) mean nothing if the origin also answers
anyone who connects to its address directly. Today's plan points `api.maktaba.tv` at the
Floating IP with port 443 open, so the API can be reached around Cloudflare, and the API would
then trust a forged `CF-Connecting-IP` header whenever `server.trust_proxy_headers` is on
(`cloud/internal/middleware/clientip.go` reads the header from any peer). That breaks every
per-IP limit, the blocklist and the audit log's IP column.

This story makes Cloudflare the only way in for the API, the web app and the operator console
(README §15 decision), and makes the API believe `CF-Connecting-IP` only when the request really
came through Cloudflare.

## Description

### 1. The Tunnel (the default)

- **One named tunnel**, `maktaba-cloud-production` (created by the owner, README §15.7), with a
  **remotely managed** ingress that CI writes from `deploy/cloudflare/tunnel.maktaba.tv.json`
  (50.34):

  | Hostname | Service |
  |---|---|
  | `api.maktaba.tv` | `http://127.0.0.1:8090` (user listener) |
  | `app.maktaba.tv` (the Worker's `/v1/*` pass-through) | `http://127.0.0.1:8090` |
  | `admin.maktaba.tv` (the admin Worker's `/v1/*` pass-through) | `http://127.0.0.1:8091` (admin listener, 50.32) |
  | anything else | `http_status:404` |

  Each rule sets `originRequest.httpHostHeader` to its own hostname and `noTLSVerify: false`
  (the origin is plain HTTP on loopback; the Tunnel itself is TLS to Cloudflare). No relay name
  is ever in the ingress (README §15.5; T05).
- **`cloudflared` on the node.** Installed from Cloudflare's signed package repository, version
  pinned in `deploy/cloud/versions.env` and updated by pull request (Renovate-style bump), run
  as the systemd unit `maktaba-cloudflared.service` (`cloudflared tunnel --no-autoupdate run
  --token-file /opt/maktaba-cloud/secrets/tunnel.token --metrics 127.0.0.1:20241`), user
  `cloudflared`, no capabilities, `ProtectSystem=strict`.
- **Leader only.** The unit is part of `maktaba-cloud.target`, which 50.07's Patroni role-change
  callback starts on the node that becomes leader and stops on the one that loses the lease.
  The standby never holds a connector, so Cloudflare never sends a request to a stopped API.
- **Failover.** The new leader's `cloudflared` registers within seconds of starting (it opens 4
  connections to 2 Cloudflare data centres). Requests in the gap get Cloudflare's `502/530`;
  apps retry (50.13). The RTO in README §4.2 (≤ 3 min, expected ~90 s) already covers it; the
  rehearsal measures the Tunnel part (T07).
- **Health.** 50.10 scrapes `cloudflared_tunnel_ha_connections` from `127.0.0.1:20241` on the
  leader and alerts when it is below 2 for 2 minutes. The outside probe of
  `https://api.maktaba.tv/readyz` (50.10) goes through the Tunnel, so it tests the whole path.
- **Phase 2 (50.17).** `cloudflared` runs on two API nodes and points at the LB11's private
  address; the LB has no public interface.

### 2. Nothing else listens for the API

- The api role's two listeners (`server.listen_addr = 127.0.0.1:8090`,
  `server.admin_listen_addr = 127.0.0.1:8091`) bind loopback. In production
  (`server.env = production`) the config validator refuses a non-loopback, non-private address
  for either, with the message naming the setting.
- **Caddy serves relay names only**: the site blocks are `relay.maktaba.tv` and
  `*.relay.maktaba.tv`; the catch-all `https://` block goes away (the wildcard certificate,
  50.02, removes the need for on-demand TLS). A TLS handshake for any other SNI fails, and a
  plain-HTTP request with another `Host` gets `421` with no body.
- **The Hetzner Cloud Firewall and UFW** (50.07) open only TCP 80 and 443 (Caddy, relay) and
  UDP/TCP 53 (the direct-name responder, 50.15) to the internet; SSH only from the witness W
  and the owner's listed addresses. Nothing opens 8090 or 8091.

### 3. The fallbacks, if the Tunnel can't be used

`server.origin_mode` (`tunnel` default, `aop` fallback) picks the shape. The fallback exists for
a Tunnel outage that lasts (Cloudflare incident, a broken `cloudflared` release) and is switched
by a deploy, never automatically.

- **`aop`: per-hostname Authenticated Origin Pulls *and* an IP allow-list.**
  - DNS for `api`, `app` and `admin` becomes proxied `A` records to the Floating IP (CI writes
    them, 50.34).
  - Cloudflare presents **Maktaba's own client certificate** for those hostnames (per-hostname
    AOP, uploaded by CI from the `production` environment's `CLOUDFLARE_AOP_CERT` /
    `CLOUDFLARE_AOP_KEY`; the zone-level certificate is never used, because it is shared by all
    Cloudflare customers).
  - Caddy gets site blocks for the three names with `client_auth { mode require_and_verify;
    trusted_ca_cert_file /etc/caddy/maktaba-aop-ca.pem }` **and** a `remote_ip` matcher for
    Cloudflare's published ranges (`deploy/cloudflare/ips-v4.txt`, `ips-v6.txt`; a weekly
    scheduled job opens a pull request when they change). A request failing either check gets
    `403` with no body.
  - Their origin certificate is 50.02's DNS-01 certificate (SSL mode Full (strict)).
- **Never an IP allow-list alone:** every Cloudflare customer's traffic comes from the same
  ranges, so it proves nothing about which zone sent it.

### 4. The trusted client IP

- `server.trust_proxy_headers` (a bool) is **replaced** by `server.trusted_proxies`, a list of
  CIDRs. The client IP is `CF-Connecting-IP` **only when the TCP peer is in that list** and the
  header is one valid IP; otherwise it is the TCP peer, and the header is ignored (and logged at
  debug once a minute). `X-Forwarded-For` is no longer read.
  - `tunnel` mode: `["127.0.0.1/32", "::1/128"]` (the local `cloudflared`).
  - `aop` mode: the same (Caddy, on loopback, after its two checks). Caddy sets
    `CF-Connecting-IP` from the verified request and strips any client-sent copy first.
  - Phase 2: the LB11's private address.
  - Dev and CI: empty (the TCP peer).
- An old config that still sets `trust_proxy_headers` is refused at start with a message
  pointing to `trusted_proxies`.
- **The relay never reads `CF-Connecting-IP`.** Relay names are DNS only, so a request with that
  header on a relay name was written by the client. Caddy strips it on the relay blocks too.
- The same rule feeds the rate limiter, the blocklist, the lockout, the audit log, Turnstile's
  `remoteip` (50.30) and the staff IP allow-list (50.33).

### 5. Why the Tunnel is on the leader and not on both

Cloudflare sends a Tunnel's traffic to the connector closest to the visitor, not to a healthy
one. With connectors on both nodes, roughly half the requests would reach the standby, where
the API is stopped. Starting `cloudflared` with the cloud processes keeps the rule "everything
follows the database leader" (README §6).

## Acceptance criteria

- **Given** a production node,
  **when** anyone connects to the Floating IP on 443 with SNI `api.maktaba.tv`,
  **then** the TLS handshake fails, and no port answers for the API on any public address.
- **Given** the Tunnel's ingress,
  **when** CI checks it,
  **then** it routes `api`, `app` and `admin` to their loopback listeners, everything else to
  `404`, and contains no relay name.
- **Given** a request through the Tunnel from client IP 198.51.100.7,
  **when** the API logs and rate-limits it,
  **then** it uses 198.51.100.7.
- **Given** a request to the relay or to a listener from a peer outside `trusted_proxies`,
  **when** it carries `CF-Connecting-IP: 203.0.113.9`,
  **then** the header is ignored and the TCP peer is used.
- **Given** the leader is powered off,
  **when** the standby becomes leader,
  **then** its `cloudflared` is connected and `api.maktaba.tv/readyz` answers 200 through the
  stand-in edge within the RTO, and the old leader holds no connector.
- **Given** `origin_mode = aop`,
  **when** a request reaches Caddy without the client certificate, or from outside Cloudflare's
  ranges,
  **then** it gets `403` and never reaches the API.
- **Given** a config with `trust_proxy_headers = true`,
  **when** the api role starts,
  **then** it refuses, naming `server.trusted_proxies`.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (go) | `middleware.WithClientIP(trusted)` | header from a trusted peer; from an untrusted peer; two IPs in the header; garbage; IPv6 | header IP; peer; peer; peer; header IP |
| T02 | unit (go) | config validation | production with `listen_addr = 0.0.0.0:8090`; `trust_proxy_headers` set; empty `trusted_proxies` in production | refused ×3, each naming the setting |
| T03 | unit (go) | relay role | request with `CF-Connecting-IP` | ignored; peer used |
| T04 | unit (shell) | Caddyfile | `caddy validate`; grep for site blocks | only relay names; no catch-all; `request_header -CF-Connecting-IP` on both |
| T05 | unit (node:test) | `tunnel.mjs check` on `tunnel.maktaba.tv.json` | the file; a fixture adding `x.relay.maktaba.tv`; a fixture without the 404 catch-all | pass; fail; fail |
| T06 | rehearsal (50.06, new scenario `origin-only-via-edge`) | two nodes; a stand-in edge container (Caddy adding `CF-Connecting-IP`) that reaches the leader's loopback listeners only through a stand-in connector (a `socat` process the role callback starts in place of `cloudflared`) | scan the Floating IP's ports; send a forged header straight to the relay | only 80, 443, 53 open; 443 refuses SNI `api.maktaba.tv`; the forged header is ignored |
| T07 | rehearsal | same | power off the leader | the stand-in connector moves with the leader; `readyz` back within the RTO; the old leader has no connector |
| T08 | rehearsal | `origin_mode = aop` with a test CA | with and without the client certificate; from an address outside the test "Cloudflare ranges" | 200; 403; 403 |
| T09 | unit (go) | `server.origin_mode` | `tunnel` with an `aop` certificate set; `aop` without the CA file | warning; refused |

No test runs the real `cloudflared` against Cloudflare. The owner's first deploy is checked by
50.10's probes and a port scan from the witness (rollout).

## Edge cases

- **Cloudflare has an incident and the Tunnel is down.** The API is unreachable while it lasts;
  the relay (DNS only) keeps working, and so do the home and direct remote (50.13). If it lasts,
  the runbook (50.12) switches `origin_mode` to `aop` by a deploy.
- **The tunnel token leaks.** Anyone with it could register a connector and receive part of the
  traffic. Rotate it in the dashboard (the old one stops at once), update the `production`
  secret, redeploy. The runbook has the steps.
- **Both nodes briefly run `cloudflared`** during a switchover. Patroni stops the old leader's
  target before the new one starts (the callback order in 50.07); if a stale connector lingers,
  its API is read-only and answers `503`, which apps retry.
- **A home's agent pinned to an old address.** Homes reach the API by name; none use an IP.
- **IPv6.** The Tunnel carries both; `trusted_proxies` includes `::1/128`.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `deploy/cloudflare/tunnel.maktaba.tv.json` (new), `tools/cloudflare/tunnel.mjs` (new) | The ingress, its check and its apply (50.34 runs it). |
| `deploy/cloudflare/ips-v4.txt`, `ips-v6.txt` (new), `.github/workflows/cloudflare-ips.yml` (new, weekly, opens a PR) | Cloudflare's ranges for the fallback. |
| `deploy/cloud/maktaba-cloudflared.service`, `deploy/cloud/versions.env` (new or extended, 50.07's layout) | The unit and the pinned version. |
| `deploy/cloud/patroni-callback.sh` (50.07) | Start and stop `cloudflared` with the target. |
| `deploy/cloud-relay/Caddyfile` | Relay names only; strip `CF-Connecting-IP`; the `aop` blocks behind an env switch. |
| `cloud/internal/middleware/clientip.go` | `trusted_proxies`, no `X-Forwarded-For`. |
| `cloud/internal/config/config.go`, `cloud/configs/cloud.example.toml` | `trusted_proxies`, `origin_mode`, `admin_listen_addr`; refuse `trust_proxy_headers`. |
| `cloud/cmd/maktaba-cloud/role_api.go`, `role_relay.go` | Loopback listeners; the relay ignores the header. |
| `tools/rehearsal/` (50.06) | Scenarios `origin-only-via-edge`, `origin-aop`. |
| `cloud/README.md`, `cloud/docs/accounts-billing-admin.md` | The client-IP rule. |

### Tests

T01–T03 and T09 in the cloud Go jobs; T04 in the relay job (`relay.yml`); T05 in the
`cloudflare` job; T06–T08 in the cloud rehearsal (CI only).

### Rollout

1. Merge with `origin_mode = tunnel` in the production config but the Tunnel not yet created:
   the api role starts on loopback; nothing serves it publicly yet (no VPS exists).
2. The owner creates the tunnel and its token (README §15.7); CI applies the ingress.
3. 50.10's probes and a port scan from W confirm the shape before the launch gate.

### Rollback

`origin_mode = aop` by a deploy (the fallback, with its two checks), never back to an open
origin. Reverting the code restores `trust_proxy_headers`, which is refused only by the new
validator, so a revert must keep `trusted_proxies`.

### Dependencies

50.01, 50.06 (scenarios), 50.07 (the callback, the firewall). 50.32 adds the admin listener this
story routes to.

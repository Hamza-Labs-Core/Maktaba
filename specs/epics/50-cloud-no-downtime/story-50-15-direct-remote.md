# Story 50.15 — Direct remote access, like Plex

> Epic 50 · Phase 1b · Depends on: 50.01 · Needed by: — (recommended before launch, doesn't
> gate it; used by 50.13 when present)

## Why

Most homes reach the outside world only through the relay, so a relay outage is total for
them, and every relayed byte costs us traffic. Plex sends most remote play **directly**: the
server opens a port on the router by itself (UPnP or NAT-PMP), plex.tv checks it from outside,
and each server has its own certificate so the direct path is HTTPS
([research.md](research.md) §2).

Ours today: the home list can give `direct_url = http://<ip>:<port>`
(`cloud/internal/handlers/homes/homes.go`), plain HTTP, only if the home reported an address.
The `direct_ip` / `direct_port` columns exist (`cloud/migrations/00040001_servers.sql`), but we
found no code that writes them, and nothing in the repo talks UPnP or NAT-PMP.

## Description

### 1. Opening the port

- The home's server gets a **TLS listener for direct access** (`direct.listen`, default
  `:8443`), serving the same API and web as the LAN port, with the per-home certificate (§3).
- On start and every 20 minutes it asks the router to map an external port to it, trying in
  order **UPnP-IGD**, **NAT-PMP**, **PCP**. It asks for the same external port first, else a
  random one in 30000–49999, with a 1-hour lease renewed every 20 minutes. Candidate libraries:
  `github.com/huin/goupnp` (UPnP-IGD) and `github.com/jackpal/go-nat-pmp` (NAT-PMP); PCP by a
  small client of our own if no maintained library fits.
- **Manual port**: in the home's settings the admin can turn mapping off and type the external
  port they forwarded by hand (Plex's "Manually specify public port").
- The router's reported external address tells us about **CGNAT**: an address in
  `100.64.0.0/10` or a private range means the provider shares the address, so direct access
  can't work; the home says so and leaves remote play to the relay.
- The home reports its external port and the LAN addresses of the direct listener to the cloud
  in the `META_ENDPOINTS` frame (`0x40`, architecture §13.4). The **public IP** is the one the
  cloud sees the tunnel come from (the home can't always know it), and the cloud writes
  `direct_ip` / `direct_port`.

### 2. The outside check

A worker job in the cloud connects to the home's direct name (§3) on the reported port, from
the cloud node (outside the home), every 15 minutes, on any change, and when the admin presses
**Check now**. It passes when the TLS certificate is valid for that home and
`GET /api/health` answers with the home's own `server_id`. The result (`ok`, `closed`,
`timeout`, `wrong_home`, `cgnat`) goes back to the home over the tunnel and shows in its
settings. Only homes whose last check passed get a `direct` entry in the connection list
(50.13).

### 3. HTTPS: a certificate per home, with IP-encoded names

- Each home gets a stable **home ID**: the first 32 hex characters of
  `SHA-256("direct-v1" ‖ server_id)`. It never changes, unlike the slug.
- Names under the home's **direct-name domain** (one of several separate domains, §4; never
  under `maktaba.tv`): `1-2-3-4.<home-id>.<direct-domain>` resolves to `1.2.3.4` (IPv4), and
  `2001-db8--1.<home-id>.<direct-domain>` to `2001:db8::1` (IPv6). One wildcard certificate
  `*.<home-id>.<direct-domain>` covers every address of that home and proves only that home.
  The examples below use `direct-a.example`, `direct-b.example`, … for the domains the owner
  will buy.
- **DNS: our own small authoritative responder**, chosen over one Cloudflare record per home.
  Each direct-name domain is delegated at its registrar by `NS` records to
  `ns1.<direct-domain>` and `ns2.<direct-domain>` (with glue), the primary IPs of nodes A and B
  (Phase 2: any relay). Why:
  - the answers are **computed from the name**, so a changed home IP or a LAN address needs no
    API call and no wait for a record to update, and the responder needs no database to answer
    `A` / `AAAA`;
  - it also serves the `_acme-challenge.<home-id>.<direct-domain>` TXT records from Postgres,
    so certificates don't depend on the Cloudflare API either;
  - per-home Cloudflare records would mean one API call per address change per home, and
    Cloudflare's per-zone record limits at 10,000 homes.

  It runs in the cloud binary as `--role=dns` on UDP and TCP 53, answers only names under the
  configured direct-name domains (`direct.domains`), refuses everything else (not an open resolver), and rate-limits by
  source.
- **Issuing**: the home makes its key and a CSR; **the key never leaves the home**. It sends
  the CSR over the tunnel; the cloud picks the home's domain and CA (§4), orders the
  certificate with DNS-01, answers the challenge from its responder, and returns the
  certificate. Renewed at two thirds of its life. The home keeps the last good certificate if renewal fails, and the alert in
  50.10 fires 14 days before expiry.
- **DNS rebinding**: names that resolve to LAN addresses are blocked by some routers' rebinding
  protection (research §2). So the connection list keeps the **raw-IP entries** too (LAN
  `https://192.168.1.20:8443` with the app pinning the home's certificate by its home ID, and
  the relay), and the home's settings explain the router option when the LAN name fails but the
  raw IP works.

### 4. Several direct-name domains, so Let's Encrypt's limit never blocks a home

**Owner decision, 2026-11-03 (README §13 decision 5).** Let's Encrypt allows **50 new
certificates per registered domain per week** ([rate limits](https://letsencrypt.org/docs/rate-limits/)).
So direct names use **multiple separate registered domains, never `maktaba.tv`**, starting
with **3–4**. Each has its own 50-a-week limit, so 4 domains give about 180 new homes a week
with a margin, and `maktaba.tv`'s own certificates are never at risk.

- **The list is configuration.** `direct.domains = ["direct-a.example", "direct-b.example",
  "direct-c.example", "direct-d.example"]` (placeholders until the owner buys them). Adding a
  domain is **configuration only**: the owner registers it, delegates its `NS` to the
  responder (README §12), and appends it to the list; the responder answers it and assignment
  starts using it with no code change and no release.
- **Assigning homes to domains.** When a home first asks for a certificate, the cloud picks the
  domain with the **most room left this week**: per domain it counts new certificates issued in
  the last 7 days (`direct_certs.issued_at`, renewals excluded) and keeps each under a budget of
  **45** (5 spare under Let's Encrypt's 50 for retries). Ties go to the domain with the fewest
  homes. The choice is stored (`direct_certs.domain`) and **stays with the home**: renewals reuse
  the same name (renewals don't count as new certificates; checked at build time against Let's
  Encrypt's then-current rules), so a home's names never change.
- **Overflow.** When every domain's Let's Encrypt budget for the week is used, the cloud issues
  from a second ACME CA instead: **ZeroSSL** (already the backup in 25.23), then **Google Trust
  Services**, both with ACME and external account binding. The certificate is renewed from the
  same CA. Each domain's `CAA` records allow all three CAs (`letsencrypt.org`, `sectigo.com` for
  ZeroSSL, `pki.goog`).
- **Growth.** When new homes regularly reach overflow, the owner either adds a domain (config
  only, above) or asks Let's Encrypt for a higher limit on the existing ones (they grant
  increases to hosting providers); the admin fleet page (25.20) shows each domain's weekly use
  and alerts (SEV3) when overflow was used in a week.
- **Rejected: paid per-home certificates.** A commercial certificate per home costs about
  $35–1,000 a year each at retail; the owner rejected it.
- **Not pursued now:** adding the domains to the Public Suffix List (it would make each home ID
  its own registered domain for rate limits, but takes weeks and isn't needed with several
  domains).

### 5. Who gets it: free for everyone

**Owner decision, 2026-11-03 (README §13 decision 6): direct remote is free, on every plan
including Free. Only the relay is paid** (plans in 50.19). Direct access carries no relay
bytes, so it costs us almost nothing. The home doesn't check its plan before opening the direct
listener, mapping the port or asking for a certificate, and the cloud issues certificates and
runs the outside check for Free homes too. (Plex, by contrast, has required a pass for any
remote play since 2025-04-29.)

## Acceptance criteria

- **Given** a router with UPnP enabled,
  **when** the home starts with direct access on,
  **then** within 60 s a port is mapped, the outside check passes, and the home list shows a
  `direct` connection `https://<ip-encoded>.<home-id>.<direct-domain>:<port>`, on every plan
  including Free.
- **Given** a router without UPnP, NAT-PMP or PCP,
  **when** the admin enters a manually forwarded port,
  **then** the outside check uses it and passes when the forward is right.
- **Given** the home is behind CGNAT,
  **when** mapping reports a shared address,
  **then** settings say direct access isn't possible with this provider, and no `direct` entry
  is listed.
- **Given** the outside check fails,
  **when** the home list is built,
  **then** it has no `direct` entry for that home, and settings show the reason.
- **Given** a query for `81-2-3-4.<home-id>.direct-b.example`,
  **when** either name server answers,
  **then** the answer is `A 81.2.3.4`; a query for any name outside the configured direct-name
  domains is refused, and no direct name exists under `maktaba.tv`.
- **Given** domain A has had 45 new certificates this week and domain B 10,
  **when** a new home asks for a certificate,
  **then** it is assigned domain B and keeps it at every renewal.
- **Given** every domain's weekly budget is used,
  **when** a new home asks for a certificate,
  **then** it is issued by ZeroSSL (or Google Trust Services if ZeroSSL fails), and an overflow
  alert is raised.
- **Given** the owner appends a fifth domain to `direct.domains`,
  **when** the config is reloaded,
  **then** the responder answers it and new homes can be assigned to it, with no release.
- **Given** a Free home,
  **when** direct access is turned on,
  **then** it gets a certificate and a `direct` entry exactly like a paid home.
- **Given** a home sends a CSR,
  **when** the cloud issues the certificate,
  **then** it is valid for `*.<home-id>.<direct-domain>` only, and the private key was never
  sent.
- **Given** the home's public IP changes,
  **when** the tunnel reconnects from the new address,
  **then** the next home list has the new direct name, with no DNS change made anywhere.
- **Given** an app on a LAN whose router blocks rebinding,
  **when** the LAN name fails,
  **then** the app uses the raw-IP LAN entry, checking the home's certificate.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | name codec | encode/decode IPv4, IPv6, bad labels | round-trips; bad ones refused |
| T02 | unit | DNS responder | A, AAAA, TXT, out-of-zone, ANY | computed answers; refusal; no amplification on ANY |
| T03 | unit | home ID | same `server_id` twice | same ID; differs per server |
| T04 | integration | fake IGD (UPnP) and fake NAT-PMP gateway | home start | mapping made, renewed, removed on stop |
| T05 | integration | CGNAT address from the gateway | home start | `cgnat` reported; no direct entry |
| T06 | integration | Pebble (ACME test server) + responder | CSR over the tunnel | certificate issued; key never in the cloud's logs or DB |
| T07 | integration | worker check against a home stack | open / closed / wrong cert | `ok` / `closed` / `wrong_home` |
| T08 | unit (cloud) | `GET /v1/homes` | home with a passing check | `direct` connection with `https` |
| T09 | Arabic | new keys | strings check | Arabic in web `ar.json` (home settings) |
| T11 | unit (cloud) | domain picker | budgets 45/10/0/30, all full, a new domain in config | picks the most room; overflow CA when all full; new domain used after reload |
| T12 | integration | two Pebble instances as Let's Encrypt and ZeroSSL | exhaust the budget | issued by the overflow CA; renewal from the same CA |
| T13 | unit (home) | plan = Free | turn direct access on | listener, mapping and CSR proceed; no plan check |
| T10 | rehearsal (CI, 50.06 `direct-remote`) | home behind a NAT container with a fake IGD, Pebble, the responder | start, check, connect from outside | HTTPS direct play works; relay unused |

## Edge cases

- **Two homes behind one public IP.** Different home IDs, so different names; each maps its
  own external port.
- **The router forgets the mapping.** The 20-minute renewal and the 15-minute check catch it;
  the list drops the `direct` entry until it passes again.
- **Security of an open port.** Only the TLS port is mapped; sign-in rate limits and lockout
  (Epic 10) apply; settings show "Reachable from the internet" plainly, and turning direct
  access off removes the mapping at once.
- **Certificate transparency.** Every home ID appears in public CT logs. It reveals nothing
  but a random-looking label (no slug, no name, no IP).
- **DNS responder down on both nodes.** Existing app caches still hold resolved names for their
  TTL (300 s); raw-IP and relay entries remain.
- **Name server load / abuse.** Responses are small and only for our zone; per-source rate
  limit; no recursion.

## User-facing strings

| Key | EN | AR |
|---|---|---|
| `direct.title` | Direct access from the internet | الوصول المباشر من الإنترنت |
| `direct.toggle` | Let apps reach this home directly from outside, without the relay | السماح للتطبيقات بالوصول إلى هذا الجهاز الرئيسي مباشرة من الخارج، دون الوسيط |
| `direct.state.ok` | Reachable from the internet at port {port}. Checked {time}. | يمكن الوصول إليه من الإنترنت عبر المنفذ {port}. آخر فحص {time}. |
| `direct.state.closed` | Not reachable: the router didn't open port {port}. Turn on UPnP on your router, or forward the port by hand. | لا يمكن الوصول إليه: لم يفتح جهاز التوجيه المنفذ {port}. فعّل UPnP في جهاز التوجيه، أو وجّه المنفذ يدويًا. |
| `direct.state.timeout` | No answer from outside on port {port}. A firewall may be blocking it. | لا رد من الخارج على المنفذ {port}. قد يكون جدار حماية يحجبه. |
| `direct.state.cgnat` | Your internet provider shares your address with others, so direct access isn't possible. Remote access uses the relay. | مزوّد الإنترنت يشارك عنوانك مع آخرين، فلا يمكن الوصول المباشر. يستخدم الوصول عن بُعد الوسيط. |
| `direct.state.wrongHome` | Something else answers on port {port}. Check the port forward points at this home. | جهاز آخر يرد على المنفذ {port}. تأكد أن توجيه المنفذ يشير إلى هذا الجهاز الرئيسي. |
| `direct.manual` | I forwarded a port by hand | وجّهت منفذًا يدويًا |
| `direct.manualPort` | Public port | المنفذ العام |
| `direct.checkNow` | Check now | افحص الآن |
| `direct.rebinding` | Apps on this network can't use the home's secure name: your router blocks it (DNS rebinding protection). Apps will use its address instead. | لا تستطيع التطبيقات على هذه الشبكة استخدام الاسم الآمن للجهاز الرئيسي لأن جهاز التوجيه يحجبه (الحماية من إعادة ربط DNS). ستستخدم التطبيقات عنوانه بدلًا منه. |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cloud/internal/direct/` (new) | Name codec, home ID, DNS responder (`--role=dns`) for every configured domain, domain picker (§4), ACME DNS-01 ordering for CSRs against Let's Encrypt with ZeroSSL and Google Trust Services as overflow, outside check. |
| `cloud/configs/cloud.example.toml` | `direct.domains` list, per-domain weekly budget, overflow CA settings. |
| `cloud/cmd/maktaba-cloud/main.go`, `role_dns.go` (new), `role_worker.go` | New role; worker runs the outside check. |
| `cloud/migrations/` (new migration) | `direct_certs` (home ID, domain, CA, issued_at, not-after), `direct_checks` (result, at), `acme_challenges` (TXT values). |
| `cloud/internal/stores/servers.go` | Write `direct_ip` / `direct_port` from `META_ENDPOINTS` and the tunnel's source address. |
| `cloud/internal/handlers/homes/homes.go` | `direct` connection with `https` when the last check passed. |
| `cloud/internal/cloudlink/` (`frame.go`, `health.go`), `api/internal/auth/cloudlink/reporter.go` | Report endpoints; send CSRs; receive certificates and check results. |
| `api/internal/` (new package `directaccess`) | Port mapping (UPnP-IGD, NAT-PMP, PCP), CGNAT detection, TLS listener with the per-home certificate, settings API. |
| `web/src/` settings page for remote access, `web/src/i18n/en.json`, `ar.json` | Toggle, manual port, state, Check now. |
| `deploy/cloud-ha/` (from 50.07) | Run the `dns` role on A and B; open 53/udp+tcp in the firewall. |
| `docs/cloud-dns.md` (from 50.01) | The direct-name domains, their `NS` + glue and `CAA` records, and how to add one. |
| `specs/architecture.md` §13.5 | Direct names and the responder. |

### Tests

- T01–T08 in `relay.yml`'s `test` job and the home's Go tests; T09 with the existing
  missing-Arabic test.
- **CI only:** rehearsal scenario `direct-remote` (50.06), with Pebble standing in for Let's
  Encrypt (no live CA, no live DNS).

### Rollout

1. Cloud: the responder and issuing with a **staging** Let's Encrypt account first; the owner
   buys the first 3–4 direct-name domains and adds their `NS` delegation (README §12).
2. Home: ships in a release with direct access **off** by default; owner tries it on the live
   home.
3. Turn on by default for new homes, on every plan, once the domains are delegated.

### Rollback

- Home: turning the toggle off removes the mapping and the listener.
- Cloud: `direct.enabled = false` stops listing `direct` entries; apps fall back to relay.
  Removing a domain from `direct.domains` stops new assignments to it (its homes keep their
  certificates until expiry, then are reassigned); removing its `NS` delegation stops its names
  resolving.

### Dependencies

50.01 (the names plan: direct names on separate domains, never under `maktaba.tv`).
Independent of the HA stories otherwise.

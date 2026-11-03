# Story 50.01 — Names and the DNS plan

> Epic 50 · Phase 1 · Depends on: — · Needed by: 50.02, 50.06, 50.15

## Why

Failover only works if every name users and homes touch points at something that moves (the
Floating IP), and nothing points at a single machine's own address. The names also have to be
ours: **maktaba.tv** is for users, **maktaba.dev** is for development, the wiki and staging, and
**maktaba.app is not ours**. One migration comment still says `skylark-12.relay.maktaba.app`; a
copied example like that would send users to a domain someone else owns.

This story writes the one list of names, what each points at, its TTL and whether Cloudflare
proxies it, fixes the stale names, and adds a CI check so `maktaba.app` hostnames don't come
back.

## Description

### 1. The names

| Name | Points at | Cloudflare | TTL | Used by | Phase |
|---|---|---|---|---|---|
| `maktaba.tv`, `www.maktaba.tv` | marketing site (as today) | proxied | auto | people | — |
| `api.maktaba.tv` | the Cloudflare Tunnel (`<tunnel-id>.cfargotunnel.com`, 50.31) | **proxied** | auto | apps, homes (claim, entitlements, home list) | 1 |
| `app.maktaba.tv`, route `maktaba.tv/developers*` | the user web Worker, `/v1/*` passed through the Tunnel (50.32) | proxied | auto | cloud web app | 1 |
| `admin.maktaba.tv` | the admin web Worker, `/v1/*` passed through the Tunnel (50.32) | proxied | auto | the operator console (staff only, 50.33) | 1 |
| `relay.maktaba.tv` | Floating IP | **DNS only** | 300 s | homes' tunnel (`/v1/relay/ws`) | 1 |
| `*.relay.maktaba.tv` | Floating IP | **DNS only** | 300 s | apps reaching a home: `<slug>.relay.maktaba.tv` | 1 |
| `canary.relay.maktaba.tv` | covered by the wildcard | DNS only | — | the canary home (50.10); a reserved slug | 1 |
| `status.maktaba.tv` | status page host **off Hetzner** (50.11) | provider's choice | 300 s | people, apps' banner | 1 |
| `r<N>.relay.maktaba.tv` | relay N's own primary IPv4 | DNS only | 60 s | homes' tunnel to an assigned relay | 2 |
| `*.r<N>.relay.maktaba.tv` | relay N's own primary IPv4 | DNS only | 60 s | relay hints for apps (50.16) | 2 |
| `relay.maktaba.tv`, `*.relay.maktaba.tv` | round robin over healthy relays | DNS only | **60 s** | stable names in Phase 2 | 2 |
| `staging.relay.maktaba.dev`, `*.staging.relay.maktaba.dev`, `staging-api.maktaba.dev` | the owner's staging pair (50.14) | DNS only | 60 s | load tests, never users | 1 |

Rules:

- **Relayed video never goes through the Cloudflare proxy** (Cloudflare's terms forbid video on
  non-Enterprise plans, research §3). So every relay name is DNS only.
- **The API, the web app and the operator console never point at an address** (README §15,
  50.31): they reach the origin only through the Cloudflare Tunnel, so no inbound port serves
  them. The Tunnel's DNS records are written by CI (50.34).
- **No name points at a node's own address in Phase 1**, only at the Floating IP. Failover
  never needs a DNS change. The 300 s TTL matters only for the manual zone-disaster path
  (50.09), where it caps how long the DNS change takes to spread.
- Phase 2 relay names use **60 s** because the witness removes a dead relay's record.
- Slugs `canary`, `status`, `api`, `app`, `admin`, `www`, `r1`…`r99`, `direct`, `staging` are added to
  the reserved list (story 25.22) so no home can take them.
- **Direct names are never under `maktaba.tv`** (owner decision, 2026-11-03; README §13
  decision 5). They live on **several separate registered domains used only for direct names**
  (3–4 to start), each delegated by `NS` at its registrar to the cloud's own DNS responder
  (`--role=dns`, UDP/TCP 53, on nodes A and B), with names like
  `1-2-3-4.<home-id>.<direct-domain>` → `1.2.3.4`. The spec writes them as `direct-a.example`,
  `direct-b.example`, … until the owner buys them; the list is configuration
  (`direct.domains`), and the table for them is in 50.15 §4. `maktaba.tv` gets no direct
  record, so no direct certificate ever counts against `maktaba.tv`'s Let's Encrypt limit.

### 2. Fix the stale names

Found with `grep -rIn "maktaba\.app"` on 2026-11-03:

| File | Says | Becomes |
|---|---|---|
| `cloud/migrations/00040001_servers.sql:5` (comment) | `skylark-12.relay.maktaba.app` | `skylark-12.relay.maktaba.tv` |
| `specs/epics/22-devops/plan-22-07-multi-platform-packaging.md:251` | `appId: 'io.maktaba.app'` | leave: an app identifier, not a hostname (see below) |

Not hostnames, so they stay: the bundle ID `com.maktaba.app` in
`cloud/configs/cloud.example.toml`, `web/src/pages/Cloud/Devices.test.tsx`,
`web/test-cases/admin-devices.md` and the Epic 12 plans (`plan-12-01`, `-06`, `-07`, `-09`).
Bundle IDs are reverse-DNS identifiers; they don't resolve and changing a shipped one breaks
App Store updates. The README's G11 line mentioned "some Epic 12/22 plans": on inspection those
hits are all identifiers.

Editing the migration's comment is safe: the cloud migrator (`cloud/migrations/migrations.go`,
`cloud/internal/db/`) records versions, not file checksums, and no VPS has applied it yet.

### 3. A CI check

`tools/lint/no-foreign-domains.sh` (new) fails when a changed file adds a hostname under
`maktaba.app` (regex `[a-z0-9.-]*\.?maktaba\.app\b` not preceded by `com.` or `io.`). It runs in
`ci.yml`'s existing lint step for every PR. Exceptions are listed in the script, each with a
reason.

## Acceptance criteria

- **Given** the names table above,
  **when** the owner sets up DNS (README §12),
  **then** every Phase 1 name the apps and homes use resolves to the Floating IP or to the
  off-Hetzner status host, and none to a node's own address.
- **Given** a PR that adds `https://foo.maktaba.app` to any file,
  **when** CI runs,
  **then** the lint fails and names the file and line.
- **Given** a PR that adds the bundle ID `com.maktaba.app`,
  **when** CI runs,
  **then** the lint passes.
- **Given** a user picks the slug `canary`, `status` or `r3`,
  **when** they claim it,
  **then** the cloud refuses it as reserved (story 25.22).
- **Given** the repo after this story,
  **when** someone greps for `maktaba.app` hostnames,
  **then** only the lint script's own test data matches.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (shell) | `tools/lint/no-foreign-domains-test.sh` | hostname, bundle ID, `io.` ID, URL in a comment | fail, pass, pass, fail |
| T02 | unit | reserved list | claim `canary`, `r12`, `direct` | `409 subdomain_reserved` |
| T03 | CI | `ci.yml` lint job on a PR | add a `maktaba.app` URL | job fails |

## Edge cases

- **Owner's own names.** If HamzaLabs ever buys `maktaba.app`, the lint's list is the one place
  to change.
- **Old homes with a cached relay URL.** None exist yet (no VPS), so no redirect is needed.
- **CAA records.** Add `CAA 0 issue "letsencrypt.org"` and `CAA 0 issue "sectigo.com"` (ZeroSSL's
  CA) on `maktaba.tv`, so only our two CAs can issue (50.02).

## Build plan

### Files to touch

| File | Change |
|---|---|
| `docs/cloud-dns.md` (new) | The names table, TTLs, proxy flags, CAA records, who changes what. |
| `cloud/migrations/00040001_servers.sql` | Comment → `relay.maktaba.tv`. |
| `cloud/migrations/<next free slot>_reserved_infra_slugs.sql` (new) | Insert the reserved slugs into `reserved_slugs` (the table `subdomain.go` checks, seeded by `00080001_subdomains.sql`). |
| `tools/lint/no-foreign-domains.sh` (new), `tools/lint/no-foreign-domains-test.sh` (new) | The check and its test. |
| `.github/workflows/ci.yml` | Run the check in the lint job. |
| `deploy/cloud-relay/README.md` | Point at `docs/cloud-dns.md` for production names. |

### Tests

- T01–T03 above. No rehearsal scenario; 50.06 uses the names from `docs/cloud-dns.md` with a
  test zone (`*.relay.test`).

### Rollout

Docs and lint merge first. The owner creates the records when the nodes exist (README §12).

### Rollback

Revert the PR. DNS records are the owner's and change in the Cloudflare dashboard.

### Dependencies

None.

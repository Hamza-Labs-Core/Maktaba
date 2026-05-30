# Epic 15 — Discovery & Networking: Spec-vs-Implementation Gap Analysis

**Verdict:** Epic 15 is ~10% implemented — only a partial in-memory QR-pairing skeleton (3 routes, now mounted) exists; mDNS publication, cloud relay, federation, DLNA, SPKI/TOFU, and all client-side discovery are entirely absent.

> Method: every AC below was checked against actual code (not the audit
> or spec self-claims). The prior audit
> (`specs/FULL_IMPLEMENTATION_AUDIT.md`) said the pairing handler was
> UNMOUNTED — that is now **stale**: `api/main.go:266` calls
> `router.MountP10`, which mounts `discoveryh.Handler` at
> `api/internal/router/p10.go:91`. However it is wired with the
> **in-memory** store (PairingStore left nil → `NewMemoryPairingStore()`
> at p10.go:84-86), so pairing state is per-process and lost on restart
> despite a Postgres DB being available and a `pairing_tickets` table
> existing.

---

## Key structural findings

- **No mDNS/zeroconf dependency** in `api/go.mod` (`zeroconf`,
  `grandcat`, `hashicorp/mdns` — none present). Only `NoopPublisher`
  exists (`api/internal/discovery/discovery.go:59-85`); no real
  `Publisher`, never called from boot. No `_maktaba._tcp` is ever
  advertised.
- **No `server_identity` / `mdns_id`** anywhere in the API code
  (migration `0050_server_identity.sql` from plan-15-01 was never
  created; slot 0050 is `transcript_units`).
- **`pairing_tickets` table exists** (`shared/db/migrations/0055_pairing_tickets.sql`)
  but **no Go code references it** — no DB-backed `PairingStore`, no
  sqlc query. The migration is dead relative to the running handler.
- **`services/` and `api/cmd/` directories do not exist** → no
  `relay-agent`, no `services/relay`, no `services/dlna`.
- **No federation/relay/dlna code** of any kind (no
  `federation_partners`, `relay_settings`, `dlna_settings`,
  `cert-rotation`, X25519/Ed25519 handshake, SAS).
- **No client-side discovery/pairing** except one stub:
  `apps/tv/tvos/.../PairingService.swift` returns hardcoded
  `"ABCD-1234"` / `"stub-device-id"` (lines 6-18). No mobile/desktop
  mDNS browser, no QR scanner, no SPKI pin store.
- **No GraphQL `federationOrigin` / `federatedLibrary`** in any schema.

---

## Story 15.1 — Local network discovery (mDNS / Bonjour)

| AC | Status | Evidence / Gap |
|---|---|---|
| Server advertises `_maktaba._tcp.local.` with TXT `version,name,tls,auth_required,mdns_id` | **missing** | No zeroconf dep; only `NoopPublisher` (`discovery.go:59`). `ServiceType="_maktaba._tcp"` const exists (`discovery.go:32`) but is never published. No `auth_required`/`mdns_id` fields modeled. No boot wiring in `api/main.go`. |
| Client (mobile+desktop, web exempt) queries on launch / net-change | **missing** | No mDNS browser in `apps/mobile`, `apps/desktop`, `apps/tv` (no NWBrowser/NsdManager/mdns-sd). |
| Web relies on "Open Maktaba" link / manual URL | **missing** | No such surface found in `web/`. |
| Server registers under `local.` + configured search domains | **missing** | No registration code at all. |

**Story verdict: missing (0/4).**

## Story 15.2 — Global discovery (cloud relay) + TOFU

| AC | Status | Evidence / Gap |
|---|---|---|
| Outbound long-lived QUIC server→relay; clients routed | **missing** | No `quic-go` dep, no `api/cmd/relay-agent`, no `services/relay`. |
| Strictly opt-in; Settings → Remote Access toggle | **missing** | No `relay_settings` table/migration, no UI. |
| End-to-end encrypted; clients enforce SPKI pinning | **missing** | No SPKI/pinning code on server or any client. |
| Relay identity bound to Maktaba account | **missing** | No relay code. |
| Quota free 50 GB/mo, premium higher | **missing** | No `relay_usage` table. |
| Latency / Direct vs Relayed badge | **missing** | No client networking surface. |
| TOFU flow: `GET /api/system/cert-rotation` (signed) | **missing** | No `cert-rotation`/`cert_rotation` endpoint anywhere in `api/`. |

**Story verdict: missing (0/7).**

## Story 15.3 — Server-to-server federation (consumption)

| AC | Status | Evidence / Gap |
|---|---|---|
| Pairing via 15.7 token; exchange Ed25519 keys + signed agreement | **missing** | No federation code; depends on absent 15.7. |
| Crypto details owned by 15.7 (consumed here) | **missing** | 15.7 absent. |
| Asymmetric per-library permissions | **missing** | No `federation_partners.acl`, no `requireFederationScope`. |
| Federated browsing via GraphQL + `federationOrigin` on `Video` | **missing** | No `federationOrigin`/`federatedLibrary` in any `.graphql`/resolver. |
| Federated streaming direct from owner (two JWTs) | **missing** | No federation streaming-token mint. |
| Off by default, never silent | **missing** | No feature exists. |

**Story verdict: missing (0/6).**

## Story 15.4 — UPnP / DLNA compatibility

| AC | Status | Evidence / Gap |
|---|---|---|
| Opt-in toggle Settings → Compatibility | **missing** | No `dlna_settings` table/migration; `services/dlna` absent. |
| Advertise as `MediaServer`, direct-play only | **missing** | No SSDP/SOAP code, no `anacrolix/dms` dep. |
| Flat list (no tag/search/progress) | **missing** | No ContentDirectory. |
| Read-only (no DLNA delete/upload) | **missing** | No DLNA service. |
| Browse tree Library/Genre/Speaker/Recently Added | **missing** | None. |
| Sidecar SRT exposed | **missing** | None. |

**Story verdict: missing (0/6).**

## Story 15.5 — QR code pairing (client flow)

| AC | Status | Evidence / Gap |
|---|---|---|
| TV/desktop generates one-time code via `POST /api/auth/pair` → 6-digit + QR URL | **partial / unwired** | Endpoint is `POST /api/pairing/request` (not `/api/auth/pair`); returns 8-char base32 `XXXX-XXXX` code (`discovery.go:182-196`), not 6-digit. QR URL is `maktaba://pair?code=...` (`pairing.go:76`) — no `mid`/`spki`/`n`. tvOS issuer is a stub (`PairingService.swift:6-18`). |
| QR URL form `https://{server}/pair?code=&mid=&spki=&n=` | **missing** | Handler emits `maktaba://pair?code=` only — no mDNS id, SPKI, nonce. |
| Mobile "Add device" scans QR, LAN-or-relay | **missing** | No QR scanner in `apps/mobile`. |
| Pairing exchanges device-bound refresh token (30 d) | **missing** | `Exchange` returns only `{user_id, expires_at}` (`pairing.go:130-133`) — no access/refresh token minted; comment says caller must separately call `/api/devices/register`. |
| Code TTL 5 min; one-time; expires on success | **partial** | TTL 5 min default (`pairing.go:61-63`); one-time consume enforced in-memory (`discovery.go:161-178`) — but not persisted (lost on restart; per-process only). |

**Story verdict: partial (1 partial impl, rest missing); endpoint contract diverges from spec.**

## Story 15.6 — API: device pairing endpoints

| AC | Status | Evidence / Gap |
|---|---|---|
| `pairing_codes` table (code,nonce,created_by_user_id,device_kind,claimed_*…) | **partial** | Table is named `pairing_tickets` (`0055_pairing_tickets.sql`) with `code,user_id,issued_at,expires_at,consumed_at,consumed_by` — **no `nonce`, no `device_kind`, no `device_label`**. And **no Go code reads/writes it** (handler uses in-memory store). |
| Indexes `(expires_at)`, `(created_by_user_id, claimed_at)` | **partial** | Migration has `(expires_at) WHERE consumed_at IS NULL` + `(user_id, issued_at)`; not used by code. |
| `POST /api/auth/pair {device_label?,device_kind}` → 201 {code,qr_url,expires_at}; auth; rate-limit 5/min | **unwired / partial** | Route is `/api/pairing/request`; no `device_kind`/`device_label` input; `qr_url` lacks mid/spki/n; **no rate limiting** (`pairing.go` has none); auth check only via `principal.FromContext` with no auth middleware in the mount chain (`p10.go:91` mounts bare). |
| `POST /api/auth/pair/claim {code,nonce,…}` → tokens + server{mdns_id,spki}; typed 400s | **missing** | Route is `/api/pairing/exchange`; no nonce check; returns no tokens, no `server{mdns_id,spki}`; error mapping is generic 404/409 (`pairing.go:136-147`), not `nonce-mismatch`/`code-already-claimed`. |
| `DELETE /api/auth/pair/{code}` revoke; idempotent | **missing** | No DELETE route. |
| `GET /api/auth/pair` list mine (24 h) | **missing** | No list route (`Status` GET is per-code poll only). |
| 30 s sweeper expire-flip + 7 d hard delete | **missing** | No reaper for pairing in Go or Python. |
| Security: nonce 2nd factor, SPKI in response, WAF/ratelimit, audit `category='pair'` | **missing** | No nonce, no SPKI, no rate limit, no audit-log write in pairing path. |

**Story verdict: partial — a divergent 3-route in-memory prototype; spec contract (auth/pair, nonce, tokens, SPKI, revoke, list, sweep, audit) unmet.**

## Story 15.7 — API: federation endpoints + crypto

| AC | Status | Evidence / Gap |
|---|---|---|
| `federation_pending` / `federation_partners` tables | **missing** | No `0054_federation.sql` (slot 0054 = `audit_log`); no tables. |
| X25519 KEX, Ed25519 sig, SAS rendering | **missing** | No `api/internal/auth/federation/` package; no `curve25519`/SAS/pgp-words. |
| Pair token encode/decode (CRC32) | **missing** | None. |
| Endpoints: `/federation/{init,pair,{id}/confirm,{id}/token}`, `GET`, `PATCH`, `DELETE` | **missing** | No `api/internal/http/federation/`; no routes. |
| Admin-only, ephemeral key wipe, audit `category='federation'` | **missing** | None. |
| Pending sweep (60 s) | **missing** | None. |

**Story verdict: missing (0/6).**

---

## AC status totals

| Status | Count |
|---|---|
| complete | 0 |
| partial | ~5 (all within 15.5/15.6 pairing prototype) |
| unwired | ~2 (pairing routes diverge from spec contract / no auth middleware) |
| stub | 1 (tvOS `PairingService`) |
| missing | ~36 (all of 15.1, 15.2, 15.3, 15.4, 15.7, plus most of 15.5/15.6) |

(Counts are approximate where an AC bundles multiple sub-requirements;
44 discrete AC bullets across 7 stories.)

---

## Top gaps by impact

1. **Entire epic non-functional for its stated goal.** Four of seven
   stories (15.1 mDNS, 15.2 relay, 15.3 federation, 15.4 DLNA) and
   15.7 federation-API have **zero code**. The epic goal ("easy to find
   on the LAN, optionally reachable remotely, pair-able in seconds") is
   unmet: nothing is discoverable (no mDNS), nothing is remotely
   reachable (no relay), and the pairing prototype can't actually log a
   device in.

2. **Pairing prototype cannot complete a login (worst gap).**
   `POST /api/pairing/exchange` consumes a ticket and returns only
   `{user_id, expires_at}` (`api/internal/handlers/discovery/pairing.go:130-133`)
   — it mints **no access/refresh token**, so AC 15.5 "Pairing
   exchanges a refresh token tied to the device, valid 30 d" is unmet.
   The flow dead-ends; a paired phone is not authenticated. Compounded
   by an **in-memory store** (`p10.go:84-86` passes nil → `NewMemoryPairingStore()`)
   despite a `pairing_tickets` Postgres table and live DB existing, so
   any code is lost on restart and unusable across replicas.

3. **No TOFU/SPKI trust anchor anywhere.** REVIEW §5.5's
   "verifiable, not aspirational" e2e claim is unaddressed: no
   `cert-rotation` endpoint, no SPKI in the pairing response, no
   `nonce`, no client pin store. The QR URL is `maktaba://pair?code=…`
   with no `mid`/`spki`/`n` (`pairing.go:76`), so even a future client
   has nothing to pin — phishing/MITM ECs (15.5, 15.2) are wide open.

4. **Spec contract divergence misrepresents progress.** Routes are
   `/api/pairing/{request,status,exchange}` not the spec'd
   `/api/auth/pair[/claim]`; codes are 8-char base32 not 6-digit; no
   rate-limit, no audit (`category='pair'`), no `device_kind`, no
   revoke/list endpoints, no sweeper. The `0055_pairing_tickets`
   migration lacks `nonce`/`device_kind` and is referenced by no code,
   so the schema is also non-conformant and dead.

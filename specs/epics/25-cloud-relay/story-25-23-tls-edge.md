# Story 25.23 — TLS at the edge (wildcard ACME)

> Epic 25 · Cloud relay · Phase 5 (operations)

## Description

The cloud serves three TLS hostnames:

- `*.maktaba.app` — wildcard for user-claimed subdomains and the
  `app.`, `web.`, `api.`, `admin.`, `cloud.`, `relay.`,
  `releases.` administrative names.
- `maktaba.app` — apex (marketing site).
- `*.maktaba.cloud` — staging environment.

The wildcard is non-negotiable: per-user certs would mean
issuing thousands of LE certs and managing rate limits, none of
which serves a real need.

Issuance:

- **Let's Encrypt** as primary CA via DNS-01 challenge against
  Cloudflare. ACME automation runs every 30 days inside the
  cloud's `--role=worker` process.
- DNS-01 over HTTP-01 because the wildcard is not issuable via
  HTTP-01.
- Cloudflare API token scoped to `Zone.DNS:Edit` for the two
  zones (`maktaba.app`, `maktaba.cloud`); rotated quarterly.
- **ZeroSSL** as backup CA; hot-swappable via config flag.
- Certificates persisted in Postgres
  (`cloud_tls_certs(host, fullchain_pem, key_sealed_pem,
  issued_at, not_before, not_after, issuer)`); key sealed with
  KMS data key. Loaded into the LB / relay processes at
  startup; reloaded on SIGHUP after rotation.

Distribution:

- Hetzner LB terminates TLS for the relay path
  (streaming-heavy; bypasses Cloudflare to avoid pricing
  surprises).
- Cloudflare terminates TLS for `app.`, `api.`, `admin.`
  (Cloudflare-issued cert at the edge; origin cert from
  the wildcard).
- Streaming subdomains have HTTP/2 enabled, ALPN
  `h2,http/1.1`, OCSP stapling on, HSTS with
  `max-age=31536000; includeSubDomains; preload`.

Cipher suites:

- TLS 1.2 + 1.3.
- TLS 1.2 ciphers limited to AEAD: ECDHE-ECDSA / ECDHE-RSA
  with CHACHA20-POLY1305 or AES-GCM only.
- No TLS 1.0/1.1, no RC4, no 3DES, no static-RSA key
  exchange.
- ECDSA primary; RSA fallback for compatibility.

Pinning posture:

- We do **not** ship cert pinning to clients. (Per the
  README, the cloud is in the trust boundary by design;
  pinning would brittle on rotation. Server-to-cloud
  tunnel pins the Cloudflare intermediate CA.)

## Acceptance criteria

- **Given** the operator runs `make tls-issue`,
  **when** ACME completes,
  **then** a fresh wildcard cert valid for 90 days is
  stored in `cloud_tls_certs` and reloaded into the LB.
- **Given** an existing cert is < 30 days from expiry,
  **when** the rotation cron runs,
  **then** ACME issues a new cert and the old one is
  retained until expiry.
- **Given** Let's Encrypt is unreachable,
  **when** issuance fails,
  **then** the operator is paged; ZeroSSL fallback can be
  enabled with a one-line config change; existing cert
  continues serving.
- **Given** a user opens `https://mahmoud.maktaba.app`,
  **when** TLS handshake completes,
  **then** the chain validates against public roots and
  the SAN includes `*.maktaba.app`.
- **Given** a client tries TLS 1.0,
  **when** the LB negotiates,
  **then** the handshake fails; TLS errors metric
  increments.
- **Given** an operator forces a cert rotation,
  **when** the new cert is loaded,
  **then** in-flight TLS sessions complete on the old
  cert; new sessions use the new cert.
- **Given** Cloudflare's API token is rotated,
  **when** the cron runs the next renewal,
  **then** the new token is read from the secret store
  and the renewal succeeds.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | parse cert, check SAN | inspect | wildcard present |
| T02 | integration | ACME against staging LE | run | cert issued, parsed, stored |
| T03 | integration | force rotation | trigger | new cert in DB; LB reloads |
| T04 | regression  | TLS 1.0 attempt | handshake | rejected |
| T05 | regression  | weak cipher (AES-CBC) | handshake | rejected |
| T06 | unit        | OCSP stapling | inspect handshake | OCSP response present |
| T07 | integration | cert near expiry (29 days) | cron | renewal triggered |
| T08 | regression  | renewal failure 3x | cron | pages on-call |
| T09 | unit        | HSTS header | curl | `max-age=31536000; includeSubDomains; preload` |
| T10 | integration | invalid cert in DB | startup | LB refuses to start with that cert; falls back to last good |

## Edge cases

- **Cert preload list.** `maktaba.app` is in HSTS preload
  list; documented operationally that we cannot remove
  HSTS without 6+ months lead time. Do not break it.
- **Certificate transparency.** All certs we issue end up
  in CT logs. We monitor Cert-Spotter for unexpected
  issuances against `maktaba.app` (could indicate a
  rogue issuance via compromised DNS).
- **OCSP must-staple.** We staple but don't set the
  must-staple OID in the CSR (rate of failure too high
  on Let's Encrypt v1). Defer.
- **Key compromise.** The cert key is stored sealed in
  Postgres with the cloud's KMS data key. On compromise:
  rotate key, force-issue new cert, rotate KMS key. Audit
  every step.
- **Zone-level lockdown.** The Cloudflare API token can
  only read/write DNS in those two zones; cannot create
  zones, transfer, or modify settings. Logged.
- **Subdomain-of-subdomain.** A wildcard
  `*.maktaba.app` does **not** cover `a.b.maktaba.app`;
  per RFC 6125, wildcards are single-label. We don't
  use deeper levels.
- **Cert size.** Wildcard ECDSA P-256 is ~700 bytes;
  RSA-2048 fallback ~1.5 KB. Both fine for typical TLS
  budgets.
- **Old client compatibility.** Some smart-TVs ship with
  expired root stores (looking at you, Samsung 2017).
  Our cert chain is short and uses LE's ISRG Root X2
  (ECDSA) + cross-sign for legacy. Document.

## Files / packages

- `cloud/internal/tls/acme.go` — ACME orchestrator (uses
  `lego` library).
- `cloud/internal/tls/store.go` — Postgres cert storage.
- `cloud/internal/tls/loader.go` — process-side reload
  on SIGHUP.
- `cloud/cmd/maktaba-cloud/main.go` — wiring.

## Open questions

- **Multiple issuers in parallel.** Could bracket against
  a Sectigo-issued backup; defer.
- **Key types.** Currently ECDSA P-256; some IoT devices
  prefer RSA. v1: ECDSA only.

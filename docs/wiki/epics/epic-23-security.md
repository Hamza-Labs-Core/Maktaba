# Epic 23 — Security

> **Status:** spec + plans complete. **Source:** `specs/epics/23-security/`.
> **Anchors:** [`architecture.md`](../../../specs/architecture.md) §11.5 (secrets), §1.4 (inter-service gRPC), §4 (session model), §8 (ACL table).

## Goal

Maktaba is safe to expose on a home LAN by default and safe to expose to the internet with the documented production hardening. **No secret leaves the host that wasn't authorized.** Users authenticate once and stay authenticated across the device fleet. The supply chain is auditable. This epic addresses authentication, authorization, transport, secrets, content safety, and supply-chain integrity. It composes with [Epic 21](epic-21-observability.md) (audit log) and [Epic 22](epic-22-devops.md) (signed artifacts).

## Stories & Plans

| # | Story | Plan | Summary |
|---|-------|------|---------|
| 23.1 | [Authentication](../../../specs/epics/23-security/story-23-01-authentication.md) | [plan-23-01](../../../specs/epics/23-security/plan-23-01-authentication.md) | Argon2id hashing + rehash on login; RS256 JWT with rotation (90 d active, 30 d overlap); JWKS endpoint; sentinel-UUID single-user mode. |
| 23.2 | [Authorization & ACLs](../../../specs/epics/23-security/story-23-02-authorization-acls.md) | [plan-23-02](../../../specs/epics/23-security/plan-23-02-authorization-acls.md) | Single `authorize(action, resource)` per handler; per-library roles (admin/editor/viewer); JWT `lib` claim canonical for offline streaming authz. |
| 23.3 | [Transport security](../../../specs/epics/23-security/story-23-03-transport-security.md) | [plan-23-03](../../../specs/epics/23-security/plan-23-03-transport-security.md) | Caddy TLS 1.2+ (modern ciphers, OCSP, ALPN h2); HSTS default-on; inter-service mTLS; native cert-pin (TOFU). |
| 23.4 | [Secrets management](../../../specs/epics/23-security/story-23-04-secrets-management.md) | [plan-23-04](../../../specs/epics/23-security/plan-23-04-secrets-management.md) | `_FILE > _<NAME> > config` precedence; no secrets in DB/logs/metrics; settings API metadata-only; log redaction middleware. |
| 23.5 | [Input validation](../../../specs/epics/23-security/story-23-05-input-validation.md) | — | Schema validation (OpenAPI, GraphQL); opaque UUIDs only; symlink-resolving canonical-path check; argv-array cmd defense; SSRF allow-list. |
| 23.6 | [Rate limiting](../../../specs/epics/23-security/story-23-06-rate-limiting.md) | — | Per-IP + per-route limits (login 10/min, refresh 60/min, other 30/min); per-user on expensive ops; 5 failures → 15-min lockout; confirmation token on destructive ops. |
| 23.7 | [Supply-chain security](../../../specs/epics/23-security/story-23-07-supply-chain-security.md) | — | SBOM (cyclonedx) per artifact; CVE scan gate (govulncheck, pip-audit, npm audit); base images digest-pinned; Renovate/Dependabot. |
| 23.8 | [Coordinated disclosure](../../../specs/epics/23-security/story-23-08-coordinated-disclosure.md) | — | SECURITY.md SLA (3 biz days ack, 90 days fix); GHSA drafts; advisories surfaced to clients. |

## Cross-cutting decisions

### Auth flow

- **Web (browser):** Login form → `POST /api/auth/login` → password verified against argon2id (rehash if `needsRehash`) → `httpOnly Secure SameSite=lax` session cookie → CSRF token paired with session, validated on state-changing requests (Story 10.2).
- **Native (mobile/TV):** `POST /api/auth/login` → mint short-lived access JWT (RS256, 15 min) + opaque refresh token (30 d, stored hashed; rotation revokes previous, with reuse detection) → return token pair.
- **Single-user dev mode:** `MAKTABA_ADMIN_TOKEN` env → constant-time bearer comparison → synthetic user with sentinel UUID `00000000-0000-0000-0000-000000000001` and `IsAdmin=true`.

### JWT lifecycle

1. RSA 3072-bit keypair generated on startup (or loaded from `signing_keys` table).
2. Single active signing key; identifier `kid` embedded in JWT header.
3. **Mint (`auth.Minter`):**
   - Standard claims: `iss`, `aud` (one of `streaming | streaming-direct | streaming-static`), `sub`, `iat`, `nbf` (now − 30 s skew), `exp` (now + 15 min), `jti`, `kid`.
   - Authorization claims: `usr` (user UUID), `lib` (array of library UUIDs), `is_admin`.
4. **JWKS:** `GET /.well-known/jwks.json` → public key document; cached by streaming (≤5 min, ETag-aware).
5. **Rotation (90 d):** Hourly daemon checks active key age; new key minted, previous retired but kept in JWKS for 30 d overlap (legacy tokens still validate); after overlap, key purged.
6. **Streaming verification:** Pull `kid` from JWT header → JWKS cache → verify signature (**RS256 only**, algorithm-confusion defended) → check `aud`, `exp`, and `lib` claim contains requested library.

### Signed URLs

- Issued for poster, sprite, subtitle, segment with `aud ∈ {streaming-direct, streaming-static}`.
- Short TTL (≤24 h); `sub` set to `session_id` for session-less direct access.
- Streaming verifier checks `aud` matches request path.

### Rate limits

- **Per-IP:** login 10/min, refresh 60/min, other auth 30/min.
- **Per-user:** search 60/min, bulk 10/min.
- Returns `429 Retry-After` on breach.
- Failed-login lockout: ≥5 failures within 15-min sliding window per `(user, ip)` → 15-min lock; admin override via `POST /api/users/{id}/unlock` (audited).

### Input validation

- **Schema validation:** OpenAPI (REST) + GraphQL; rejected input → `400 problem+json`.
- **Filesystem paths:** clients submit only opaque UUIDs.
- **`paths.canonical_under_roots(p)`** resolves symlinks; rejects `..`, NUL bytes, paths outside library roots.
- **Command injection defense:** FFmpeg / whisper / pyannote spawned via `os/exec` with argv slice; never shell strings.
- **SSRF defense:** URL fetch checks resolved IP is not RFC 1918, loopback, or link-local; ≤3 redirects.
- **Subtitle injection:** sidecar SRT and Maktaba-generated VTT cues HTML-escaped before render.

### TLS & certs

- **Caddy reverse proxy:** terminates TLS. Mac uses local-CA (trusted to keychain); Linux uses Let's Encrypt.
- **TLS profile:** 1.2+ minimum, modern ciphers (ECDHE + AEAD), OCSP stapling, ALPN h2.
- **HSTS:** default `max-age=31536000; includeSubDomains`; opt-out via `MAKTABA_DISABLE_HSTS=true`.
- **Inter-service mTLS** when not loopback: in-process CA persisted in `signing_keys` table (encrypted private key) issues 24-h leaf certs for streaming/pipeline; SANs include `localhost` and container name. Daemon re-fetches within 1 h of expiry. Loopback exception `internal_mtls=auto|off|on`.
- **Native cert-pinning (TOFU):** Capacitor/Tauri apps store issuer fingerprint on first connect; mismatch refuses to send credentials.

### Secrets management

- **Canonical secrets** (per architecture §11.5): `MAKTABA_ADMIN_TOKEN`, `MAKTABA_DATABASE_URL`, `MAKTABA_JWT_PRIVATE_KEY_PEM`, `MAKTABA_JWT_PUBLIC_KEY_PEM`, `MAKTABA_KEY_ENCRYPTION_KEY`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`.
- **Source precedence:** `<KEY>_FILE` env (Docker secrets) > `<KEY>` env > config file value.
- **Streaming binary allowlist:** loads only `MAKTABA_JWT_PUBLIC_KEY_PEM` (and read-only DB if present); `strings` binary contains no reference to private key or STT backend keys; CI lint enforces.
- **Settings API:** `GET /api/settings` returns metadata only (`{name, configured, source}`), never values; write-only via `PUT /api/settings/<key>`.
- **Log redaction:** slog handler + Python filter; redacts any field whose key ends in `_KEY`, `_TOKEN`, `_PASSWORD`, `_SECRET`, `_PEM`; redacts high-entropy values (24+ base64-like chars).
- **Reload:** SIGHUP triggers atomic reload; in-flight requests see old, new requests see new.

### Supply chain

- **SBOM** (cyclonedx) generated per artifact during release.
- **CVE scan gate:** `govulncheck` (Go), `pip-audit` (Python), `npm audit` (web). High-severity blocks merge unless suppressed under `security/suppressions/<cve-id>.md` with rationale + expiry.
- **Base images** digest-pinned; renovate/dependabot weekly with security-update auto-approve when green.

### Coordinated disclosure

- `SECURITY.md` documents: 3 business days ack; 90-day fix window; GHSA workflow; client version surface for advisories.

## Migrations claimed

| Slot | Plan | Tables |
|---|---|---|
| `0040_signing_keys` (story 23.1) | plan-23-01 | `signing_keys(kid, algorithm, public_pem, private_pem, created_at, activated_at, retired_at, purge_after)`. |
| (extends `library_acl`, story 23.2) | plan-23-02 | Existing `library_acl(user_id, library_id, role)` consumed; no new table. |
| Rate-limit / lockout buckets (story 23.6) | — | `failed_login_attempts(user_id, ip, attempted_at)`, `rate_limit_bucket(key, count, window_start)`. |

## Dependencies

- **Story 10.1** (user store, password hashing).
- **Story 10.2** (login UI, CSRF tokens, sessions).
- **Story 10.3** (JWT minting, extended for `lib` claim and rotation).
- **Story 10.4** (refresh-token rotation, JWKS rollover).
- **Story 19.8** (`library_acl` + multi-tenant readiness).
- **Story 21.6** (audit log; 23.6 writes lockout + confirmation events).
- **Story 22.1** (CI gates; 23.7 CVE-scan gate integrates).
- **Story 22.2** (signed artifacts, SBOM).
- **Story 22.3** (Caddy TLS reverse proxy).
- **Story 24.9** (forward-back-compat invariants for ACL removal and key rotation).

## Out of scope

- 2FA / phishing-resistant auth (UI is Story 10.2).
- DDoS mitigation beyond rate limiting.
- Hardware security modules.
- OAuth/OIDC federation.
- Database row-level security.
- Encrypted-at-rest user data (media files remain plaintext on disk).

## See also

- [Security architecture summary](../security.md) — consolidated view of auth flow, JWT lifecycle, signed URLs, rate limits, input validation, TLS, secrets management.
- [Epic 10 (auth)](#) — foundational user, session, and JWT plumbing.
- [Epic 21 — Observability](epic-21-observability.md) (audit log).
- [Epic 22 — DevOps](epic-22-devops.md) (signed artifacts, SBOM).
- [Glossary](../glossary.md) — Argon2id, RS256, Bearer token, session cookie, refresh token, JWKS, key rotation, sentinel user, CSRF, ACL, role, library claim, revocation lag, mTLS, SPIFFE-style cert, TOFU, in-process CA, redaction, path masking, SBOM, CVE scan, GHSA.

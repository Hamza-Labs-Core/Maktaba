# Maktaba — Security Architecture Summary

> Cross-references **Epic 10 (Auth & Security)** and **Epic 23 (Security)**.
> See per-epic pages: [Epic 23 — Security](epics/epic-23-security.md).
> Anchors: [`architecture.md`](../../specs/architecture.md) §1.4, §4, §8 (ACL), §9.1, §9.4, §11.5.

This page is a consolidated, operator-facing view of how Maktaba authenticates, authorizes, transports, and protects data — pulled from Epic 10 (foundational auth) and Epic 23 (hardening) plans and stories. Where two epics describe the same surface (e.g., JWT minting) the canonical owner is named.

---

## 1. Auth flow

### Web (browser)

1. User submits credentials at the login form.
2. `POST /api/auth/login` (Story 10.2) → API verifies password against **Argon2id** hash (Story 10.1).
   - If `needsRehash` (e.g., parameters bumped), update hash with current params on the same request.
3. Server issues an **`httpOnly Secure SameSite=lax` session cookie**; CSRF token stored in session and validated on every state-changing request.

### Native (mobile / desktop / TV)

1. `POST /api/auth/login` returns a **token pair**:
   - **Access JWT** (RS256, 15 min) — bearer-attached on every API call.
   - **Refresh token** (opaque, 30 d) — stored hashed in DB; rotation revokes the previous; reuse triggers session-wide revocation.
2. Client refreshes via `POST /api/auth/refresh` (Story 10.4), single-flight gated on the client (Epic 14 `actor RefreshGate`, mirrored in Capacitor and Tauri clients).

### Single-user / dev mode

- Sets `MAKTABA_ADMIN_TOKEN` env. Bearer token compared in **constant time**; no user table interaction.
- Synthetic user: sentinel UUID `00000000-0000-0000-0000-000000000001` with `IsAdmin=true`.
- This mapping is also used by the multi-tenant readiness flag flip (Story 19.8).

### QR pairing (TV / desktop → mobile)

- TV/desktop is the **issuer** (`POST /api/auth/pair`, plan-10-17).
- Mobile is the **claimer** (`POST /api/auth/pair/claim`, plan-15-06 nonce extension).
- QR URL: `https://{server}/pair?code={code}&mid={mdns_id}&spki={hash}&n={nonce}` — embeds enough material for TOFU bootstrapping over an internet-only network.

---

## 2. JWT lifecycle

### Keys

- Owner: Story 10.6 (RS256 keys & JWKS); hardening in Story 23.1.
- RSA 3072-bit keypair persisted in `signing_keys` table (private key encrypted with `MAKTABA_KEY_ENCRYPTION_KEY` per Story 10.14).

### Mint

`auth.Minter` produces an access token with:

| Claim | Value |
|---|---|
| `iss` | API issuer URL |
| `aud` | `streaming` \| `streaming-direct` \| `streaming-static` |
| `sub` | user UUID |
| `iat`, `nbf` (now − 30 s skew), `exp` (now + 15 min) | timing |
| `jti` | unique token ID |
| `kid` | key ID for JWKS lookup |
| `usr` | user UUID (auth metadata) |
| `lib` | array of library UUIDs the user has a role on |
| `is_admin` | boolean |

### JWKS

- `GET /.well-known/jwks.json` publishes the public key document.
- Streaming caches with TTL ≤5 min (ETag-aware).

### Rotation (90 days)

1. Daemon runs hourly; checks if active key age ≥ rotation window.
2. Mints a new key; previous key is **retired**, but kept in JWKS for a **30-day overlap** so legacy tokens still validate.
3. After overlap, `purge_after` timestamp triggers DB deletion.

### Verification (streaming service)

1. Pull `kid` from JWT header.
2. Fetch from JWKS cache (refresh if missing).
3. Verify signature with **RS256 only** (algorithm-confusion attacks rejected).
4. Validate `aud`, `exp`, `lib` claim contains the requested library.

Streaming **never calls back to the API** for ACL checks during a session; the JWT `lib` claim is canonical for offline authorization. ACL revocation lag is bounded by the 15-min token TTL.

---

## 3. Signed URLs

For direct streaming (poster, sprite, subtitle, segment) where session-less access is required:

- `aud` set to `streaming-direct` (segment) or `streaming-static` (poster/sprite/subtitle).
- `sub` set to `session_id` rather than user UUID.
- TTL ≤24 h.
- Streaming verifier checks `aud` matches the request path category.

---

## 4. Rate limits

Story 23.6 + Story 10.16 (security audit).

### Per-IP (auth surfaces)

| Route | Budget |
|---|---|
| `POST /api/auth/login` | 10/min |
| `POST /api/auth/refresh` | 60/min |
| All other auth routes | 30/min |

### Per-user (expensive operations)

| Route | Budget |
|---|---|
| `POST /api/search` | 60/min |
| Bulk job submission | 10/min |
| `POST /api/recommendations/refresh` | 1/h |

Exceeding a bucket returns `429 Retry-After`.

### Failed-login lockout

- Tracked per `(user, ip)` tuple in `failed_login_attempts`.
- **≥5 failures within 15-min sliding window** → user locked for 15 minutes.
- Locked users receive `423 Locked` even with the correct password.
- Admin override: `POST /api/users/{id}/unlock` (audit category `admin`).
- Every lock event writes an `audit` row (category `auth`).

### Pairing rate limit

- `POST /api/auth/pair/claim` → 6/min/IP (Story 15.6).

### Confirmation tokens

Destructive operations (delete library, delete user, rotate signing key) require an explicit confirm field matching the resource name/ID.

---

## 5. Input validation

Story 23.5.

- **Schema validation:** OpenAPI (REST) + GraphQL; invalid → `400 problem+json`.
- **Filesystem paths:** clients submit only opaque UUIDs — never file paths.
- **`paths.canonical_under_roots(p)`** is the universal gate:
  - Resolves symlinks.
  - Rejects `..` traversal.
  - Rejects NUL bytes.
  - Asserts the resolved path lies under a configured library root.
  - Called by every filesystem operation (scan, hash, FFmpeg, transcribe).
- **Command-injection defense:** FFmpeg / whisper / pyannote spawned via `os/exec` with **argv slices**; never shell strings.
- **SSRF defense:** URL fetch resolves IP; rejects RFC 1918, loopback, link-local; ≤3 redirects.
- **Subtitle injection:** sidecar SRT and Maktaba-generated VTT cues HTML-escaped before player render (`<script>` → `&lt;script&gt;`).
- **File content untrusted:** media probe outputs size-bounded; malformed files error gracefully.

---

## 6. TLS

Story 23.3.

- **Caddy reverse proxy** terminates TLS at the edge.
  - macOS: local-CA installed in keychain.
  - Linux: Let's Encrypt by default.
- **Profile:** TLS 1.2+ minimum, modern cipher suite (ECDHE + AEAD), OCSP stapling, ALPN h2.
- **HSTS:** default `max-age=31536000; includeSubDomains`. Opt-out via `MAKTABA_DISABLE_HSTS=true`.
- **Inter-service mTLS** (when not loopback):
  - In-process CA persisted in `signing_keys` (encrypted private key) issues 24-h leaf certs for streaming/pipeline.
  - SANs include `localhost` and container names.
  - Daemon refreshes leaf cert within 1 h of expiry; graceful hot-reload (no downtime).
  - Loopback exception: `internal_mtls = auto | off | on`. `auto` skips mTLS when all services bind `127.0.0.1`; startup banner warns. `on` forces mTLS regardless.
- **Cert rotation overlap (Story 15.2):** 7-day window where both old and new SPKI hashes are accepted, surfaced via JWS-signed `GET /api/system/cert-rotation`.
- **Native cert pinning (TOFU):** Capacitor/Tauri/tvOS/Android TV apps store the issuer fingerprint on first connect; mismatch refuses to send credentials; the UI shows a downgrade warning.

---

## 7. Secrets management

Story 23.4 + architecture §11.5.

### Canonical secrets

| Env name | Purpose |
|---|---|
| `MAKTABA_ADMIN_TOKEN` | Single-user-mode bearer token (sentinel UUID). |
| `MAKTABA_DATABASE_URL` | Postgres / SQLite connection string. |
| `MAKTABA_JWT_PRIVATE_KEY_PEM` | RS256 signing key (API only). |
| `MAKTABA_JWT_PUBLIC_KEY_PEM` | RS256 verify key (streaming). |
| `MAKTABA_KEY_ENCRYPTION_KEY` | KEK that wraps the private signing key in DB. |
| `OPENAI_API_KEY`, `ANTHROPIC_API_KEY` | Optional STT / LLM backends. |

### Source precedence

```
<KEY>_FILE  (Docker secrets at /run/secrets/<name>)
   > <KEY>     (env var)
   > config file value
```

### Streaming binary allowlist

The streaming service loads **only** `MAKTABA_JWT_PUBLIC_KEY_PEM` (and read-only DB if present). The compiled binary contains no reference to private keys or STT backend keys. A CI lint (`tools/binary-secret-scan.go`) enforces this on every build.

### Settings API

- `GET /api/settings` (admin only) returns metadata only: `{ name, configured, source }`. Never the value.
- `PUT /api/settings/<key>` is **write-only**.
- Logs/metrics never contain secret values.

### Log redaction

- slog handler (Go) + Python logging filter scrub any field whose key ends in `_KEY`, `_TOKEN`, `_PASSWORD`, `_SECRET`, `_PEM`.
- High-entropy values (24+ chars of base64-like alphabet) replaced with `***`.
- Canonical redaction list: `shared/redact/list.yaml`. CI lint forbids logging known-sensitive field names.

### Reload

`SIGHUP` triggers an atomic reload of secrets from env / file. In-flight requests see the old value; new requests see the new value. Documented for operators.

---

## 8. Coordinated disclosure

Story 23.8.

- `SECURITY.md` documents the disclosure process.
- **SLA:** 3 business days to acknowledge; 90 days to fix or coordinate disclosure.
- **GHSA** drafts published with CVE (if assigned), mitigations, affected versions.
- Patched versions released as patch versions on supported branches; release notes link the GHSA.
- Client version check surfaces advisories to users.

---

## 9. Supply chain

Story 23.7.

- **SBOM** (cyclonedx) generated per artifact during release.
- **CVE scan gate** in CI:
  - Go: `govulncheck`
  - Python: `pip-audit`
  - Web: `npm audit`
- High-severity vulns block merge unless suppressed under `security/suppressions/<cve-id>.md` with rationale + expiry.
- **Base images digest-pinned** (no `:latest`).
- Renovate / Dependabot runs weekly; security updates auto-approve when CI is green.

---

## 10. ACL & authorization

Story 23.2.

- **Single `authorize(action, resource)`** call per HTTP handler — central enforcement point.
- **Roles per library** in `library_acl(user_id, library_id, role)`:
  - `admin` — full control.
  - `editor` — ingest, edit metadata.
  - `viewer` — read, watch only.
- **JWT `lib` claim** is canonical for streaming offline authorization (no API callback).
- **Revocation lag** bounded by JWT TTL (15 min).

---

## 11. Story / plan map

| Concern | Owning story | Plan |
|---|---|---|
| User store, password hashing | Story 10.1 | `specs/epics/10-auth-security/plan-10-01-user-store.md` |
| Login, sessions, CSRF | Story 10.2 | `specs/epics/10-auth-security/plan-10-02-login-session.md` |
| JWT minting | Story 10.3 | `specs/epics/10-auth-security/plan-10-03-jwt-mint.md` |
| Refresh tokens | Story 10.4 | `specs/epics/10-auth-security/plan-10-04-token-refresh.md` |
| RS256 keys & JWKS | Story 10.6 | `specs/epics/10-auth-security/plan-10-06-rs256-keys-jwks.md` |
| Bootstrap token / sentinel | Story 10.9 | — |
| Data encryption key | Story 10.14 | — |
| Security audit | Story 10.16 | — |
| Auth pair (canonical) | Story 10.17 | `specs/epics/10-auth-security/plan-10-17-auth-pair.md` |
| Long-term server identity (Ed25519) | Story 10.18 | — |
| Authentication hardening | Story 23.1 | [plan-23-01](../../specs/epics/23-security/plan-23-01-authentication.md) |
| Authorization & ACLs | Story 23.2 | [plan-23-02](../../specs/epics/23-security/plan-23-02-authorization-acls.md) |
| Transport security | Story 23.3 | [plan-23-03](../../specs/epics/23-security/plan-23-03-transport-security.md) |
| Secrets management | Story 23.4 | [plan-23-04](../../specs/epics/23-security/plan-23-04-secrets-management.md) |
| Input validation | Story 23.5 | — |
| Rate limiting | Story 23.6 | — |
| Supply chain | Story 23.7 | — |
| Coordinated disclosure | Story 23.8 | — |
| QR pairing nonce extension | Story 15.6 | [plan-15-06](../../specs/epics/15-discovery/plan-15-06-pairing-api.md) |
| Federation crypto (X25519+Ed25519+SAS) | Story 15.7 | [plan-15-07](../../specs/epics/15-discovery/plan-15-07-federation-api.md) |
| Cert rotation | Story 15.2 | [plan-15-02](../../specs/epics/15-discovery/plan-15-02-cloud-relay.md) |
| Audit log | Story 21.6 | [plan-21-06](../../specs/epics/21-observability/plan-21-06-audit-log.md) |

## See also

- [Epic 23 — Security](epics/epic-23-security.md) — full story-by-story breakdown.
- [Epic 21 — Observability](epics/epic-21-observability.md) — `audit_log`, redaction.
- [Epic 22 — DevOps](epics/epic-22-devops.md) — signed artifacts, SBOM.
- [Epic 15 — Discovery](epics/epic-15-discovery.md) — TOFU pinning, federation crypto, cert rotation overlap.
- [Glossary](glossary.md) — all security terminology.

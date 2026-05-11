# Epic 10 — Auth & Security

> Identity, sessions, signed URLs, secret handling, transport hardening. Two surfaces, one identity: web uses httpOnly cookies + CSRF; mobile/desktop/TV use bearer JWTs + refresh tokens. The Streaming Service validates JWTs offline against the API's published JWKS, so a playing video survives an API restart.

- **Spec README:** [`specs/epics/10-auth-security/README.md`](../../../specs/epics/10-auth-security/README.md)
- **Architecture anchors:** §9.4 (streaming JWT), §9.8 (auth model)
- **Out of scope (this version):** RBAC beyond admin/viewer (multi-tenant permissions are v2), SSO (OIDC, SAML), 2FA. Hooks are documented but not implemented.
- **First-to-land epic:** Epic 07 endpoints depend on this epic's `User`, `Session`, and JWT primitives.

## Stories & Plans

| #     | Story                                                  | Plan                                                    | Depends on    |
|-------|--------------------------------------------------------|---------------------------------------------------------|---------------|
| 10.1  | [User store + argon2id password hashing](../../../specs/epics/10-auth-security/story-10-01-user-store.md) | [plan](../../../specs/epics/10-auth-security/plan-10-01-user-store.md) | —             |
| 10.2  | [Web login (cookie + CSRF)](../../../specs/epics/10-auth-security/story-10-02-web-login.md) | [plan](../../../specs/epics/10-auth-security/plan-10-02-web-login.md) | 10.1          |
| 10.3  | [Native login (JWT access + opaque refresh)](../../../specs/epics/10-auth-security/story-10-03-native-login.md) | [plan](../../../specs/epics/10-auth-security/plan-10-03-native-login.md) | 10.1          |
| 10.4  | [Token refresh + rotation](../../../specs/epics/10-auth-security/story-10-04-token-refresh.md) | [plan](../../../specs/epics/10-auth-security/plan-10-04-token-refresh.md) | 10.3          |
| 10.5  | [Logout + session revocation](../../../specs/epics/10-auth-security/story-10-05-logout-revocation.md) | [plan](../../../specs/epics/10-auth-security/plan-10-05-logout-revocation.md) | 10.2, 10.3    |
| 10.6  | [RS256 key generation, rotation, JWKS publication](../../../specs/epics/10-auth-security/story-10-06-rs256-keys-jwks.md) | [plan](../../../specs/epics/10-auth-security/plan-10-06-rs256-keys-jwks.md) | —             |
| 10.7  | [Streaming-side offline JWT verification](../../../specs/epics/10-auth-security/story-10-07-streaming-jwt-verify.md) | [plan](../../../specs/epics/10-auth-security/plan-10-07-streaming-jwt-verify.md) | 10.6, 8.1     |
| 10.8  | [Signed-URL minter (manifest, direct, sidecar)](../../../specs/epics/10-auth-security/story-10-08-signed-url-minter.md) | [plan](../../../specs/epics/10-auth-security/plan-10-08-signed-url-minter.md) | 10.6          |
| 10.9  | [Single-user mode (admin token bypass)](../../../specs/epics/10-auth-security/story-10-09-single-user-mode.md) | [plan](../../../specs/epics/10-auth-security/plan-10-09-single-user-mode.md) | 10.1, 10.6    |
| 10.10 | [CSRF protection (web only)](../../../specs/epics/10-auth-security/story-10-10-csrf-protection.md) | [plan](../../../specs/epics/10-auth-security/plan-10-10-csrf-protection.md) | 10.2          |
| 10.11 | [Brute-force / credential-stuffing protection](../../../specs/epics/10-auth-security/story-10-11-brute-force-protection.md) | [plan](../../../specs/epics/10-auth-security/plan-10-11-brute-force-protection.md) | 10.2, 10.3    |
| 10.12 | [Rate limiting on auth endpoints](../../../specs/epics/10-auth-security/story-10-12-rate-limiting-auth.md) | [plan](../../../specs/epics/10-auth-security/plan-10-12-rate-limiting-auth.md) | 10.2, 7.19    |
| 10.13 | [Permission model (admin vs viewer; resource scope)](../../../specs/epics/10-auth-security/story-10-13-permission-model.md) | [plan](../../../specs/epics/10-auth-security/plan-10-13-permission-model.md) | 10.1          |
| 10.14 | [Secret loading and redaction](../../../specs/epics/10-auth-security/story-10-14-secret-loading.md) | [plan](../../../specs/epics/10-auth-security/plan-10-14-secret-loading.md) | 7.15          |
| 10.15 | [Transport security (TLS, HSTS, secure cookies, CORS)](../../../specs/epics/10-auth-security/story-10-15-transport-security.md) | [plan](../../../specs/epics/10-auth-security/plan-10-15-transport-security.md) | —             |
| 10.16 | [Audit log for security-sensitive actions](../../../specs/epics/10-auth-security/story-10-16-security-audit.md) | [plan](../../../specs/epics/10-auth-security/plan-10-16-security-audit.md) | 9.17          |
| 10.17 | [Device pairing endpoint (`POST /api/auth/pair`)](../../../specs/epics/10-auth-security/story-10-17-auth-pair.md) | [plan](../../../specs/epics/10-auth-security/plan-10-17-auth-pair.md) | 10.3, 10.6    |
| 10.18 | [Ed25519 server identity keypair](../../../specs/epics/10-auth-security/story-10-18-ed25519-server-identity.md) | [plan](../../../specs/epics/10-auth-security/plan-10-18-ed25519-server-identity.md) | 25.7, 25.26   |

## DB tables owned

| Table             | Story | Purpose                                                                                       |
|-------------------|-------|-----------------------------------------------------------------------------------------------|
| `users`           | 10.1  | Argon2id password hashes; `is_admin` flag; `failed_attempts` + `locked_until` (10.11). Sentinel UUID `00000000-...-001` for single-user mode. |
| `web_sessions`    | 10.2  | One row per cookie session with CSRF token, IP, UA, expiry. Reaper-indexed.                   |
| `refresh_tokens`  | 10.3  | Argon2id-hashed opaque refresh tokens. `family_id` shared across the rotation chain (10.4).   |
| `pairing_codes`   | 10.17 | 8-char base32 codes for device pairing; `state` = pending / claimed / expired.                |
| `audit_log`       | 10.16 (jointly with 9.17) | Canonical append-only audit table; this epic writes `category='security'`.        |

> See [`specs/epics/10-auth-security/README.md`](../../../specs/epics/10-auth-security/README.md#schema-additions-owned-by-this-epic) for full DDL.

## API endpoints owned

> Canonical OpenAPI: [`shared/api/openapi.yaml`](../../../shared/api/openapi.yaml).

| Endpoint                          | Story  | Surface                              |
|-----------------------------------|--------|--------------------------------------|
| `POST /auth/login`                | 10.2, 10.3 | Web cookie **and** native JWT/refresh |
| `POST /auth/register`             | 10.1   | Disabled in single-user mode         |
| `POST /auth/refresh`              | 10.4   | Rotates refresh token (family-bound) |
| `POST /auth/logout`               | 10.5   | Revokes web session **or** refresh family |
| `GET  /auth/me`                   | 10.1, 10.13 | Current user + permissions          |
| `POST /auth/pair`                 | 10.17  | Device pairing (display code → claim) |
| `GET  /.well-known/jwks.json`     | 10.6   | Public-key set consumed offline by Streaming |

## JWT shape

All minting goes through 10.8. Claims include `lib[]`, required by [Epic 08](epic-08-streaming.md) story 8.1 AC-1 for offline authorization.

```json
{
  "iss": "maktaba",
  "aud": "streaming" | "streaming-direct" | "streaming-static" | "api",
  "sub": "<session_id | video_id | artifact-hash | user_id>",
  "iat": <unix>, "exp": <unix>, "jti": "<uuid v7>", "kid": "<key-id>",
  "usr": "<user_id>",
  "lib": ["<library_id>", "..."],
  "is_admin": true
}
```

## Mockups

| File | Story | Platform | UI states |
|---|---|---|---|
| [`web/mockups/admin/login.html`](../../../web/mockups/admin/login.html) | 10.2 | admin (web) | Login form (default, error, locked) |
| [`web/mockups/admin/register.html`](../../../web/mockups/admin/register.html) | 10.1 | admin (web) | Registration form |
| [`web/mockups/admin/lockout.html`](../../../web/mockups/admin/lockout.html) | 10.11 | admin (web) | Account-locked screen, countdown, unlock path |
| [`web/mockups/admin/qr-pairing.html`](../../../web/mockups/admin/qr-pairing.html) | 10.17 | admin (web) | Pairing-code display + QR |
| [`web/mockups/admin/sessions.html`](../../../web/mockups/admin/sessions.html) | 10.5, 11.14 | admin (web) | Active sessions list, revoke action |

## Diagrams

| Diagram | Type | Coverage |
|---|---|---|
| [`auth-flow.drawio`](../../../specs/diagrams/auth-flow.drawio) | Flow | Cookie + JWT login, refresh rotation, logout, pairing |
| [`security-architecture.drawio`](../../../specs/diagrams/security-architecture.drawio) | Security | Trust boundaries, JWKS publication, secret loading |
| [`api-streaming-stories.drawio`](../../../specs/diagrams/api-streaming-stories.drawio) | Story-relationship | Auth stories grouped with 07/08/09 |
| [`entity-relationship.drawio`](../../../specs/diagrams/entity-relationship.drawio) | ER | `users → web_sessions / refresh_tokens / pairing_codes` |

## Dependencies on other epics

- **[Epic 07](epic-07-api-server.md):** consumes the middleware this epic produces (cookie verify, CSRF, JWT verify); mounts `/auth/*` handlers; calls 10.8 from story 7.10.
- **[Epic 08](epic-08-streaming.md) story 8.1:** consumes JWKS via 10.7; library-scoped JWT enforces per-segment authorization.
- **[Epic 09](epic-09-library-management.md):** owns the `audit_log` schema this epic also writes to.
- **Epic 14 (TV) / [Epic 12](epic-12-mobile.md) (Mobile):** use `/auth/pair` (10.17) for device onboarding.

## Key decisions

- **Two surfaces, one identity.** Web uses httpOnly `auth` + readable `csrf_token` cookies; native uses `Authorization: Bearer <access>` + opaque refresh in Keychain/Keystore. The login endpoint always issues both — clients pick.
- **Argon2id for password hashing** (10.1) — memory-hard against GPU-cracking. Tuned per-deployment via `app_settings`.
- **RS256 JWTs with JWKS publication** (10.6) — Streaming verifies offline; key rotation is non-disruptive to in-flight sessions.
- **Refresh tokens are opaque** (random bytes, argon2id-hashed at rest) — never JWTs. Rotation is family-bound (10.4); reuse of a revoked token revokes the entire family.
- **Library-scoped JWT** (`lib[]` claim) is the spine of offline authorization in [Epic 08](epic-08-streaming.md). [Epic 09](epic-09-library-management.md) library deletion immediately invalidates streaming tokens for that library.
- **Single-user mode** (10.9) — synthetic-admin sentinel UUID (`00000000-...-001`) attributed in `audit_log`; bypass is opt-in via env var, never the default.
- **Audit log is append-only and unified** with [Epic 09](epic-09-library-management.md) (one table, `category` discriminator). Triggers reject UPDATE/DELETE.
- **CSRF is web-only** (10.10) — bearer-token surfaces are CSRF-immune by definition.

## Sequencing

Land in order: **10.1 → 10.6 → 10.15 → 10.2/10.10 → 10.3/10.4 → 10.5 → 10.7/10.8 → 10.9 → 10.11/10.12 → 10.13 → 10.14 → 10.16 → 10.17.**

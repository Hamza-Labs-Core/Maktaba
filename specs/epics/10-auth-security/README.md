# Epic 10 — Auth & Security

Identity, sessions, signed URLs, secret handling, transport hardening.
Two surfaces, one identity (§9.8): web uses httpOnly cookies + CSRF;
mobile/desktop/TV use bearer JWTs + refresh tokens. The Streaming
Service validates JWTs *offline* against the API's published JWKS, so a
playing video survives an API restart (§9.4, §9.8).

**Out of scope for Epic 10:** RBAC beyond admin/viewer (multi-tenant
permissions are a v2 concern), SSO (OIDC, SAML), 2FA. These are noted
where relevant as future hooks but not implemented.

This epic produces the middleware and stores that Epic 7's handlers
consume. It is the first epic to land in any deployable build because
Epic 7 endpoints depend on its `User`, `Session`, and JWT primitives.

## Story map

| #     | Story                                                | Depends on |
|-------|------------------------------------------------------|------------|
| 10.1  | [User store + argon2id password hashing](story-10-01-user-store.md)               | —          |
| 10.2  | [Web login (cookie + CSRF)](story-10-02-web-login.md)                            | 10.1       |
| 10.3  | [Native login (JWT access + opaque refresh)](story-10-03-native-login.md)           | 10.1       |
| 10.4  | [Token refresh + rotation](story-10-04-token-refresh.md)                             | 10.3       |
| 10.5  | [Logout + session revocation](story-10-05-logout-revocation.md)                          | 10.2, 10.3 |
| 10.6  | [RS256 key generation, rotation, JWKS publication](story-10-06-rs256-keys-jwks.md)     | —          |
| 10.7  | [Streaming-side offline JWT verification](story-10-07-streaming-jwt-verify.md)              | 10.6, 8.1  |
| 10.8  | [Signed-URL minter (manifest, direct, sidecar)](story-10-08-signed-url-minter.md)        | 10.6       |
| 10.9  | [Single-user mode (admin token bypass)](story-10-09-single-user-mode.md)                | 10.1, 10.6 |
| 10.10 | [CSRF protection (web only)](story-10-10-csrf-protection.md)                           | 10.2       |
| 10.11 | [Brute-force / credential-stuffing protection](story-10-11-brute-force-protection.md)         | 10.2, 10.3 |
| 10.12 | [Rate limiting on auth endpoints](story-10-12-rate-limiting-auth.md)                      | 10.2, 7.19 |
| 10.13 | [Permission model (admin vs viewer; resource scope)](story-10-13-permission-model.md)   | 10.1       |
| 10.14 | [Secret loading and redaction](story-10-14-secret-loading.md)                          | 7.15       |
| 10.15 | [Transport security (TLS, HSTS, secure cookies, CORS)](story-10-15-transport-security.md) | —          |
| 10.16 | [Audit log for security-sensitive actions](story-10-16-security-audit.md)             | 9.17       |
| 10.17 | [Device pairing endpoint (`POST /api/auth/pair`)](story-10-17-auth-pair.md)            | 10.3, 10.6 |

## Schema additions owned by this epic

### `users`

Architecture §8.5 defines this; this epic owns the constraints and the
sentinel UUID for single-user mode.

```
CREATE TABLE users (
  id                UUID PRIMARY KEY,
  username          TEXT NOT NULL,
  pw_hash           TEXT NOT NULL,
  is_admin          BOOLEAN NOT NULL DEFAULT false,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  failed_attempts   INTEGER NOT NULL DEFAULT 0,
  locked_until      TIMESTAMPTZ,
  CONSTRAINT users_username_lower_unique UNIQUE (lower(username))
);
-- Sentinel for the single-user/admin-token bypass path:
INSERT INTO users (id, username, pw_hash, is_admin)
  VALUES ('00000000-0000-0000-0000-000000000001', 'admin', '<unsalted-disabled>', true);
```

The fixed sentinel UUID is referenced by Epic 4 NFR Story 19.8 and Story
10.9 for synthetic-admin attribution in `audit_log`.

### `web_sessions`

Owned by Story 10.2.

```
CREATE TABLE web_sessions (
  id            UUID PRIMARY KEY,
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf_token    TEXT NOT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at    TIMESTAMPTZ NOT NULL,
  ip            INET,
  user_agent    TEXT,
  revoked_at    TIMESTAMPTZ
);
CREATE INDEX web_sessions_user_active ON web_sessions (user_id) WHERE revoked_at IS NULL;
CREATE INDEX web_sessions_reaper ON web_sessions (expires_at) WHERE revoked_at IS NULL;
```

### `refresh_tokens`

Owned by Story 10.3.

```
CREATE TABLE refresh_tokens (
  id            UUID PRIMARY KEY,
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  hash          TEXT NOT NULL,                 -- argon2id(token)
  family_id     UUID NOT NULL,                 -- shared across rotation chain
  issued_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at    TIMESTAMPTZ NOT NULL,
  revoked_at    TIMESTAMPTZ,
  replaced_by   UUID REFERENCES refresh_tokens(id),
  client_meta   JSONB
);
CREATE INDEX refresh_tokens_user_active ON refresh_tokens (user_id, family_id) WHERE revoked_at IS NULL;
```

### `pairing_codes`

Owned by Story 10.17.

```
CREATE TABLE pairing_codes (
  code          TEXT PRIMARY KEY,             -- 8-char base32, displayed to user
  device_id     UUID,
  user_id       UUID REFERENCES users(id) ON DELETE CASCADE,
  state         TEXT NOT NULL DEFAULT 'pending', -- 'pending' | 'claimed' | 'expired'
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at    TIMESTAMPTZ NOT NULL,
  ip            INET
);
CREATE INDEX pairing_codes_state ON pairing_codes (state, expires_at);
```

### `audit_log` (security category)

Defined in Epic 9 [README.md](../09-library-management/README.md). Story
10.16 inserts rows with `category='security'`.

## Streaming JWT shape

All JWT minting goes through Story 10.8. The claims include
`library_ids` (`lib`), which is required by Epic 8 Story 8.1 AC-1 for
offline authorization:

```
{
  "iss": "maktaba",
  "aud": "streaming" | "streaming-direct" | "streaming-static" | "api",
  "sub": <session_id | video_id | artifact-hash | user_id>,
  "iat": <unix>,
  "exp": <unix>,
  "jti": "<uuid v7>",
  "kid": "<key-id>",
  "usr": "<user_id>",
  "lib": ["<library_id>", ...],
  "is_admin": true | false        // only for aud="api"
}
```

## Sequencing

Land in order: 10.1 → 10.6 → 10.15 → 10.2/10.10 → 10.3/10.4 → 10.5 →
10.7/10.8 → 10.9 → 10.11/10.12 → 10.13 → 10.14 → 10.16 → 10.17.

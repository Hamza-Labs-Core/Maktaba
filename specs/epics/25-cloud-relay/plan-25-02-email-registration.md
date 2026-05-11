# Implementation Plan — Story 25.2 Email + password registration

> Companion to [story-25-02-email-registration.md](story-25-02-email-registration.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Password hash | argon2id, params `m=64*1024, t=3, p=1`, salt 16B, hash 32B; stored as encoded string. Use `github.com/alexedwards/argon2id`. |
| Verification tokens | HMAC-SHA256(secret, `verify:<user_id>:<exp>`), `exp = 24h`; URL-safe base64. Stored only as `(user_id, exp)` server-side (HMAC verifies; no DB row needed). |
| Password-reset tokens | Same shape, 1h TTL, **single-use** — stored as a row in `email_verifications` with `purpose='reset'` (declared in the slot-0002 migration below). |
| Access tokens | RS256 JWT, 1h, claims `{sub, email, plan, kid}`. Keypair rotation handled in this story (`jwks` table). |
| Refresh tokens | Opaque 32-byte random, base64url. Stored as `(token_hash, user_id, exp=30d, rotated_at, rotated_from)` in `sessions`. Rotation is single-use; replay triggers cascade revoke. |
| Lockout state | Per `(email_normalized, ip_block)` Redis counter (`fail:<email>:<bucket>`), TTL 15m. After 10, set `lock:<email>` TTL 30m. |
| Email delivery | Postmark adapter (`internal/email/postmark.go`); swappable interface; templates `verify.html.tmpl`, `reset.html.tmpl` + plain-text. |
| Out of scope | OAuth providers (25.3/25.4). TOTP/2FA (v2). Magic-link login (deferred). |

## 1. Migration `00020001_identity.sql` (slot 0002 per README)

This story owns the canonical identity tables in slot 0002. OAuth
linkage rows (25.3 Google, 25.4 Apple) reuse the `oauth_links` table
declared here without a new migration; their plans only carry handler
code. `audit_events` is **not** declared here — it lands in slot
0002 sub-sequence `00020002_audit.sql` owned by plan-25-20. Until
that migration applies, audit-write helpers degrade to no-ops; see
§10.

```sql
-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email               CITEXT NOT NULL,
    display_name        TEXT,
    password_hash       TEXT,                  -- nullable: OAuth-only users
    email_verified_at   TIMESTAMPTZ,
    email_verified      BOOLEAN NOT NULL DEFAULT FALSE,
    locale              TEXT NOT NULL DEFAULT 'en',
    timezone            TEXT NOT NULL DEFAULT 'UTC',
    avatar_url          TEXT,
    plan                TEXT NOT NULL DEFAULT 'free' CHECK (plan IN ('free','pro','family')),
    status              TEXT NOT NULL DEFAULT 'active',
    suspended_at        TIMESTAMPTZ,
    deleted_at          TIMESTAMPTZ,
    email_bounced       BOOLEAN NOT NULL DEFAULT FALSE,
    tos_version_accepted TEXT,                  -- nullable; set on register or first OAuth signin
    password_changed_at TIMESTAMPTZ,
    last_login_at       TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_active_uq
    ON users(email) WHERE deleted_at IS NULL;

CREATE TABLE oauth_links (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider    TEXT         NOT NULL,           -- 'google' | 'apple'
    subject     TEXT         NOT NULL,           -- provider-side stable id
    email       TEXT,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (provider, subject)
);
CREATE INDEX idx_oauth_links_user ON oauth_links (user_id);

CREATE TABLE sessions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_hash  BYTEA NOT NULL,           -- sha256 of opaque refresh
    ip                  INET,
    user_agent          TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ NOT NULL,
    revoked_at          TIMESTAMPTZ,
    rotated_from        UUID REFERENCES sessions(id)
);
CREATE UNIQUE INDEX sessions_token_uq ON sessions(refresh_token_hash);
CREATE INDEX sessions_user_idx ON sessions(user_id) WHERE revoked_at IS NULL;

CREATE TABLE email_verifications (
    token_hash  BYTEA PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose     TEXT NOT NULL,                    -- 'verify' | 'reset'
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- JWT signing keys (RS256). Renamed from cloud_jwks → jwt_keys to
-- disambiguate from the Ed25519 entitlement keystore in plan-25-26.
CREATE TABLE jwt_keys (
    kid         TEXT PRIMARY KEY,
    alg         TEXT NOT NULL DEFAULT 'RS256',
    public_pem  TEXT NOT NULL,
    private_pem_sealed BYTEA NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    rotated_at  TIMESTAMPTZ,
    retired_at  TIMESTAMPTZ
);

-- +goose Down
DROP TABLE IF EXISTS jwt_keys, email_verifications, sessions, oauth_links, users CASCADE;
```

`stripe_customer_id` is intentionally **not** stored on `users`; it
lives on `subscriptions` (slot 0006, plan-25-13) as the single source
of truth. Lookups by Stripe customer id join through that table.

## 2. Endpoints

```
POST   /api/auth/register
POST   /api/auth/verify-email
POST   /api/auth/verify-email/resend
POST   /api/auth/login
POST   /api/auth/refresh
POST   /api/auth/logout
POST   /api/auth/forgot-password
POST   /api/auth/reset-password
GET    /.well-known/jwks.json
```

All return `Content-Type: application/json`. Error body shape: `{"error":"<code>","message":"<human>"}`.

## 3. Email normalization

```go
func NormalizeEmail(raw string) (string, error) {
    s := strings.TrimSpace(raw)
    s = norm.NFKC.String(s)
    s = strings.ToLower(s)
    if len(s) < 3 || len(s) > 254 { return "", ErrInvalidEmail }
    // RFC 5322 lite via net/mail.
    addr, err := mail.ParseAddress(s)
    if err != nil { return "", ErrInvalidEmail }
    return addr.Address, nil
}
```

Plus-tagged addresses (`user+x@x.com`) are kept distinct from `user@x.com` — by design.

## 4. Password validation

- Minimum 10 chars. Maximum 256 chars.
- Reject if matches a leaked-password Bloom filter (top-1000) bundled at build time at `cloud/internal/auth/leaked_top1k.bloom`.
- No max complexity rules; argon2 handles entropy.

## 5. Register flow

```go
// POST /api/auth/register
type registerReq struct {
    Email       string `json:"email"`
    Password    string `json:"password"`
    DisplayName string `json:"display_name"`
    Locale      string `json:"locale"`
    AcceptTOS   bool   `json:"accept_tos"`
}

func register(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req registerReq
        if err := decodeJSON(r, &req, 4<<10); err != nil { problem(w, 400, "bad_request", ""); return }
        if !req.AcceptTOS { problem(w, 400, "tos_required", ""); return }
        email, err := NormalizeEmail(req.Email)
        if err != nil { problem(w, 400, "invalid_email", ""); return }
        if err := ValidateDisplayName(req.DisplayName); err != nil { problem(w, 400, err.Code(), ""); return }
        if err := s.PolicyCheckPassword(req.Password); err != nil { problem(w, 400, err.Code(), ""); return }
        hash, err := argon2id.CreateHash(req.Password, argon2id.DefaultParams)  // m=64MB t=3 p=1
        if err != nil { problem(w, 500, "internal", ""); return }
        userID, err := s.repo.CreateUser(r.Context(), email, req.DisplayName, hash, req.Locale, "v1")
        switch {
        case errors.Is(err, ErrEmailTaken):
            // Email-enumeration defense: behave indistinguishably from happy path.
            s.email.EnqueueGenericExisting(r.Context(), email, req.Locale)
            writeJSON(w, 202, map[string]string{"status":"pending_verification"})
            return
        case err != nil:
            problem(w, 500, "internal", ""); return
        }
        token := s.tokens.MintVerify(userID, 24*time.Hour)
        s.email.EnqueueVerify(r.Context(), email, token, req.Locale)
        s.audit(r.Context(), "auth.register", userID.String())
        writeJSON(w, 202, map[string]string{"status":"pending_verification"})
    }
}
```

The branch on `ErrEmailTaken` returns the *same* shape so an attacker cannot tell whether the address was already registered (we still send the existing user a "did you mean to log in?" email).

## 6. Verify flow

```go
// POST /api/auth/verify-email  body={"token":"..."}
func verifyEmail(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req struct{ Token string `json:"token"` }
        if err := decodeJSON(r, &req, 2<<10); err != nil { problem(w, 400, "bad_request", ""); return }
        userID, err := s.tokens.VerifyVerify(req.Token)  // checks HMAC + exp
        switch {
        case errors.Is(err, ErrExpired):
            problem(w, 410, "verification_token_expired", "")
            return
        case err != nil:
            problem(w, 400, "invalid_token", "")
            return
        }
        already, err := s.repo.MarkEmailVerified(r.Context(), userID, s.clock.Now())
        if err != nil { problem(w, 500, "internal", ""); return }
        if !already { s.audit(r.Context(), "auth.email_verified", userID.String()) }
        writeJSON(w, 200, map[string]bool{"verified": true})
    }
}
```

Re-clicking a still-valid token = idempotent 200. Expired token = 410 with friendly user-facing copy.

## 7. Login + lockout

```go
func login(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // Constant-time path: every branch performs an argon2 verify
        // (either real or against a dummy hash) so wrong-password and
        // unknown-email take ~the same time.
        var req struct{ Email, Password string }
        if err := decodeJSON(r, &req, 4<<10); err != nil { problem(w, 400, "bad_request", ""); return }
        email, err := NormalizeEmail(req.Email)
        if err != nil { ttsAccept(w); return }

        if locked, until := s.rl.IsLocked(r.Context(), email); locked {
            w.Header().Set("Retry-After", strconv.Itoa(int(time.Until(until)/time.Second)))
            problem(w, 429, "too_many_attempts", "")
            return
        }

        user, err := s.repo.GetUserByEmail(r.Context(), email)
        // Always verify *something* to defeat email-enum.
        if err != nil || user == nil {
            _, _ = argon2id.ComparePasswordAndHash(req.Password, s.dummyHash)
            s.rl.RecordFail(r.Context(), email, clientIP(r))
            problem(w, 401, "invalid_credentials", "")
            return
        }
        ok, _ := argon2id.ComparePasswordAndHash(req.Password, user.PasswordHash)
        if !ok {
            s.rl.RecordFail(r.Context(), email, clientIP(r))
            problem(w, 401, "invalid_credentials", "")
            return
        }
        if user.EmailVerifiedAt == nil {
            problem(w, 403, "email_not_verified", "")
            return
        }
        if user.SuspendedAt != nil {
            problem(w, 403, "account_suspended", "")
            return
        }
        s.rl.Reset(r.Context(), email)
        tok, err := s.IssueSession(r.Context(), user, r)
        if err != nil { problem(w, 500, "internal", ""); return }
        s.audit(r.Context(), "auth.login", user.ID.String())
        writeJSON(w, 200, tok)
    }
}
```

`tts*` shapes (Timing-Safe) — every error path performs the dummy argon2 verify so wall-clock budget is the same.

## 8. Refresh + theft detection

```go
// POST /api/auth/refresh  Authorization: Bearer <refresh>
func refresh(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        raw := bearerToken(r)
        hash := sha256.Sum256([]byte(raw))
        sess, err := s.repo.LookupSession(r.Context(), hash[:])
        if err != nil || sess == nil { problem(w, 401, "invalid_refresh", ""); return }
        if sess.ExpiresAt.Before(s.clock.Now()) { problem(w, 401, "refresh_expired", ""); return }
        if sess.RevokedAt != nil {
            // REPLAY: an already-rotated token is being presented again.
            s.repo.RevokeAllUserSessions(r.Context(), sess.UserID)
            s.audit(r.Context(), "auth.refresh_replay", sess.UserID.String())
            s.abuse.Record(r.Context(), "refresh_token_replay", sess.UserID, 4)
            problem(w, 401, "session_invalidated", "")
            return
        }
        next, err := s.RotateSession(r.Context(), sess, r)
        if err != nil { problem(w, 500, "internal", ""); return }
        writeJSON(w, 200, next)
    }
}
```

Rotation = create new row with `rotated_from = old.id`, set old `revoked_at = now()` in same txn. Replay detection: presenting `old` after rotation tries to find it; `revoked_at` is non-NULL → cascade revoke.

## 9. JWKS

`cloud/internal/auth/jwks.go`:

```go
type JWKS struct {
    activeKID string
    keys      map[string]*Key   // kid → {pub, priv, alg, retiredAt}
}

func (j *JWKS) Active() *Key { return j.keys[j.activeKID] }
func (j *JWKS) Public() ([]byte, error) { ... json.Marshal({keys:[...]}) ... }
```

Rotation cron (every 90 days): generate new RSA-2048 keypair, insert into `jwt_keys` with new `kid=k<seq>`, set as active, mark previous `rotated_at`. Previous remains in JWKS (so live tokens verify) until `retired_at` = active.created + 1h + 5min skew. A nightly job clears retired entries.

`GET /.well-known/jwks.json` cached 5min at CF; returns active + previous keys.

## 10. Forgot/reset password

`forgot-password`: always 200 (no enumeration). If user exists, mint token, insert into `email_verifications` with `purpose='reset'`, send email.

`reset-password`: validates HMAC + DB row not used + not expired; on success:
1. Update `password_hash`, set `password_changed_at = now()`.
2. Revoke *all* sessions (`UPDATE sessions SET revoked_at = now() WHERE user_id = ? AND revoked_at IS NULL`).
3. Mark reset row `used_at`.
4. Send "your password was changed" notification email.
5. Audit.

## 11. Test plan

### 11.1 Unit

| Test | Pins |
|---|---|
| `TestNormalizeEmail` | Unicode NFKC + lower + trim; rejects malformed. |
| `TestArgon2Roundtrip` | Hash then verify. |
| `TestLeakedPasswordBloomReject` | `password123` rejected. |
| `TestVerifyTokenHMAC` | Bad signature rejected; tampered exp rejected. |
| `TestTimingSafeLogin` | 1000 wrong-pw + 1000 unknown-email; mean delta < 1ms. |
| `TestRefreshReplayCascades` | Rotated session presented again → all sessions revoked, abuse logged. |
| `TestJWKSRotationPreviousValid` | Token signed by old kid verifies for 1h after rotation. |

### 11.2 Integration (Postgres + Redis in CI)

| Test | Pins |
|---|---|
| `TestRegisterVerifyLoginHappy` | end-to-end; tokens issued. |
| `TestRegisterEmailEnumeration` | New vs taken email → identical 202 responses. |
| `TestVerifyExpiredToken` | clock+25h → 410. |
| `TestLoginLockoutAfter10Failures` | 11th = 429 with Retry-After: 1800. |
| `TestResendVerifyRateLimit` | 6th in 1h → 429. |
| `TestResetPasswordRevokesSessions` | After reset, old refresh = 401. |
| `TestConcurrentRegisterSameEmail` | Race; one 202, one 409 (mapped to 202 for enumeration; internal flag asserts). |
| `TestPlusTaggedEmails` | `a+x@x.com` distinct from `a@x.com`. |
| `TestUnicodeEmail` | `mö@x.com` stored canonicalized. |

### 11.3 Property

| Test | Pins |
|---|---|
| `PropertyNoCrashOnArbitraryBody` | Quickcheck random JSON; never 500. |
| `PropertyVerifyTokenSymmetry` | mint → verify always succeeds within TTL. |

## 12. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Tampered verify token | 400 `invalid_token`. | `TestVerifyTokenHMAC`. |
| Re-verify already-verified | 200 idempotent. | `TestVerifyIdempotent`. |
| Lockout never reveals existence | 429 fires whether or not the email exists. | `TestLockoutEnumeration`. |
| Concurrent register same email | One 202 + verification email; other 202 + "did you mean to log in?" email. | `TestConcurrentRegisterSameEmail`. |
| Reset token reuse | `used_at` is set; second use → 410. | `TestResetTokenSingleUse`. |
| JWT clock skew 60s | Accepted both directions. | `TestJWTSkew60s`. |
| Bounce flag | `email_bounced=true` users see banner; not auto-suspended. | `TestBounceFlag`. |
| TOS not accepted | 400 `tos_required`. | `TestTOSRequired`. |
| Display name 81 graphemes | 400 `display_name_too_long`. | Unit. |
| Password 257 chars | 400 `password_too_long`. | Unit. |
| OAuth-only user without password | `password_hash IS NULL`; login by password → 401 `invalid_credentials` (cannot reveal OAuth-only status). | `TestOAuthOnlyLoginByPassword`. |

## 13. Dependencies

- 25.1 (cloud bootstrap, DB pool, audit table scaffolding, request_id).
- Future: 25.3/25.4 share `users`, `sessions`, `jwt_keys`, `IssueSession`.

## 14. Acceptance checklist

- [ ] All 9 endpoints implemented.
- [ ] argon2id with stated params.
- [ ] JWKS endpoint serves active + previous keys with 5min cache header.
- [ ] Refresh rotation single-use; replay → cascade revoke + abuse event.
- [ ] Lockout 10/15min → 30min cooldown.
- [ ] Postmark adapter behind interface; templates in en + ar.
- [ ] Email enumeration defenses pass tests in §11.
- [ ] Migration 00020001 (identity) applies; reversible.
- [ ] Audit rows for register/verify/login/reset/logout.

# Implementation Plan — Story 25.4 Apple OAuth (Sign in with Apple)

> Companion to [story-25-04-apple-oauth.md](story-25-04-apple-oauth.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Reuses | All of 25.3's `Provider` scaffolding, state-cookie, redirect validator, merge flow, `cloud_identities` table. |
| Differences | (a) client-secret JWT we mint with ES256 + `.p8`, (b) profile data only on first sign-in (`name`/`email` only in *first* response), (c) form_post mode (Apple POSTs the callback), (d) optional native iOS path with `authorizationCode`+`identityToken`, (e) Apple `notifications` webhook for revocation/email-disabled. |
| Out of scope | App Store IAP (deferred to 25.13). macOS native bindings (web flow on desktop). |

## 1. Migration `00020003_apple_oauth.sql`

```sql
-- +goose Up
ALTER TABLE cloud_identities
    ADD COLUMN apple_relay_email BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE cloud_apple_notifications (
    id            BIGSERIAL PRIMARY KEY,
    received_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    event         TEXT NOT NULL,    -- email-disabled | email-enabled | consent-revoked | account-delete
    sub           TEXT NOT NULL,
    payload       JSONB NOT NULL,
    applied_at    TIMESTAMPTZ
);
CREATE INDEX cloud_apple_notifications_event_idx ON cloud_apple_notifications(event, applied_at);

-- +goose Down
DROP TABLE IF EXISTS cloud_apple_notifications;
ALTER TABLE cloud_identities DROP COLUMN IF EXISTS apple_relay_email;
```

## 2. Endpoints

```
GET    /api/auth/oauth/apple/start
POST   /api/auth/oauth/apple/callback      (Apple uses form_post)
POST   /api/auth/oauth/apple/native        (iOS native: ASAuthorization)
POST   /api/auth/oauth/apple/notifications (server-to-server webhook)
```

## 3. Client-secret JWT minter

```go
// cloud/internal/auth/oauth/apple_jwt.go
type AppleJWT struct {
    teamID, keyID, clientID string
    key   *ecdsa.PrivateKey
    cache atomic.Pointer[mintedJWT]
}

type mintedJWT struct{ token string; exp time.Time }

func (a *AppleJWT) Get() (string, error) {
    if m := a.cache.Load(); m != nil && time.Until(m.exp) > 30*time.Minute {
        return m.token, nil
    }
    now := time.Now()
    claims := jwt.MapClaims{
        "iss": a.teamID,
        "iat": now.Unix(),
        "exp": now.Add(6 * 30 * 24 * time.Hour).Unix(),  // 6-month cap (Apple max)
        "aud": "https://appleid.apple.com",
        "sub": a.clientID,
    }
    t := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
    t.Header["kid"] = a.keyID
    s, err := t.SignedString(a.key)
    if err != nil { return "", err }
    a.cache.Store(&mintedJWT{token: s, exp: now.Add(6*30*24*time.Hour)})
    return s, nil
}
```

`.p8` loaded from `[oauth.apple].key_path` at startup; reload on SIGUSR1 for rotation.

## 4. Token exchange & ID-token verification

`Apple` provider:

```go
func (a *Apple) ExchangeCode(ctx context.Context, code, codeVerifier string) (*Claims, error) {
    secret, err := a.jwt.Get()
    if err != nil { return nil, err }
    form := url.Values{
        "client_id":     {a.clientID},
        "client_secret": {secret},
        "code":          {code},
        "grant_type":    {"authorization_code"},
        "redirect_uri":  {a.redirectURI},
        "code_verifier": {codeVerifier},
    }
    resp, err := a.http.PostForm("https://appleid.apple.com/auth/token", form)
    if err != nil { return nil, err }
    defer resp.Body.Close()
    if resp.StatusCode >= 400 { return nil, ErrOAuthProvider }
    var t struct{ IDToken string `json:"id_token"`; RefreshToken string `json:"refresh_token"` }
    json.NewDecoder(resp.Body).Decode(&t)
    return a.parseIDToken(ctx, t.IDToken)
}

func (a *Apple) parseIDToken(ctx context.Context, raw string) (*Claims, error) {
    idt, err := a.verifier.Verify(ctx, raw)        // go-oidc verifier; aud=clientID, iss=appleid.apple.com
    if err != nil { return nil, err }
    var c struct{
        Sub           string  `json:"sub"`
        Email         string  `json:"email"`
        EmailVerified bool    `json:"email_verified,string"`   // Apple sends as string sometimes
        IsPrivateRelay bool   `json:"is_private_email,string"`
    }
    if err := idt.Claims(&c); err != nil { return nil, err }
    return &Claims{
        Sub: c.Sub, Email: c.Email, EmailVerified: c.EmailVerified,
        ProviderMeta: map[string]any{"relay_email": c.IsPrivateRelay},
    }, nil
}
```

We **don't store Apple refresh tokens**; we use the exchange only to verify the user.

## 5. First-call profile capture

Apple sends `user` JSON (with `name` + `email`) **only on the first sign-in's form_post**. The callback handler reads it:

```go
type appleFormPost struct {
    Code  string `form:"code"`
    State string `form:"state"`
    User  string `form:"user"`     // JSON; only present on first sign-in
    IDToken string `form:"id_token"`
}

// Decode and persist the `User` payload BEFORE attempting token exchange,
// because Apple WILL NOT include it on subsequent flows.
type appleUserPayload struct {
    Name struct{ FirstName, LastName string } `json:"name"`
    Email string `json:"email"`
}
```

If `User` is present and we end up creating a new user, set `display_name = First Last` (fallback "Apple User").

## 6. Native iOS endpoint

```go
// POST /api/auth/oauth/apple/native
// body: {"authorization_code": "...", "identity_token": "...", "user": {"name": {...}, "email": "..."}}
func nativeApple(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req nativeReq
        if err := decodeJSON(r, &req, 8<<10); err != nil { problem(w, 400, "bad_request", ""); return }
        // Identity token first (already signed; skip code-verifier round-trip).
        claims, err := s.apple.parseIDToken(r.Context(), req.IdentityToken)
        if err != nil { problem(w, 400, "invalid_identity_token", ""); return }
        // ResolveIdentity uses claims + first-call user payload.
        if claims.DisplayName == "" && req.User.Name.FirstName != "" {
            claims.DisplayName = strings.TrimSpace(req.User.Name.FirstName+" "+req.User.Name.LastName)
        }
        res, err := s.resolveAndIssue(r.Context(), "apple", claims, r)
        if err != nil { problem(w, mapOAuthErr(err)); return }
        writeJSON(w, 200, res.Tokens)
    }
}
```

## 7. Apple notifications webhook

```go
// POST /api/auth/oauth/apple/notifications
// Apple posts a signed JWT in the body (single field "payload").
func appleNotifications(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var body struct{ Payload string }
        if err := decodeJSON(r, &body, 64<<10); err != nil { problem(w, 400, "bad_request", ""); return }
        claims, err := s.apple.verifier.Verify(r.Context(), body.Payload)
        if err != nil { problem(w, 401, "invalid_signature", ""); return }
        var ev struct{ Type, Sub string; EventTimeMs int64 `json:"event_time"` }
        claims.Claims(&ev)
        s.repo.RecordAppleNotification(r.Context(), ev.Type, ev.Sub, claims)
        switch ev.Type {
        case "consent-revoked", "account-delete":
            uid, _ := s.repo.UserIDByIdentity(r.Context(), "apple", ev.Sub)
            if uid != uuid.Nil {
                s.repo.RevokeAllUserSessions(r.Context(), uid)
                s.repo.MarkIdentityRevoked(r.Context(), "apple", ev.Sub)
                s.audit(r.Context(), "auth.apple_revoked", uid.String())
            }
        case "email-disabled", "email-enabled":
            // Informational only.
        }
        w.WriteHeader(200)
    }
}
```

Idempotency: insert into `cloud_apple_notifications` with a unique key on `(event, sub, event_time_ms)`; second delivery is a no-op.

## 8. Test plan

### 8.1 Unit

| Test | Pins |
|---|---|
| `TestAppleJWTMintAndVerify` | Sign with test `.p8`; verify with test public key; matches structure. |
| `TestAppleJWTRefreshBeforeCap` | After 5.5 months, mint refreshes. |
| `TestParseIDTokenBadAud` | aud≠clientID → reject. |
| `TestPrivateRelayEmailCaptured` | `is_private_email=true` → identity row flagged. |

### 8.2 Integration

| Test | Pins |
|---|---|
| `TestFirstSignInCapturesName` | `user` form post present; display_name persisted. |
| `TestSecondSignInOmitsUserPayload` | Apple omits; found by `sub`; no overwrite. |
| `TestRelayEmailFlow` | Email = `xyz@privaterelay.appleid.com`; persisted unchanged. |
| `TestNativeIOSEndpoint` | POST `/native` returns tokens. |
| `TestNotificationsConsentRevoked` | Sessions revoked, identity marked. |
| `TestNotificationsIdempotent` | Same event twice → second is no-op. |
| `TestVerifiedEmailCollisionMerge` | Same as 25.3 Path C. |
| `TestUnverifiedEmailRefused` | Same as 25.3 Path D. |

## 9. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Apple omits `email_verified` | Treat absence as `false` → reject. | Unit. |
| `team_id` rotation | Config reload + JWT cache flush via SIGUSR1. | Doc. |
| `.p8` swap | Symlink swap; SIGUSR1 reload; cached JWT invalidated. | Operational runbook. |
| `consent-revoked` for unknown sub | Insert notification row; no user action. | `TestNotificationsUnknownSub`. |
| Account-delete notification | Same as consent-revoked + soft-delete `cloud_users`? No — Apple deletion just disconnects identity; user keeps account, can re-link. | Spec wording. |
| Relay email later forwards disabled | Bounce flag (25.2) covers user-side messaging. | Cross-story. |
| iOS native flow without identity token | 400 `bad_request`. | Unit. |
| Test against Apple staging | Apple has no public staging; we use a mock + recorded fixtures. | Test harness. |
| App Store privacy nutrition labels | Documented in `docs/distribution/app-store.md`. | Doc only. |

## 10. Dependencies

- 25.1 (config, audit).
- 25.2 (sessions, `cloud_users`, dummy hash for OAuth-only).
- 25.3 (`oauth.go` scaffolding, `cloud_identities`, merge flow, redirect validator).

## 11. Acceptance checklist

- [ ] Migration 00020003 applies; `apple_relay_email` flag + notifications table.
- [ ] ES256 JWT minter cached, ≤ 6-month TTL.
- [ ] `/start`, `/callback`, `/native`, `/notifications` endpoints implemented.
- [ ] First-call `user` payload captured.
- [ ] consent-revoked / account-delete → sessions revoked, identity revoked.
- [ ] Notifications idempotent.
- [ ] Tests in §8 pass.

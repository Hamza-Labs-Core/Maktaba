# Implementation Plan — Story 25.3 Google OAuth sign-in

> Companion to [story-25-03-google-oauth.md](story-25-03-google-oauth.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Library | `golang.org/x/oauth2` + `github.com/coreos/go-oidc/v3` for ID-token verification against Google's JWKS. |
| State storage | HMAC-signed cookie (`__Host-oauth_state`), 10-min TTL, `SameSite=Lax`, `HttpOnly`, `Secure`. Payload = `{nonce, code_verifier, next, provider, exp}` Base64-JSON, signed with `cloud_jwks` active key. No DB row. |
| PKCE | S256, 64-byte verifier. |
| Token persistence | We do **not** store Google access/refresh tokens long-term. We extract `sub` + `email` + `email_verified` from the ID token, write `cloud_identities`, and discard the rest. |
| Identity table | New: `cloud_identities(user_id, provider, provider_user_id, email_at_provider, linked_at, revoked_at)`. Unique on `(provider, provider_user_id)`. |
| Out of scope | Apple (25.4 will reuse the same OAuth scaffolding). |

## 1. Migration `00020002_oauth_identities.sql`

```sql
-- +goose Up
CREATE TABLE cloud_identities (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            UUID NOT NULL REFERENCES cloud_users(id) ON DELETE CASCADE,
    provider           TEXT NOT NULL,            -- 'google' | 'apple'
    provider_user_id   TEXT NOT NULL,            -- google 'sub' / apple 'sub'
    email_at_provider  CITEXT,
    linked_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at         TIMESTAMPTZ,
    last_seen_at       TIMESTAMPTZ
);
CREATE UNIQUE INDEX cloud_identities_provider_sub_uq
    ON cloud_identities(provider, provider_user_id);
CREATE INDEX cloud_identities_user_idx ON cloud_identities(user_id);

CREATE TABLE cloud_oauth_merge_tokens (
    token_hash   BYTEA PRIMARY KEY,
    user_id      UUID NOT NULL REFERENCES cloud_users(id) ON DELETE CASCADE,
    provider     TEXT NOT NULL,
    provider_user_id TEXT NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    used_at      TIMESTAMPTZ
);

-- +goose Down
DROP TABLE IF EXISTS cloud_oauth_merge_tokens, cloud_identities;
```

## 2. Endpoints

```
GET    /api/auth/oauth/google/start?next=/path     # 302 to Google
GET    /api/auth/oauth/google/callback             # Google calls us
POST   /api/auth/oauth/merge                       # confirms link with password re-auth
```

Generic provider-agnostic shape in `oauth.go`; `google.go` plugs in provider-specific config.

## 3. Provider-agnostic `oauth.go`

```go
type Provider interface {
    Name() string                            // "google" | "apple"
    AuthURL(state, nonce, codeChallenge string) string
    ExchangeCode(ctx context.Context, code, codeVerifier string) (*Claims, error)
}

type Claims struct {
    Sub           string
    Email         string
    EmailVerified bool
    DisplayName   string  // best-effort; not always present
    Locale        string  // best-effort
}

type Service struct {
    providers map[string]Provider
    repo      Repo
    sessions  SessionIssuer  // shared with 25.2
    jwks      *JWKS
}
```

`start` handler:
1. Validate `next` against allowlist (`https://app.maktaba.app/*`, `https://web.maktaba.app/*`, `maktaba://oauth/done`).
2. Generate `nonce` (16B), `code_verifier` (64B), `code_challenge = S256(code_verifier)`.
3. Pack `state = HMAC{provider, nonce, code_verifier, next, exp}`; set as `__Host-oauth_state` cookie (10-min TTL, secure attrs).
4. Redirect to `provider.AuthURL(state, nonce, code_challenge)`.

`callback` handler:
1. Read `__Host-oauth_state`; HMAC-verify; check `exp`. Mismatch/missing → `400 invalid_state` + abuse event `oauth_state_mismatch`.
2. Read `code` + `state` from query; require `state` in query equals cookie's `state` value (defense-in-depth).
3. `provider.ExchangeCode(ctx, code, state.code_verifier)` → `Claims`.
4. Find-or-create+link flow (§4).
5. Issue session (shared `SessionIssuer.Issue(user)` from 25.2).
6. Clear cookie; redirect to `state.next` with tokens delivered:
   - Web: token in HttpOnly cookie set by `/oauth/done`.
   - Mobile (`maktaba://oauth/done`): tokens in URL fragment so they don't appear in logs.

## 4. Find-or-create + collision logic

```go
func (s *Service) ResolveIdentity(ctx context.Context, p string, c *Claims, req *http.Request) (*ResolveResult, error) {
    // Path A: identity already linked.
    if u, _ := s.repo.UserByIdentity(ctx, p, c.Sub); u != nil {
        return &ResolveResult{User: u, Created: false}, nil
    }
    if !c.EmailVerified {
        return nil, ErrProviderEmailUnverified  // 400 google_email_not_verified
    }
    email, _ := NormalizeEmail(c.Email)
    existing, _ := s.repo.UserByEmail(ctx, email)
    switch {
    case existing == nil:
        // Path B: brand-new user.
        u, err := s.repo.CreateOAuthUser(ctx, email, c.DisplayName, c.Locale)
        if err != nil { return nil, err }
        s.repo.LinkIdentity(ctx, u.ID, p, c.Sub, email)
        return &ResolveResult{User: u, Created: true}, nil
    case existing.EmailVerifiedAt != nil:
        // Path C: email collision on a verified account → require explicit merge.
        merge := s.mintMergeToken(existing.ID, p, c.Sub)
        return &ResolveResult{NeedsMerge: true, MergeToken: merge, ExistingUserID: existing.ID}, nil
    default:
        // Path D: existing-but-unverified email — refuse (claim-jacking risk).
        return nil, ErrVerifyPasswordFirst  // 409 verify_password_first
    }
}
```

On Path C, the callback redirects to `https://app.maktaba.app/oauth/merge?token=<merge>` (or mobile equivalent). The merge page collects the user's *existing password* and POSTs to `/api/auth/oauth/merge`. Server:

1. Verify merge token (single-use, 10-min TTL).
2. Verify password against `cloud_users.password_hash` (argon2id). Wrong → 401.
3. Insert `cloud_identities` linking provider sub.
4. Mark merge token `used_at`.
5. Issue session.
6. Audit `auth.identity_linked`.

## 5. Redirect URI validation

```go
var allowedNextHosts = []string{
    "https://app.maktaba.app",
    "https://web.maktaba.app",
}

func validateNext(next string) error {
    if next == "" || next == "/" { return nil }
    if strings.HasPrefix(next, "maktaba://oauth/done") { return nil }
    u, err := url.Parse(next)
    if err != nil { return errBadRedirect }
    if u.Scheme != "https" { return errBadRedirect }
    origin := u.Scheme + "://" + u.Host
    for _, ok := range allowedNextHosts {
        if origin == ok { return nil }
    }
    return errBadRedirect
}
```

## 6. Google specifics

```go
type Google struct {
    clientID, clientSecret string
    oauthCfg *oauth2.Config        // endpoint = google.Endpoint
    verifier *oidc.IDTokenVerifier // backed by Google's JWKS
}

func (g *Google) Name() string { return "google" }

func (g *Google) AuthURL(state, nonce, challenge string) string {
    return g.oauthCfg.AuthCodeURL(state,
        oauth2.SetAuthURLParam("nonce", nonce),
        oauth2.SetAuthURLParam("code_challenge", challenge),
        oauth2.SetAuthURLParam("code_challenge_method", "S256"),
        oauth2.AccessTypeOnline,
    )
}

func (g *Google) ExchangeCode(ctx context.Context, code, verifier string) (*Claims, error) {
    tok, err := g.oauthCfg.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", verifier))
    if err != nil { return nil, err }
    raw, _ := tok.Extra("id_token").(string)
    idt, err := g.verifier.Verify(ctx, raw)
    if err != nil { return nil, err }
    var c struct{ Sub, Email string; EmailVerified bool `json:"email_verified"`; Name string; Locale string }
    if err := idt.Claims(&c); err != nil { return nil, err }
    return &Claims{ Sub: c.Sub, Email: c.Email, EmailVerified: c.EmailVerified, DisplayName: c.Name, Locale: c.Locale }, nil
}
```

`oidc.NewVerifier(...)` is configured with Google's issuer (`https://accounts.google.com`) and validates `aud == clientID`, `iss`, `exp` automatically.

## 7. Test plan

### 7.1 Unit

| Test | Pins |
|---|---|
| `TestStateCookieHMAC` | Tamper → reject. |
| `TestStateCookieExpiry` | exp passed → reject. |
| `TestValidateNextAllowedHosts` | `app.maktaba.app`, `web.maktaba.app`, `maktaba://oauth/done` pass; `evil.tld` rejected. |
| `TestPKCEChallengeS256` | Verifier-challenge correspondence verified. |
| `TestIDTokenVerifyBadAud` | aud≠clientID → reject. |

### 7.2 Integration (mock Google JWKS)

| Test | Pins |
|---|---|
| `TestGoogleSignInNewUser` | Path B: user + identity rows; session issued; 302 to `next`. |
| `TestGoogleSignInExistingIdentity` | Path A: existing sub → no new rows; session issued. |
| `TestGoogleSignInVerifiedEmailCollision` | Path C: redirect to `/oauth/merge?token=...`. |
| `TestGoogleSignInUnverifiedEmailCollision` | Path D: 409 `verify_password_first`. |
| `TestGoogleSignInEmailNotVerifiedAtGoogle` | 400 `google_email_not_verified`. |
| `TestStateMismatch` | Cookie one nonce, query another → 400 + abuse event. |
| `TestMergeFlowConfirmsWithPassword` | Wrong password → 401; correct → identity linked. |
| `TestMergeTokenSingleUse` | Reuse → 410. |

### 7.3 E2E / regression

| Test | Pins |
|---|---|
| `TestMobileDeepLink` | `next=maktaba://oauth/done` → tokens in URL fragment. |
| `TestClockSkew30s` | ID token `iat` within tolerance. |
| `TestNewNextBadRedirect` | `next=https://evil.tld/x` → 400 bad_redirect. |

## 8. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Google `sub` stable across email change | Identity rows keep `email_at_provider`; we never overwrite `cloud_users.email`. | `TestIdentityEmailChangePersists`. |
| Replay of authorization code | Google returns 400; we surface `oauth_provider_error`. | `TestCodeReplay`. |
| Account merge requires re-auth | Merge endpoint requires password verify, never one-click. | `TestMergeFlowConfirmsWithPassword`. |
| GDPR delete for OAuth-only user | Cascade deletes `cloud_identities`; audit keeps `provider=google, provider_user_id_hash=<sha256>` for 90d. | Story 25.5 plan, cross-tested here. |
| Locale | We use Google's `locale`; user can override at `PATCH /api/me`. | Implementation note. |
| Workspace `hd` claim | Allowed; no `hd` restriction in v1. | Spec. |
| `name` is fragile | Default to "User" if missing. | Implementation note. |
| Tokens in mobile callback in URL fragment | We omit access/refresh from query params; fragment only. | `TestMobileDeepLink`. |
| State cookie blocked by Safari ITP | `SameSite=Lax` ensures it's sent on top-level redirects (the OAuth callback case). | Doc. |

## 9. Dependencies

- 25.1 (router, slog, audit table).
- 25.2 (`cloud_users`, `cloud_sessions`, `SessionIssuer`, JWKS for HMAC-signing state).

## 10. Acceptance checklist

- [ ] Migration 00020002 applies; `cloud_identities` + `cloud_oauth_merge_tokens` exist.
- [ ] `/start` validates `next`, sets state cookie, redirects.
- [ ] `/callback` runs all 4 paths (new / existing / merge / refuse).
- [ ] PKCE S256 enforced.
- [ ] `/oauth/merge` requires existing-account password.
- [ ] Mobile deep-link delivers tokens via fragment.
- [ ] All §7 tests pass.

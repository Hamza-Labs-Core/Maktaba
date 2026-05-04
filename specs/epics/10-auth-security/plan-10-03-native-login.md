# Implementation Plan — Story 10.3 Native login (JWT access + refresh)

> Companion to [story-10-03-native-login.md](story-10-03-native-login.md).
> The cookie/JWT branch in the login handler is shared with
> [Story 10.2](plan-10-02-web-login.md). RS256 keys come from
> [Story 10.6](story-10-06-rs256-keys-jwks.md). `lib[]` resolution comes
> from [Story 10.13](story-10-13-permission-model.md). Refresh rotation
> is owned by [Story 10.4](story-10-04-token-refresh.md); this story
> only *issues* the first refresh token.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration file | `shared/db/migrations/0022_refresh_tokens.sql` (Postgres) and `0022_refresh_tokens.sqlite.sql` (SQLite). |
| JWT package | `api/internal/auth/jwt.go` — typed `Claims`, `Mint(...)`, `VerifyAccess(...)`. Uses `github.com/golang-jwt/jwt/v5` for signing/parsing. |
| Refresh-token package | `api/internal/auth/refresh.go` — `Issue`, `Hash`, `LookupByPlaintext` (constant-time scan replaced by lookup-by-id+hash, see §8). |
| Library ACL helper | `api/internal/auth/lib_acl.go` — `LibrariesForUser(ctx, userID) []uuid.UUID` (this story owns the *call site*; the underlying ACL table is owned by Story 10.13). |
| Bearer middleware | `api/internal/http/middleware/bearer.go` — JWT verify; injects user + claims into ctx. |
| Out of scope | Refresh rotation/reuse-detect (10.4), JWKS endpoint (10.6), signed-URL mint (10.8). |

## 1. Architecture diagram

```
POST /api/auth/login    (X-Maktaba-Client: native)
   ▼
┌────────────────────────────────────────────────────────────────┐
│ http/auth_login.go                                             │
│   (shared with Story 10.2; isNativeClient(r) branches here)    │
│        │                                                        │
│        ▼                                                        │
│ http/auth_native.go :: issueJWT(w, r, user)                     │
│   1. libs := lib_acl.LibrariesForUser(ctx, user.ID)             │
│   2. claims := buildClaims(user, libs, "api", access_ttl)       │
│   3. accessTok := jwt.Mint(claims, signer)                       │
│   4. refresh, plain := refresh.Issue(ctx, store, user.ID, ...)   │
│   5. write JSON {access_token, access_expires_in,                │
│                  refresh_token, refresh_expires_in, user}        │
└────────────────────────────────────────────────────────────────┘

Subsequent request: Authorization: Bearer <jwt>
   ▼
┌────────────────────────────────────────────────────────────────┐
│ http/middleware/bearer.go                                       │
│   - parse "Authorization: Bearer ..."                            │
│   - jwt.VerifyAccess(token, jwks) → claims                       │
│        check: iss=maktaba, aud=api, exp/nbf with leeway,         │
│               kid in JWKS, sig RS256                             │
│   - record claims.JTI for audit                                  │
│   - load user by claims.Sub; inject auth.WithUser(ctx)           │
│   - inject claims into ctx via auth.WithJWTClaims(ctx)           │
└────────────────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `shared/db/migrations/0022_refresh_tokens.sql` | Postgres table per [README.md](README.md). |
| `shared/db/migrations/0022_refresh_tokens.sqlite.sql` | SQLite variant. |
| `shared/db/queries/refresh_tokens.sql` | sqlc input — Insert/GetByID/RevokeFamily/ListActive. |
| `api/internal/auth/jwt.go` | `Claims`, `Mint`, `VerifyAccess`, `kid` resolution. |
| `api/internal/auth/refresh.go` | Plaintext mint (`crypto/rand`), argon2id hash, store insert, opaque-id encoding. |
| `api/internal/auth/lib_acl.go` | `LibrariesForUser` — wraps a sqlc query; returns `[]uuid.UUID`. |
| `api/internal/http/auth_native.go` | `issueJWT(w, r, user)` — called from the shared login handler. |
| `api/internal/http/middleware/bearer.go` | Bearer authn middleware. |
| `api/internal/auth/jwt_test.go` | Mint/Verify unit tests. |
| `api/internal/auth/refresh_test.go` | Hash/issue/lookup tests. |
| `api/internal/http/auth_native_test.go` | End-to-end login → JWT verify. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/config/config.go` | Add `Auth.AccessTTLSec` (default 900), `Auth.RefreshTTLSec` (default 2592000), `Auth.ClockSkewLeewaySec` (default 60), `Auth.RefreshTokenLen` (default 32). |
| `api/internal/http/auth_login.go` | The native branch from Story 10.2 calls `issueJWT`. |
| `api/internal/http/router.go` | Install `bearer.Middleware` *alongside* `session.Middleware`; both run; the request is authenticated by either. |
| `api/cmd/api/main.go` | Wire `RefreshStore`, `Signer`, `JWKS` into the router. |

### 2.3 Type definitions

```go
// api/internal/auth/jwt.go
package auth

import (
    "time"

    "github.com/golang-jwt/jwt/v5"
    "github.com/google/uuid"
)

// Claims is the canonical Maktaba JWT shape (README.md "Streaming JWT shape").
// All audience-specific subsets are produced by the same struct; minters
// fill different fields per use.
type Claims struct {
    Iss     string      `json:"iss"`             // "maktaba"
    Aud     string      `json:"aud"`             // "api" | "streaming" | "streaming-direct" | "streaming-static"
    Sub     string      `json:"sub"`             // user_id (api) | session_id (streaming) | etc.
    Iat     int64       `json:"iat"`
    Nbf     int64       `json:"nbf,omitempty"`
    Exp     int64       `json:"exp"`
    JTI     string      `json:"jti"`             // uuid v7
    KID     string      `json:"kid,omitempty"`   // mirrored in header; here for audit
    Usr     string      `json:"usr,omitempty"`   // user_id (when sub != user_id)
    Lib     []string    `json:"lib"`             // library_ids accessible at issue
    IsAdmin bool        `json:"is_admin,omitempty"`
}

// Standard jwt.Claims interface; we delegate to RegisteredClaims-like fields.
func (c Claims) GetExpirationTime() (*jwt.NumericDate, error) { return jwt.NewNumericDate(time.Unix(c.Exp, 0)), nil }
// ... GetIssuedAt, GetNotBefore, GetIssuer, GetSubject, GetAudience implementations.

type Signer interface {
    Sign(c Claims) (string, error)
    KID() string
}
```

```go
// api/internal/auth/refresh.go
type RefreshToken struct {
    ID         uuid.UUID
    UserID     uuid.UUID
    FamilyID   uuid.UUID
    IssuedAt   time.Time
    ExpiresAt  time.Time
    RevokedAt  *time.Time
    ReplacedBy *uuid.UUID
    ClientMeta map[string]any
}

type IssueResult struct {
    Row       RefreshToken
    Plaintext string  // returned ONCE at issue; never re-derivable
}

type RefreshStore interface {
    Issue(ctx context.Context, p IssueParams) (IssueResult, error)
    GetByID(ctx context.Context, id uuid.UUID) (RefreshToken, string /*hash*/, error)
    RotateOnce(ctx context.Context, oldID uuid.UUID, p IssueParams) (IssueResult, error) // Story 10.4
    RevokeFamily(ctx context.Context, familyID uuid.UUID) (int, error)                    // Story 10.4 / 10.5
}

type IssueParams struct {
    UserID     uuid.UUID
    FamilyID   uuid.UUID   // zero = mint a new family
    TTL        time.Duration
    ClientMeta map[string]any
}
```

### 2.4 Function signatures

```go
// api/internal/auth/jwt.go
func Mint(c Claims, s Signer) (string, error)
func VerifyAccess(token string, jwks JWKS, opts VerifyOpts) (Claims, error)

type VerifyOpts struct {
    ExpectedAud   string         // "api" for the bearer middleware
    LeewaySeconds int64
}
```

```go
// api/internal/auth/refresh.go
// Plaintext shape: "mkt_rt_v1.<base64url(id)>.<base64url(secret)>"
//   - id is the row's UUID v7 → constant-time DB lookup by id
//   - secret is 32 bytes from crypto/rand → argon2id-hashed in `hash` column
// Verifying a refresh token:
//   1. Parse the prefix and extract id + secret.
//   2. SELECT hash FROM refresh_tokens WHERE id = $1 (still O(1)).
//   3. argon2id Verify(secret, hash) — constant-time.
// This avoids the "scan all rows" trap of hashing-the-full-plaintext.

func Issue(ctx context.Context, store RefreshStore, p IssueParams) (IssueResult, error)
func ParsePlaintext(token string) (id uuid.UUID, secret []byte, error)
```

## 3. Database migration — Postgres

`shared/db/migrations/0022_refresh_tokens.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE refresh_tokens (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    hash         TEXT NOT NULL,           -- argon2id($argon2id$v=19$...) of the secret
    family_id    UUID NOT NULL,           -- shared across rotation chain (Story 10.4)
    issued_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    revoked_at   TIMESTAMPTZ,
    replaced_by  UUID REFERENCES refresh_tokens(id),
    client_meta  JSONB
);

CREATE INDEX refresh_tokens_user_active
    ON refresh_tokens (user_id, family_id) WHERE revoked_at IS NULL;

-- Reaper helper: drop fully-expired-and-revoked rows after 90 days.
CREATE INDEX refresh_tokens_reaper
    ON refresh_tokens (expires_at) WHERE revoked_at IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS refresh_tokens;
-- +goose StatementEnd
```

### 3.1 SQLite variant

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE refresh_tokens (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    hash         TEXT NOT NULL,
    family_id    TEXT NOT NULL,
    issued_at    TEXT NOT NULL DEFAULT (CURRENT_TIMESTAMP),
    expires_at   TEXT NOT NULL,
    revoked_at   TEXT,
    replaced_by  TEXT REFERENCES refresh_tokens(id),
    client_meta  TEXT     -- JSON
);

CREATE INDEX refresh_tokens_user_active
    ON refresh_tokens (user_id, family_id) WHERE revoked_at IS NULL;

CREATE INDEX refresh_tokens_reaper
    ON refresh_tokens (expires_at) WHERE revoked_at IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS refresh_tokens;
-- +goose StatementEnd
```

## 4. JWT details

| Field | Source / value |
|---|---|
| `iss` | Constant `"maktaba"`. |
| `aud` | `"api"` for access tokens. Streaming auds (10.7/10.8) reuse the same `Claims` shape. |
| `sub` | `user.ID.String()`. |
| `iat` | `time.Now().Unix()` at mint. |
| `exp` | `iat + access_ttl_sec` (default 900). |
| `nbf` | omitted by default; equal to `iat` when set. |
| `jti` | UUID v7 string (`uuid.NewV7().String()`). Recorded for audit on every verify in middleware. |
| `kid` | The active signing key's `kid` (Story 10.6 AC-1: SHA-256 of public-key DER, hex, first 16 chars). Set in JWS header *and* in the `Claims.KID` payload field for audit. |
| `usr` | Set only when `aud != "api"` (e.g., on streaming JWTs where `sub` is a session/video id). For `aud="api"`, omitted (sub == user). |
| `lib` | `[]string` of `library_id`s the user has read access to at issue time, resolved by `LibrariesForUser`. Empty array when the user has no library access; never `null`. |
| `is_admin` | `user.IsAdmin`. Only set for `aud="api"`. |

Signing uses RS256 via `golang-jwt/jwt/v5`:

```go
// api/internal/auth/jwt.go
func Mint(c Claims, s Signer) (string, error) {
    if c.JTI == "" {
        c.JTI = uuid.Must(uuid.NewV7()).String()
    }
    c.KID = s.KID()
    return s.Sign(c)
}
```

`Signer` is an interface so Story 10.6's keyring can swap the active
private key without touching this package:

```go
// api/internal/auth/keys.go (Story 10.6 owns the impl; this story consumes the iface)
type RSASigner struct {
    kid string
    pk  *rsa.PrivateKey
}

func (s *RSASigner) Sign(c Claims) (string, error) {
    tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
    tok.Header["kid"] = s.kid
    return tok.SignedString(s.pk)
}
func (s *RSASigner) KID() string { return s.kid }
```

`VerifyAccess` performs:

```go
func VerifyAccess(token string, jwks JWKS, opts VerifyOpts) (Claims, error) {
    parser := jwt.NewParser(
        jwt.WithValidMethods([]string{"RS256"}),
        jwt.WithLeeway(time.Duration(opts.LeewaySeconds) * time.Second),
        jwt.WithIssuer("maktaba"),
        jwt.WithAudience(opts.ExpectedAud),
        jwt.WithIssuedAt(),                  // require iat present
        jwt.WithExpirationRequired(),
    )
    var c Claims
    parsed, err := parser.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
        kid, _ := t.Header["kid"].(string)
        pk, ok := jwks.LookupRSA(kid)
        if !ok {
            return nil, fmt.Errorf("unknown kid %q", kid)
        }
        return pk, nil
    })
    if err != nil { return Claims{}, err }
    if !parsed.Valid  { return Claims{}, errors.New("token invalid") }
    return c, nil
}
```

The leeway absorbs the `clock_skew_leeway_sec` from config (story AC
edge-case: skewed device clocks).

## 5. Refresh-token details

The opaque-token format is engineered so the verify path is O(1) and
constant-time. A naïve "hash the whole plaintext, scan rows for a
match" approach is O(N) and forces a non-constant-time DB scan. We
instead embed the row's UUID v7 in the plaintext and hash only the
secret half:

```
plaintext = "mkt_rt_v1." + base64url(id_bytes) + "." + base64url(secret_bytes)
              ^prefix     ^16 bytes UUID         ^32 bytes random
```

```go
// api/internal/auth/refresh.go
const RefreshPrefix = "mkt_rt_v1."

func Issue(ctx context.Context, store RefreshStore, p IssueParams) (IssueResult, error) {
    secret := make([]byte, 32)
    if _, err := rand.Read(secret); err != nil {
        return IssueResult{}, err
    }
    id := uuid.Must(uuid.NewV7())
    hash, err := Hash(string(secret), DefaultArgon2)   // reuses Story 10.1's hasher
    if err != nil {
        return IssueResult{}, err
    }
    famID := p.FamilyID
    if famID == uuid.Nil {
        famID = uuid.Must(uuid.NewV7())
    }
    row, err := store.insert(ctx, insertRow{
        ID: id, UserID: p.UserID, Hash: hash, FamilyID: famID,
        ExpiresAt: time.Now().Add(p.TTL), ClientMeta: p.ClientMeta,
    })
    if err != nil { return IssueResult{}, err }
    plain := RefreshPrefix +
        base64.RawURLEncoding.EncodeToString(id[:]) + "." +
        base64.RawURLEncoding.EncodeToString(secret)
    return IssueResult{Row: row, Plaintext: plain}, nil
}

func ParsePlaintext(token string) (uuid.UUID, []byte, error) {
    if !strings.HasPrefix(token, RefreshPrefix) {
        return uuid.Nil, nil, ErrInvalidRefreshToken
    }
    parts := strings.SplitN(token[len(RefreshPrefix):], ".", 2)
    if len(parts) != 2 {
        return uuid.Nil, nil, ErrInvalidRefreshToken
    }
    idBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
    if err != nil || len(idBytes) != 16 {
        return uuid.Nil, nil, ErrInvalidRefreshToken
    }
    secret, err := base64.RawURLEncoding.DecodeString(parts[1])
    if err != nil || len(secret) != 32 {
        return uuid.Nil, nil, ErrInvalidRefreshToken
    }
    var id uuid.UUID
    copy(id[:], idBytes)
    return id, secret, nil
}

// Verify path (called from Story 10.4's refresh handler):
//   id, secret := ParsePlaintext(token)
//   row, hash  := store.GetByID(ctx, id)         // O(1) by PK
//   ok, _, _   := Verify(string(secret), hash)   // constant-time argon2id
```

The argon2id-of-secret approach has the property that a DB-only leak
yields rows whose hashes cannot be inverted to plaintexts. Combined
with the embedded id, the verify is both O(1) and constant-time.

## 6. Native login handler

```go
// api/internal/http/auth_native.go
package http

import (
    "encoding/json"
    "net/http"
    "time"

    "maktaba/api/internal/auth"
)

func issueJWT(w http.ResponseWriter, r *http.Request,
    user auth.User, signer auth.Signer, refresh auth.RefreshStore,
    libACL auth.LibACL, cfg auth.Config) {

    libs, err := libACL.LibrariesForUser(r.Context(), user.ID)
    if err != nil {
        problem(w, http.StatusInternalServerError, "internal", "")
        return
    }
    libStrings := make([]string, len(libs))
    for i, l := range libs {
        libStrings[i] = l.String()
    }

    now := time.Now().Unix()
    claims := auth.Claims{
        Iss: "maktaba", Aud: "api", Sub: user.ID.String(),
        Iat: now, Exp: now + int64(cfg.AccessTTLSec),
        IsAdmin: user.IsAdmin, Lib: libStrings,
    }
    accessTok, err := auth.Mint(claims, signer)
    if err != nil {
        problem(w, http.StatusInternalServerError, "signing-unavailable", "")
        return
    }

    issued, err := auth.Issue(r.Context(), refresh, auth.IssueParams{
        UserID: user.ID,
        TTL:    time.Duration(cfg.RefreshTTLSec) * time.Second,
        ClientMeta: map[string]any{
            "ua":     r.Header.Get("User-Agent"),
            "ip":     clientIPString(r),
            "device": r.Header.Get("X-Maktaba-Device"),
        },
    })
    if err != nil {
        problem(w, http.StatusInternalServerError, "internal", "")
        return
    }

    writeJSON(w, http.StatusOK, map[string]any{
        "access_token":       accessTok,
        "access_expires_in":  cfg.AccessTTLSec,
        "refresh_token":      issued.Plaintext,
        "refresh_expires_in": cfg.RefreshTTLSec,
        "user": map[string]any{
            "id": user.ID, "username": user.Username, "is_admin": user.IsAdmin,
        },
    })
}
```

## 7. Bearer middleware

```go
// api/internal/http/middleware/bearer.go
package middleware

import (
    "net/http"
    "strings"

    "maktaba/api/internal/auth"
)

func Bearer(jwks auth.JWKS, users auth.Store, cfg auth.Config, audit auth.AuditSink) func(next http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            h := r.Header.Get("Authorization")
            if !strings.HasPrefix(h, "Bearer ") {
                next.ServeHTTP(w, r)   // fall through to anonymous / cookie middleware
                return
            }
            tok := strings.TrimPrefix(h, "Bearer ")
            claims, err := auth.VerifyAccess(tok, jwks, auth.VerifyOpts{
                ExpectedAud:   "api",
                LeewaySeconds: int64(cfg.ClockSkewLeewaySec),
            })
            if err != nil {
                writeProblemUnauthorized(w, classifyJWTError(err))
                return
            }
            uid, err := uuid.Parse(claims.Sub)
            if err != nil {
                writeProblemUnauthorized(w, "bad-sub")
                return
            }
            user, err := users.GetByID(r.Context(), uid)
            if err != nil {
                writeProblemUnauthorized(w, "unknown-sub")
                return
            }
            audit.Record(r.Context(), auth.AuditAccessUsed{JTI: claims.JTI, UID: uid})

            ctx := auth.WithUser(r.Context(), user)
            ctx = auth.WithJWTClaims(ctx, claims)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

func classifyJWTError(err error) string {
    switch {
    case errors.Is(err, jwt.ErrTokenExpired):     return "token-expired"
    case errors.Is(err, jwt.ErrTokenNotValidYet): return "token-not-yet-valid"
    case errors.Is(err, jwt.ErrSignatureInvalid): return "bad-signature"
    case strings.Contains(err.Error(), "unknown kid"): return "unknown-kid"
    case strings.Contains(err.Error(), "audience"):    return "wrong-aud"
    default: return "invalid-token"
    }
}
```

The middleware sits *before* the cookie session middleware. If both
`Authorization: Bearer …` and `mkt_sess` are present, bearer wins (the
explicit native client header takes priority).

## 8. Test plan

### 8.1 JWT (`jwt_test.go`)

| Test | What it pins |
|---|---|
| `TestMintRoundTrip` | Mint with default Claims → VerifyAccess returns equal `Sub`, `Aud`, `IsAdmin`, `Lib`. |
| `TestMintSetsKIDInHeaderAndPayload` | Decode JOSE header → `kid` matches signer.KID(); same value in `Claims.KID`. |
| `TestMintAddsJTIIfMissing` | Empty `JTI` in input → emitted `JTI` is a parseable UUID v7. |
| `TestVerifyRejectsBadSignature` | Flip a byte of the signature → `jwt.ErrSignatureInvalid`. |
| `TestVerifyRejectsExpired` | `Exp = now - 60s`, no leeway → `jwt.ErrTokenExpired`. With `LeewaySeconds=120` → succeeds. |
| `TestVerifyRejectsWrongAudience` | Token with `aud="streaming"` against `ExpectedAud="api"` → audience error. |
| `TestVerifyRejectsUnknownKID` | JWKS without the token's kid → "unknown kid" error. |
| `TestVerifyRejectsHS256` | A token signed with HS256 (alg-confusion attack) → rejected by `WithValidMethods([]string{"RS256"})`. |
| `TestClaimsLibIsArrayNotNull` | A user with no libs → JSON `"lib":[]`, never `"lib":null`. |
| `TestClockSkewLeewayHonored` | `Iat = now + 30s`, `Exp = Iat + 900`, `LeewaySeconds=60` → verifies. |

### 8.2 Refresh (`refresh_test.go`)

| Test | What it pins |
|---|---|
| `TestIssueReturnsPlaintextOnce` | Issue returns plaintext; the same row read back has `hash` (different shape) and no plaintext. |
| `TestParsePlaintextRoundTrip` | `ParsePlaintext(plain)` → `id` matches row id; `secret` is 32 bytes. |
| `TestParsePlaintextRejectsTampered` | Flip one char of the secret → ParsePlaintext succeeds (it's just decoded); subsequent argon2 verify fails. |
| `TestParsePlaintextRejectsBadPrefix` | `"foo"`, `"mkt_rt_v0..."`, `"bearer ..."` → `ErrInvalidRefreshToken`. |
| `TestVerifyOpaqueIsConstantTime` | Two valid plaintexts whose secrets differ in the last byte vs the first byte: argon2 verify timing within 5 % of each other across 100 trials. |
| `TestRefreshHashStoresArgon2` | Inserted row's `hash` starts with `$argon2id$v=19$`. |
| `TestIssueExpiresAtMatchesTTL` | TTL=24h → `expires_at - issued_at` within 1 s of 24h. |
| `TestIssueAssignsNewFamilyByDefault` | Issue with `FamilyID=Nil` → row's `family_id` is a fresh UUID; two consecutive Issues use different families. |

### 8.3 Native login (`auth_native_test.go`)

| Test | What it pins |
|---|---|
| `TestLoginNativeReturnsAccessAndRefresh` | POST with `X-Maktaba-Client: native` and valid creds → response has `access_token`, `refresh_token`, no `Set-Cookie`. |
| `TestLoginNativeAccessTokenDecodesClaims` | Decode `access_token`; assert `Sub == user.ID`, `Aud == "api"`, `IsAdmin` matches, `Lib` non-empty when ACLs exist. |
| `TestLoginNativeAccessTokenLibEmpty` | A user with no library_acl rows → `lib: []`. |
| `TestLoginNativeRefreshIsHashedInDB` | Insert one user; native login; SELECT `hash` FROM refresh_tokens → starts with `$argon2id$v=19$`; the response's `refresh_token` is `mkt_rt_v1.<...>`. |
| `TestBearerMiddlewareAcceptsValidJWT` | Issue token; GET `/api/me` with `Authorization: Bearer …` → 200; ctx user populated. |
| `TestBearerMiddlewareRejectsExpired` | Mint with `Exp=now-1s`; → 401 `token-expired`. |
| `TestBearerMiddlewareRejectsWrongAud` | Mint with `Aud="streaming"`; → 401 `wrong-aud`. |
| `TestBearerMiddlewareRejectsTampered` | Flip a base64 char in the signature → 401 `bad-signature`. |
| `TestBearerMiddlewareRecordsAuditOnVerify` | A successful verify writes one `audit_log` row (Story 10.16) with the JTI in the payload. |

### 8.4 Cross-dialect parity

`auth_native_dialect_test.go` runs the integration flow against PG and
SQLite via the parametrized fixture.

## 9. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Native client misses `X-Maktaba-Client: native` | Cookie branch fires; the response has cookies, not JWT. The native client should self-correct by adding the header on retry. | `TestLoginMissingHeaderFallsToCookie` |
| Skewed device clock — `iat` slightly future | Leeway (default 60s) absorbs it. Beyond leeway: `token-not-yet-valid`. | `TestClockSkewLeewayHonored` |
| User's lib access changes after issue | The access token's `lib` is a snapshot; revocation lag = up to 15 min. Documented in Story 10.5; refresh re-snapshots in Story 10.4. | `TestLoginNativeAccessTokenLibEmpty` (control case) |
| User deleted while their JWT is still valid | Bearer middleware's `users.GetByID` returns `ErrNotFound` → 401 `unknown-sub`. | `TestBearerRejectsDeletedUser` |
| Refresh-token plaintext logged accidentally | The plaintext is returned only in the JSON response body; logger redaction (Story 10.14) strips `refresh_token` keys. | Story 10.14 plan |
| `lib` claim too large (e.g., 10K libraries) | The token compresses badly above ~30 libraries; for v1 we cap `lib` at 1000 entries and log a warning if exceeded. v2 will switch to a derived "all libraries" sentinel for the admin sentinel user. | `TestLibClaimSizeWarning` |
| Two parallel logins for the same user | Each issues an independent refresh family; both work; logout-all (Story 10.5) revokes both. | `TestParallelLoginsIndependent` |
| `kid` rotation between mint and verify | If verify happens before the verifier's JWKS poll picks up the new key, → `unknown-kid`. Story 10.6 AC-4's `LISTEN jwks_changed` minimizes this window. | Story 10.6 plan |
| Bearer token sent over plain HTTP in dev | Allowed when `MAKTABA_DEV=1`; logged at WARN. Production refuses (Story 10.15 AC-3). | Story 10.15 plan |
| User with `is_admin=true` but no library access | `IsAdmin=true, Lib=[]`. The signed-URL minter (Story 10.8 AC-5) treats admin specially in the lib-resolution code; the access token is honest about the snapshot. | `TestAdminWithEmptyLibClaim` |

## 10. Dependencies

| Dep | Version | Why |
|---|---|---|
| `github.com/golang-jwt/jwt/v5` | v5.x | Battle-tested JWT lib; supports `WithValidMethods`, `WithLeeway`, `WithAudience`. |
| `github.com/google/uuid` | already | UUID v7 (`NewV7`) for JTI and refresh ids. |
| `golang.org/x/crypto/argon2` | from Story 10.1 | Refresh-secret hashing. |
| `github.com/jackc/pgx/v5` | already | Postgres driver. |

## 11. Acceptance checklist

**Migration**
- [ ] `0022_refresh_tokens.sql` applies; both indexes present.
- [ ] CASCADE from `users(id)` deletes refresh tokens.

**JWT**
- [ ] Signed with RS256; verify rejects HS256 (alg-confusion guard).
- [ ] Header carries `kid`; payload carries the same `kid` for audit.
- [ ] Default Claims include `iss="maktaba"`, `aud="api"`, `exp = iat + 900`.
- [ ] `Lib` is always an array (never `null`).
- [ ] Leeway honors `clock_skew_leeway_sec` (default 60).

**Refresh**
- [ ] Plaintext format `mkt_rt_v1.<id>.<secret>`; secret is 32 random bytes.
- [ ] `hash` column stores argon2id of the secret only (not the prefix or id).
- [ ] Verify is O(1) (PK lookup + one argon2 verify).
- [ ] Plaintext is never re-derivable from the row (no logged copy, no DB column).

**Login flow**
- [ ] AC-1: native login returns `{access_token, access_expires_in, refresh_token, refresh_expires_in, user}` with no `Set-Cookie`.
- [ ] AC-2: claims have all documented fields including `lib[]`.
- [ ] AC-3: bearer middleware verifies signature + exp + aud; tampered → 401.
- [ ] AC-4: refresh-token plaintext returned only at issue; DB stores hash.

**Tests**
- [ ] All §8 tests pass on both dialects.
- [ ] Constant-time refresh verify pinned.
- [ ] `TestVerifyRejectsHS256` — alg-confusion guard.

**Docs**
- [ ] README.md ticks story 10.3.

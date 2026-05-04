# Implementation Plan — Story 23.1 Authentication

> Companion to [story-23-01-authentication.md](story-23-01-authentication.md).
> Story states *what* and *why*; this plan states *how*.
> Argon2id and the user table are already owned by
> [Epic 10 Story 10.1](../10-auth-security/plan-10-01-user-store.md);
> JWT issuance/refresh by [Epic 10 Stories 10.3 and 10.4](../10-auth-security/plan-10-03-native-login.md);
> JWKS publishing and the signing-key/JWKS document by
> [Epic 10 Story 10.6](../10-auth-security/plan-10-06-rs256-keys-jwks.md).
> This plan adds the security-hardening surface on top: rehash on
> login, single-user mode bypass, and the cross-service JWKS cache.
> Per [PLAN_REVIEW_18_24 §2](../../PLAN_REVIEW_18_24.md), the JWT
> private key is loaded from the `MAKTABA_JWT_PRIVATE_KEY_PEM` env var
> (architecture §11.5 canonical); there is no `signing_keys` DB table
> owned by this plan. JWKS document construction is owned by plan-10-06.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Argon2id rehash on login | Implemented in `api/internal/auth/login.go`; uses the `needsRehash` return value of `auth.Verify` from [Story 10.1](../10-auth-security/plan-10-01-user-store.md). |
| JWKS endpoint | Owned by [plan-10-06](../10-auth-security/plan-10-06-rs256-keys-jwks.md); not duplicated here. |
| Signing-key store | Owned by [plan-10-06](../10-auth-security/plan-10-06-rs256-keys-jwks.md): private key loaded from `MAKTABA_JWT_PRIVATE_KEY_PEM` env (arch §11.5); historic public keys (KIDs) tracked by plan-10-06 if/when rotation history is needed. |
| Streaming JWKS cache | `streaming/internal/auth/jwks_cache.go`. TTL ≤ 5 min, ETag-aware refresh, fail-closed. |
| Single-user mode | `MAKTABA_ADMIN_TOKEN` env-supplied opaque bearer; mapped to sentinel UUID `00000000-0000-0000-0000-000000000001` (Story 19.8). Single-user-mode short-circuit is enabled when `auth.multi_user=false` (default for fresh installs). |
| Out of scope | Login UI (Story 10.2/10.3); refresh-token rotation mechanics (Story 10.4); CSRF token implementation (covered by Story 10.2 — referenced here only for AC2); JWKS document/key store (plan-10-06). |

## 1. Architecture diagram

```
┌─────────┐   POST /api/auth/login   ┌───────────────────────────┐
│ Client  │ ────────────────────────►│ api/internal/auth/login.go│
└─────────┘                          │  Verify → if needsRehash, │
                                     │  Hash w/ default & UPDATE │
                                     └───────┬───────────────────┘
                                             │
                                             ▼
                                     ┌───────────────────────────┐
                                     │ api/internal/auth/jwt.go  │
                                     │  signs RS256 with key     │
                                     │  loaded from              │
                                     │  MAKTABA_JWT_PRIVATE_KEY_PEM
                                     └───────┬───────────────────┘
                                             │
                                  GET /.well-known/jwks.json (plan-10-06)
                                             │
                             ┌───────────────┼─────────────────┐
                             ▼               ▼                 ▼
                          web client    streaming JWKS    third-party
                                          cache (≤5 min)
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/auth/login.go` | Login handler with rehash-on-login. |
| `api/internal/auth/admintoken.go` | Single-user mode bearer-token middleware. |
| `streaming/internal/auth/jwks_cache.go` | Streaming-side cache; ETag/If-None-Match aware. |
| Tests — `*_test.go` per file plus `api/internal/auth/integration_test.go` for the cross-service JWKS cache. |

JWKS handler (`api/internal/auth/jwks.go`), key store (`api/internal/auth/keys.go`),
the `keys rotate`/`keys list` CLI, and any signing-key persistence are
owned by [plan-10-06](../10-auth-security/plan-10-06-rs256-keys-jwks.md).
This plan does not introduce a `signing_keys` migration.

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/http/router.go` | Mount `/api/.well-known/jwks.json`. Wire admin-token middleware ahead of the user table lookup. |
| `api/internal/config/config.go` | Add `[auth]` keys: `multi_user`, `key_rotation_period`, `key_overlap`, `clock_skew_seconds`. |
| `streaming/internal/config/config.go` | Add `[auth]` `jwks_url`, `jwks_cache_ttl`, `clock_skew_seconds`. |

### 2.3 Signing-key store (deferred to plan-10-06)

The signing-key store, JWKS document construction, and rotation
mechanics are owned by
[plan-10-06](../10-auth-security/plan-10-06-rs256-keys-jwks.md). Per
architecture §11.5, the JWT private key is loaded from
`MAKTABA_JWT_PRIVATE_KEY_PEM` (env-only — there is no DB-encrypted
storage). Public-key history (KIDs) for JWKS publication is also
plan-10-06's responsibility.

There is no `MAKTABA_KEY_ENCRYPTION_KEY` and no `signing_keys` table in
this plan.

### 2.4 Active-key picker (deferred to plan-10-06)

The `Active(ctx)` accessor and any rotation API are exposed by
plan-10-06. JWT minting (§2.5 below) calls into that package; this plan
does not duplicate the implementation.

### 2.5 JWT issuance

`api/internal/auth/jwt.go` (extends Story 10.3). The user UUID is
carried in the standard `sub` claim only (no `usr` mirror); audience is
emitted via `RegisteredClaims.Audience` only (no separate `aud` field).

```go
type AccessClaims struct {
    Lib       []string `json:"lib"`         // library UUIDs
    IsAdmin   bool     `json:"is_admin"`
    jwt.RegisteredClaims                    // Sub = user UUID; Aud = ["streaming"|"streaming-direct"|"streaming-static"]
}

func (m *Minter) MintAccess(ctx context.Context, u User, libs []string, aud string) (string, error) {
    k, err := m.keys.Active(ctx)  // implementation in plan-10-06
    if err != nil { return "", err }
    now := time.Now().UTC()
    tok := jwt.NewWithClaims(jwt.SigningMethodRS256, AccessClaims{
        Lib: libs, IsAdmin: u.IsAdmin,
        RegisteredClaims: jwt.RegisteredClaims{
            Subject:   u.ID.String(),
            Issuer:    m.iss,
            Audience:  jwt.ClaimStrings{aud},
            IssuedAt:  jwt.NewNumericDate(now),
            NotBefore: jwt.NewNumericDate(now.Add(-30 * time.Second)),  // EC1 skew tolerance
            ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
            ID:        uuid.NewString(),
        },
    })
    tok.Header["kid"] = k.Kid
    return tok.SignedString(k.Priv)
}
```

Streaming-side verifiers read the user UUID from `claims.Subject` (no
separate `usr` claim required).

The `kid` header is mandatory; verifiers reject tokens without it
(streaming returns 403 with `type: kid-missing`). The `aud` shape is
owned by Story 23.2; this plan only emits it.

### 2.6 Streaming JWKS cache

`streaming/internal/auth/jwks_cache.go`:

```go
type JWKSCache struct {
    url     string
    ttl     time.Duration
    skew    time.Duration
    http    *http.Client

    mu      sync.RWMutex
    keys    map[string]*rsa.PublicKey
    etag    string
    loaded  time.Time
}

func (c *JWKSCache) PublicKey(kid string) (*rsa.PublicKey, error) {
    c.mu.RLock()
    if k := c.keys[kid]; k != nil && time.Since(c.loaded) < c.ttl {
        c.mu.RUnlock()
        return k, nil
    }
    c.mu.RUnlock()

    if err := c.refresh(); err != nil {
        return nil, fmt.Errorf("jwks refresh: %w", err)
    }
    c.mu.RLock()
    defer c.mu.RUnlock()
    if k := c.keys[kid]; k != nil { return k, nil }
    return nil, ErrUnknownKid
}

func (c *JWKSCache) refresh() error {
    req, _ := http.NewRequest("GET", c.url, nil)
    if c.etag != "" {
        req.Header.Set("If-None-Match", c.etag)
    }
    resp, err := c.http.Do(req)
    if err != nil { return err }
    defer resp.Body.Close()
    if resp.StatusCode == http.StatusNotModified {
        c.mu.Lock(); c.loaded = time.Now(); c.mu.Unlock()
        return nil
    }
    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("jwks status %d", resp.StatusCode)
    }
    var doc jwks.Document
    if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil { return err }
    keys := map[string]*rsa.PublicKey{}
    for _, k := range doc.Keys {
        if pub, err := k.RSA(); err == nil { keys[k.Kid] = pub }
    }
    c.mu.Lock()
    c.keys = keys
    c.etag = resp.Header.Get("ETag")
    c.loaded = time.Now()
    c.mu.Unlock()
    return nil
}
```

Fail-closed: a token whose `kid` isn't in the cache forces a refresh
once; if still missing, the request is rejected with 403. Clock-skew
tolerance is wired into the JWT verifier:

```go
parser := jwt.NewParser(jwt.WithLeeway(c.skew))   // default 30 s
```

### 2.7 Rehash on login

`api/internal/auth/login.go`:

```go
func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
    body, err := decodeJSON[loginReq](r)
    if err != nil { problem(w, 400, "invalid-json", err.Error()); return }

    // Rate limiting + lockout owned by Story 23.6; we just call the handler.
    u, hash, err := h.users.GetByUsername(r.Context(), body.Username)
    if errors.Is(err, ErrNotFound) || hashEqualsSentinel(hash) {
        // Constant-time refusal; never leak account existence.
        _, _, _ = auth.Verify("garbage", DummyPHC)
        problem(w, 401, "invalid-credentials", "")
        return
    }
    ok, needsRehash, err := auth.Verify(body.Password, hash)
    if err != nil || !ok {
        problem(w, 401, "invalid-credentials", "")
        return
    }
    if needsRehash {
        // AC1 — opportunistic upgrade. Fail-soft: a rehash error doesn't
        // block the login; a metric and warning log fire instead.
        if newHash, err := auth.Hash(body.Password, auth.DefaultArgon2); err == nil {
            _ = h.users.UpdatePasswordHash(r.Context(), u.ID, newHash)
        } else {
            slog.Warn("rehash on login failed", "err", err, "user", u.ID)
            metrics.RehashErrors.Inc()
        }
    }
    // Mint cookie (web flow) or token pair (native flow).
    h.session.Issue(r.Context(), w, u)
}
```

### 2.8 Single-user mode bypass

`api/internal/auth/admintoken.go`. The bypass is enabled when
`auth.multi_user=false` (single-user mode); in multi-user mode it is a
no-op even if `MAKTABA_ADMIN_TOKEN` is set.

```go
const SentinelUserID = "00000000-0000-0000-0000-000000000001"

type AdminToken struct {
    expected []byte         // sha256 of MAKTABA_ADMIN_TOKEN
    enabled  bool           // true only when auth.multi_user=false AND env set
}

// NewAdminToken constructs the middleware. multiUser==true disables the
// bypass entirely (returns a no-op middleware); multiUser==false enables
// it iff env is non-empty.
func NewAdminToken(env string, multiUser bool) AdminToken {
    if multiUser || env == "" {
        return AdminToken{enabled: false}
    }
    sum := sha256.Sum256([]byte(env))
    return AdminToken{expected: sum[:], enabled: true}
}

func (a AdminToken) Middleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if !a.enabled { next.ServeHTTP(w, r); return }
        h := r.Header.Get("Authorization")
        if !strings.HasPrefix(h, "Bearer ") {
            next.ServeHTTP(w, r)  // fall through to normal auth path
            return
        }
        got := sha256.Sum256([]byte(strings.TrimPrefix(h, "Bearer ")))
        if subtle.ConstantTimeCompare(got[:], a.expected) == 1 {
            ctx := authcontext.With(r.Context(), authcontext.User{
                ID:      uuid.MustParse(SentinelUserID),
                IsAdmin: true,
            })
            next.ServeHTTP(w, r.WithContext(ctx))
            return
        }
        problem(w, 401, "invalid-admin-token", "")
    })
}
```

`auth.multi_user=true` disables the admin-token middleware entirely
(the bypass only fires in single-user mode where `multi_user=false`).
The config value is loaded at startup; rotation of the bypass token
requires a restart per EC3.

### 2.9 Key-rotation daemon (deferred to plan-10-06)

Auto-rotation, the rotation daemon, and the `keys rotate` CLI are
owned by [plan-10-06](../10-auth-security/plan-10-06-rs256-keys-jwks.md).
This plan does not duplicate the implementation.

## 3. Test plan

### 3.1 Hashing (TC1)

| Test | What it pins |
|---|---|
| `TestLoginRehashesOnParamBump` | Seed a user with `m=8192,t=1` hash; bump `DefaultArgon2`; login → DB row's `pw_hash` updated to current params; subsequent logins see `needsRehash=false`. |
| `TestRehashFailureSoftFails` | Mock the hasher to error; login still succeeds; metric increments. |
| `TestSentinelUserNeverLogsIn` | Attempt login with username `admin`, password `bootstrap-sentinel`; refused with 401. |

### 3.2 JWKS rollover (TC3)

JWKS-document and rollover behaviour is owned by plan-10-06 tests; the
streaming-cache tests below pin the **client** half of the contract.

| Test | What it pins |
|---|---|
| `TestStreamingValidatesAcrossOverlap` | Within the rotation overlap window (plan-10-06), streaming validates tokens signed by the previous key; outside overlap, rejects with 403 `type: unknown-kid`. |
| `TestStreamingJWKSCacheETag` | Two refreshes within TTL — second uses `If-None-Match`; the server returns 304; the cache stays warm. |
| `TestStreamingJWKSFailClosedOnRefresh` | Network failure during refresh; existing keys still serve until TTL; after TTL with no refresh, requests are rejected. |

### 3.3 Refresh tokens (TC2)

| Test | What it pins |
|---|---|
| Owned by [Story 10.4 plan](../10-auth-security/plan-10-04-token-refresh.md). | This story re-exercises rotation by integration test in `auth/integration_test.go` to confirm 23.1 didn't regress 10.4. |

### 3.4 Single-user mode

| Test | What it pins |
|---|---|
| `TestAdminTokenSetsSentinelUser` | `Authorization: Bearer <token>` with `MAKTABA_ADMIN_TOKEN=<token>` populates `auth.User.ID == 0…001`, `IsAdmin=true`. |
| `TestAdminTokenWrongRefused` | A malformed bearer is refused with 401; constant-time compare. |
| `TestMultiUserModeDisablesAdminToken` | `auth.multi_user=true` makes the admin-token middleware a no-op even with the env var set. |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| Clock skew (EC1) | JWT parser uses `WithLeeway(30s)`. Both api and streaming. Documented. | `TestClockSkewLeeway` |
| Lost refresh token (EC2) | User logs in fresh; the lost token's family is invalid; reuse on next attempt revokes the family per [Story 10.4](../10-auth-security/plan-10-04-token-refresh.md). | n/a (covered there) |
| Admin token leaked (EC3) | Operator bumps `MAKTABA_ADMIN_TOKEN` and restarts; documented in ops guide. There's no rotation API for the admin token by design — single-user mode is meant for the simple case. | Documented |
| Argon2id memory exhaustion attack | Story 10.1 already enforces `PasswordMaxLen=256`; the lockout in Story 23.6 caps attempts per IP. | n/a |
| JWKS publishing under high load | The handler returns the cached body with a `max-age=300, public` header; 304 served on `If-None-Match`. | `TestJWKSCacheable` |
| `kid` mismatch from a forgery | `subtle.ConstantTimeCompare` is unnecessary — the lookup is by exact string and a missing key produces 403 `type: unknown-kid`. | `TestForgedKidRejected` |
| Algorithm confusion attack | Verifier hard-codes `jwt.WithValidMethods([]string{"RS256"})`; HS256 forgeries refused. | `TestAlgConfusionRefused` |
| `MAKTABA_JWT_PRIVATE_KEY_PEM` missing or unparseable on startup | The API logs and refuses to serve in multi-user mode; in single-user mode, the path is bypassed entirely (no JWTs minted). Owned by plan-10-06; cross-checked here. | `TestPrivateKeyLoadFailureRefuses` |
| Streaming caches the JWKS after a rotation but before the API publishes | TTL ≤ 5 min bounds the staleness; the streaming verifier treats `unknown-kid` as a refresh trigger, retrying once. | `TestStreamingForcesRefreshOnUnknownKid` |
| JWKS endpoint exposed unauthenticated | Yes — it must be. The endpoint contains only public keys; CSRF protection is not required (no state change). | n/a |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `github.com/golang-jwt/jwt/v5` | latest | RS256 sign/verify. |
| `golang.org/x/crypto/argon2` | already | Hashing (Story 10.1). |
| `crypto/rsa`, `crypto/rand` | stdlib | RSA key handling (key generation lives in plan-10-06 tooling). |
| `crypto/subtle` | stdlib | Constant-time compares. |

## 6. Acceptance checklist

**Hashing**
- [ ] `auth.Verify` returns `needsRehash` when params drift; login uses it.
- [ ] Sentinel hash never authenticates.

**JWT**
- [ ] Tokens signed RS256 with `kid` header.
- [ ] Verifier hard-codes `RS256` to prevent algorithm confusion.
- [ ] Skew tolerance 30 s on both api and streaming.

**JWKS**
- [ ] `/api/.well-known/jwks.json` (built by plan-10-06) is consumed by the streaming cache.
- [ ] Streaming cache supports `If-None-Match` (304); TTL ≤ 5 min; fail-closed on refresh failures.

**Rotation** — owned by [plan-10-06](../10-auth-security/plan-10-06-rs256-keys-jwks.md). This plan only verifies that the streaming cache picks up rotated keys within TTL.

**Single-user mode**
- [ ] `MAKTABA_ADMIN_TOKEN` middleware honored when `auth.multi_user=false`.
- [ ] Sentinel UUID used as `auth.User.ID`.
- [ ] `auth.multi_user=true` disables the bypass even with the env var set.

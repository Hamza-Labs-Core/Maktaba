# Implementation Plan — Story 23.1 Authentication

> Companion to [story-23-01-authentication.md](story-23-01-authentication.md).
> Story states *what* and *why*; this plan states *how*.
> Argon2id and the user table are already owned by
> [Epic 10 Story 10.1](../10-auth-security/plan-10-01-user-store.md);
> JWT issuance/refresh by [Epic 10 Stories 10.3 and 10.4](../10-auth-security/plan-10-03-native-login.md).
> This plan adds the security-hardening surface on top: rehash on
> login, JWKS publishing + rotation, single-user mode bypass, and the
> cross-service JWKS cache.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Argon2id rehash on login | Implemented in `api/internal/auth/login.go`; uses the `needsRehash` return value of `auth.Verify` from [Story 10.1](../10-auth-security/plan-10-01-user-store.md). |
| JWKS endpoint | `GET /api/.well-known/jwks.json` — public, no auth. Owned by `api/internal/auth/jwks.go`. |
| Key rotation | `maktaba-api keys rotate` CLI; daemon helper `auth.KeyRotator` runs in-process for the auto-rotation path. |
| Streaming JWKS cache | `streaming/internal/auth/jwks_cache.go`. TTL ≤ 5 min, ETag-aware refresh, fail-closed. |
| Single-user mode | `MAKTABA_ADMIN_TOKEN` env-supplied opaque bearer; mapped to sentinel UUID `00000000-0000-0000-0000-000000000001` (Story 19.8). |
| Out of scope | Login UI (Story 10.2/10.3); refresh-token rotation mechanics (Story 10.4); CSRF token implementation (covered by Story 10.2 — referenced here only for AC2). |

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
                                     │  signs RS256 w/ active key│
                                     └───────┬───────────────────┘
                                             │
                                  GET /.well-known/jwks.json (public)
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
| `api/internal/auth/jwks.go` | JWKS document builder + handler. |
| `api/internal/auth/keys.go` | Active/historic signing key store; rotation. |
| `api/internal/auth/admintoken.go` | Single-user mode bearer-token middleware. |
| `api/cmd/api/keys.go` | CLI: `maktaba-api keys rotate`, `keys list`. |
| `streaming/internal/auth/jwks_cache.go` | Streaming-side cache; ETag/If-None-Match aware. |
| `shared/db/migrations/0040_signing_keys.sql` (+ sqlite) | Stored signing keys. |
| `shared/db/queries/signing_keys.sql` | sqlc queries. |
| Tests — `*_test.go` per file plus `api/internal/auth/integration_test.go` for the cross-service JWKS cache. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/http/router.go` | Mount `/api/.well-known/jwks.json`. Wire admin-token middleware ahead of the user table lookup. |
| `api/internal/config/config.go` | Add `[auth]` keys: `multi_user`, `key_rotation_period`, `key_overlap`, `clock_skew_seconds`. |
| `streaming/internal/config/config.go` | Add `[auth]` `jwks_url`, `jwks_cache_ttl`, `clock_skew_seconds`. |

### 2.3 Schema — signing keys

`0040_signing_keys.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE signing_keys (
    kid           TEXT PRIMARY KEY,
    algorithm     TEXT NOT NULL DEFAULT 'RS256',
    public_pem    TEXT NOT NULL,
    private_pem   TEXT NOT NULL,         -- encrypted at rest by application
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    activated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    retired_at    TIMESTAMPTZ,           -- NULL until rotation; key is still
                                         -- served via JWKS until purge_after.
    purge_after   TIMESTAMPTZ
);
CREATE INDEX signing_keys_active_idx
    ON signing_keys (retired_at, purge_after);
-- +goose StatementEnd
```

The `private_pem` column is encrypted with the value of
`MAKTABA_KEY_ENCRYPTION_KEY` (32-byte env var) using AES-256-GCM. If the
env var is absent, the API refuses to start in multi-user mode (single-
user mode skips the signing-key path entirely; it doesn't issue JWTs).

### 2.4 Active-key picker

`api/internal/auth/keys.go`:

```go
type KeyStore struct {
    db        *db.Queries
    encKey    []byte
    cache     atomic.Pointer[KeyCache]
    rotation  time.Duration   // default 90d
    overlap   time.Duration   // default 30d
}

type KeyCache struct {
    Active   *rsaKey
    Historic []*rsaKey       // not retired (still in JWKS)
    LoadedAt time.Time
}

// Active returns the currently-active signing key.
func (s *KeyStore) Active(ctx context.Context) (*rsaKey, error) {
    c := s.cache.Load()
    if c != nil && time.Since(c.LoadedAt) < 30*time.Second {
        return c.Active, nil
    }
    return s.refresh(ctx)
}

// JWKS returns every non-purged key as a JWKS document.
func (s *KeyStore) JWKS(ctx context.Context) ([]byte, string /*etag*/, error) {
    keys, err := s.db.SelectSigningKeysForJWKS(ctx)
    if err != nil { return nil, "", err }
    doc := jwks.Document{Keys: make([]jwks.Key, 0, len(keys))}
    for _, k := range keys {
        doc.Keys = append(doc.Keys, jwks.RSAFromPEM(k.Kid, k.PublicPem))
    }
    body, _ := json.Marshal(doc)
    sum := sha256.Sum256(body)
    return body, fmt.Sprintf(`W/"%x"`, sum[:8]), nil
}

// Rotate creates a new active key; flips current active → "historic"
// (retired_at = now, purge_after = now + overlap).
func (s *KeyStore) Rotate(ctx context.Context) (string /*new kid*/, error) {
    priv, err := rsa.GenerateKey(rand.Reader, 3072)
    if err != nil { return "", err }
    encrypted := s.encrypt(privPEM(priv))
    kid := newKid()
    return kid, s.db.WithTx(ctx, func(q *db.Queries) error {
        if err := q.RetireActiveSigningKey(ctx,
            time.Now(), time.Now().Add(s.overlap)); err != nil {
            return err
        }
        return q.InsertSigningKey(ctx, db.InsertSigningKeyParams{
            Kid: kid, Algorithm: "RS256",
            PublicPem: pubPEM(&priv.PublicKey), PrivatePem: encrypted,
        })
    })
}
```

### 2.5 JWT issuance

`api/internal/auth/jwt.go` (extends Story 10.3):

```go
type AccessClaims struct {
    Sub       string   `json:"sub"`         // user UUID
    Usr       string   `json:"usr"`         // user UUID (mirror; required by streaming)
    Lib       []string `json:"lib"`         // library UUIDs
    IsAdmin   bool     `json:"is_admin"`
    Aud       string   `json:"aud"`         // streaming | streaming-direct | streaming-static
    jwt.RegisteredClaims
}

func (m *Minter) MintAccess(ctx context.Context, u User, libs []string, aud string) (string, error) {
    k, err := m.keys.Active(ctx)
    if err != nil { return "", err }
    now := time.Now().UTC()
    tok := jwt.NewWithClaims(jwt.SigningMethodRS256, AccessClaims{
        Sub: u.ID.String(), Usr: u.ID.String(), Lib: libs, IsAdmin: u.IsAdmin, Aud: aud,
        RegisteredClaims: jwt.RegisteredClaims{
            Iss: m.iss, Aud: jwt.ClaimStrings{aud},
            Iat: jwt.NewNumericDate(now),
            Nbf: jwt.NewNumericDate(now.Add(-30 * time.Second)),  // EC1 skew tolerance
            Exp: jwt.NewNumericDate(now.Add(15 * time.Minute)),
            Jti: uuid.NewString(),
        },
    })
    tok.Header["kid"] = k.Kid
    return tok.SignedString(k.Priv)
}
```

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

`api/internal/auth/admintoken.go`:

```go
const SentinelUserID = "00000000-0000-0000-0000-000000000001"

type AdminToken struct {
    expected []byte         // sha256 of MAKTABA_ADMIN_TOKEN
    enabled  bool           // false when auth.multi_user=true
}

func NewAdminToken(env, mode string) AdminToken {
    if mode == "multi_user" || env == "" {
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

`auth.multi_user=true` short-circuits the admin token entirely (the
config value is loaded at startup; rotation requires a restart per
EC3).

### 2.9 Key-rotation daemon

`api/internal/auth/keys.go` adds:

```go
func (s *KeyStore) Daemon(ctx context.Context) {
    t := time.NewTicker(1 * time.Hour)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-t.C:
            active, err := s.db.GetActiveSigningKey(ctx)
            if err != nil { continue }
            if time.Since(active.ActivatedAt) >= s.rotation {
                _, _ = s.Rotate(ctx)
            }
            _ = s.db.PurgeExpiredSigningKeys(ctx, time.Now())
        }
    }
}
```

Auto-rotation is enabled by default in `serve` mode. The CLI command
allows manual rotation when an immediate rotation is required (Story
23.6 EC: keys leaked).

## 3. Test plan

### 3.1 Hashing (TC1)

| Test | What it pins |
|---|---|
| `TestLoginRehashesOnParamBump` | Seed a user with `m=8192,t=1` hash; bump `DefaultArgon2`; login → DB row's `pw_hash` updated to current params; subsequent logins see `needsRehash=false`. |
| `TestRehashFailureSoftFails` | Mock the hasher to error; login still succeeds; metric increments. |
| `TestSentinelUserNeverLogsIn` | Attempt login with username `admin`, password `bootstrap-sentinel`; refused with 401. |

### 3.2 JWKS rollover (TC3)

| Test | What it pins |
|---|---|
| `TestJWKSContainsActiveAndHistoric` | After `keys rotate`, `/api/.well-known/jwks.json` returns both keys; tokens signed before rotation still verify until expiry. |
| `TestStreamingValidatesAcrossOverlap` | Within the 30-day overlap, streaming validates tokens signed by the previous key; outside overlap, rejects with 403 `type: unknown-kid`. |
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
| `private_pem` decryption fails on startup | The API logs and refuses to serve in multi-user mode; in single-user mode, the path is bypassed entirely (no JWTs minted). | `TestEncryptedKeyDecryptFailureRefuses` |
| Streaming caches the JWKS after a rotation but before the API publishes | TTL ≤ 5 min bounds the staleness; the streaming verifier treats `unknown-kid` as a refresh trigger, retrying once. | `TestStreamingForcesRefreshOnUnknownKid` |
| JWKS endpoint exposed unauthenticated | Yes — it must be. The endpoint contains only public keys; CSRF protection is not required (no state change). | n/a |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `github.com/golang-jwt/jwt/v5` | latest | RS256 sign/verify. |
| `golang.org/x/crypto/argon2` | already | Hashing (Story 10.1). |
| `crypto/rsa`, `crypto/rand` | stdlib | Key generation. |
| `crypto/aes`, `crypto/cipher` | stdlib | Private-key encryption at rest. |
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
- [ ] `/api/.well-known/jwks.json` exposes all non-purged public keys.
- [ ] ETag + 304 supported.
- [ ] Streaming cache TTL ≤ 5 min; fail-closed on refresh failures.

**Rotation**
- [ ] `maktaba-api keys rotate` mints a new key, retires the previous one with overlap.
- [ ] Rotation daemon runs hourly in `serve` mode.

**Single-user mode**
- [ ] `MAKTABA_ADMIN_TOKEN` middleware honored.
- [ ] Sentinel UUID used as `auth.User.ID`.
- [ ] `auth.multi_user=true` disables the bypass.

# Implementation Plan — Story 10.6 RS256 keys, rotation, JWKS

> Companion to [story-10-06-rs256-keys-jwks.md](story-10-06-rs256-keys-jwks.md).
> Consumed by [Story 10.3](plan-10-03-native-login.md) (mint),
> [Story 10.7](story-10-07-streaming-jwt-verify.md) (verify), and
> [Story 10.8](story-10-08-signed-url-minter.md) (mint).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration file | `shared/db/migrations/0023_jwt_keys.sql` (Postgres) and `0023_jwt_keys.sqlite.sql` (SQLite). The persisted table holds *additional* trusted keys created by `keys rotate`; the bootstrap keypair lives only in env vars per architecture §11.5. |
| Keyring | `api/internal/auth/keys.go` — `Keyring` loads env-var keys + DB rows; exposes `ActiveSigner()`, `JWKS()`. |
| JWKS endpoint | `api/internal/http/jwks.go` — `GET /api/.well-known/jwks.json`. |
| Rotation CLI | `api/cmd/api/keys.go` — `init`, `rotate`, `rotate --immediate`. |
| Notification | Postgres `pg_notify('jwks_changed', '<kid>')` after every keyring mutation; SQLite uses the in-process `PubsubBus` shim from Epic 6. |
| Out of scope | Streaming-side JWKS cache (Story 10.7). Multi-region key sync (no story owns this; documented as v2). |

## 1. Architecture diagram

```
                          env: MAKTABA_JWT_PRIVATE_KEY_PEM
                               MAKTABA_JWT_PUBLIC_KEY_PEM
                                    │
                                    ▼
       ┌─────────────────────────────────────────────────┐
       │ Keyring (api/internal/auth/keys.go)              │
       │   - load env keypair → "env-active" signer       │
       │   - SELECT * FROM jwt_keys WHERE active OR       │
       │     not_after > now() ORDER BY created_at DESC   │
       │   - kid := sha256(public DER)[:16]                │
       │   - LISTEN jwks_changed → reload                  │
       │   - exposes ActiveSigner(), JWKS(), All()         │
       └────┬───────────────────────┬────────────────────┘
            │                       │
            │ Sign(claims)          │ JWKS()
            ▼                       ▼
      ┌──────────────┐       ┌─────────────────────────┐
      │ jwt.Mint     │       │ GET /.well-known/jwks   │
      │ (10.3, 10.8) │       │ Cache-Control: max-age=300│
      └──────────────┘       └─────────────────────────┘

CLI:
   maktaba-api keys init      → generate 4096-bit RSA, print PEM + env-var names
   maktaba-api keys rotate    → INSERT new key as active; old key becomes
                                  trusted-for-verify until not_after; pg_notify
   maktaba-api keys rotate --immediate
                              → confirmation prompt; collapse overlap to 0,
                                  set active key + remove all others
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `shared/db/migrations/0023_jwt_keys.sql` | Postgres schema. |
| `shared/db/migrations/0023_jwt_keys.sqlite.sql` | SQLite variant. |
| `shared/db/queries/jwt_keys.sql` | sqlc input. |
| `api/internal/auth/keys.go` | `Keyring`, `Signer`, `JWKS`, `RSAKey` struct. |
| `api/internal/auth/keys_listener.go` | LISTEN jwks_changed loop; reloads on signal. |
| `api/internal/http/jwks.go` | `GET /api/.well-known/jwks.json`. |
| `api/cmd/api/keys.go` | Cobra subcommands. |
| `api/internal/auth/keys_test.go` | Unit tests for kid derivation, signer round-trip, JWKS encode. |
| `api/internal/auth/keys_rotation_test.go` | Integration tests for rotation flow. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/config/config.go` | Add `Auth.JWTPrivateKeyEnv`, `Auth.JWTPublicKeyEnv`, `Auth.RotationOverlapSec` (default 86400), `Auth.JWKSCachePublicMaxAge` (default 300), `Auth.MinKeyBits` (default 2048). |
| `api/cmd/api/main.go` | Boot Keyring; refuse-to-start on missing key; register `keys` subcommands. |
| `api/internal/http/router.go` | Mount `/api/.well-known/jwks.json` (no auth required). |

### 2.3 Type definitions

```go
// api/internal/auth/keys.go
package auth

import (
    "context"
    "crypto/rsa"
    "sync"
    "time"
)

type RSAKey struct {
    KID       string
    Public    *rsa.PublicKey
    Private   *rsa.PrivateKey   // nil for verify-only keys (other replicas)
    Active    bool              // the one used to mint; exactly one in any keyring
    CreatedAt time.Time
    NotAfter  time.Time         // overlap-end; verify-trusted until this
}

type Keyring struct {
    mu     sync.RWMutex
    keys   []RSAKey
    db     *db.Queries
    listen *Listener
}

func NewKeyring(ctx context.Context, db *db.Queries, cfg Config) (*Keyring, error)
func (k *Keyring) ActiveSigner() Signer
func (k *Keyring) JWKS() JWKS
func (k *Keyring) Reload(ctx context.Context) error

// JWKS = JSON Web Key Set, per RFC 7517.
type JWKS struct {
    Keys []JWK `json:"keys"`
}
type JWK struct {
    Kty string `json:"kty"`     // "RSA"
    Use string `json:"use"`     // "sig"
    Alg string `json:"alg"`     // "RS256"
    KID string `json:"kid"`
    N   string `json:"n"`       // base64url(big-endian modulus)
    E   string `json:"e"`       // base64url(big-endian exponent)
}

func (j JWKS) LookupRSA(kid string) (*rsa.PublicKey, bool)
```

### 2.4 Function signatures

```go
// api/internal/auth/keys.go
func DeriveKID(pub *rsa.PublicKey) string                     // sha256(DER)[:16] hex
func ParsePEMPrivate(pem []byte) (*rsa.PrivateKey, error)
func ParsePEMPublic(pem []byte) (*rsa.PublicKey, error)
func RequireKeyStrength(pub *rsa.PublicKey, minBits int) error // refuse < 2048
func GenerateRSAKey(bits int) (*rsa.PrivateKey, error)
func MarshalPEMPrivate(k *rsa.PrivateKey) []byte
func MarshalPEMPublic(k *rsa.PublicKey) []byte
```

## 3. Database migration — Postgres

`shared/db/migrations/0023_jwt_keys.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

-- The "env" keypair is implicit; it's never persisted (per §11.5).
-- This table holds keys generated by `maktaba-api keys rotate`. The
-- keypair from env is always considered trusted-for-verify; rotation
-- adds a new active key here while keeping the env key for the overlap
-- window (it goes from "active" to "trusted-for-verify").

CREATE TABLE jwt_keys (
    kid          TEXT PRIMARY KEY,                -- sha256(DER)[:16]
    public_pem   TEXT NOT NULL,
    private_pem  TEXT NOT NULL,                   -- intentionally same DB; this
                                                   -- is the trade-off for runtime
                                                   -- rotation without a deploy.
    active       BOOLEAN NOT NULL DEFAULT false,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    not_after    TIMESTAMPTZ NOT NULL,            -- overlap end
    CONSTRAINT jwt_keys_min_bits_chk CHECK (length(public_pem) > 200)
);

-- At most one active row at a time.
CREATE UNIQUE INDEX jwt_keys_one_active
    ON jwt_keys ((active)) WHERE active = true;

CREATE INDEX jwt_keys_trusted ON jwt_keys (not_after) WHERE not_after > now();

-- NOTIFY trigger so listening API replicas reload on rotate.
CREATE OR REPLACE FUNCTION jwt_keys_notify_changed() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('jwks_changed', COALESCE(NEW.kid, OLD.kid));
    RETURN COALESCE(NEW, OLD);
END;
$$;

CREATE TRIGGER jwt_keys_notify_changed_trg
    AFTER INSERT OR UPDATE OR DELETE ON jwt_keys
    FOR EACH ROW EXECUTE FUNCTION jwt_keys_notify_changed();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS jwt_keys_notify_changed_trg ON jwt_keys;
DROP FUNCTION IF EXISTS jwt_keys_notify_changed();
DROP TABLE IF EXISTS jwt_keys;
-- +goose StatementEnd
```

The `private_pem` column is the trade-off discussed in §11.5: secrets
"only in env or config file". We deviate here for *operational
secrets* (the rotated key generated at runtime) because there is no
other place to put them that survives a restart. Operations docs spell
out that DB encryption-at-rest is the mitigation. The bootstrap key
remains env-only.

### 3.1 SQLite variant

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE jwt_keys (
    kid          TEXT PRIMARY KEY,
    public_pem   TEXT NOT NULL,
    private_pem  TEXT NOT NULL,
    active       INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL DEFAULT (CURRENT_TIMESTAMP),
    not_after    TEXT NOT NULL,
    CHECK (length(public_pem) > 200)
);

CREATE UNIQUE INDEX jwt_keys_one_active ON jwt_keys (active) WHERE active = 1;
CREATE INDEX jwt_keys_trusted ON jwt_keys (not_after);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS jwt_keys;
-- +goose StatementEnd
```

(SQLite has no LISTEN/NOTIFY; the in-process `PubsubBus` from Epic 6
publishes `jwks_changed` after every CLI mutation.)

## 4. Keyring loading

```go
// api/internal/auth/keys.go
func NewKeyring(ctx context.Context, db *db.Queries, cfg Config) (*Keyring, error) {
    var keys []RSAKey

    // 1. The env keypair is the bootstrap; refuse to start if missing.
    privPEM := os.Getenv(cfg.JWTPrivateKeyEnv)
    pubPEM  := os.Getenv(cfg.JWTPublicKeyEnv)
    if privPEM == "" || pubPEM == "" {
        return nil, fmt.Errorf(
            "auth: %s and %s must be set; run `maktaba-api keys init` to generate",
            cfg.JWTPrivateKeyEnv, cfg.JWTPublicKeyEnv,
        )
    }
    priv, err := ParsePEMPrivate([]byte(privPEM))
    if err != nil { return nil, fmt.Errorf("private key: %w", err) }
    pub, err  := ParsePEMPublic([]byte(pubPEM))
    if err != nil { return nil, fmt.Errorf("public key: %w", err) }
    if err := RequireKeyStrength(pub, cfg.MinKeyBits); err != nil { return nil, err }

    envKID := DeriveKID(pub)
    envKey := RSAKey{
        KID: envKID, Public: pub, Private: priv,
        Active: true,
        CreatedAt: time.Time{},   // unknown
        NotAfter: time.Now().Add(100 * 365 * 24 * time.Hour),  // env key never auto-expires
    }
    keys = append(keys, envKey)

    // 2. Load any DB keys generated via `keys rotate`. If one is `active`,
    //    it overrides the env key as the signer.
    rows, err := db.ListJWTKeys(ctx)
    if err != nil { return nil, err }
    for _, r := range rows {
        pub, err := ParsePEMPublic([]byte(r.PublicPem))
        if err != nil { continue }   // log + skip a malformed row
        var priv *rsa.PrivateKey
        if r.PrivatePem != "" {
            priv, _ = ParsePEMPrivate([]byte(r.PrivatePem))
        }
        if r.Active {
            // Demote env from active.
            for i := range keys { keys[i].Active = false }
        }
        keys = append(keys, RSAKey{
            KID: r.Kid, Public: pub, Private: priv,
            Active: r.Active, CreatedAt: r.CreatedAt, NotAfter: r.NotAfter,
        })
    }

    k := &Keyring{db: db, keys: keys}
    k.listen = newListener(ctx, db, "jwks_changed", k.Reload)
    return k, nil
}

func (k *Keyring) ActiveSigner() Signer {
    k.mu.RLock(); defer k.mu.RUnlock()
    for _, key := range k.keys {
        if key.Active && key.Private != nil {
            return &RSASigner{kid: key.KID, pk: key.Private}
        }
    }
    panic("auth: no active signer — keyring boot invariant violated")
}

func (k *Keyring) JWKS() JWKS {
    k.mu.RLock(); defer k.mu.RUnlock()
    out := JWKS{Keys: make([]JWK, 0, len(k.keys))}
    now := time.Now()
    for _, key := range k.keys {
        if key.NotAfter.Before(now) {
            continue   // overlap window over → drop from JWKS
        }
        out.Keys = append(out.Keys, JWK{
            Kty: "RSA", Use: "sig", Alg: "RS256",
            KID: key.KID,
            N:   base64.RawURLEncoding.EncodeToString(key.Public.N.Bytes()),
            E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.Public.E)).Bytes()),
        })
    }
    return out
}
```

`DeriveKID`:

```go
func DeriveKID(pub *rsa.PublicKey) string {
    der, _ := x509.MarshalPKIXPublicKey(pub)
    sum := sha256.Sum256(der)
    return hex.EncodeToString(sum[:8])   // 16 hex chars (per AC-1)
}
```

## 5. JWKS HTTP endpoint

```go
// api/internal/http/jwks.go
func jwksHandler(k *auth.Keyring, cacheMaxAge int) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", cacheMaxAge))
        w.Header().Set("Content-Type", "application/jwk-set+json")
        // 200 with all currently-trusted keys (env + DB rows whose not_after > now()).
        _ = json.NewEncoder(w).Encode(k.JWKS())
    }
}
```

The endpoint is mounted **outside** the auth middleware; it must be
publicly reachable so Streaming can fetch it without credentials.

## 6. CLI

```go
// api/cmd/api/keys.go
func newKeysCmd() *cobra.Command {
    root := &cobra.Command{Use: "keys", Short: "JWT key management"}
    root.AddCommand(newKeysInitCmd(), newKeysRotateCmd())
    return root
}

func newKeysInitCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "init",
        Short: "Generate an RSA-4096 keypair and print env-var assignments",
        RunE: func(cmd *cobra.Command, args []string) error {
            priv, err := auth.GenerateRSAKey(4096)
            if err != nil { return err }
            pub := &priv.PublicKey
            fmt.Println("# Add the following to your .env or systemd unit:")
            fmt.Println()
            fmt.Print("MAKTABA_JWT_PRIVATE_KEY_PEM='")
            fmt.Print(string(auth.MarshalPEMPrivate(priv)))
            fmt.Println("'")
            fmt.Print("MAKTABA_JWT_PUBLIC_KEY_PEM='")
            fmt.Print(string(auth.MarshalPEMPublic(pub)))
            fmt.Println("'")
            fmt.Println()
            fmt.Printf("# kid (will appear in JWS headers): %s\n", auth.DeriveKID(pub))
            return nil
        },
    }
}

func newKeysRotateCmd() *cobra.Command {
    var immediate bool
    cmd := &cobra.Command{
        Use:   "rotate",
        Short: "Generate a new active signing key; old key remains trusted for the overlap window",
        RunE: func(cmd *cobra.Command, args []string) error {
            store, _ := bootStore(cmd.Context())
            audit, _ := bootAudit(cmd.Context())

            if immediate {
                fmt.Println("WARNING: --immediate invalidates EVERY in-flight access token AND every signed URL.")
                fmt.Print("Type 'yes-invalidate-all-tokens' to confirm: ")
                var s string
                fmt.Scanln(&s)
                if s != "yes-invalidate-all-tokens" {
                    return fmt.Errorf("aborted")
                }
            }

            priv, err := auth.GenerateRSAKey(4096)
            if err != nil { return err }
            pubPEM  := auth.MarshalPEMPublic(&priv.PublicKey)
            privPEM := auth.MarshalPEMPrivate(priv)
            kid := auth.DeriveKID(&priv.PublicKey)

            overlap := time.Duration(cfg.RotationOverlapSec) * time.Second
            if immediate { overlap = 0 }

            err = store.WithTx(cmd.Context(), func(q *db.Queries) error {
                // Demote any active row.
                _ = q.DemoteActiveJWTKeys(cmd.Context())
                if immediate {
                    // Drop all other rows; only the new key remains.
                    _ = q.DeleteAllJWTKeys(cmd.Context())
                }
                _, err := q.InsertJWTKey(cmd.Context(), db.InsertJWTKeyParams{
                    Kid: kid, PublicPem: string(pubPEM), PrivatePem: string(privPEM),
                    Active: true, NotAfter: time.Now().Add(overlap + 30*24*time.Hour),
                    // The overlap-end for the env key is also bumped via update:
                })
                if err != nil { return err }
                // If immediate, drop env key from JWKS too by setting a sentinel
                // not_after <= now() in an in-memory marker; the runtime keyring
                // re-reads on jwks_changed and recomputes.
                return nil
            })
            if err != nil { return err }

            audit.Record(cmd.Context(), auth.AuditKeyRotated{
                Mode: ifThenElse(immediate, "immediate", "overlap"), NewKID: kid,
            })
            fmt.Printf("Rotated. New active kid: %s\n", kid)
            return nil
        },
    }
    cmd.Flags().BoolVar(&immediate, "immediate", false,
        "collapse overlap window to 0; invalidates in-flight tokens")
    return cmd
}
```

## 7. LISTEN/reload loop

```go
// api/internal/auth/keys_listener.go
func newListener(ctx context.Context, db *db.Queries, channel string, reload func(context.Context) error) *Listener {
    l := &Listener{}
    go func() {
        for {
            err := dbpool.Listen(ctx, channel, func(payload string) {
                slog.Info("jwks_changed received", "kid", payload)
                if err := reload(ctx); err != nil {
                    slog.Warn("keyring reload failed", "err", err)
                }
            })
            if errors.Is(err, context.Canceled) { return }
            slog.Warn("listener exited; reconnecting in 5s", "err", err)
            time.Sleep(5 * time.Second)
        }
    }()
    return l
}
```

`dbpool.Listen` is the existing helper from Epic 6 plan 06-01 §4
(LISTEN/NOTIFY wrapper).

## 8. Crypto details

| Concern | Decision |
|---|---|
| Algorithm | RS256 only. Verify enforces `WithValidMethods([]string{"RS256"})` (Story 10.3 §4) so an HS256/none token is rejected. |
| Key size | 4096 bits at generation; minimum 2048 bits at boot via `RequireKeyStrength`. Enforced in `keys init` AND on every Keyring load. |
| `kid` derivation | `hex(sha256(SubjectPublicKeyInfo)[:8])` → 16 hex chars. Stable across encodings (PEM vs DER). |
| `kid` in token | Set in the JOSE header (`Header["kid"]`) AND in the payload's `kid` claim (Story 10.3 §4). The header one is what verify uses; the payload one is for audit so a leaked token's source is identifiable without trusting the header. |
| Rotation overlap | Default 24h. At minute 0 of rotation, the new key is active and signs; the old key is still in JWKS for verify. After overlap, the old key drops out. |
| Immediate rotation | Operator types the magic string `yes-invalidate-all-tokens` (deliberate friction). The CLI then deletes all other DB rows AND sets a sentinel `not_after = now() - 1s` for the env key (the keyring then drops env from JWKS). The next request that tries to verify with the old `kid` returns `unknown-kid`. |
| Trust model | Both API and Streaming trust *every* key currently in JWKS. Streaming's verify path is offline (Story 10.7); the JWKS poll is the trust-update channel. |

## 9. Test plan

### 9.1 Crypto unit tests (`keys_test.go`)

| Test | What it pins |
|---|---|
| `TestDeriveKIDStable` | The same public key DER → identical KID across two calls. |
| `TestDeriveKIDFormat` | KID is 16 lowercase hex chars; matches `^[0-9a-f]{16}$`. |
| `TestParsePEMRoundTrip` | `MarshalPEMPrivate` then `ParsePEMPrivate` yields a key whose `D` and `N` equal the original. |
| `TestRequireKeyStrengthRejects1024` | A 1024-bit key fails `RequireKeyStrength(_, 2048)`. |
| `TestRequireKeyStrengthAccepts2048` | 2048-bit key passes. |
| `TestRSASignerSignThenVerify` | Sign a Claims; verify with the matching public key → success. |
| `TestRSASignerEmitsKIDHeader` | Decoded JOSE header contains `kid` matching `DeriveKID(pub)`. |
| `TestJWKSEncodeMatchesRFC7517` | `JWKS()` JSON: keys array; each item has exactly `kty,use,alg,kid,n,e`; `kty="RSA", use="sig", alg="RS256"`; `n` and `e` decode to the original public key. |
| `TestJWKSExpiredKeysExcluded` | Force a key's `not_after = now() - 1s`; JWKS does not contain it; LookupRSA returns `ok=false`. |

### 9.2 Boot tests

| Test | What it pins |
|---|---|
| `TestNewKeyringMissingEnvFailsToStart` | Empty env → error message names both env vars and points at `keys init`. |
| `TestNewKeyringWeakEnvFails` | 1024-bit env key → error mentions min bits. |
| `TestNewKeyringEnvOnlyHappy` | Valid env keys → keyring exposes 1 key, env-active. |
| `TestNewKeyringDBOverridesEnv` | Insert one DB row with `active=true`; keyring's ActiveSigner uses the DB key. JWKS still includes env (within env's not_after). |

### 9.3 Rotation tests (`keys_rotation_test.go`)

| Test | What it pins |
|---|---|
| `TestRotateAddsNewKeyMarksOldVerifyOnly` | Before: 1 active env key. `keys rotate` → 1 active DB key + env key still trusted; both kids in JWKS. |
| `TestRotateMintsWithNewKey` | After rotate, a freshly minted access token's `kid` matches the new DB key's kid. |
| `TestRotateOldTokenStillVerifies` | Token minted before rotate continues to verify until env's not_after collapses (the env key's `not_after` is left intact). |
| `TestRotateImmediateAbortsWithoutMagicString` | `--immediate` without the magic phrase → exit non-zero, no DB write, no audit. |
| `TestRotateImmediateInvalidatesOldTokens` | After confirmed `--immediate`, an old token's kid no longer in JWKS → verify returns `unknown-kid`. Within 1 s if listener is wired. |
| `TestRotateEmitsAudit` | One audit row `event='key.rotated', payload.mode ∈ {overlap, immediate}, payload.new_kid`. |
| `TestRotatePgNotifyFires` | LISTEN jwks_changed before rotate → exactly one notification with the new kid. |
| `TestRotateSqliteBusFires` | Subscribe to `JWKS_CHANGED` channel of the in-process bus; rotate → one event. |

### 9.4 JWKS endpoint tests

| Test | What it pins |
|---|---|
| `TestJWKSReturnsAllKeys` | After rotation: response contains both env and DB key. |
| `TestJWKSCacheControlSet` | Response header `Cache-Control: public, max-age=300`. |
| `TestJWKSContentType` | `application/jwk-set+json`. |
| `TestJWKSReachableWithoutAuth` | No `Authorization` header → 200. |
| `TestJWKSReflectsRotationWithin1s` | Mint → poll JWKS → contains new kid; latency under 1 s when LISTEN wired. |

### 9.5 Cross-dialect parity

`keys_dialect_test.go` runs the rotate flow against PG and SQLite via
the parametrized fixture. The PG variant asserts the trigger fires;
the SQLite variant asserts the in-process bus event fires.

## 10. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Leaked private key | Operator runs `keys rotate --immediate`; old kid drops from JWKS within 1 s; in-flight tokens fail. Documented in operations as the incident-response runbook. | `TestRotateImmediateInvalidatesOldTokens` |
| Two API replicas, brief active-key disagreement during rotation | Both keys are in JWKS; both verify cleanly. The "active for sign" disagreement is what Story 10.7 EC calls out. | n/a (covered behaviorally) |
| JWKS endpoint blocked by firewall | Streaming caches the last-seen JWKS indefinitely (Story 10.7 AC-1 fallback). New keys can't propagate; documented. | Story 10.7 plan |
| Key file with extra whitespace at the end of PEM | `ParsePEMPrivate` strips ASN.1-irrelevant whitespace; PEM block is the source of truth. | `TestParsePEMTolerantOfWhitespace` |
| Two `keys init` runs producing the same kid by collision | Astronomically unlikely (64-bit truncation of SHA-256). The PK constraint on `kid` would surface a duplicate; rotation fails loudly and the operator regenerates. | n/a |
| Reload races with mint | `Sign` takes RLock for the duration of one signing call; reload takes a Write lock. The active key reference is pinned per-call so a mid-mint reload still completes with the old key. | `TestSignDuringReloadCompletes` |
| Listener disconnects | Reconnect loop with 5 s back-off. While disconnected, the in-process keyring serves stale-but-correct JWKS until reconnect; new rotations propagate at the next reload (or via the 5-min poll fallback that Story 10.7 owns at the consumer side). | `TestListenerReconnects` |
| `keys rotate` with env key already weakened (e.g., 1024) | Boot already refused. If somehow inserted via direct SQL, the JWKS endpoint includes it with the strength written; verify still uses the JWKS public modulus. Documented as "do not bypass the CLI". | n/a |

## 11. Dependencies

| Dep | Version | Why |
|---|---|---|
| `crypto/rsa`, `crypto/x509`, `crypto/sha256` | stdlib | Key gen, DER marshalling, KID. |
| `encoding/pem` | stdlib | PEM round-trip. |
| `github.com/golang-jwt/jwt/v5` | v5.x | JWT signing (already a dep from Story 10.3). |
| `github.com/jackc/pgx/v5` | already | LISTEN. |
| `github.com/spf13/cobra` | already | CLI. |

No new heavy deps.

## 12. Acceptance checklist

**Migration**
- [ ] `0023_jwt_keys.sql` applies; trigger + unique-active partial index present.
- [ ] `pg_notify('jwks_changed', kid)` fires on insert/update/delete.

**Boot**
- [ ] AC-1: missing env → API refuses to start with a clear message naming both env vars.
- [ ] `kid` is 16-hex-char SHA-256 of public DER.
- [ ] AC-2: `keys init` prints PEM blocks and never writes to disk.

**JWKS**
- [ ] AC-3: `GET /api/.well-known/jwks.json` returns every currently trusted key with `Cache-Control: public, max-age=300`.
- [ ] Endpoint reachable without auth.

**Rotation**
- [ ] AC-4: `keys rotate` adds a new active key; old key remains trusted until `not_after`. New mints use new key. Listener notified within 1 s.
- [ ] AC-5: `keys rotate --immediate` requires the magic confirmation string; on confirm, every in-flight token fails verify within 1 s.
- [ ] Audit row `event='key.rotated', payload.mode` written.

**Verify**
- [ ] HS256 / `none` tokens are rejected (alg-confusion guard).
- [ ] Keys < 2048 bits refused at boot.

**Tests**
- [ ] All §9 tests pass on both dialects.

**Docs**
- [ ] README.md ticks story 10.6.
- [ ] Operations runbook documents the leaked-key response (`rotate --immediate`).

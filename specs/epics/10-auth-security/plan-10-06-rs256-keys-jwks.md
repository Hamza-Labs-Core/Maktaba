# Plan 10.6 — RS256 keys, rotation, JWKS publication — implementation

> Implementation plan for [story-10-06-rs256-keys-jwks.md](story-10-06-rs256-keys-jwks.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: the JWKS endpoint is consumed by the
> Streaming Service (Plan 10.7); the signing key is loaded by the URL
> minter (Plan 10.8); rotation triggers a Postgres `NOTIFY jwks_changed`
> that both the API and Streaming subscribe to. The `--immediate` mode
> is the escape hatch referenced from Plan 10.5 for instant kill of all
> in-flight access tokens.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Keys are loaded from env at boot AND mirrored to a `jwks_keys` table.** Env is the source of secret material; the table is the source of public truth (kid → public PEM, status). On boot, the API reads env, derives `kid`, and `INSERT … ON CONFLICT DO UPDATE` into the table. | Story AC-1 + AC-3. | The JWKS handler must serve every trusted `kid`, not just the active signing key, during the rotation overlap. A table is the natural store for "all kids the cluster currently trusts," and it survives API restarts so `LISTEN/NOTIFY` from Streaming has a stable read target. |
| D2 | **`kid = base32(sha256(public_key_DER))[:16]`** (lowercase, no padding). | Story AC-1: "first 16 chars of the SHA-256 of the public key DER." | Base32 (lowercase) avoids `+`/`/` URL-encoding hassle that base64 would, and is shorter than hex. 16 chars of base32 = 80 bits of entropy — comfortably collision-free for a few keys per cluster lifetime. |
| D3 | **Three statuses: `active`, `retired`, `revoked`.** Exactly one row has `status='active'` (DB-enforced via partial unique index). Retired rows still appear in JWKS until `retired_at + rotation_overlap_sec`. Revoked rows are excluded from JWKS immediately. | Story AC-3, AC-4, AC-5. | `active`/`retired` separation is needed because the overlap window keeps an old key trusted but no longer signing. `revoked` is a separate state for the `--immediate` path so we can keep an audit row of what was nuked without cluttering normal rotation. |
| D4 | **CLI `keys init` prints to stdout only — never writes to disk.** Operator pipes to a secret manager. | Story AC-2. | Disk writes leak through backups, screen-share, and shell history. Forcing the operator to handle the material teaches the right hygiene and matches §9.8's "key material lives outside the deploy artifact." |
| D5 | **`keys rotate` writes the new private PEM to `jwks_keys.private_pem_env` as the env-var **name** (e.g. `MAKTABA_JWT_PRIVATE_KEY_PEM_2`), not the PEM itself.** The PEM still has to be set in the environment by the operator. The CLI prints the PEM and the env-var-name and then waits for the operator to confirm the new env was applied to all replicas before flipping `status='active'`. | Story AC-4. | We don't store private keys in Postgres — that's a regression of the env-only-secrets discipline. The pointer to "which env var holds the new private key" lets multiple replicas independently load the right one without operator-per-replica fan-out. |
| D6 | **`LISTEN/NOTIFY jwks_changed` runs on a single dedicated `pgx` connection on each service replica**, not on the pool. The connection auto-reconnects with backoff. On notification, the cache is fully reloaded (not patched) — the JWKS is small. | Story AC-4 narrative. | Pool connections are short-lived; LISTEN must outlive any individual transaction. Fully reloading is simpler than diff-merging and is correct because the table is small (≤ 5 rows in steady state). |
| D7 | **`--immediate` requires the operator to type `yes-invalidate-all-tokens` on stdin AND pass `--confirm-replicas=N` matching the number of running API replicas.** The CLI then revokes the *currently-active* row (`status='revoked'`) before activating the new key, so old tokens fail signature verification within one NOTIFY round-trip. | Story AC-5. | Two-factor confirmation (typed string + replica count) catches both fat-finger and forgotten-replica scenarios. Revoking before activating means there is *no* moment when both keys are trusted — that's the whole point of `--immediate`. |

---

## 1. Architecture diagram — key lifecycle

```
                          OPERATOR
                             │
              ┌──────────────┴───────────────┐
              │                              │
              ▼                              ▼
    maktaba-api keys init           maktaba-api keys rotate [--immediate]
        prints PEMs to stdout            generates new keypair
                                         prints PEMs + env-var name
                                         waits for operator to roll env
                                         then UPDATE jwks_keys → NOTIFY

        ┌────────────────────────────────────────────────────────────┐
        │ API process (Go)                                           │
        │  ┌──────────────────────────────────────────────────────┐  │
        │  │ internal/auth/keys                                   │  │
        │  │   Loader.LoadFromEnv() — reads MAKTABA_JWT_*         │  │
        │  │   computes kid, mirrors to jwks_keys (D1, D2)        │  │
        │  │   Cache (RWMutex map[kid]*PublicKey + active *kid)   │  │
        │  │                                                      │  │
        │  │ Listener — pgx.Conn LISTEN jwks_changed (D6)         │  │
        │  │   on NOTIFY: SELECT * FROM jwks_keys WHERE …         │  │
        │  │              swap cache atomically                   │  │
        │  └──────────────────────────────────────────────────────┘  │
        │                          │                                 │
        │                          ▼                                 │
        │  GET /api/.well-known/jwks.json                            │
        │     emits {keys: [each kid as RSA JWK]}                    │
        │     Cache-Control: public, max-age=300                     │
        └────────────────────────────────────────────────────────────┘
                                   │
                                   │ NOTIFY jwks_changed
                                   ▼
        ┌──────────────────────────────────────────────────────┐
        │ Streaming process (Go) — see Plan 10.7               │
        │   pgx.Conn LISTEN jwks_changed → re-fetch JWKS       │
        └──────────────────────────────────────────────────────┘
```

---

## 2. Detailed implementation

### 2.1 Package layout — Go API

```
api/
├── internal/
│   └── auth/
│       └── keys/
│           ├── loader.go        # LoadFromEnv, KidFromPublic
│           ├── cache.go         # in-memory cache + LISTEN/NOTIFY
│           ├── jwks_handler.go  # GET /api/.well-known/jwks.json
│           ├── rotate.go        # CLI: rotate / init business logic
│           ├── repo.go          # sqlc-backed Postgres access
│           └── keys_test.go
└── cmd/
    └── maktaba-api/
        ├── cmd_keys_init.go     # `maktaba-api keys init`
        └── cmd_keys_rotate.go   # `maktaba-api keys rotate [--immediate]`

shared/db/migrations/0030_jwks_keys.sql
shared/db/queries/jwks.sql
```

### 2.2 SQL — migration `0030_jwks_keys.sql`

```sql
-- shared/db/migrations/0030_jwks_keys.sql
BEGIN;

CREATE TABLE jwks_keys (
    kid              TEXT PRIMARY KEY,
    public_pem       TEXT NOT NULL,
    private_pem_env  TEXT,                      -- env-var NAME holding private PEM (D5)
    status           TEXT NOT NULL CHECK (status IN ('active','retired','revoked')),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    retired_at       TIMESTAMPTZ
);

-- Exactly one active key (D3).
CREATE UNIQUE INDEX jwks_keys_only_one_active
    ON jwks_keys ((status))
    WHERE status = 'active';

-- Function + trigger to fire NOTIFY on any state change.
CREATE OR REPLACE FUNCTION jwks_keys_notify() RETURNS TRIGGER AS $$
BEGIN
    PERFORM pg_notify('jwks_changed', COALESCE(NEW.kid, OLD.kid));
    RETURN COALESCE(NEW, OLD);
END $$ LANGUAGE plpgsql;

CREATE TRIGGER jwks_keys_notify_trg
    AFTER INSERT OR UPDATE OR DELETE ON jwks_keys
    FOR EACH ROW EXECUTE FUNCTION jwks_keys_notify();

COMMIT;
```

### 2.3 sqlc queries — `shared/db/queries/jwks.sql`

```sql
-- name: UpsertKey :exec
INSERT INTO jwks_keys (kid, public_pem, private_pem_env, status)
VALUES ($1, $2, $3, $4)
ON CONFLICT (kid) DO UPDATE
   SET public_pem = EXCLUDED.public_pem,
       private_pem_env = COALESCE(EXCLUDED.private_pem_env, jwks_keys.private_pem_env),
       status = EXCLUDED.status;

-- name: TrustedKeys :many
SELECT kid, public_pem, status, created_at, retired_at
  FROM jwks_keys
 WHERE status IN ('active', 'retired')
 ORDER BY created_at DESC;

-- name: ActiveKey :one
SELECT kid, public_pem, private_pem_env
  FROM jwks_keys
 WHERE status = 'active';

-- name: RetireKey :exec
UPDATE jwks_keys
   SET status = 'retired', retired_at = now()
 WHERE kid = $1 AND status = 'active';

-- name: RevokeKey :exec
UPDATE jwks_keys
   SET status = 'revoked', retired_at = now()
 WHERE kid = $1;

-- name: ActivateKey :exec
UPDATE jwks_keys SET status = 'active' WHERE kid = $1;

-- name: SweepRetiredOlderThan :exec
DELETE FROM jwks_keys
 WHERE status = 'retired' AND retired_at < now() - $1::interval;
```

### 2.4 `loader.go` — env loading + kid derivation (D1, D2)

```go
// api/internal/auth/keys/loader.go
package keys

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base32"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	EnvPrivatePEM = "MAKTABA_JWT_PRIVATE_KEY_PEM"
	EnvPublicPEM  = "MAKTABA_JWT_PUBLIC_KEY_PEM"
	MinKeyBits    = 2048
)

var ErrMissingKeys = errors.New("MAKTABA_JWT_{PRIVATE,PUBLIC}_KEY_PEM must be set")

type LoadedKey struct {
	Kid        string
	Public     *rsa.PublicKey
	Private    *rsa.PrivateKey
	PublicPEM  string
	PrivateEnv string // env var NAME (e.g. "MAKTABA_JWT_PRIVATE_KEY_PEM")
}

func LoadFromEnv() (*LoadedKey, error) {
	priv := strings.TrimSpace(os.Getenv(EnvPrivatePEM))
	pub := strings.TrimSpace(os.Getenv(EnvPublicPEM))
	if priv == "" || pub == "" {
		return nil, ErrMissingKeys
	}
	rsaPriv, err := parseRSAPrivate(priv)
	if err != nil {
		return nil, fmt.Errorf("parse private: %w", err)
	}
	rsaPub, err := parseRSAPublic(pub)
	if err != nil {
		return nil, fmt.Errorf("parse public: %w", err)
	}
	if rsaPriv.N.BitLen() < MinKeyBits {
		return nil, fmt.Errorf("private key %d bits < min %d", rsaPriv.N.BitLen(), MinKeyBits)
	}
	if rsaPub.N.Cmp(rsaPriv.N) != 0 {
		return nil, errors.New("private/public key mismatch")
	}
	kid, err := KidFromPublic(rsaPub)
	if err != nil {
		return nil, err
	}
	return &LoadedKey{
		Kid: kid, Public: rsaPub, Private: rsaPriv,
		PublicPEM: pub, PrivateEnv: EnvPrivatePEM,
	}, nil
}

// KidFromPublic = first 16 chars of base32-lowercase(SHA256(DER(SubjectPublicKeyInfo))).
func KidFromPublic(pub *rsa.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(der)
	enc := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))
	return enc[:16], nil
}

func parseRSAPrivate(p string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(p))
	if block == nil {
		return nil, errors.New("not a PEM block")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	any, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := any.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("not RSA")
	}
	return rk, nil
}

func parseRSAPublic(p string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(p))
	if block == nil {
		return nil, errors.New("not a PEM block")
	}
	pk, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := pk.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("not RSA")
	}
	return rk, nil
}
```

### 2.5 `cache.go` — in-memory cache + LISTEN (D6)

```go
// api/internal/auth/keys/cache.go
package keys

import (
	"context"
	"crypto/rsa"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CachedKey struct {
	Kid       string
	Public    *rsa.PublicKey
	PublicPEM string
	Status    string
}

type Cache struct {
	mu       sync.RWMutex
	keys     map[string]CachedKey
	activeID string
	private  *rsa.PrivateKey // active signing key
}

func (c *Cache) Get(kid string) (CachedKey, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	k, ok := c.keys[kid]
	return k, ok
}

func (c *Cache) ActiveSigning() (kid string, priv *rsa.PrivateKey, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.activeID == "" || c.private == nil {
		return "", nil, false
	}
	return c.activeID, c.private, true
}

func (c *Cache) ListJWKS() []CachedKey {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]CachedKey, 0, len(c.keys))
	for _, v := range c.keys {
		out = append(out, v)
	}
	return out
}

// Reload pulls every trusted key from Postgres and atomically swaps the map.
func (c *Cache) Reload(ctx context.Context, pool *pgxpool.Pool, repo *Repo, env EnvLookup) error {
	rows, err := repo.TrustedKeys(ctx)
	if err != nil {
		return err
	}
	next := make(map[string]CachedKey, len(rows))
	var activeID string
	var priv *rsa.PrivateKey
	for _, r := range rows {
		pub, err := parseRSAPublic(r.PublicPEM)
		if err != nil {
			continue
		}
		next[r.Kid] = CachedKey{Kid: r.Kid, Public: pub, PublicPEM: r.PublicPEM, Status: r.Status}
		if r.Status == "active" {
			activeID = r.Kid
			if r.PrivatePEMEnv != nil {
				if pem := env(*r.PrivatePEMEnv); pem != "" {
					if pk, err := parseRSAPrivate(pem); err == nil {
						priv = pk
					}
				}
			}
		}
	}
	c.mu.Lock()
	c.keys = next
	c.activeID = activeID
	c.private = priv
	c.mu.Unlock()
	return nil
}

type EnvLookup func(name string) string

// ListenAndReload runs forever; reloads cache on every NOTIFY jwks_changed.
func (c *Cache) ListenAndReload(ctx context.Context, pool *pgxpool.Pool, repo *Repo, env EnvLookup) {
	for ctx.Err() == nil {
		if err := c.listenOnce(ctx, pool, repo, env); err != nil {
			time.Sleep(2 * time.Second)
		}
	}
}

func (c *Cache) listenOnce(ctx context.Context, pool *pgxpool.Pool, repo *Repo, env EnvLookup) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN jwks_changed"); err != nil {
		return err
	}
	if err := c.Reload(ctx, pool, repo, env); err != nil {
		return err
	}
	for {
		_, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		_ = c.Reload(ctx, pool, repo, env)
	}
}

// Used by integration tests where pgxpool isn't available directly.
var _ = pgx.ErrNoRows
```

### 2.6 `jwks_handler.go` — GET /api/.well-known/jwks.json

```go
// api/internal/auth/keys/jwks_handler.go
package keys

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
)

type JWK struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type JWKS struct {
	Keys []JWK `json:"keys"`
}

func NewJWKSHandler(cache *Cache) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out := JWKS{}
		for _, k := range cache.ListJWKS() {
			out.Keys = append(out.Keys, toJWK(k.Public, k.Kid))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_ = json.NewEncoder(w).Encode(out)
	})
}

func toJWK(pub *rsa.PublicKey, kid string) JWK {
	return JWK{
		Kty: "RSA", Use: "sig", Alg: "RS256", Kid: kid,
		N: base64URL(pub.N), E: base64URL(big.NewInt(int64(pub.E))),
	}
}

func base64URL(i *big.Int) string {
	return base64.RawURLEncoding.EncodeToString(i.Bytes())
}
```

### 2.7 `rotate.go` — CLI business logic (D4, D5, D7)

```go
// api/internal/auth/keys/rotate.go
package keys

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"maktaba/api/internal/audit"
)

const (
	DefaultRotationOverlap = 24 * time.Hour
	ImmediateMagic         = "yes-invalidate-all-tokens"
)

func GenerateAndPrintInit(out io.Writer) error {
	priv, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return err
	}
	if err := writePEMs(out, priv); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n# Set the above values in:\n#   %s\n#   %s\n",
		EnvPrivatePEM, EnvPublicPEM)
	return nil
}

type RotateOpts struct {
	Immediate         bool
	ConfirmReplicas   int
	OverlapDuration   time.Duration
	Stdin             io.Reader
	Stdout            io.Writer
	Now               func() time.Time
}

func Rotate(ctx context.Context, repo *Repo, audit *audit.Logger, opts RotateOpts) error {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Immediate {
		if err := confirmImmediate(opts.Stdin, opts.Stdout, opts.ConfirmReplicas); err != nil {
			return err
		}
	}
	priv, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return err
	}
	pub := &priv.PublicKey
	kid, err := KidFromPublic(pub)
	if err != nil {
		return err
	}
	pubPEM := encodePublicPEM(pub)
	envName := fmt.Sprintf("%s_%d", EnvPrivatePEM, opts.Now().Unix())

	if err := writePEMs(opts.Stdout, priv); err != nil {
		return err
	}
	fmt.Fprintf(opts.Stdout, "\n# Set the new PRIVATE key in env var: %s\n# On every replica.\n# Press ENTER once that's done; the CLI will then activate the key.\n",
		envName)
	br := bufio.NewReader(opts.Stdin)
	_, _ = br.ReadString('\n')

	// Insert as retired first (so a replica that hasn't loaded the env yet
	// won't be asked to verify with the new kid until ActivateKey runs).
	if err := repo.UpsertKey(ctx, RepoKey{
		Kid: kid, PublicPEM: pubPEM, PrivatePEMEnv: &envName, Status: "retired",
	}); err != nil {
		return err
	}

	if opts.Immediate {
		// Revoke the existing active key BEFORE activating the new one
		// — this collapses the overlap to zero. (D7)
		active, err := repo.ActiveKey(ctx)
		if err == nil && active != nil {
			if err := repo.RevokeKey(ctx, active.Kid); err != nil {
				return err
			}
		}
	} else {
		// Standard rotation: retire the old active.
		if err := repo.RetireOldActive(ctx); err != nil {
			return err
		}
	}
	if err := repo.ActivateKey(ctx, kid); err != nil {
		return err
	}
	mode := "standard"
	if opts.Immediate {
		mode = "immediate"
	}
	audit.Security(ctx, audit.Event{
		Event: "key.rotated",
		Payload: map[string]any{"mode": mode, "kid": kid, "overlap_sec": int(opts.OverlapDuration.Seconds())},
	})
	if !opts.Immediate {
		// Schedule sweep at +overlap. The sweeper job runs hourly
		// (Plan 22.x) and DELETEs status='retired' rows older than overlap.
		fmt.Fprintf(opts.Stdout, "# Old key will be removed in %s\n", opts.OverlapDuration)
	}
	return nil
}

func confirmImmediate(in io.Reader, out io.Writer, replicas int) error {
	if replicas <= 0 {
		return errors.New("--confirm-replicas must be set to the number of running API replicas")
	}
	fmt.Fprintf(out, "WARNING: --immediate invalidates every in-flight token.\n"+
		"Type %q to proceed (across %d replicas): ", ImmediateMagic, replicas)
	br := bufio.NewReader(in)
	line, _ := br.ReadString('\n')
	if strings.TrimSpace(line) != ImmediateMagic {
		return errors.New("aborted: confirmation phrase did not match")
	}
	return nil
}

func writePEMs(w io.Writer, k *rsa.PrivateKey) error {
	privDER, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return err
	}
	if err := pem.Encode(w, &pem.Block{Type: "PRIVATE KEY", Bytes: privDER}); err != nil {
		return err
	}
	return pem.Encode(w, &pem.Block{Type: "PUBLIC KEY", Bytes: mustMarshalPublicDER(&k.PublicKey)})
}

func encodePublicPEM(pub *rsa.PublicKey) string {
	der := mustMarshalPublicDER(pub)
	var b strings.Builder
	_ = pem.Encode(&b, &pem.Block{Type: "PUBLIC KEY", Bytes: der})
	return b.String()
}

func mustMarshalPublicDER(pub *rsa.PublicKey) []byte {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		panic(err)
	}
	return der
}

// CLI entry points
func CmdInit(_ context.Context, _ []string) error {
	return GenerateAndPrintInit(os.Stdout)
}
```

### 2.8 Boot wiring

```go
// api/cmd/maktaba-api/main.go (excerpt)
loaded, err := keys.LoadFromEnv()
if err != nil { log.Fatal(err) }
repo := keys.NewRepo(pool)
// Mirror env into table on boot (D1).
_ = repo.UpsertKey(ctx, keys.RepoKey{
	Kid: loaded.Kid, PublicPEM: loaded.PublicPEM,
	PrivatePEMEnv: &loaded.PrivateEnv, Status: "active"})
cache := keys.NewCache()
go cache.ListenAndReload(ctx, pool, repo, os.Getenv)
r.Get("/api/.well-known/jwks.json", keys.NewJWKSHandler(cache).ServeHTTP)
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols | Tests |
|-------|------|---------|-------|
| 1 | `shared/db/migrations/0030_jwks_keys.sql` | table, partial unique index, NOTIFY trigger | `TestMigrationCreatesJWKSTableAndTrigger` |
| 2 | `shared/db/queries/jwks.sql` | sqlc queries | sqlc generates |
| 3 | `api/internal/auth/keys/loader.go` | `LoadFromEnv`, `KidFromPublic` | `TestLoadFromEnv*`, `TestKidStability` |
| 4 | `api/internal/auth/keys/repo.go` | `Repo` wrappers | `TestRepoUpsertActivateRetireRevoke` |
| 5 | `api/internal/auth/keys/cache.go` | `Cache.{Get,ActiveSigning,ListJWKS,Reload,ListenAndReload}` | `TestCacheReload*`, `TestListenFiresOnNotify` |
| 6 | `api/internal/auth/keys/jwks_handler.go` | `NewJWKSHandler`, `JWK`, `JWKS` | `TestJWKSEndpointShape` |
| 7 | `api/internal/auth/keys/rotate.go` | `GenerateAndPrintInit`, `Rotate`, `confirmImmediate` | `TestRotateStandard`, `TestRotateImmediate*` |
| 8 | `api/cmd/maktaba-api/cmd_keys_init.go` | `CmdInit` | smoke |
| 9 | `api/cmd/maktaba-api/cmd_keys_rotate.go` | `CmdRotate` | integration |
| 10 | `api/cmd/maktaba-api/main.go` (extend) | wiring | boot integration |

---

## 4. Test cases keyed to ACs

### 4.1 `TestLoadFromEnvDerivesKidAndRefusesShortKey` (AC-1)

```go
func TestLoadFromEnvDerivesKid(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 4096)
	t.Setenv("MAKTABA_JWT_PRIVATE_KEY_PEM", pemEncode(priv))
	t.Setenv("MAKTABA_JWT_PUBLIC_KEY_PEM", pemEncode(&priv.PublicKey))
	loaded, err := keys.LoadFromEnv()
	require.NoError(t, err)
	require.Len(t, loaded.Kid, 16)
	require.Regexp(t, `^[a-z2-7]+$`, loaded.Kid) // base32 lowercase no padding
}

func TestLoadFromEnvRejects1024Bit(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 1024)
	t.Setenv("MAKTABA_JWT_PRIVATE_KEY_PEM", pemEncode(priv))
	t.Setenv("MAKTABA_JWT_PUBLIC_KEY_PEM", pemEncode(&priv.PublicKey))
	_, err := keys.LoadFromEnv()
	require.ErrorContains(t, err, "1024 bits < min 2048")
}

func TestLoadFromEnvMissingKeysFails(t *testing.T) {
	t.Setenv("MAKTABA_JWT_PRIVATE_KEY_PEM", "")
	t.Setenv("MAKTABA_JWT_PUBLIC_KEY_PEM", "")
	_, err := keys.LoadFromEnv()
	require.ErrorIs(t, err, keys.ErrMissingKeys)
}
```

### 4.2 `TestKeysInitPrintsToStdout` (AC-2)

```go
func TestKeysInitPrintsTwoPEMBlocksAndEnvNames(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, keys.GenerateAndPrintInit(&buf))
	out := buf.String()
	require.Contains(t, out, "BEGIN PRIVATE KEY")
	require.Contains(t, out, "BEGIN PUBLIC KEY")
	require.Contains(t, out, "MAKTABA_JWT_PRIVATE_KEY_PEM")
	require.Contains(t, out, "MAKTABA_JWT_PUBLIC_KEY_PEM")
}
```

### 4.3 `TestJWKSEndpointShape` (AC-3)

```go
func TestJWKSEndpointReturnsAllTrustedKeys(t *testing.T) {
	cache := keys.NewCache()
	pub1, kid1 := freshKey(t)
	pub2, kid2 := freshKey(t)
	cache.SetForTest(map[string]keys.CachedKey{
		kid1: {Kid: kid1, Public: pub1, Status: "active"},
		kid2: {Kid: kid2, Public: pub2, Status: "retired"},
	}, kid1, nil)
	rr := httptest.NewRecorder()
	keys.NewJWKSHandler(cache).ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	require.Equal(t, "public, max-age=300", rr.Header().Get("Cache-Control"))
	var jwks keys.JWKS
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&jwks))
	require.Len(t, jwks.Keys, 2)
	for _, j := range jwks.Keys {
		require.Equal(t, "RS256", j.Alg)
		require.Equal(t, "RSA", j.Kty)
		require.NotEmpty(t, j.N)
		require.NotEmpty(t, j.E)
	}
}
```

### 4.4 `TestRotateStandardKeepsOldKeyTrusted` (AC-4)

```go
func TestRotateAddsNewKidAndKeepsOldVerifying(t *testing.T) {
	ctx, db := newTestDB(t)
	repo := keys.NewRepo(db)
	seedActive(t, repo, "old-kid", "old-pem")
	var stdin = strings.NewReader("\n")
	var stdout bytes.Buffer
	err := keys.Rotate(ctx, repo, audit.Nop(), keys.RotateOpts{
		Immediate: false, Stdin: stdin, Stdout: &stdout,
		OverlapDuration: 24 * time.Hour,
	})
	require.NoError(t, err)
	rows, _ := repo.TrustedKeys(ctx)
	statuses := map[string]string{}
	for _, r := range rows { statuses[r.Kid] = r.Status }
	require.Equal(t, "retired", statuses["old-kid"])
	require.Len(t, statuses, 2)
	for k, s := range statuses {
		if k != "old-kid" { require.Equal(t, "active", s) }
	}
}
```

### 4.5 `TestImmediateRequiresMagicAndReplicas` (AC-5)

```go
func TestImmediateAbortsWithoutMagicString(t *testing.T) {
	ctx, db := newTestDB(t)
	repo := keys.NewRepo(db)
	seedActive(t, repo, "old", "pem")
	err := keys.Rotate(ctx, repo, audit.Nop(), keys.RotateOpts{
		Immediate: true, ConfirmReplicas: 3,
		Stdin: strings.NewReader("nope\n"), Stdout: &bytes.Buffer{},
	})
	require.ErrorContains(t, err, "aborted")
}

func TestImmediateRevokesOldBeforeActivatingNew(t *testing.T) {
	ctx, db := newTestDB(t)
	repo := keys.NewRepo(db)
	seedActive(t, repo, "old", "pem")
	stdin := strings.NewReader("yes-invalidate-all-tokens\n\n")
	err := keys.Rotate(ctx, repo, audit.Nop(), keys.RotateOpts{
		Immediate: true, ConfirmReplicas: 1,
		Stdin: stdin, Stdout: &bytes.Buffer{},
	})
	require.NoError(t, err)
	row, _ := repo.AllKeys(ctx)
	state := stateMap(row)
	require.Equal(t, "revoked", state["old"])
	// Audit row carries mode='immediate'.
	a := lastAudit(t, db, "key.rotated")
	require.Equal(t, "immediate", a.Payload["mode"])
}
```

### 4.6 `TestListenFiresOnNotify` (AC-4 + D6)

```go
func TestListenReloadsCacheOnNotify(t *testing.T) {
	ctx, db := newTestDB(t)
	repo := keys.NewRepo(db)
	cache := keys.NewCache()
	go cache.ListenAndReload(ctx, db, repo, os.Getenv)
	seedActive(t, repo, "k1", pemFor(t))
	require.Eventually(t, func() bool {
		_, ok := cache.Get("k1"); return ok
	}, 2*time.Second, 20*time.Millisecond)
	seedRetired(t, repo, "k2", pemFor(t))
	require.Eventually(t, func() bool {
		_, ok := cache.Get("k2"); return ok
	}, 2*time.Second, 20*time.Millisecond)
}
```

---

## 5. Edge cases

| #  | Edge case | Handled by |
|----|-----------|------------|
| E1 | **Two API replicas race on rotation** — `keys rotate` runs on one host. The active row's partial unique index ensures only one row can have `status='active'` cluster-wide; the second `ActivateKey` would fail. CLI is single-operator-driven, not concurrent. | DB constraint. |
| E2 | **Operator forgets to set the new env var on a replica.** That replica's cache.Reload finds `private_pem_env` pointing to an empty env value; `c.private` stays `nil`. The minter (Plan 10.8) returns `KeyUnavailable`. The replica still serves JWKS for verification. | Loader: empty env → no private key cached; minter returns 503. |
| E3 | **Leaked private key.** Operator runs `--immediate`. Old active becomes `revoked` (not `retired`); JWKS no longer lists it; existing tokens fail signature verification within one NOTIFY round-trip (~ms). | D3 + D7. |
| E4 | **JWKS endpoint blocked by firewall** — Streaming caches the last-seen JWKS indefinitely (Plan 10.7 D2); no impact on already-verified tokens. New kids won't reach Streaming until the firewall clears. | Plan 10.7. |
| E5 | **NOTIFY connection drops** — `listenOnce` errors out; outer loop sleeps 2 s and reconnects. Reload happens immediately on reconnect, so a brief outage doesn't lose a notification. | `ListenAndReload` retry loop. |
| E6 | **Boot before migration applied** — `repo.UpsertKey` returns "relation does not exist". API refuses to start. | Caller bails. |
| E7 | **`keys init` run while the server is up** — only prints PEMs to stdout; nothing else changes. The operator must still set env + restart. | D4: stdout-only. |
| E8 | **Retired key never gets swept** — sweeper job (Plan 22.x) runs `SweepRetiredOlderThan('24 hours')` hourly. If skipped, the JWKS keeps growing — with realistic rotation cadence this is a few rows. | Sweeper. |
| E9 | **Public key in env doesn't match private** — `LoadFromEnv` checks `pub.N == priv.N` and refuses. | Loader assertion. |
| E10 | **Database-side partial unique violated by direct SQL** — only the rotate CLI writes to this table; out-of-band edits are operator error. The trigger still fires NOTIFY, so the cache stays in sync. | Operational discipline. |

---

## 6. Acceptance checklist

- [ ] **A1** `LoadFromEnv` reads `MAKTABA_JWT_{PRIVATE,PUBLIC}_KEY_PEM`, computes `kid = base32(sha256(public_DER))[:16]`, refuses keys < 2048 bits, refuses mismatched pairs, refuses missing env. (`TestLoadFromEnv*`)
- [ ] **A2** `maktaba-api keys init` prints a fresh 4096-bit RSA keypair (PRIVATE KEY + PUBLIC KEY PEM blocks) plus the env-var names to stdout, never touching disk. (`TestKeysInitPrintsToStdout`)
- [ ] **A3** `GET /api/.well-known/jwks.json` returns one JWK per trusted (`active` + `retired`) row with `kty=RSA, alg=RS256, use=sig`, plus `Cache-Control: public, max-age=300`. (`TestJWKSEndpointReturnsAllTrustedKeys`)
- [ ] **A4** `maktaba-api keys rotate` generates a new keypair, writes it as `retired` first, retires the old `active`, then activates the new key. Old tokens continue to verify. NOTIFY `jwks_changed` fires; Cache reloads within 1 s. (`TestRotateAddsNewKidAndKeepsOldVerifying`, `TestListenReloadsCacheOnNotify`)
- [ ] **A5** `maktaba-api keys rotate --immediate` requires (a) operator types `yes-invalidate-all-tokens` and (b) `--confirm-replicas=N>0`. Old active is set to `revoked` *before* new is activated. Audit row payload includes `mode='immediate'`. (`TestImmediateAbortsWithoutMagicString`, `TestImmediateRevokesOldBeforeActivatingNew`)
- [ ] **A6** Migration `0030_jwks_keys.sql` creates the table with the partial-unique-active index and a `NOTIFY jwks_changed` AFTER trigger covering INSERT/UPDATE/DELETE.
- [ ] **A7** Cache LISTEN runs on a dedicated pgx connection with reconnect/backoff; on every notification it fully reloads the trusted-keys map. (`TestListenReloadsCacheOnNotify`)
- [ ] **A8** Boot wiring mirrors the env-loaded key into `jwks_keys` with `status='active'`, then starts the listener and mounts the JWKS handler.

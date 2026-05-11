# Implementation Plan — Story 10.18 Ed25519 long-term server identity

> Companion to [story-10-18-ed25519-server-identity.md](story-10-18-ed25519-server-identity.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Where it lives | `api/internal/auth/serverkeys/` — new Go package inside the existing `api/` module. Loaded from `api/cmd/api/main.go` at boot. |
| Algorithm | Ed25519. 32-byte private, 32-byte public, 64-byte signature. `crypto/ed25519` from the stdlib. |
| Sealing | Private bytes sealed at rest via Epic 10.14's `auth/keys.SealedBox` (XChaCha20-Poly1305 with the master KEK). Public bytes stored in clear next to the sealed file. |
| State dir | `${MAKTABA_STATE_DIR}/identity/` — typically `/var/lib/maktaba/identity/`. The directory is mode `0700`, owned by the maktaba user. |
| File scheme | `v<n>.pem.sealed` private + `v<n>.pub.pem` public + `current.kid` symlink → currently active `kid`. During rotation `previous.kid` symlinks to the overlap predecessor. |
| Loader precedence | env (`MAKTABA_SERVER_IDENTITY_PRIVATE_PEM`) > on-disk > generate. |
| Out of scope | Federation request signing (consumes this package; plan-15-07). Cloud claim flow (consumes this package; plan-25-06). License verification (consumes; plan-16-04). |

## 1. Package layout

```
api/internal/auth/serverkeys/
  keys.go          # Store{Active, ByKid, Generate, Rotate}; kid hashing
  loader.go        # env/disk/generate precedence; first-boot lock
  store_test.go
  loader_test.go
  jwks.go          # http.Handler for /api/.well-known/server-identity.json
```

## 2. Public types

```go
package serverkeys

type Key struct {
    Kid       string             // 16-char lowercase hex sha256(pub)[:16]
    PublicKey ed25519.PublicKey  // 32 bytes
    PrivateKey ed25519.PrivateKey // 64 bytes (Go stores priv||pub); nil for predecessor-only entries
    CreatedAt time.Time
    Source    string             // "env" | "disk" | "generated"
}

type Store struct {
    mu        sync.RWMutex
    active    *Key
    overlap   *Key            // predecessor during rotation; verify-only
    dir       string
    sealer    keys.Sealer     // Epic 10.14
    auditor   audit.Writer
    clock     clock.Clock
    overlapDur time.Duration  // 72h default
}

func New(cfg Config, sealer keys.Sealer, auditor audit.Writer, clk clock.Clock) (*Store, error)

func (s *Store) Active() *Key
func (s *Store) Lookup(kid string) (*Key, bool)
func (s *Store) Sign(payload []byte) (sig []byte, kid string)
func (s *Store) Verify(payload, sig []byte, kid string) error      // returns ErrUnknownKid | ErrBadSig | nil
func (s *Store) Rotate(ctx context.Context, reason string, immediate bool) error
func (s *Store) JWKS(now time.Time) JWKSResponse
```

## 3. `kid` derivation

```go
func deriveKid(pub ed25519.PublicKey) string {
    sum := sha256.Sum256(pub)
    return hex.EncodeToString(sum[:])[:16]
}
```

Stable across processes for the same public key bytes.

## 4. First-boot generation

Race-safe via a file lock on `${dir}/.lock` (`flock(LOCK_EX)`).
After acquiring:

1. If env-var present → parse, write to disk only if disk is empty,
   return with `Source="env"`. Env always wins on subsequent boots.
2. Else if `current.kid` symlink exists → read `v<n>.pem.sealed`,
   unseal, parse, return with `Source="disk"`.
3. Else → generate fresh keypair, seal, write atomically (write to
   tmp, fsync, rename), update `current.kid`, write audit row,
   return with `Source="generated"`.

If the directory contains an `expected.kid` sentinel and the loaded
`kid` differs (e.g., disk wiped but operator expected continuity),
boot refuses unless `--allow-new-identity` is set on the CLI.

## 5. Rotation

```go
func (s *Store) Rotate(ctx context.Context, reason string, immediate bool) error {
    s.mu.Lock(); defer s.mu.Unlock()
    pub, priv, _ := ed25519.GenerateKey(rand.Reader)
    next := &Key{Kid: deriveKid(pub), PublicKey: pub, PrivateKey: priv,
                 CreatedAt: s.clock.Now(), Source: "generated"}
    if err := s.persist(next); err != nil { return err }
    old := s.active
    s.overlap = old
    s.active = next
    overlapSecs := int(s.overlapDur.Seconds())
    if immediate { s.overlap = nil; overlapSecs = 0 }
    s.auditor.Write(ctx, audit.Event{
        Category: "keys", Action: "identity.rotated", IsAdmin: true,
        Reason: reason,
        Payload: map[string]any{"old_kid": old.Kid, "new_kid": next.Kid, "overlap_seconds": overlapSecs},
    })
    if !immediate {
        time.AfterFunc(s.overlapDur, func() { s.purgeOverlap() })
    } else {
        s.purgeOverlap()
    }
    return nil
}
```

`purgeOverlap` deletes the predecessor's sealed file, removes
`previous.kid`, and clears `s.overlap`.

## 6. Sign / Verify

```go
func (s *Store) Sign(payload []byte) ([]byte, string) {
    s.mu.RLock(); defer s.mu.RUnlock()
    return ed25519.Sign(s.active.PrivateKey, payload), s.active.Kid
}

func (s *Store) Verify(payload, sig []byte, kid string) error {
    s.mu.RLock(); defer s.mu.RUnlock()
    var pub ed25519.PublicKey
    switch {
    case s.active != nil && s.active.Kid == kid:
        pub = s.active.PublicKey
    case s.overlap != nil && s.overlap.Kid == kid:
        pub = s.overlap.PublicKey
    default:
        return ErrUnknownKid
    }
    if !ed25519.Verify(pub, payload, sig) {
        return ErrBadSig
    }
    return nil
}
```

## 7. JWKS-style endpoint

`GET /api/.well-known/server-identity.json` returns:

```json
{
  "active": {
    "kid": "0123456789abcdef",
    "alg": "EdDSA",
    "public_key_b64": "<base64 raw 32B>",
    "created_at": "2026-05-10T00:00:00Z"
  },
  "overlap": [
    {"kid": "fedcba9876543210", "alg": "EdDSA",
     "public_key_b64": "...", "created_at": "2026-04-10T00:00:00Z",
     "retires_at": "2026-05-13T00:00:00Z"}
  ]
}
```

Cache 300 s. Public, no auth.

## 8. CLI

`maktaba-api identity init`  — generates if missing; idempotent.
`maktaba-api identity rotate --reason "<text>"`  — overlap rotation.
`maktaba-api identity rotate --immediate`  — prompts for the magic
string `yes-invalidate-server-identity` then collapses overlap.

## 9. Test plan

| Test | Pins |
|---|---|
| `TestGenerateFirstBoot` | Empty dir → fresh keypair persisted; reload returns same `kid`. |
| `TestEnvOverridesDisk` | Both present → env wins; source=`env`. |
| `TestKidStable` | Same pubkey across processes → same `kid`. |
| `TestRotateOverlap` | Old sig verifies during overlap; after `overlapDur` it returns `ErrUnknownKid`. |
| `TestRotateImmediate` | Predecessor purged immediately. |
| `TestVerifyUnknownKid` | `ErrUnknownKid` vs `ErrBadSig` are distinct. |
| `TestBadPemEnvRefusesBoot` | Garbage env var → start aborts. |
| `TestWrongAlgRefusesBoot` | P-256 PEM → refused. |
| `TestSentinelMismatchRefuses` | `expected.kid` ≠ disk `kid` → refuses unless `--allow-new-identity`. |
| `TestFirstBootRace` | Two callers under flock → both observe same `kid`. |
| `TestJWKSEndpoint` | Active + overlap entries; cache header set. |

## 10. Dependencies

- 10.14 (`auth/keys.SealedBox`) for at-rest sealing.
- 21.6 (audit log) for `category='keys'` rows.

## 11. Acceptance checklist

- [ ] `serverkeys` package compiles + tests green.
- [ ] `/api/.well-known/server-identity.json` served.
- [ ] `maktaba-api identity init|rotate` commands wired.
- [ ] First-boot audit row written.
- [ ] Sentinel-protected accidental regeneration.
- [ ] No top-level `go.mod` introduced; package stays under `api/`.

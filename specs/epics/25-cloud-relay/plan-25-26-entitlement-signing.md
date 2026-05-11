# Implementation Plan — Story 25.26 Cloud→server entitlement signing

> Companion to [story-25-26-entitlement-signing.md](story-25-26-entitlement-signing.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Signature | Ed25519 over JCS-canonicalized JSON payload. Compact JWS-style serialization: `entitlement.<b64-payload>.<b64-sig>`. |
| Key | Cloud-held Ed25519 keypair; `kid` like `ent-2026-05` rotated monthly. Old kids kept in JWKS for 90 days. |
| Trust anchor on server | A bundled public key in the server build (`keys/cloud-ent.pub.pem`). Chain-of-trust allows new keys IF signed by a still-valid old key. |
| Distribution | At claim (25.6), at handshake (`0x21 ENT_REFRESH`), and via daily cron + on-demand fetch. |
| Offline grace | 7 days; after that, cloud features off. |
| Revocation | List of revoked `kid`s in JWKS endpoint; server checks daily. |
| Out of scope | Mutual cloud authentication beyond the bundled trust anchor (v2). |

## 1. Migration `00100001_entitlement.sql` (slot 0010 per README)

This keystore is **separate** from `jwt_keys` (slot 0002, RS256
session tokens). They serve different purposes: `jwt_keys` signs
access tokens consumed by the cloud's own routes; `entitlement_keys`
signs Ed25519 entitlement blobs consumed by the on-prem server. Two
keystores by design.

```sql
-- +goose Up
CREATE TABLE entitlement_keys (
    fingerprint        TEXT PRIMARY KEY,           -- SHA-256 of public key bytes
    public_key         BYTEA NOT NULL,             -- 32-byte Ed25519 public key
    private_pem_sealed BYTEA NOT NULL,
    public_pem         TEXT NOT NULL,
    issued_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    retired_at         TIMESTAMPTZ,
    revoked_at         TIMESTAMPTZ,
    active             BOOLEAN NOT NULL DEFAULT TRUE,
    reason             TEXT                         -- populated when revoked
);
CREATE INDEX entitlement_keys_active_idx ON entitlement_keys(active) WHERE active;

CREATE TABLE entitlement_grants (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    server_id     UUID REFERENCES servers(id) ON DELETE SET NULL,
    tier          TEXT NOT NULL CHECK (tier IN ('free','pro','family')),
    interval      TEXT CHECK (interval IS NULL OR interval IN ('monthly','yearly')),
    issued_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    fingerprint   TEXT NOT NULL REFERENCES entitlement_keys(fingerprint),
    revoked_at    TIMESTAMPTZ
);
CREATE INDEX entitlement_grants_user_idx ON entitlement_grants(user_id);
CREATE INDEX entitlement_grants_server_idx ON entitlement_grants(server_id);

-- +goose Down
DROP TABLE IF EXISTS entitlement_grants, entitlement_keys;
```

## 2. Signer

```go
// cloud/internal/entitlement/sign.go
type Signer struct {
    repo    *Repo
    sealer  Sealer
    active  *atomic.Pointer[Key]   // {kid, priv}
}

type Key struct{ KID string; Priv ed25519.PrivateKey }

func (s *Signer) Sign(payload Payload) (string, error) {
    payload.Fingerprint = s.active.Load().KID
    payload.V = 1
    body, _ := json.Marshal(payload)
    canon, _ := jcs.Transform(body)
    sig := ed25519.Sign(s.active.Load().Priv, canon)
    return fmt.Sprintf("entitlement.%s.%s",
        base64.RawURLEncoding.EncodeToString(canon),
        base64.RawURLEncoding.EncodeToString(sig)), nil
}
```

JCS via `github.com/cyberphone/json-canonicalization`.

## 3. Payload shape

```go
type Payload struct {
    Iss         string                 `json:"iss"`           // "cloud.maktaba.app"
    Sub         string                 `json:"sub"`           // server_id
    UserID      string                 `json:"user_id"`
    Tier        string                 `json:"tier"`          // 'free' | 'pro' | 'family' (matches architecture.md §13.10)
    Interval    string                 `json:"interval,omitempty"` // 'monthly' | 'yearly'; omitted for free
    Suspended   bool                   `json:"suspended,omitempty"`
    IssuedAt    time.Time              `json:"issued_at"`
    ExpiresAt   time.Time              `json:"expires_at"`
    Features    map[string]any         `json:"features"`
    Fingerprint string                 `json:"kid"`           // pubkey fingerprint; named `kid` in wire form for OIDC familiarity
    V           int                    `json:"v"`
}
```

When `Suspended=true`, set all `features` to disabled; the verifier
on the server treats it the same as `tier=free` but also surfaces a
"subscription action required" prompt to the user.

## 4. Key rotation

Monthly cron (1st of month, 00:30 UTC):

```go
func RotateKeys(ctx context.Context, s *Signer) error {
    pub, priv, _ := ed25519.GenerateKey(rand.Reader)
    kid := fmt.Sprintf("ent-%s", time.Now().UTC().Format("2006-01"))
    pubPem := pemEncodePublic(pub)
    privPem := pemEncodePrivate(priv)
    sealed, _ := s.sealer.Seal(privPem)
    _, _ = s.repo.InsertKey(ctx, kid, sealed, pubPem)
    // Sign a "key intro" record with previous active key so servers can chain trust
    intro, _ := s.SignKeyIntro(ctx, kid, pubPem)
    _, _ = s.repo.InsertKeyIntro(ctx, kid, intro)
    // Retire previous after 90 days
    s.active.Store(&Key{KID: kid, Priv: priv})
    return nil
}
```

`SignKeyIntro` signs a `{kid, public_pem, issued_at}` blob with the *outgoing* private key so a server already trusting the old key can accept the new one.

## 5. JWKS endpoint

```go
// GET /.well-known/maktaba-ent-jwks.json
type JWKSPayload struct {
    Keys []KeyDoc `json:"keys"`
    KeyIntros []KeyIntroDoc `json:"key_intros"`
    Revocations []string `json:"revocations"`
}
```

CDN-cached 5min.

## 6. Distribution paths

### 6.1 At claim (25.6)

`s.signer.Sign(payload)` invoked from `claim` handler; included in response.

### 6.2 On handshake (25.8)

After handshake completes, the cloud emits `0x21 ENT_REFRESH` with a fresh entitlement.

```go
func (t *Tunnel) sendEntitlement(ctx context.Context, signer *Signer, repo *Repo) {
    user, _ := repo.GetUser(ctx, t.UserID)
    payload := buildPayload(t.ServerID, user)
    jws, _ := signer.Sign(payload)
    t.send(FrameEntRefresh, []byte(jws))
}
```

### 6.3 Daily cron

`PushFreshEntitlements`: iterate `servers WHERE deleted_at IS NULL AND last_seen_at >= now()-INTERVAL '7 days'`; if registry has the tunnel, emit `ENT_REFRESH`.

### 6.4 Pull

`GET /api/servers/{id}/entitlement` returns the latest signed blob (for server-suspected staleness).

## 7. Local server consumption (cloudlink, 25.7)

```go
// internal/cloudlink/entitlement.go
func (c *Conn) handleEntRefresh(ctx context.Context, body []byte) error {
    payload, err := c.verifier.Verify(string(body))
    if err != nil {
        c.storage.WipeEntitlement(ctx)
        c.metrics.EntInvalid.Inc()
        return err
    }
    c.storage.SaveEntitlement(ctx, body)
    c.observers.OnEntitlement(payload)   // local API reads feature flags
    return nil
}
```

Verifier first checks the bundled trust-anchor pubkey; if `kid` is not the bundled one, it consults cached `KeyIntros` chain.

`internal/cloudlink/grace.go`:

```go
// FeatureEnabled returns whether `cloud_relay`, `cloud_push`, etc. is allowed.
func FeatureEnabled(p *Payload, clock clock.Clock) bool {
    now := clock.Now()
    if now.After(p.ExpiresAt) && now.Sub(p.ExpiresAt) > 7*24*time.Hour {
        return false  // grace expired
    }
    if p.Suspended { return false }
    return true
}
```

The local server reads `Features.cloud_relay`, etc. for granular gating.

## 8. Revocation

`POST /api/admin/entitlement-keys/{fingerprint}/revoke` (admin only)
UPDATEs `entitlement_keys SET active=false, revoked_at=now(), reason=$1`.
Servers fetch the JWKS daily and honor revocations.

## 9. Test plan

### 9.1 Unit

| Test | Pins |
|---|---|
| `TestSignVerifyRoundtrip` | Sign + verify with known key passes. |
| `TestVerifyTamperedFails` | Flip a byte → reject. |
| `TestJCSDeterministic` | Two equivalent JSON shapes produce the same signature. |
| `TestPayloadKIDFilled` | Sign sets active KID. |
| `TestKeyIntroChainsTrust` | New `kid` accepted via intro signed by older `kid`. |

### 9.2 Integration

| Test | Pins |
|---|---|
| `TestClaimReturnsValidEntitlement` | Cross-test with 25.6. |
| `TestHandshakeEmitsEntitlement` | Tunnel handshake → `0x21` received within 1s. |
| `TestCronRefreshesNightly` | Over 5 simulated days, always 24h ahead. |
| `TestOffline6DaysFeatureOn` | Local-side: still on. |
| `TestOffline8DaysFeatureOff` | Local-side: off. |
| `TestRevokedKIDFeatureOff` | Add to revocation list; local refresh → off. |
| `TestServerBootsWithoutEntitlement` | LAN features unaffected; cloud features off. |
| `TestClockTamperingResistance` | 1y future clock → entitlement still rejected once past expires+8d. |

## 10. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Data key lost | Re-claim drops fresh copy. | Spec. |
| Clock tampering | `now() < issued_at + 8d` defense. | `TestClockTampering`. |
| Downscope | 24h window before next push reflects new tier. | Spec. |
| Family member servers | Each member's tier in their own entitlement. | Spec. |
| Suspended user | `suspended=true` entitlement disables features; tier still reflects last paid. | `TestSuspendedPayload`. |
| Compromised key | Revoke + rotate. | Doc. |
| Replay across servers | `Sub=server_id` binds. | `TestReplayAcrossServers`. |
| JCS bugs | Pin lib version; vector cases. | Spec. |
| Tier strings catalog | `tier IN ('free','pro','family')` × `interval IN ('monthly','yearly')`. Single canonical vocab shared with `architecture.md` §13.10, plan-16-04, plan-25-12, plan-25-13. | Cross-epic. |

## 11. Dependencies

- 25.1 (KMS data key for sealing).
- 25.6 (claim emits first entitlement).
- 25.7 (cloudlink consumes; `0x21` frame).
- 25.8 (handshake hooks).
- Epic 16 Story 16.4 (local validator pattern reused for verify).

## 12. Acceptance checklist

- [ ] Migration 00100001 applies.
- [ ] Signer + JWKS + revocations + intros.
- [ ] Distribution: claim + handshake + daily cron + on-demand.
- [ ] Server-side verifier with chained trust + 7-day grace.
- [ ] Local features honor `Features.cloud_*`.
- [ ] Tests in §9 pass.

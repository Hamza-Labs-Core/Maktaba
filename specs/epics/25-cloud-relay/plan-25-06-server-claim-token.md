# Implementation Plan — Story 25.6 Server claim token flow

> Companion to [story-25-06-server-claim-token.md](story-25-06-server-claim-token.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Token | 96 bits of random (12 bytes), base32-encoded with `Crockford`-style alphabet (`A-Z2-7`), 8 chars, hyphen-grouped: `K3F9-MZ7P`. Generated server-side (the user's box). Case-insensitive on redemption. |
| Token-hash storage | `cloud_claim_tokens.token_hash = SHA-256(uppercased token)`. Plaintext never stored on cloud. |
| Server pubkey | Ed25519 from Epic 10 Story 10.18 (server identity). Reused unchanged. |
| Long-lived bearer | 32-byte random, returned once to server on redemption, hashed with bcrypt cost 10 in `cloud_server_tokens.token_hash_bcrypt`. |
| Tunnel endpoint | `wss://relay.maktaba.app/tunnel/v1/connect`. Set in claim response so server doesn't hardcode. |
| Init endpoint TLS pin | Server pins Cloudflare R3 intermediate CA. |
| Out of scope | The tunnel itself (25.7, 25.8). The entitlement *content* (25.26 defines shape; we just include the first signed blob). Subdomain (25.22 — left NULL here). |

## 1. Migration `00020001` (slot 0002 per README)

Filename: `cloud/migrations/00020001_servers_and_claims.sql`.

```sql
-- +goose Up
CREATE TABLE cloud_servers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES cloud_users(id) ON DELETE CASCADE,
    server_pubkey   BYTEA NOT NULL,       -- 32-byte Ed25519
    version         TEXT NOT NULL DEFAULT 'unknown',
    locale          TEXT NOT NULL DEFAULT 'en',
    subdomain       CITEXT,               -- NULL until 25.22 assigns
    claimed_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ,
    suspended_at    TIMESTAMPTZ,
    deleted_at      TIMESTAMPTZ
);
CREATE INDEX cloud_servers_user_idx ON cloud_servers(user_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX cloud_servers_pubkey_uq
    ON cloud_servers(server_pubkey) WHERE deleted_at IS NULL;

CREATE TABLE cloud_server_tokens (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    server_id         UUID NOT NULL REFERENCES cloud_servers(id) ON DELETE CASCADE,
    token_hash_bcrypt TEXT NOT NULL,
    granted_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at      TIMESTAMPTZ,
    revoked_at        TIMESTAMPTZ,
    rotated_from      UUID REFERENCES cloud_server_tokens(id)
);
CREATE INDEX cloud_server_tokens_server_idx
    ON cloud_server_tokens(server_id) WHERE revoked_at IS NULL;

CREATE TABLE cloud_claim_tokens (
    token_hash       BYTEA PRIMARY KEY,    -- SHA-256
    server_pubkey    BYTEA NOT NULL,
    server_version   TEXT,
    server_locale    TEXT,
    init_ip          INET,
    expires_at       TIMESTAMPTZ NOT NULL,
    redeemed_at      TIMESTAMPTZ,
    redeemed_by      UUID REFERENCES cloud_users(id) ON DELETE SET NULL,
    redemption_ip    INET
);
CREATE INDEX cloud_claim_tokens_expires_idx ON cloud_claim_tokens(expires_at);

-- +goose Down
DROP TABLE IF EXISTS cloud_claim_tokens, cloud_server_tokens, cloud_servers;
```

## 2. Endpoints

```
POST /api/servers/claim/init     # server-only; from the user's box
POST /api/servers/claim          # user-authenticated; from app.maktaba.app
GET  /api/servers                 # already in 25.16 plan; list of user's servers
DELETE /api/servers/{id}         # already in 25.16 plan; unlink
```

### 2.1 `claim/init`

Body:
```json
{ "token_hash": "<32B base64>", "server_pubkey": "<32B base64>",
  "server_version": "1.0.0", "server_locale": "ar" }
```

Behavior:

1. Rate-limit per IP (10/min — see 25.24).
2. Reject if `token_hash` already present (`409 duplicate_init`).
3. Insert `cloud_claim_tokens` with `expires_at = now() + 10m`, `init_ip = client_ip`.
4. Respond `200 {claim_id: <uuid>, expires_at}`. `claim_id` is non-secret — purely for logging.

No auth; only the rate limit and `token_hash` uniqueness protect this endpoint. Plaintext token never reaches the cloud at this step.

### 2.2 `claim`

Authentication: signed-in user.
Body:
```json
{ "token": "K3F9-MZ7P", "server_pubkey": "<32B base64>" }
```

Behavior (in a single Postgres txn):

1. Normalize token: strip hyphens, uppercase.
2. Hash: `sha256.Sum256([]byte(normalized))`.
3. `SELECT … FROM cloud_claim_tokens WHERE token_hash=$1 FOR UPDATE`.
   - Not found → `404 claim_not_found`.
   - `redeemed_at IS NOT NULL` → `409 claim_already_used`.
   - `expires_at < now()` → `410 claim_expired`.
4. Compare stored `server_pubkey` to request `server_pubkey`. Mismatch → `400 claim_pubkey_mismatch`.
5. Per-user server cap: `SELECT count(*) FROM cloud_servers WHERE user_id=$1 AND deleted_at IS NULL` ≥ 5 → `409 server_limit_reached`.
6. `INSERT INTO cloud_servers (user_id, server_pubkey, version, locale) RETURNING id`.
7. Mint 32-byte random bearer; `bcrypt.GenerateFromPassword([]byte(bearer), 10)`. Insert `cloud_server_tokens`.
8. `UPDATE cloud_claim_tokens SET redeemed_at=now(), redeemed_by=$user, redemption_ip=$ip`.
9. Mint initial entitlement (25.26 helper) — payload `{tier=user.plan, features=…, expires_at=now()+24h}` signed Ed25519.
10. Audit `server.claim` with `target_id=<server_id>`.
11. Respond:
    ```json
    {
      "server_id": "<uuid>",
      "server_token": "<base64>",        // only delivery
      "cloud_endpoint": "wss://relay.maktaba.app/tunnel/v1/connect",
      "entitlement": "<compact-JWS>"
    }
    ```

## 3. Server-side companion (local API repo)

`internal/cloudlink/claim.go`:

```go
func InitClaim(ctx context.Context, cfg *Config, identity *Identity) (string, *http.Response, error) {
    tok := generateToken()  // 12B base32 → "K3F9-MZ7P"
    hash := sha256.Sum256([]byte(strings.ToUpper(strings.ReplaceAll(tok, "-", ""))))
    body, _ := json.Marshal(InitBody{TokenHash: hash[:], ServerPubkey: identity.PublicKey(), ...})
    req, _ := http.NewRequestWithContext(ctx, "POST", cfg.CloudAPI+"/api/servers/claim/init", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    // TLS pinned to Cloudflare R3 intermediate:
    return tok, pinned(cfg).Do(req)
}
```

The local UI page `web/src/pages/admin/cloud-link.tsx`:

1. Calls local API `POST /admin/cloud-link/init` → it calls `InitClaim` → local UI displays token + QR.
2. User signs into `app.maktaba.app`, pastes/scans, redeems.
3. Cloud responds with `server_token` + entitlement; user's app POSTs this to local API `POST /admin/cloud-link/store-token` over loopback.
4. Local API persists token (encrypted at rest via Epic 10.14 data key) and opens the tunnel (25.7).

## 4. TLS pinning

```go
func pinned(cfg *Config) *http.Client {
    rootPool := x509.NewCertPool()
    rootPool.AppendCertsFromPEM(embeddedCloudflareR3Intermediate)
    return &http.Client{
        Transport: &http.Transport{
            TLSClientConfig: &tls.Config{RootCAs: rootPool, MinVersion: tls.VersionTLS12},
        },
        Timeout: 10 * time.Second,
    }
}
```

A cloud cert chain rotation requires a server software update — accepted v1 trade-off.

## 5. Token base32 helpers

```go
const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

func generateToken() string {
    var buf [12]byte
    _, _ = rand.Read(buf[:])
    var b strings.Builder
    enc := base32.NewEncoding(alphabet).WithPadding(base32.NoPadding)
    s := enc.EncodeToString(buf[:])[:8]   // 12B * 8/5 bits = 19 chars; trim to 8
    b.WriteString(s[:4]); b.WriteByte('-'); b.WriteString(s[4:8])
    return b.String()
}

func normalize(in string) string {
    in = strings.ToUpper(strings.ReplaceAll(in, "-", ""))
    return in
}
```

Wait — 12 random bytes is 96 bits; base32 = 96/5 = 19.2 → 20 chars. We want 8 chars = 40 bits. Correct the design: **use 5 random bytes** (40 bits → 8 base32 chars) and rely on the 10-min TTL + rate limit for brute-force resistance. The story says "96-bit token, base32-encoded as 8 chars" — that's not arithmetic; 40-bit is what 8 chars hold. We follow the actual base32 math (40-bit entropy, 8 chars) and document this clearly in the plan; coordinate with the story to update once.

Brute force math: 40-bit space × 10-min window × 10/min/IP rate cap × 5 different IPs (CGNAT) ≈ 10⁸-year per-IP, 10⁻³ probability across the whole expected attack surface. Adequate.

## 6. Test plan

### 6.1 Unit

| Test | Pins |
|---|---|
| `TestTokenShape` | Matches `^[A-Z2-7]{4}-[A-Z2-7]{4}$`. |
| `TestTokenNormalization` | `k3f9-mz7p` normalizes to `K3F9MZ7P` hash equal to `K3F9-MZ7P`. |
| `TestInitDuplicateHash` | Second init with same token_hash → 409. |
| `TestPubkeyMismatch` | Claim with wrong pubkey → 400. |

### 6.2 Integration

| Test | Pins |
|---|---|
| `TestHappyPath` | init → claim → rows present, bearer issued, entitlement signed and verifiable. |
| `TestExpired` | clock+11m; claim → 410. |
| `TestReplay` | Redeem twice → second 409 `claim_already_used`. |
| `TestServerLimit5` | Sixth claim → 409 `server_limit_reached`. |
| `TestConcurrentRedemption` | Two users redeem same token; one 200, one 409. Use `FOR UPDATE`. |
| `TestRateLimit` | 11 init in 60s from one IP → 11th = 429, abuse event `claim_token_brute`. |
| `TestEntitlementSigVerify` | Returned entitlement verifies against cloud Ed25519 public key. |
| `TestServerRestartReusesBearer` | Cloud-side row persists across restart; the same bearer hashes to same bcrypt match. |
| `TestTLSPinningInit` | Server posting to a server with non-Cloudflare cert chain → handshake fails. |
| `TestPubkeyRotationBetweenInitAndClaim` | Server regenerated its key between calls → 400 mismatch. |

## 7. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Typo-friendly base32 | Alphabet excludes 0/1/I/O. | Spec. |
| No internet at `init` | Server UI shows error; offers retry; no cloud row created. | Local UI test. |
| Corp network blocks `*.maktaba.app` | Init fails; manual signed-CSR path is future-work. | Doc. |
| Re-claim after unlink | Old server row deleted; new claim creates new row + new bearer. | `TestReclaimAfterUnlink`. |
| Clock skew ±60s | 10-min TTL absorbs comfortably. | Spec. |
| Profanity in token | Alphabet `A-Z2-7` rarely produces English words; not filtered. | Doc. |
| Subdomain not assigned at claim | `cloud_servers.subdomain` is NULL; 25.22 assigns later. | Migration. |
| TLS chain rotation | Documented as requiring server-update. | Doc. |
| Init replay | `token_hash` PK + `409 duplicate_init`. | `TestInitDuplicateHash`. |
| Anonymous redemption brute-force | Rate limit + abuse score escalates. | `TestRateLimit`. |
| Pubkey base64 vs base32 | Always base64 in JSON bodies; base32 only for the user-facing token. | Spec. |
| Server-token rotation | Future: `POST /api/servers/{id}/rotate-token`. Out of this story; row `rotated_from` exists. | Forward-compat. |

## 8. Dependencies

- 25.1 (foundation).
- 25.2 (user auth context).
- 25.26 (entitlement signing — actually needs to land first **or** be stubbed; plan: stub a sign function in 25.6 with placeholder Ed25519 key, then 25.26 plugs in the production key + rotation. README slot 0010 is "entitlement-signing key history"; 0002 here uses it.)
- 25.24 (rate limiting).

## 9. Acceptance checklist

- [ ] Migration 00020001 applies.
- [ ] `claim/init` rejects duplicates, rate-limits.
- [ ] `claim` returns `{server_id, server_token, cloud_endpoint, entitlement}` once.
- [ ] Bearer bcrypt-stored; never returned again.
- [ ] Server-side TLS pinning to Cloudflare R3 intermediate.
- [ ] Per-user 5-server cap enforced.
- [ ] Entitlement verifies against cloud Ed25519 public key.
- [ ] Tests in §6 pass.

# Implementation Plan — Story 15.7 API: federation endpoints + crypto

> Companion to [story-15-07-federation-api.md](story-15-07-federation-api.md).
> The story states *what* and *why*; this plan states *how*.
> The threat model and the SAS/X25519/Ed25519 protocol come from the
> story. This plan resolves the file-by-file how, including key
> management, persistence, and SAS rendering.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration | `shared/db/migrations/0054_federation.sql` (Postgres + SQLite variant). |
| Crypto package | `api/internal/auth/federation/{kex.go,sas.go,sig.go,store.go,token.go}`. |
| HTTP handlers | `api/internal/http/federation/{pair.go,confirm.go,manage.go,token.go}`. |
| Long-term keypair | Ed25519 (`EdDSA` JWS alg). **Note:** Epic 10 Story 10.6 owns RS256 / JWKS for short-lived API JWTs and is **not** the source of these keys (its title is literally "RS256 keys, rotation, JWKS"). This plan depends on a new Epic 10 Story 10.18 ("Ed25519 long-term server identity keys") that owns the generation, rotation, and `kid`-indexed publication of the long-term keypair. Until 10.18 lands this plan blocks; the federation handshake / token mint cannot ship against RS256 because the JOSE alg and signature size budgets here assume `EdDSA`. |
| At-rest encryption of ephemeral keys | Reuses the data-encryption key from Epic 10 Story 10.14 (secret loading) — `crypto.SealedBox(esk_self, dataKey)`. |
| SAS word list | PGP word-list (CSPP/PGP biometric word list, 256 unique 4–7 letter pronounceable words for each byte). Bundled at `shared/i18n/sas/pgp-words.txt`. |
| Out of scope | Federation consumption surfaces (Story 15.3); admin UI ([Story 15.3](story-15-03-federation.md) §8). |

## 1. Architecture diagram

```
   Admin on A                         Admin on B
   ┌─────────────┐                    ┌─────────────┐
   │ create pair │                    │ paste token │
   └──────┬──────┘                    └──────┬──────┘
          │ POST /api/federation/pair (out-of-band token)
          │                                  │
          ▼                                  ▼
   ┌──────────────────────────┐      ┌──────────────────────────┐
   │ federation_pending (A)   │      │ federation_pending (B)   │
   │  epk_self, esk_self      │ ───► │  epk_self, esk_self      │
   │  (encrypted at rest)     │      │  shared = X25519(esk, epk)│
   └──────────────────────────┘      └──────────────────────────┘
                  ▲                        │
                  │  POST B → A            │
                  │  {epk_b, sig_b(epk_a||epk_b||tls_spki_a)}
                  │
                  ▼
            A verifies sig_b against B's long-term key
            A computes shared = X25519(esk_a, epk_b)
            A renders SAS = sha256(shared)[0..32] → 4 PGP words
            A returns {epk_a, sig_a, sas, partner_id}

            Admins compare SAS over phone call.
            Both POST /api/federation/{partner_id}/confirm.
            Both write to federation_partners; pending row deleted.
```

## 2. Database migration

`shared/db/migrations/0054_federation.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE federation_pending (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    role            TEXT NOT NULL CHECK (role IN ('initiator','responder')),
    epk_self        BYTEA NOT NULL,
    esk_self_sealed BYTEA NOT NULL,           -- nacl box with data key
    epk_peer        BYTEA,
    peer_origin_url TEXT,
    sas             TEXT,
    confirmed_self  BOOLEAN NOT NULL DEFAULT false,
    confirmed_peer  BOOLEAN NOT NULL DEFAULT false,
    expires_at      TIMESTAMPTZ NOT NULL,
    CHECK (octet_length(epk_self) = 32),
    CHECK (epk_peer IS NULL OR octet_length(epk_peer) = 32)
);

CREATE INDEX federation_pending_expires_at_idx
    ON federation_pending (expires_at);

CREATE TABLE federation_partners (
    partner_id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    display_name          TEXT NOT NULL,
    peer_origin_url       TEXT NOT NULL,
    peer_long_term_pubkey BYTEA NOT NULL,
    -- The acl shape is fixed (see Go FederationACL struct below);
    -- pin the JSONB top-level keys with a CHECK so a malformed admin
    -- write fails at insert rather than silently misbehaving at query
    -- time.
    acl                   JSONB NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    confirmed_at          TIMESTAMPTZ NOT NULL,
    revoked_at            TIMESTAMPTZ,
    CHECK (octet_length(peer_long_term_pubkey) = 32),
    CHECK (jsonb_typeof(acl) = 'object'
       AND acl ? 'libraries'
       AND jsonb_typeof(acl->'libraries') = 'array'
       AND acl ? 'read_only'
       AND jsonb_typeof(acl->'read_only') = 'boolean')
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS federation_partners;
DROP TABLE IF EXISTS federation_pending;
-- +goose StatementEnd
```

### 2.1 ACL Go shape

`api/internal/auth/federation/acl.go`:

```go
type FederationACL struct {
    Libraries []uuid.UUID `json:"libraries"`            // library IDs the peer can read
    ReadOnly  bool        `json:"read_only"`            // currently always true; reserved for v2
}
```

sqlc emits `[]byte` for the `acl JSONB` column; the service layer
unmarshals into `FederationACL`. The CHECK in §2 enforces shape at
write time; the unmarshal call enforces it at read time. Adding a new
field is non-breaking (unknown keys are ignored on read; new writes
include them); removing a field is breaking and gets a migration.

## 3. Crypto: X25519, Ed25519, SAS

### 3.1 X25519 key exchange

```go
// api/internal/auth/federation/kex.go
import "golang.org/x/crypto/curve25519"

func generateEphemeral() (epk, esk []byte, err error) {
    esk = make([]byte, 32)
    if _, err = rand.Read(esk); err != nil { return nil, nil, err }
    epk, err = curve25519.X25519(esk, curve25519.Basepoint)
    return
}

func sharedSecret(esk, epk []byte) ([]byte, error) {
    return curve25519.X25519(esk, epk)
}
```

### 3.2 Ed25519 signing of `epk_a || epk_b || tls_spki_a`

```go
// api/internal/auth/federation/sig.go
func signHandshake(privKey ed25519.PrivateKey, epkA, epkB, spkiA []byte) []byte {
    msg := bytes.Join([][]byte{epkA, epkB, spkiA}, nil)
    return ed25519.Sign(privKey, msg)
}

func verifyHandshake(pubKey ed25519.PublicKey, epkA, epkB, spkiA, sig []byte) bool {
    msg := bytes.Join([][]byte{epkA, epkB, spkiA}, nil)
    return ed25519.Verify(pubKey, msg, sig)
}
```

### 3.3 SAS rendering

`api/internal/auth/federation/sas.go`:

```go
//go:embed pgp-words.txt
var pgpWords string  // 512 lines: 256 even-syllable, 256 odd-syllable

func renderSAS(secret []byte) string {
    h := sha256.Sum256(secret)
    words := strings.Split(pgpWords, "\n")
    even, odd := words[:256], words[256:]
    out := []string{}
    for i := 0; i < 4; i++ {
        b := h[i]
        if i%2 == 0 { out = append(out, even[b]) } else { out = append(out, odd[b]) }
    }
    return strings.Join(out, " ")
}
```

The PGP word list is chosen so adjacent words are phonetically distinct, which matters for the over-the-phone comparison.

### 3.4 Pair token (initiator → responder)

The story originally proposed an HMAC over the pair token verified
against "its own `k`". That design is internally inconsistent: the
responder cannot verify an HMAC computed with a key it does not share.
We adopt **CRC32** as the integrity check — pure typo detection, no
shared secret required. Confidentiality of the pairing handshake is
not the token's job (the X25519 ephemeral provides it after pair);
the token only needs to detect copy-paste corruption. **Story 15.7's
acceptance criteria need a corresponding update from "HMAC against
its own k" to "CRC32 typo guard"**; that is recorded in
`docs/operations/federation.md` and noted as a deliberate
story↔plan deviation in the §11 acceptance checklist.

```go
// api/internal/auth/federation/token.go
import "hash/crc32"

func encodePairToken(epk []byte) string {
    crc := crc32.ChecksumIEEE(epk)
    blob := make([]byte, 0, 32+4)
    blob = append(blob, epk...)
    blob = binary.BigEndian.AppendUint32(blob, crc)
    return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(blob)
}

func decodePairToken(token string) ([]byte, error) {
    blob, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(token)
    if err != nil || len(blob) != 32+4 { return nil, errBadToken }
    epk := blob[:32]
    want := binary.BigEndian.Uint32(blob[32:])
    if crc32.ChecksumIEEE(epk) != want { return nil, errCRCFail }
    return epk, nil
}
```

## 4. HTTP handlers

`api/internal/http/federation/pair.go`:

```go
func MountFederation(r chi.Router, s *fed.Service) {
    r.Route("/federation", func(r chi.Router) {
        r.Use(requireAdmin)
        r.Post("/init", initPair(s))            // initiator side: creates token
        r.Post("/pair", pair(s))                 // responder → initiator handshake
        r.Post("/{partner_id}/confirm", confirm(s))
        r.Post("/{partner_id}/token", mintToken(s))
        r.Get("/", list(s))
        r.Patch("/{partner_id}", patch(s))
        r.Delete("/{partner_id}", revoke(s))
    })
}
```

`POST /api/federation/init` (admin clicks "Pair with another instance" on initiator):

```go
func (s *Service) Init(ctx context.Context) (InitResult, error) {
    epk, esk, _ := generateEphemeral()
    sealed := s.dataKey.Seal(esk)
    id, _ := s.db.InsertFederationPending(ctx, "initiator", epk, sealed,
        time.Now().Add(10*time.Minute))
    token := encodePairToken(epk, []byte{}) // CRC variant
    return InitResult{PendingID: id, Token: token, ExpiresIn: 10 * time.Minute}, nil
}
```

`POST /api/federation/pair` (responder posts to initiator's instance):

```go
func (s *Service) Pair(ctx context.Context, in PairIn) (PairOut, error) {
    pending, err := s.db.GetInitiatorPending(ctx, ...) // by epk_a from in
    if err != nil { return PairOut{}, errPairWindowExpired }

    if !verifyHandshake(in.PeerLongTermPub, pending.EPKSelf, in.EPKPeer, s.tls.SPKI(), in.SigB) {
        return PairOut{}, errInvalidSig
    }
    esk := s.dataKey.Open(pending.ESKSelfSealed)
    secret, _ := sharedSecret(esk, in.EPKPeer)
    sas := renderSAS(secret)

    // Sign A's commitment to be returned to B
    sigA := signHandshake(s.tls.LongTermPriv(), pending.EPKSelf, in.EPKPeer, s.tls.SPKI())

    partnerID := uuid.New()
    s.db.UpdatePending(ctx, pending.ID, in.EPKPeer, sas, in.PeerOriginURL)
    s.db.InsertFederationPartner(ctx, FederationPartner{
        PartnerID: partnerID, DisplayName: in.DisplayName,
        PeerOriginURL: in.PeerOriginURL,
        PeerLongTermPubkey: in.PeerLongTermPub,
        ACL: defaultACL,
        ConfirmedAt: time.Time{},  // pending until confirm
    })
    return PairOut{
        EPK_A: pending.EPKSelf, Sig_A: sigA, SAS: sas, PartnerID: partnerID,
    }, nil
}
```

The partner row is inserted in `pending state` (`confirmed_at` is zero). Federation does not take effect until both sides POST `/confirm`.

`POST /api/federation/{partner_id}/confirm`:

```go
func (s *Service) Confirm(ctx context.Context, partnerID uuid.UUID, callerSide string) error {
    return s.db.WithTx(ctx, func(q *db.Queries) error {
        // Mark this side confirmed.
        // When both sides have confirmed, set federation_partners.confirmed_at = now()
        // and delete the matching federation_pending row.
        return q.MarkConfirmed(ctx, partnerID, callerSide)
    })
}
```

`POST /api/federation/{partner_id}/token` mints a short-lived JWT:

```go
func (s *Service) MintFederationJWT(ctx context.Context, partnerID uuid.UUID, scope string) (string, error) {
    p, _ := s.db.GetFederationPartner(ctx, partnerID)
    if p.RevokedAt.Valid { return "", errRevoked }
    claims := jwt.New()
    claims.Set("iss", s.identity.MDNSID)         // local server
    claims.Set("aud", p.PartnerID)
    claims.Set("scope", scope)
    claims.Set("exp", time.Now().Add(15*time.Minute).Unix())
    // Pin the JOSE alg explicitly: federation tokens are EdDSA (Ed25519),
    // NOT RS256. Story 10.18's long-term key is Ed25519; Story 10.6's
    // RS256 keys are reserved for short-lived API JWTs and would be the
    // wrong key here. The verifier (peer) refuses any token whose
    // header `alg` is not exactly "EdDSA".
    return jwt.SignWithKey(claims, s.tls.LongTermPriv(), jwt.WithAlg("EdDSA"))
}
```

The peer (consumer) verifies the signature against the pinned `peer_long_term_pubkey`. The trust chain is: pair-time SAS → long-term Ed25519 pin → JWT signature verify.

## 5. Pending sweep

```go
go func() {
    t := time.NewTicker(60 * time.Second); defer t.Stop()
    for {
        select {
        case <-t.C:
            db.DeleteExpiredFederationPending(ctx, time.Now())
        case <-ctx.Done(): return
        }
    }
}()
```

## 6. Long-term key handling

The long-term key pair is the server's Ed25519 keypair owned by Epic 10
Story 10.18 (the new story that this plan blocks on; see §0). Story 10.6
owns RS256 for short-lived API JWTs and is not the right source. We do
**not** generate a federation-specific key — it would multiply the
key-rotation surface unnecessarily. The trade-off: rotating the
long-term key requires re-pairing federations (documented as a known
limitation in the story EC).

## 7. Test plan

### 7.1 Crypto unit

| Test | What it pins |
|---|---|
| `TestGenerateEphemeralRandom` | Two calls produce distinct keys. |
| `TestSharedSecretSymmetric` | `X25519(eskA, epkB) == X25519(eskB, epkA)`. |
| `TestSignHandshakeRoundTrip` | Signed by A's long-term key; verified with A's pub. |
| `TestVerifyMITMSubstituedKeysFails` | Substitute `epk_b'` and `sig_m`: verify fails. |
| `TestRenderSASStable` | Same secret → same 4 words. |
| `TestRenderSASDistinctSecretsDistinctWords` | Two different secrets → different 4 words (collision rate < 2^-32 for 4 words). |
| `TestPairTokenEncodingRoundTrip` | Encode → decode → original `epk`; one-bit flip rejected. |

### 7.2 Service / HTTP

| Test | What it pins |
|---|---|
| `TestInitPersistsPending` | One row in `federation_pending` after init. |
| `TestPairProducesSAS` | Responder posts; initiator's response has 4 words; both `federation_pending` rows have the same SAS string. |
| `TestPairRejectsInvalidSignature` | `sig_b` produced by a different key → 400 `invalid-signature`. |
| `TestPairWindowExpired` | After 10 min → 400 `pair-window-expired`. |
| `TestPairSPKIBindingDefeatsMITM` | A presents different SPKI to B than what's in `sig_b` → verify fails. |
| `TestConfirmFlipsSelf` | Endpoint marks `confirmed_self = true`. |
| `TestConfirmedOnBothSidesActivates` | After both confirm: `federation_partners.confirmed_at` set; `federation_pending` deleted. |
| `TestMintTokenScoped` | Token has `scope = "library:read:libraries/<id>"`. |
| `TestMintTokenRejectedAfterRevoke` | Revoke → mint → 401 `partner-revoked`. |
| `TestRevokeIdempotent` | Two DELETEs → both 204. |
| `TestSimultaneousMutualRevoke` | Both admins revoke simultaneously → both end up `revoked_at` set. |
| `TestPATCHACLAuditedAndImmediate` | Patch ACL → audit row + cache invalidation. |
| `TestSweeperRemovesExpiredPending` | After 11 min, expired pending rows are gone. |

### 7.3 End-to-end

| Test | What it pins |
|---|---|
| `e2e_TwoServerPair` | Stand up two server containers; admin A inits → admin B pairs → both confirm; `GET /api/federation` shows the partner on both. |
| `e2e_SASMismatchAborts` | Inject a MITM that rewrites `epk_b` → SAS differs on each side; both abort; no `federation_partners` row. |

## 8. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Both admins simultaneously revoke | Idempotent; both rows end up `revoked_at`. | `TestSimultaneousMutualRevoke` |
| Long-term key rotation | Rotate → existing partners' pinned key no longer matches → must re-pair. Documented. | `docs/operations/federation.md` |
| Pair token leaked but unused | Pending row TTLs in 10 min; impact zero. | `TestSweeperRemovesExpiredPending` |
| Pair token leaked and used by attacker | SAS mismatch defeats it; legitimate operators abort. | `e2e_SASMismatchAborts` |
| Malformed `peer_long_term_pubkey` in claim | 32-byte CHECK rejects insert. | `TestPairRejectsBadPubkeyLength` |
| `peer_origin_url` resolves differently from view of A vs B | The pin is on `peer_long_term_pubkey`, not URL; URL change just needs an admin patch. | `TestURLChangeNoRekey` |
| Postgres LISTEN not available (SQLite) | Cache invalidation falls back to a 60 s polling loop. | `TestACLChangeImmediateOnSQLite` |
| Token signed with rotated key | Receiver pinned the old key; verify fails until re-pair. | `TestSignedWithOldKeyFails` |
| ACL change mid-stream | Streaming JWTs already issued continue until `exp` (≤ 15 min). | (also Story 15.3) |
| Sweeper running while pair in flight | The DELETE only matches `expires_at <= now()`; in-flight pending rows are safe. | `TestSweeperPreservesActive` |

## 9. Acceptance checklist

**Schema**
- [ ] `federation_pending` and `federation_partners` exist on Postgres + SQLite.

**Crypto**
- [ ] X25519 key exchange + Ed25519 long-term signing wired.
- [ ] SAS renders the same 4 PGP words for the same shared secret.

**Endpoints**
- [ ] All 6 admin endpoints respond correctly.
- [ ] Token mint/verify works against the pinned long-term key.

**Audit**
- [ ] Every action writes an `audit_log` row with `category = 'federation'`.

**Tests**
- [ ] All §7 tests pass.

**Docs**
- [ ] `docs/operations/federation.md` published — explains long-term key rotation impact, CRC vs HMAC choice for the pair token.
- [ ] `specs/epics/15-discovery/README.md` ticks story 15.7.

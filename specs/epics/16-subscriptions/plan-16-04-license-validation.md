# Implementation Plan — Story 16.4 License key validation

> Companion to [story-16-04-license-validation.md](story-16-04-license-validation.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| License JSON & signature | Ed25519 over a canonicalized JSON body (RFC 8785 JCS for stability). |
| License-server public key | Bundled at build time in `api/internal/license/embedded_pubkey.go` from `keys/license-server.pub.pem` (committed). Rotation requires a release. |
| Storage | `licenses` table (singleton) holding the active license + cached refresh + revocation list. |
| Validator | `api/internal/license/validator.go` — pure Go; no network. |
| Refresher | `api/internal/license/refresher.go` — daily fetch from license server; 30-day offline grace. |
| Revocation list (CRL) | Fetched daily; stored in `license_revocations`. |
| HTTP endpoints | `POST /api/admin/license`, `DELETE /api/admin/license`, `GET /api/admin/license` (status only, no key). |
| Out of scope | Tier feature gates (already in [Story 16.2](story-16-02-premium-features.md)); webhooks (Story 16.3). |

## 1. License JSON shape

```json
{
  "license_id":  "lic_01HX0AAAA...",
  "tier":        "home",
  "seats":       4,
  "issued_at":   "2026-01-15T00:00:00Z",
  "expires_at":  "2027-01-15T00:00:00Z",
  "customer":    "stripe_cus_xxx",
  "kid":         "k1",
  "signature":   "<base64 Ed25519 over JCS(body without 'signature')>"
}
```

JCS canonicalization handles JSON's representation flexibility (ordered keys, no whitespace, no trailing zeros) so different serializations of the same body produce the same signature.

## 2. Validator

```go
// api/internal/license/validator.go
type Validator struct {
    pubkeysByKID map[string]ed25519.PublicKey  // bundled at build
    clock        Clock                          // injectable for tests
    crl          CRL                            // revocation set
}

type ValidationResult struct {
    Tier      string
    Seats     int
    LicenseID string
    ExpiresAt time.Time
    Reason    string   // empty on success, else "expired" | "revoked" | "bad-signature" | "tampered"
}

func (v *Validator) Validate(raw []byte) (ValidationResult, error) {
    var lic License
    if err := json.Unmarshal(raw, &lic); err != nil {
        return ValidationResult{Reason: "tampered"}, err
    }
    pub, ok := v.pubkeysByKID[lic.KID]
    if !ok { return ValidationResult{Reason: "unknown-kid"}, ErrUnknownKID }

    body, _ := jcs.Canonicalize(raw, []string{"signature"})  // strip signature before hash
    sig, _ := base64.StdEncoding.DecodeString(lic.Signature)
    if !ed25519.Verify(pub, body, sig) {
        return ValidationResult{Reason: "bad-signature"}, ErrBadSignature
    }
    if v.crl.Contains(lic.LicenseID) {
        return ValidationResult{Reason: "revoked"}, ErrRevoked
    }
    now := v.clock.Now()
    if now.After(lic.ExpiresAt) {
        return ValidationResult{Reason: "expired", LicenseID: lic.LicenseID}, ErrExpired
    }
    return ValidationResult{
        Tier: lic.Tier, Seats: lic.Seats, LicenseID: lic.LicenseID, ExpiresAt: lic.ExpiresAt,
    }, nil
}
```

## 3. Storage

`shared/db/migrations/0063_licenses.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE licenses (
    id                SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    raw               TEXT NOT NULL,             -- the full JSON, signature included
    license_id        TEXT NOT NULL,
    tier              TEXT NOT NULL,
    seats             INTEGER NOT NULL,
    expires_at        TIMESTAMPTZ NOT NULL,
    last_refreshed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_status       TEXT NOT NULL DEFAULT 'active'
);

CREATE TABLE license_revocations (
    license_id   TEXT PRIMARY KEY,
    revoked_at   TIMESTAMPTZ NOT NULL,
    fetched_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS license_revocations;
DROP TABLE IF EXISTS licenses;
-- +goose StatementEnd
```

Note: `licenses` is a **singleton row** (id = 1). Multi-license support is out of scope; one server has one active license.

## 4. Apply / remove endpoints

```go
// POST /api/admin/license  body = full license JSON
func apply(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
        result, err := s.Validate(body)
        if err != nil {
            problem(w, 422, "invalid-license", "reason="+result.Reason); return
        }
        if err := s.UpsertActive(r.Context(), body, result); err != nil {
            problem(w, 500, "internal", ""); return
        }
        s.audit(r.Context(), "license-applied", result.LicenseID)
        s.flags.NotifyLicenseChange()  // triggers feature-flag invalidation (Story 16.8)
        writeJSON(w, 200, map[string]any{
            "tier": result.Tier, "expires_at": result.ExpiresAt, "seats": result.Seats,
        })
    }
}

// DELETE /api/admin/license — remove active license, fall back to free.
func remove(s *Service) http.HandlerFunc { ... }

// GET /api/admin/license — read status; never returns the raw key.
func status(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        st, _ := s.Status(r.Context())
        writeJSON(w, 200, map[string]any{
            "tier": st.Tier, "expires_at": st.ExpiresAt, "seats": st.Seats,
            "last_refreshed_at": st.LastRefreshedAt, "last_status": st.LastStatus,
        })
    }
}
```

The `raw` field never appears in any response body; the AC: "License keys are never logged or returned by `/api/settings`; admin can paste in but the field is write-only after submission."

## 5. Refresher + offline grace

```go
// api/internal/license/refresher.go
type Refresher struct {
    s     *Service
    httpc *http.Client
    cfg   Config         // license server URL, offline grace = 30d
}

func (r *Refresher) Run(ctx context.Context) {
    t := time.NewTicker(24 * time.Hour); defer t.Stop()
    for {
        select {
        case <-t.C: r.tick(ctx)
        case <-ctx.Done(): return
        }
    }
}

func (r *Refresher) tick(ctx context.Context) {
    raw, err := r.fetchActive(ctx)
    if err != nil {
        // Server unreachable. Honor offline grace.
        st, _ := r.s.Status(ctx)
        if time.Since(st.LastRefreshedAt) > r.cfg.OfflineGrace {
            r.s.MarkLocked(ctx, "offline-grace-expired")
            r.s.flags.NotifyLicenseChange()
        }
        return
    }
    result, err := r.s.Validate(raw)
    if err != nil {
        // Server returned an invalid license? Log and keep cached.
        return
    }
    r.s.UpsertActive(ctx, raw, result)
    r.s.flags.NotifyLicenseChange()
    r.fetchCRL(ctx)
}
```

The 30-day offline grace is honored by the **resolver**, not the refresher: the resolver checks `last_refreshed_at` on every read. If the gap exceeds the grace window AND no fresh fetch succeeded, the resolver returns `free` and writes an admin notification.

## 6. CRL fetch

```go
func (r *Refresher) fetchCRL(ctx context.Context) error {
    body, err := r.httpc.Get(r.cfg.LicenseServer + "/crl")
    if err != nil { return err }
    defer body.Body.Close()
    var crl struct {
        SignedAt time.Time `json:"signed_at"`
        Revoked  []string  `json:"revoked"`
        Sig      string    `json:"sig"`
    }
    if err := json.NewDecoder(body.Body).Decode(&crl); err != nil { return err }
    // CRL is also Ed25519-signed; verify before trusting.
    if !verifyCRL(r.s.pubkeyOfKID(...), crl) { return ErrCRLBadSig }
    return r.s.UpsertCRL(ctx, crl.Revoked)
}
```

If a revocation arrives while the server is offline (EC: "Revocation reaches the server while offline: we use the last-known list; on reconnect we re-evaluate"), the resolver consults the cached CRL only; on reconnect, the next CRL fetch supersedes.

## 7. Clock-manipulation defense

The story EC: "Clock manipulation (user sets system clock back): we trust the license server's `expires_at` over local time when reachable."

Implementation:
- The refresher records `last_refreshed_at` server-side based on the response timestamp from the license server (a `Date` HTTP header validated against a low/high bound).
- The resolver compares `licenses.expires_at` to the **larger** of `local now()` and `last_refreshed_at`, so a clock rewind cannot extend a license.

```go
func (s *Service) effectiveNow() time.Time {
    n := s.clock.Now()
    st, _ := s.statusCached()
    if st.LastRefreshedAt.After(n) { return st.LastRefreshedAt }
    return n
}
```

## 8. Test plan

### 8.1 Validator

| Test | What it pins |
|---|---|
| `TestValidatorAcceptsValidLicense` | Sign with real test key; validator returns `Tier: home`. |
| `TestValidatorRejectsTamperedJSON` | Mutate one byte; signature fails. |
| `TestValidatorRejectsExpired` | `expires_at < now()` → ErrExpired. |
| `TestValidatorRejectsRevoked` | Add to CRL; ErrRevoked. |
| `TestValidatorRejectsUnknownKID` | KID not in bundle. |
| `TestValidatorJCSCanonicalization` | Two equivalent JSON bodies (different whitespace) produce the same signature. |

### 8.2 Endpoints

| Test | What it pins |
|---|---|
| `TestApplyValidLicenseUnlocksWithin5s` | Apply; 5 s later flags reflect tier. |
| `TestApplyTamperedRejected` | 422 invalid-license; tier remains free. |
| `TestStatusNeverReturnsRaw` | Response shape excludes `raw`. |
| `TestRemoveDropsToFree` | DELETE → tier = free; persisted. |

### 8.3 Refresher / offline

| Test | What it pins |
|---|---|
| `TestOfflineGraceWithin30d` | License server unreachable; tier preserved up to 30 days. |
| `TestOfflinePast30dLocks` | After 35 days offline, tier flips to free with `offline-grace-expired` reason. |
| `TestRefresherUpdatesLastRefreshedAt` | Successful fetch advances `last_refreshed_at`. |
| `TestCRLArrivesAndLocks` | CRL adds the active license id; next resolver call → free with `revoked`. |
| `TestClockRewindDoesNotExtend` | Rewind local clock 1 year; resolver uses `last_refreshed_at` so license still expires on schedule. |

### 8.4 End-to-end

| Test | What it pins |
|---|---|
| `e2e_LicenseLifecycle` | Apply → tier home → simulate 35d offline → free → reconnect with new license → home. |

## 9. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Tampered license JSON | 422 invalid-license; tier stays free. | `TestApplyTamperedRejected` |
| Server clock skew | License server's reachable response; we trust its `Date` header (validated). | `TestClockRewindDoesNotExtend` |
| Seats=4 but 5 users exist | Existing 5 keep working read-only; new logins refused; admin warned. | `TestSeatsExceedReadOnly` |
| CRL fetch fails after license server reachable | Cached CRL used; warning logged. | `TestCRLFetchFailUsesCached` |
| Pubkey rotation needed | Requires release with new bundled key; documented as a planned-release event. | `docs/operations/license-key-rotation.md` |
| Two licenses applied in quick succession | Singleton row; second wins; audit row for both. | `TestUpsertOverwrites` |
| License JSON > 64 KiB | 413; LimitReader truncates. | `TestApplyOversizeRejected` |
| `kid` in license but pubkey for that kid not bundled | `unknown-kid` rejection; admin-visible. | `TestValidatorRejectsUnknownKID` |
| Daily refresh while admin is mid-edit | UPSERT; no race; flags re-resolve once. | `TestRefresherDuringApply` |
| License server returns `free` license (downgrade) | UPSERT; tier flips; grace per Story 16.2. | `TestRefreshDowngrade` |

## 10. Acceptance checklist

**Validator**
- [ ] Ed25519 signature check + JCS canonicalization.
- [ ] CRL membership check.
- [ ] Expiry check using effectiveNow.

**Storage**
- [ ] `licenses` and `license_revocations` exist; singleton row.

**Endpoints**
- [ ] POST/DELETE/GET admin endpoints; raw never returned.

**Refresher**
- [ ] Daily fetch; 30-day offline grace; CRL fetch.
- [ ] Clock-rewind defense.

**Tests**
- [ ] All §8 tests pass.

**Docs**
- [ ] `docs/operations/license-key-rotation.md` published.
- [ ] `specs/epics/16-subscriptions/README.md` ticks story 16.4.

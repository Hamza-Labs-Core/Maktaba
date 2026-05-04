# Implementation Plan — Story 10.17 Device pairing endpoint

> Companion to [story-10-17-auth-pair.md](story-10-17-auth-pair.md).
> Schema for `pairing_codes` is in [README.md](README.md). The token
> issuance reuses [Story 10.3](plan-10-03-native-login.md). Audit rows
> go through [Story 10.16](plan-10-16-security-audit.md). Rate limit is
> coordinated with [Story 10.12](plan-10-12-rate-limiting-auth.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration file | `shared/db/migrations/0027_pairing_codes.sql` (Postgres) and `.sqlite.sql`. The schema in README spells out the row shape; this plan adds a `code_hash` column and *removes plaintext storage* per AC-4. |
| Endpoints | `POST /api/auth/pair` (device), `POST /api/auth/pair/claim` (user), `GET /api/auth/pair/{code}` (device poll). |
| Code generation | 8-char Crockford base32 (`0123456789ABCDEFGHJKMNPQRSTVWXYZ`) excluding `01ILO` per AC-1. `crypto/rand`. |
| Storage | `code_hash = sha256(code)` hex; the `code` column is dropped from the schema (only the hash is persisted). The plaintext lives only in the response of the issuing call (AC-4). |
| Reaper | `pipeline/src/maktaba_pipeline/tasks/pair_reaper.py` (Python tasks runner) — runs every 60 s. |
| Out of scope | The QR rendering (Epic 15.5). The mobile/TV camera capture (Epic 12). |

## 1. Architecture diagram

```
TV / Desktop (unauthenticated)             Web / Mobile (authenticated user)
   │                                                  │
   │ 1. POST /api/auth/pair                            │
   │    {device_kind, device_label, bundle_id?}       │
   │    ◄ 201 {code, expires_at, poll_url}             │
   │                                                   │
   │ 2. show QR / OTP "ABCD1234"                       │
   │                                                   │
   │ 3. (poll loop) GET /api/auth/pair/{code}          │
   │    ◄ 202 {state: "pending"}                       │
   │                                                   │
   │                                                   │ 4. user scans QR
   │                                                   │ POST /api/auth/pair/claim {code}
   │                                                   │    ◄ 204
   │                                                   │
   │ 5. GET /api/auth/pair/{code}                      │
   │    ◄ 200 {access_token, refresh_token, user, ...} │
   │    audit: pair.code-claimed                       │
   │    row deleted                                    │

Reaper (every 60s):
   - UPDATE pairing_codes SET state='expired'
       WHERE state='pending' AND expires_at < now();
   - DELETE FROM pairing_codes WHERE state='expired'
                                  AND expires_at < now() - 24h;
   - audit: pair.code-expired (one row per minute, with count)
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `shared/db/migrations/0027_pairing_codes.sql` | Postgres schema. |
| `shared/db/migrations/0027_pairing_codes.sqlite.sql` | SQLite variant. |
| `shared/db/queries/pairing_codes.sql` | sqlc input. |
| `api/internal/auth/pair.go` | `Issue`, `Claim`, `Poll`, `Reap`, `code` generator. |
| `api/internal/http/auth_pair.go` | All three HTTP handlers. |
| `pipeline/src/maktaba_pipeline/tasks/auth_reaper.py` | Unified reaper task — sweeps `web_sessions`, `refresh_tokens`, and `pairing_codes`. See §6. |
| `api/internal/auth/pair_test.go` | Unit tests. |
| `api/internal/http/auth_pair_test.go` | Integration tests. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/config/config.go` | Add `Auth.PairingTTLSec` (default 600), `Auth.PairingPollHintSec` (default 2), `Auth.PairingReapInterval` (default 60s). |
| `api/internal/http/router.go` | Mount the three routes; the issue + claim endpoints sit under `/api/auth/*` so Story 10.12's rate-limit applies. |
| `api/internal/http/middleware/ratelimit_auth.go` | Extend selector: `/api/auth/pair` → broad rule (already covered by `strings.HasPrefix(path, "/api/auth/")`); the per-IP cap of 6/min from AC-1 is implemented by an *additional* rule keyed on `path == "/api/auth/pair"`. See §5. |

### 2.3 Type definitions

```go
// api/internal/auth/pair.go
package auth

import (
    "context"
    "crypto/rand"
    "crypto/sha256"
    "encoding/hex"
    "errors"
    "time"

    "github.com/google/uuid"
)

const PairingCodeLen   = 8
const PairingAlphabet  = "23456789ABCDEFGHJKMNPQRSTVWXYZ" // Crockford minus 01ILO

type PairingState string
const (
    PairPending PairingState = "pending"
    PairClaimed PairingState = "claimed"
    PairExpired PairingState = "expired"
)

type PairingRow struct {
    ID         uuid.UUID
    CodeHash   string                  // sha256(code) hex
    DeviceKind string
    DeviceLabel string
    BundleID    string
    UserID     *uuid.UUID
    State      PairingState
    CreatedAt  time.Time
    ExpiresAt  time.Time
    IP         net.IP
}

type PairingService interface {
    Issue(ctx context.Context, p IssueParams) (PairingIssue, error)
    Claim(ctx context.Context, code string, userID uuid.UUID) error
    Poll (ctx context.Context, code string) (PairingRow, error)
    Reap (ctx context.Context, now time.Time) (expired int, deleted int, err error)
}

type IssueParams struct {
    DeviceKind, DeviceLabel, BundleID string
    IP                                net.IP
}

type PairingIssue struct {
    Code      string         // returned ONCE; never re-derivable
    ExpiresAt time.Time
    PollURL   string
    Row       PairingRow     // CodeHash filled, Code never stored
}

var (
    ErrPairUnknown = errors.New("pair: unknown")
    ErrPairExpired = errors.New("pair: expired")
    ErrPairAlready = errors.New("pair: already claimed")
)
```

### 2.4 Function signatures

```go
func GeneratePairingCode() (string, error)        // 8 chars from PairingAlphabet
func HashPairingCode(code string) string          // sha256 hex (lowercase)
```

## 3. Database migration — Postgres

`shared/db/migrations/0027_pairing_codes.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

-- IMPORTANT — divergence from README.md schema:
-- The README pairs `code TEXT PRIMARY KEY` with the *plaintext* code.
-- Story AC-4 requires SHA-256 hashing for at-rest defense. We replace
-- `code` with `code_hash` and a synthetic UUID PK.

CREATE TABLE pairing_codes (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code_hash     TEXT NOT NULL UNIQUE,            -- sha256 hex, lowercase
    device_kind   TEXT NOT NULL,                   -- 'tv' | 'desktop' | 'mobile' | 'unknown'
    device_label  TEXT NOT NULL,
    bundle_id     TEXT,
    user_id       UUID REFERENCES users(id) ON DELETE CASCADE,
    state         TEXT NOT NULL DEFAULT 'pending',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    ip            INET,
    CONSTRAINT pairing_codes_state_chk CHECK (state IN ('pending','claimed','expired'))
);

-- Reaper sweep & "what's pending right now".
CREATE INDEX pairing_codes_state ON pairing_codes (state, expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS pairing_codes;
-- +goose StatementEnd
```

The `code_hash` is the only lookup vector: `WHERE code_hash = sha256_hex(:input)`.
SHA-256 is fast enough at single-row scale; the index makes the lookup
O(log N). We do NOT use argon2 here because (a) the entropy of an
8-char base32 code is only ~40 bits, so any hash is brute-forceable
offline given the DB; argon2 would just be theater, and (b) the
attack model is *online* brute-force, which is governed by AC-6 rate
limits (6/min/IP) — a 40-bit space takes 2^40 / (6/60) requests ≈ 350
million minutes ≈ 660 years.

### 3.1 SQLite variant

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE pairing_codes (
    id            TEXT PRIMARY KEY,
    code_hash     TEXT NOT NULL UNIQUE,
    device_kind   TEXT NOT NULL,
    device_label  TEXT NOT NULL,
    bundle_id     TEXT,
    user_id       TEXT REFERENCES users(id) ON DELETE CASCADE,
    state         TEXT NOT NULL DEFAULT 'pending',
    created_at    TEXT NOT NULL DEFAULT (CURRENT_TIMESTAMP),
    expires_at    TEXT NOT NULL,
    ip            TEXT,
    CHECK (state IN ('pending','claimed','expired'))
);

CREATE INDEX pairing_codes_state ON pairing_codes (state, expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS pairing_codes;
-- +goose StatementEnd
```

## 4. Pairing service

```go
// api/internal/auth/pair.go
func GeneratePairingCode() (string, error) {
    out := make([]byte, PairingCodeLen)
    n := byte(len(PairingAlphabet))
    for i := range out {
        var b [1]byte
        if _, err := rand.Read(b[:]); err != nil { return "", err }
        out[i] = PairingAlphabet[b[0] % n]
    }
    return string(out), nil
}

func HashPairingCode(code string) string {
    sum := sha256.Sum256([]byte(strings.ToUpper(code)))
    return hex.EncodeToString(sum[:])
}

func (s *pairingService) Issue(ctx context.Context, p IssueParams) (PairingIssue, error) {
    // Up to 5 retries on UNIQUE collision (extremely unlikely at 32^8).
    for i := 0; i < 5; i++ {
        code, err := GeneratePairingCode()
        if err != nil { return PairingIssue{}, err }
        hash := HashPairingCode(code)
        row, err := s.db.InsertPairingCode(ctx, db.InsertPairingCodeParams{
            ID:         uuid.Must(uuid.NewV7()),
            CodeHash:   hash,
            DeviceKind: p.DeviceKind, DeviceLabel: p.DeviceLabel, BundleID: p.BundleID,
            ExpiresAt:  time.Now().Add(time.Duration(s.cfg.PairingTTLSec) * time.Second),
            IP:         p.IP.String(),
        })
        if errors.Is(err, ErrUnique) { continue }
        if err != nil { return PairingIssue{}, err }
        s.audit.Record(ctx, AuditPairCodeIssued{
            CodeHash: hash, DeviceKind: p.DeviceKind, IP: p.IP,
        })
        return PairingIssue{
            Code: code, ExpiresAt: row.ExpiresAt,
            PollURL: fmt.Sprintf("/api/auth/pair/%s", code),
            Row: PairingRow{...},
        }, nil
    }
    return PairingIssue{}, errors.New("pair: code-collision-exhausted")
}

func (s *pairingService) Claim(ctx context.Context, code string, userID uuid.UUID) error {
    hash := HashPairingCode(code)
    n, err := s.db.ClaimPairingCode(ctx, db.ClaimPairingCodeParams{
        CodeHash: hash, UserID: userID, Now: time.Now(),
    })
    if err != nil { return err }
    if n == 0 {
        // The single conditional UPDATE returned 0 — figure out why.
        row, err := s.db.GetPairingByHash(ctx, hash)
        if errors.Is(err, ErrNoRows) { return ErrPairUnknown }
        if err != nil { return err }
        if row.ExpiresAt.Before(time.Now()) { return ErrPairExpired }
        if row.State == PairClaimed { return ErrPairAlready }
        return ErrPairUnknown
    }
    return nil
}

func (s *pairingService) Poll(ctx context.Context, code string) (PairingRow, error) {
    hash := HashPairingCode(code)
    row, err := s.db.GetPairingByHash(ctx, hash)
    if errors.Is(err, ErrNoRows) { return PairingRow{}, ErrPairUnknown }
    return row, err
}
```

The `Claim` SQL is a single conditional UPDATE so two concurrent
claimers see exactly one success:

```sql
-- name: ClaimPairingCode :execrows
UPDATE pairing_codes
   SET state   = 'claimed',
       user_id = $2
 WHERE code_hash = $1
   AND state    = 'pending'
   AND expires_at > $3::timestamptz;
```

## 5. HTTP handlers

```go
// api/internal/http/auth_pair.go
func issuePair(svc auth.PairingService) http.HandlerFunc {
    type req struct {
        DeviceKind  string `json:"device_kind"`
        DeviceLabel string `json:"device_label"`
        BundleID    string `json:"bundle_id,omitempty"`
    }
    return func(w http.ResponseWriter, r *http.Request) {
        var b req
        _ = json.NewDecoder(r.Body).Decode(&b)
        if b.DeviceKind == "" { b.DeviceKind = "unknown" }
        out, err := svc.Issue(r.Context(), auth.IssueParams{
            DeviceKind: b.DeviceKind, DeviceLabel: b.DeviceLabel,
            BundleID: b.BundleID, IP: clientIP(r),
        })
        if err != nil {
            problem(w, http.StatusInternalServerError, "internal", "")
            return
        }
        w.Header().Set("Location", out.PollURL)
        writeJSON(w, http.StatusCreated, map[string]any{
            "code": out.Code, "expires_at": out.ExpiresAt, "poll_url": out.PollURL,
        })
    }
}

func claimPair(svc auth.PairingService, audit auth.AuditSink) http.HandlerFunc {
    type req struct{ Code string `json:"code"` }
    return func(w http.ResponseWriter, r *http.Request) {
        u, ok := auth.UserFromContext(r.Context())
        if !ok { problem(w, http.StatusUnauthorized, "unauthorized", ""); return }
        var b req
        _ = json.NewDecoder(r.Body).Decode(&b)
        if len(b.Code) != auth.PairingCodeLen {
            problem(w, http.StatusNotFound, "pair-code-unknown", ""); return
        }
        err := svc.Claim(r.Context(), b.Code, u.ID)
        switch {
        case errors.Is(err, auth.ErrPairExpired):
            problem(w, http.StatusGone, "pair-code-expired", "")
        case errors.Is(err, auth.ErrPairAlready):
            problem(w, http.StatusConflict, "pair-code-already-claimed", "")
        case errors.Is(err, auth.ErrPairUnknown):
            problem(w, http.StatusNotFound, "pair-code-unknown", "")
        case err != nil:
            problem(w, http.StatusInternalServerError, "internal", "")
        default:
            // The audit event with `pair.code-claimed` is written by the
            // poll handler at the moment tokens are minted, so a claim
            // without a subsequent poll doesn't pollute the trail. The
            // code-issued audit was written at Issue time.
            w.WriteHeader(http.StatusNoContent)
        }
    }
}

func pollPair(svc auth.PairingService, signer auth.Signer, refresh auth.RefreshStore,
    users auth.Store, libACL auth.LibACL, audit auth.AuditSink, cfg auth.Config) http.HandlerFunc {

    return func(w http.ResponseWriter, r *http.Request) {
        code := chi.URLParam(r, "code")
        if len(code) != auth.PairingCodeLen {
            problem(w, http.StatusNotFound, "pair-code-unknown", ""); return
        }
        row, err := svc.Poll(r.Context(), code)
        switch {
        case errors.Is(err, auth.ErrPairUnknown):
            problem(w, http.StatusNotFound, "pair-code-unknown", ""); return
        case err != nil:
            problem(w, http.StatusInternalServerError, "internal", ""); return
        }
        if row.ExpiresAt.Before(time.Now()) {
            problem(w, http.StatusGone, "pair-code-expired", ""); return
        }
        switch row.State {
        case auth.PairPending:
            w.Header().Set("Retry-After", strconv.Itoa(cfg.PairingPollHintSec))
            writeJSON(w, http.StatusAccepted, map[string]any{"state": "pending"})
            return
        case auth.PairClaimed:
            // Mint tokens; delete the row so it can't be polled again.
            user, err := users.GetByID(r.Context(), *row.UserID)
            if err != nil { problem(w, 500, "internal", ""); return }
            // Reuse the native-login path's mint helper.
            issueJWTPair(w, r, user, signer, refresh, libACL, audit, cfg)
            _ = svc.deleteByHash(r.Context(), row.CodeHash)
            audit.Record(r.Context(), auth.AuditPairCodeClaimed{
                CodeHash: row.CodeHash, UserID: user.ID, IP: clientIP(r),
            })
            return
        case auth.PairExpired:
            problem(w, http.StatusGone, "pair-code-expired", "")
            return
        }
    }
}
```

The poll endpoint is the exit door: it mints the JWTs and deletes the
row in the same handler. Repeating the poll after a successful claim
returns 404 (the row is gone).

## 6. Unified auth-table reaper

This story owns the unified Python reaper that sweeps **all three**
auth-related tables that use TTL-based expiry:

| Table | TTL column | Default retention after expiry | Owner |
|---|---|---|---|
| `web_sessions` | `expires_at` | 7 days (then DELETE) | Story 10.2 |
| `refresh_tokens` | `expires_at` | 90 days (then DELETE if `revoked_at IS NOT NULL`) | Story 10.3 |
| `pairing_codes` | `expires_at` | 24 hours (then DELETE after `state='expired'`) | This story |

Stories 10.2 and 10.3 ship the reaper *indexes* (`web_sessions_reaper`,
`refresh_tokens_reaper`) but no reaper code. This plan consolidates the
reaper here so all three TTL sweeps live in one task with one schedule
and one shared dedupe-keyed audit emitter — avoiding three separate
60-second tickers.

```python
# pipeline/src/maktaba_pipeline/tasks/auth_reaper.py
"""
Unified reaper for auth tables (web_sessions, refresh_tokens, pairing_codes).
Each table has its own (state-flip, delete) pair; they share one tick.
"""
import asyncio
from datetime import datetime, timezone
from dataclasses import dataclass

@dataclass
class ReaperRule:
    table: str
    ttl_column: str          # column to compare against now()
    flip_sql: str | None     # optional state-flip step (None = skip)
    delete_sql: str          # delete step
    audit_event: str         # security audit event name on flip

RULES = [
    ReaperRule(
        table="web_sessions",
        ttl_column="expires_at",
        flip_sql=None,         # web sessions don't have a "state" — direct delete
        delete_sql="""
            DELETE FROM web_sessions
             WHERE (expires_at < $1 - interval '7 days')
                OR (revoked_at IS NOT NULL AND revoked_at < $1 - interval '7 days')
        """,
        audit_event="web-session.reaped",
    ),
    ReaperRule(
        table="refresh_tokens",
        ttl_column="expires_at",
        flip_sql=None,
        delete_sql="""
            DELETE FROM refresh_tokens
             WHERE expires_at < $1 - interval '90 days'
                OR (revoked_at IS NOT NULL AND revoked_at < $1 - interval '90 days')
        """,
        audit_event="refresh-token.reaped",
    ),
    ReaperRule(
        table="pairing_codes",
        ttl_column="expires_at",
        flip_sql="""
            UPDATE pairing_codes SET state = 'expired'
             WHERE state = 'pending' AND expires_at < $1
        """,
        delete_sql="""
            DELETE FROM pairing_codes
             WHERE state = 'expired' AND expires_at < $1 - interval '24 hours'
        """,
        audit_event="pair.code-expired",
    ),
]

async def reap_one(db, rule: ReaperRule, now: datetime) -> tuple[int, int]:
    flipped = 0
    if rule.flip_sql:
        flipped = (await db.execute(rule.flip_sql, now)) or 0
    deleted = (await db.execute(rule.delete_sql, now)) or 0
    return flipped, deleted

async def run_periodic(db, audit, interval_seconds: int = 60) -> None:
    while True:
        now = datetime.now(timezone.utc)
        try:
            for rule in RULES:
                flipped, deleted = await reap_one(db, rule, now)
                if flipped > 0:
                    await audit.record(
                        category="security", event=rule.audit_event,
                        payload={
                            "count": flipped,
                            "dedupe_key": f"{rule.audit_event}|{int(now.timestamp() // 60)}",
                        },
                    )
        except Exception as e:
            log.warning("auth reaper tick failed", err=str(e))
        await asyncio.sleep(interval_seconds)
```

The pairing-codes-only sweep that lived in earlier drafts of this plan
is folded into the loop above. The default tick is still 60s; operators
can shorten it via config without touching code.

## 7. Rate-limit composition

Per AC-1 the issue endpoint is 6/min per IP, *narrower* than the broad
30/min Story 10.12 cap. We add this to the rate-limit middleware's
selector:

```go
// addition to ratelimit_auth.go's selectRules
case path == "/api/auth/pair":
    return []rule{pairRule, broadRule}   // 6/min per IP + broad

// pairRule definition:
pairRule := rule{
    scope:   "pair",
    perMin:  6,
    burst:   2,
    keyOf:   func(r *http.Request) string { return clientIP(r).String() },
    errType: "ip-throttled-pair",
}
```

## 8. Test plan

### 8.1 Code generator (`pair_test.go`)

| Test | What it pins |
|---|---|
| `TestGenerateLengthAndAlphabet` | 1000 generations: every code is 8 chars, every char in `PairingAlphabet`. None contain `0`, `1`, `I`, `L`, `O`. |
| `TestGenerateUniformDistribution` | 100K generations, chi-square test against uniform → p > 0.05. |
| `TestHashLowercase` | `HashPairingCode` returns lowercase hex (matches DB column convention). |
| `TestHashCaseInsensitive` | `HashPairingCode("ab23")` == `HashPairingCode("AB23")` (we upper-case before hashing). |

### 8.2 Service (`pair_test.go`)

| Test | What it pins |
|---|---|
| `TestIssueInsertsRow` | After Issue: row exists with `state='pending'`, `code_hash != ""`, `expires_at > now()`. |
| `TestIssueReturnsCodeOnce` | `out.Code` is non-empty; the row's `code_hash` matches `HashPairingCode(out.Code)`. |
| `TestIssueRetriesOnCollision` | Inject a fake INSERT that returns UNIQUE on first attempt; second attempt succeeds; only one row exists. |
| `TestClaimMarksClaimed` | Issue, then Claim → row's `state='claimed'`, `user_id` set. |
| `TestClaimUnknownReturnsErrUnknown` | Claim with random 8-char code → `ErrPairUnknown`. |
| `TestClaimExpiredReturnsErrExpired` | Force `expires_at = now()-1s`; Claim → `ErrPairExpired`. |
| `TestClaimAlreadyClaimedReturnsErr` | Claim twice; second → `ErrPairAlready`. |
| `TestClaimConcurrentSerializes` | Two goroutines Claim same code; one wins, the other gets `ErrPairAlready` (the conditional UPDATE matches 0). |
| `TestPollReturnsPending` | Issue; Poll → state='pending'. |
| `TestPollReturnsClaimedAfterClaim` | Issue, Claim, Poll → state='claimed'. |
| `TestReapMarksExpired` | Issue with TTL 1s; sleep 2s; Reap → row state='expired'; second Reap (after 24h+) deletes it. |

### 8.3 HTTP integration (`auth_pair_test.go`)

| Test | What it pins |
|---|---|
| `TestPairFullFlowDeliversTokens` | Issue → claim → poll → 200 with `access_token + refresh_token`. Total flow under 10s. |
| `TestPollPendingReturns202WithRetryAfter` | Poll before claim → 202, body `{state: "pending"}`, `Retry-After` set. |
| `TestPollAfterClaimReturns200WithTokens` | Claim then poll → 200 with token shape from Story 10.3 AC-1. |
| `TestPollAfterClaimedRowDeletedReturns404` | Claim, poll (200), poll again → 404 `pair-code-unknown`. |
| `TestClaimByOtherUserAllowed` | Two users, two claims of two different codes — independent. |
| `TestExpiredCodeReturns410` | Issue with TTL 1s; sleep 2s; claim → 410 `pair-code-expired`. |
| `TestUnknownCodeReturns404` | claim with `"AAAAAAAA"` → 404 `pair-code-unknown`. |
| `TestRateLimit6PerMinPerIP` | 6 issues from one IP succeed; 7th → 429 `ip-throttled-pair`. |
| `TestStoredCodeIsHashedNotPlaintext` | After Issue, query the DB directly: `code_hash` exists, `code` column does NOT exist (the migration removed it). The plaintext appears only in the response body. |
| `TestAuditPairIssued` | Issue → one `audit_log` row `event='pair.code-issued'` with payload device_kind. |
| `TestAuditPairClaimed` | Successful poll-with-claim → one `event='pair.code-claimed'` row with user_id. |
| `TestPairIPAttempt32To8BruteForce` | Simulate 100 brute-force claim attempts from one IP — all 404; rate-limit kicks in around request 7 → 429. The math (32^8 keyspace, 6/min cap) means brute-force is infeasible. |

### 8.4 Reaper

| Test | What it pins |
|---|---|
| `TestReaperFlipsPendingToExpired` | Insert pending with past expires_at; reap → state='expired'. |
| `TestReaperDeletesAfter24h` | Expired row with `expires_at < now()-24h` → DELETE removed it. |
| `TestReaperEmitsCountedAudit` | 5 pending rows expired → one audit row with `count=5` (deduped per minute). |

### 8.5 Cross-dialect

`auth_pair_dialect_test.go` runs the full flow against PG and SQLite.

## 9. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Device polls forever, user never scans | Code expires after TTL; subsequent polls return 410 once (until the 24h delete sweeps the row, after which the poll returns 404). | `TestExpiredCodeReturns410` |
| Two users claim the same code in parallel | Conditional UPDATE makes one win; the other sees `state='claimed'` and gets `ErrPairAlready` → 409 `pair-code-already-claimed`. **Story EC update required:** `story-10-17-auth-pair.md` previously said the loser receives 404 `pair-code-unknown`; this plan distinguishes "unknown" from "already-claimed" for clarity, returning 409. The story file has been updated to match. | `TestClaimConcurrentSerializes` |
| Device claims, then user logs out everywhere | The device's refresh row is in the same family the logout-all sweep touches → revoked. Device falls back to re-pairing. | Story 10.5 plan |
| Device with a valid refresh re-runs pair | The new pair issues fresh tokens; the old refresh remains valid until rotated/revoked. The device app should overwrite its stored refresh on success. | Documented in mobile/desktop SDKs. |
| Brute-force the 32^8 keyspace (1.1e12) | Rate-limit caps at 6/min/IP → 660 years per IP; per-username NOT applicable (no username); per-IP rate limit is the defense. | `TestPairIPAttempt32To8BruteForce` |
| DB dump leaks `code_hash` rows | SHA-256 on a 40-bit space is offline-brute-forceable in seconds. The mitigation is *online* rate-limiting (still 660 years per IP), AND the TTL of 600s means leaked hashes are mostly already invalidated by the time of analysis. AC-4's "DB read leak does not yield active codes" is honored only insofar as the hash is not directly the lookup vector and the codes have a 10-min lifetime. We document this clearly. | docs |
| User claims code from a different account than the device's intended owner | The pair flow doesn't bind a device to an account at issue; the *user who claims* is the owner. The device receives that user's tokens. | n/a |
| Code generator collision | UNIQUE retry up to 5 times; after 5 → 500. With 32^8 keyspace and 600s TTL, expected collisions per minute ≈ 6 / 1.1e12 ≈ 0. | `TestIssueRetriesOnCollision` |
| `code` field omitted from claim body | Length check returns 404 `pair-code-unknown` (we don't 400 — same shape as a non-matching code keeps the API consistent). | `TestUnknownCodeReturns404` |
| Mixed-case input (user typed `aBc23DEf`) | We `strings.ToUpper` before hashing; matches the canonical hash. | `TestHashCaseInsensitive` |
| Reverse-proxy strips `Location` header from the 201 | Documented as a setup error; the response body still has `poll_url`. | n/a |

## 10. Dependencies

| Dep | Version | Why |
|---|---|---|
| `crypto/rand`, `crypto/sha256` | stdlib | Code gen + hashing. |
| `github.com/google/uuid` | already | UUID v7 for row id. |

No new heavy deps.

## 11. Acceptance checklist

**Issue**
- [ ] AC-1: returns 201 with `{code, expires_at, poll_url}`; rate-limited to 6/min/IP.
- [ ] Code is 8 chars from the visually-unambiguous Crockford alphabet.

**Claim**
- [ ] AC-2: 204 on success; 410 `pair-code-expired`; 404 `pair-code-unknown`; 409 `pair-code-already-claimed`.
- [ ] Single conditional UPDATE serializes concurrent claims.

**Poll**
- [ ] AC-3: 202 while pending with Retry-After; 200 with token bundle once claimed; row deleted after delivery.

**Hashing**
- [ ] AC-4: only `code_hash = sha256_hex(code)` is persisted; `code` column does not exist.

**Reaper**
- [ ] AC-5: every 60s, expired rows flipped; rows older than 24h deleted; deduped audit row written.

**Rate limit**
- [ ] AC-6: 6/min/IP via the auth-rate-limit middleware (Story 10.12 composition).

**Audit**
- [ ] `pair.code-issued` and `pair.code-claimed` written at Issue and successful Poll respectively.

**Tests**
- [ ] All §8 tests pass on both dialects.

**Docs**
- [ ] README.md ticks story 10.17.
- [ ] Mobile/TV SDKs document the pair-then-poll flow and the 600s TTL.

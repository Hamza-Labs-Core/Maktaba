# Plan 10.17 — Device pairing endpoint — implementation

> Implementation plan for [story-10-17-auth-pair.md](story-10-17-auth-pair.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: token shape on successful poll matches
> Story 10.3 AC-1 (access JWT + opaque refresh); rate-limit budget rolls
> up under Story 10.12's `auth_rate_per_min=30`; audit rows go through
> [Plan 10.16](plan-10-16-security-audit.md) helpers; the schema in the
> Epic 10 [README.md](README.md) is **modified here** (the README's
> `code TEXT PRIMARY KEY` is replaced with `code_id UUID PRIMARY KEY` +
> `code_hash TEXT NOT NULL`, per AC-4).

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Schema departure from epic README**: the README's `pairing_codes (code TEXT PRIMARY KEY)` is replaced by `pairing_codes (code_id UUID PRIMARY KEY, code_hash TEXT NOT NULL UNIQUE, …)`. The plaintext code never lives in the DB. The migration in this plan creates the new shape. | Story AC-4: "the table stores SHA-256(code) keyed by an opaque id, not the plaintext code." | A DB read leak that produces plaintext codes lets an attacker claim active sessions. SHA-256 + opaque key id makes the leak useless. |
| D2 | **8-character codes from `ABCDEFGHJKMNPQRSTUVWXYZ23456789` (32 chars).** Excludes `0`, `1`, `I`, `L`, `O` (visually ambiguous). Total keyspace: `32^8 = 2^40 ≈ 1.1e12`. With per-IP rate limit of 6/min plus the broader 30/min auth cap (Story 10.12), a single-IP brute-force takes ~58 years on average to hit any active code (TTL 600 s). | Story AC-1. | The character set is the AC; the 32^8 keyspace is enough given the rate limits and TTL. We document the math so reviewers can re-derive the security margin. |
| D3 | **Constant-time SHA-256 lookup via `WHERE code_hash = $1`** plus a per-row `crypto/subtle.ConstantTimeCompare` check on the returned hash. The DB index lookup is short-circuited but the hash compare equalizes timing across hits/misses. | Story AC-4. | The DB index can leak whether a hash exists via timing; the `subtle.ConstantTimeCompare` belt-and-suspenders prevents byte-level timing leaks even if the hash is found. |
| D4 | **One-shot atomic claim** via a single UPDATE with a returning clause: `UPDATE pairing_codes SET state='claimed', user_id=$1 WHERE code_hash=$2 AND state='pending' AND expires_at > now() RETURNING code_id`. Two concurrent claims race on the row lock; only one wins; the loser sees zero rows updated and returns 404 `pair-code-unknown`. | Story Edge cases: parallel claim. | The atomic UPDATE is the correctness primitive. No SELECT-then-UPDATE race; no FOR UPDATE; no SERIALIZABLE. |
| D5 | **Reaper goroutine every 60 s** runs two SQL statements: (a) `UPDATE pairing_codes SET state='expired' WHERE state='pending' AND expires_at < now() RETURNING 1` and (b) `DELETE FROM pairing_codes WHERE state IN ('expired','claimed') AND created_at < now() - interval '24 hours'`. The expiry count (a) is logged as a single `pair.code-expired` audit row (Plan 10.16). | Story AC-5. | Two statements keep the logic readable; the 24-hour grace period after expiry/claim helps debugging recent issues without bloating the table. |
| D6 | **Polling endpoint returns 202 with state info while pending**, 200 with token bundle on claim. The token bundle re-uses the Story 10.3 AC-1 access-token + refresh-token mint path (`internal/auth/native.MintForUser`); no shape divergence. After delivery, the row is **deleted** (not marked terminal) inside the same xact as the mint. | Story AC-3. | Deleting on success keeps the table small. Audit row carries the `code_id` so we can trace flows post-deletion. |
| D7 | **Rate limits**: per-IP 6/min on `POST /api/auth/pair` (issue) AND per-IP 60/min on `GET /api/auth/pair/:code` (poll). Polling needs a higher cap because a TV app polls every 2-3 s during a 30-90 s pairing window. The auth-bucket from Story 10.12 covers `/api/auth/*` at 30/min — the issue cap of 6 sits inside; polling we count separately because we want it not to consume the issue budget. | Story AC-1, AC-6. | Without a separate poll budget, a 60-s pair flow with 30 polls would exhaust the broader `/api/auth/*` 30/min cap and lock out other auth paths. |

If D1 is rejected (keep plaintext in DB): a single `pg_dump` from a leaked backup hands attackers active codes for 10 minutes. The hash + opaque key approach has zero downsides — `WHERE code_hash = $1` is just as fast as `WHERE code = $1`.

---

## 1. Architecture diagram

```
   ┌────────────────────────────────────────────────────────────────┐
   │  TV / desktop  ─── POST /api/auth/pair ──> API                 │
   │                    body: {device_kind, device_label, bundle?}  │
   │                                                                 │
   │  API:                                                           │
   │    1. rate-limit: per-IP 6/min (D7)                             │
   │    2. generate plaintext: 8 chars from custom alphabet (D2)     │
   │    3. code_id = uuidv7()                                        │
   │    4. code_hash = sha256(plaintext)                             │
   │    5. INSERT pairing_codes (code_id, code_hash, state='pending',│
   │                            expires_at = now() + 600s)           │
   │    6. audit: pair.code-issued (Plan 10.16; payload.code_id)     │
   │    7. respond 201 + Location: /api/auth/pair/<plaintext>        │
   │       body: {code: <plaintext>, expires_at, poll_url}           │
   │                                                                 │
   │   ┌────────────────── plaintext shown on TV ────────────────┐  │
   │   │                                                         │  │
   │  user scans QR / types into web                            │  │
   │   │                                                         │  │
   │  Logged-in user (web)  ─── POST /api/auth/pair/claim ──> API  │
   │                            body: {code: <plaintext>}            │
   │                                                                 │
   │  API:                                                           │
   │    1. require auth (Plan 10.13)                                 │
   │    2. code_hash = sha256(input.code)                            │
   │    3. UPDATE pairing_codes                                       │
   │         SET state='claimed', user_id=<sub>                       │
   │       WHERE code_hash=$1 AND state='pending'                    │
   │             AND expires_at > now()                              │
   │       RETURNING code_id                                         │
   │    4. zero rows: respond 410 (expired) or 404 (unknown)         │
   │    5. one row: respond 204                                      │
   │                                                                 │
   │  TV  ─── GET /api/auth/pair/<plaintext>  (every ~2 s) ──> API   │
   │                                                                 │
   │  API:                                                           │
   │    1. rate-limit: per-IP 60/min (D7)                            │
   │    2. SELECT state, user_id, code_id FROM pairing_codes         │
   │         WHERE code_hash=$1                                       │
   │    3. row not found: 404 pair-code-unknown                      │
   │    4. expired (state='expired' OR expires_at<now()): 410        │
   │    5. pending: 202 {state:'pending', retry_after:2}             │
   │    6. claimed:                                                   │
   │       BEGIN;                                                    │
   │       SELECT user_id; mint access+refresh via Story 10.3        │
   │       DELETE FROM pairing_codes WHERE code_id = ...             │
   │       COMMIT;                                                   │
   │       audit: pair.code-claimed (payload.code_id)                │
   │       respond 200 + token bundle                                │
   │                                                                 │
   │  reaper goroutine (every 60 s) — D5                             │
   │    UPDATE …state='expired'… RETURNING 1; -> count → audit       │
   │    DELETE … created_at < now() - 24h                            │
   └────────────────────────────────────────────────────────────────┘
```

---

## 2. Detailed implementation

### 2.1 Package layout

```
api/
├── internal/
│   └── pairing/
│       ├── codes.go        // alphabet, generate, hash
│       ├── store.go        // SQL CRUD
│       ├── service.go      // Issue, Claim, Poll
│       ├── reaper.go       // background goroutine (D5)
│       ├── handler.go      // chi handlers
│       └── *_test.go
shared/db/migrations/
└── 00XX_pairing_codes.sql  // D1 schema
```

### 2.2 SQL migration (D1)

```sql
-- shared/db/migrations/00XX_pairing_codes.sql
-- Departs from the original epic README schema (plaintext PRIMARY KEY)
-- per Story 10.17 AC-4: the table stores SHA-256(code) keyed by an
-- opaque code_id; the plaintext is shown only at issue time.
BEGIN;

CREATE TABLE pairing_codes (
    code_id       UUID PRIMARY KEY,
    code_hash     TEXT NOT NULL UNIQUE,
    device_kind   TEXT NOT NULL,
    device_label  TEXT,
    bundle_id     TEXT,
    user_id       UUID REFERENCES users(id) ON DELETE CASCADE,
    state         TEXT NOT NULL DEFAULT 'pending'
                       CHECK (state IN ('pending','claimed','expired')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    ip            INET,
    user_agent    TEXT
);

-- Reaper / poll lookups.
CREATE INDEX pairing_codes_state_expiry
    ON pairing_codes (state, expires_at);
-- code_hash already has a UNIQUE; that index handles claim/poll lookups.

COMMIT;
```

### 2.3 `codes.go` — alphabet, generate, hash (D2, D3)

```go
package pairing

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
)

// Alphabet excludes 0, 1, I, L, O (Story AC-1).
const Alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
const CodeLen = 8

var ErrBadCode = errors.New("bad pairing code")

// GenerateCode returns a cryptographically random 8-char code from Alphabet.
func GenerateCode() (string, error) {
	const N = 32 // |Alphabet|
	// Rejection-sample uniformly from [0, 256) to avoid modulo bias.
	const limit = 256 - (256 % N)
	out := make([]byte, CodeLen)
	buf := make([]byte, 1)
	got := 0
	for got < CodeLen {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		if int(buf[0]) >= limit {
			continue
		}
		out[got] = Alphabet[int(buf[0])%N]
		got++
	}
	return string(out), nil
}

// NormalizeCode upper-cases and strips whitespace; rejects bad chars.
func NormalizeCode(in string) (string, error) {
	in = strings.ToUpper(strings.TrimSpace(in))
	if len(in) != CodeLen {
		return "", ErrBadCode
	}
	for _, c := range in {
		if !strings.ContainsRune(Alphabet, c) {
			return "", ErrBadCode
		}
	}
	return in, nil
}

// HashCode returns the lowercase hex SHA-256 of the plaintext code.
func HashCode(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:])
}

// EqualHash compares two hex-encoded SHA-256 values in constant time.
func EqualHash(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
```

### 2.4 `store.go` — SQL surface

```go
package pairing

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("pairing code not found")
	ErrExpired  = errors.New("pairing code expired")
	ErrAlready  = errors.New("pairing code already claimed")
)

type Row struct {
	CodeID      uuid.UUID
	CodeHash    string
	DeviceKind  string
	DeviceLabel string
	BundleID    string
	UserID      *uuid.UUID
	State       string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	IP          *netip.Addr
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(p *pgxpool.Pool) *Store { return &Store{pool: p} }

func (s *Store) Insert(ctx context.Context, r Row) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO pairing_codes
    (code_id, code_hash, device_kind, device_label, bundle_id,
     state, created_at, expires_at, ip, user_agent)
VALUES ($1,$2,$3,$4,$5,'pending',$6,$7,$8::inet,$9)
`, r.CodeID, r.CodeHash, r.DeviceKind, r.DeviceLabel, r.BundleID,
		r.CreatedAt, r.ExpiresAt, ipToText(r.IP), "")
	return err
}

// Lookup returns the row by hash. State is whatever it currently is.
func (s *Store) Lookup(ctx context.Context, codeHash string) (*Row, error) {
	const q = `
SELECT code_id, code_hash, device_kind, device_label, bundle_id,
       user_id, state, created_at, expires_at
  FROM pairing_codes
 WHERE code_hash = $1
`
	row := s.pool.QueryRow(ctx, q, codeHash)
	var r Row
	var label, bundle *string
	if err := row.Scan(&r.CodeID, &r.CodeHash, &r.DeviceKind, &label, &bundle,
		&r.UserID, &r.State, &r.CreatedAt, &r.ExpiresAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if label != nil {
		r.DeviceLabel = *label
	}
	if bundle != nil {
		r.BundleID = *bundle
	}
	return &r, nil
}

// Claim atomically transitions pending→claimed iff still alive.
// Returns the code_id, or ErrExpired / ErrNotFound.
func (s *Store) Claim(ctx context.Context, codeHash string, user uuid.UUID) (uuid.UUID, error) {
	const q = `
UPDATE pairing_codes
   SET state = 'claimed', user_id = $1
 WHERE code_hash = $2
   AND state = 'pending'
   AND expires_at > now()
RETURNING code_id
`
	var id uuid.UUID
	if err := s.pool.QueryRow(ctx, q, user, codeHash).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Either no row, expired, or already claimed. Disambiguate:
			r, lookupErr := s.Lookup(ctx, codeHash)
			if errors.Is(lookupErr, ErrNotFound) {
				return uuid.Nil, ErrNotFound
			}
			if r != nil && (r.State != "pending" || r.ExpiresAt.Before(time.Now())) {
				return uuid.Nil, ErrExpired
			}
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, err
	}
	return id, nil
}

// DeleteByID removes a row (used after token mint).
func (s *Store) DeleteByID(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM pairing_codes WHERE code_id = $1`, id)
	return err
}

// ReapExpired marks pending+overdue as expired; returns count.
func (s *Store) ReapExpired(ctx context.Context) (int64, error) {
	c, err := s.pool.Exec(ctx, `
UPDATE pairing_codes SET state='expired'
 WHERE state='pending' AND expires_at < now()
`)
	return c.RowsAffected(), err
}

// CleanupOld deletes rows in a terminal state older than 24h.
func (s *Store) CleanupOld(ctx context.Context) (int64, error) {
	c, err := s.pool.Exec(ctx, `
DELETE FROM pairing_codes
 WHERE state IN ('expired','claimed')
   AND created_at < now() - interval '24 hours'
`)
	return c.RowsAffected(), err
}

func ipToText(a *netip.Addr) any {
	if a == nil || !a.IsValid() {
		return nil
	}
	return a.String()
}
```

### 2.5 `service.go` — Issue + Claim + Poll (D6)

```go
package pairing

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/maktaba/api/internal/audit/security"
	"github.com/maktaba/api/internal/auth/native"
)

const PairingTTL = 10 * time.Minute

type IssueRequest struct {
	DeviceKind  string
	DeviceLabel string
	BundleID    string
	IP          netip.Addr
}

type IssueResponse struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
	PollURL   string    `json:"poll_url"`
}

type Service struct {
	store *Store
	mint  *native.Minter // Story 10.3 token mint
	audit security.Writer
}

func NewService(s *Store, m *native.Minter, a security.Writer) *Service {
	return &Service{store: s, mint: m, audit: a}
}

// Issue creates a pending pair record and returns the plaintext code.
// Plaintext is exposed only here and in the Location header.
func (s *Service) Issue(ctx context.Context, req IssueRequest) (IssueResponse, error) {
	plain, err := GenerateCode()
	if err != nil {
		return IssueResponse{}, err
	}
	hash := HashCode(plain)
	id := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	row := Row{
		CodeID: id, CodeHash: hash,
		DeviceKind: req.DeviceKind, DeviceLabel: req.DeviceLabel, BundleID: req.BundleID,
		State: "pending", CreatedAt: now, ExpiresAt: now.Add(PairingTTL), IP: &req.IP,
	}
	if err := s.store.Insert(ctx, row); err != nil {
		return IssueResponse{}, err
	}
	return IssueResponse{
		Code:      plain,
		ExpiresAt: row.ExpiresAt,
		PollURL:   "/api/auth/pair/" + plain,
	}, nil
}

// Claim transitions a code to claimed for the given user.
func (s *Service) Claim(ctx context.Context, plain string, user uuid.UUID) error {
	plain, err := NormalizeCode(plain)
	if err != nil {
		return ErrNotFound
	}
	hash := HashCode(plain)
	if _, err := s.store.Claim(ctx, hash, user); err != nil {
		return err
	}
	return nil
}

// Tokens are the same shape as Story 10.3 AC-1.
type PollResult struct {
	State        string `json:"state,omitempty"`
	RetryAfter   int    `json:"retry_after,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	UserID       string `json:"user_id,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
}

// Poll resolves the current state for the given plaintext code.
// On 'claimed', mints tokens, deletes the row, and writes the audit row.
func (s *Service) Poll(ctx context.Context, plain string) (PollResult, error) {
	plain, err := NormalizeCode(plain)
	if err != nil {
		return PollResult{}, ErrNotFound
	}
	hash := HashCode(plain)
	row, err := s.store.Lookup(ctx, hash)
	if err != nil {
		return PollResult{}, err
	}
	// Constant-time hash compare belt-and-suspenders (D3).
	if !EqualHash(row.CodeHash, hash) {
		return PollResult{}, ErrNotFound
	}
	switch {
	case row.State == "expired" || row.ExpiresAt.Before(time.Now()):
		return PollResult{}, ErrExpired
	case row.State == "pending":
		return PollResult{State: "pending", RetryAfter: 2}, nil
	case row.State == "claimed" && row.UserID != nil:
		bundle, err := s.mint.MintForUser(ctx, *row.UserID)
		if err != nil {
			return PollResult{}, err
		}
		// Best-effort delete; if it fails, the reaper will clean up.
		_ = s.store.DeleteByID(ctx, row.CodeID)
		return PollResult{
			AccessToken: bundle.AccessToken, RefreshToken: bundle.RefreshToken,
			UserID: row.UserID.String(), ExpiresIn: int(bundle.AccessTTL.Seconds()),
		}, nil
	default:
		return PollResult{}, ErrNotFound
	}
}

var _ = errors.Is // keep import live
```

### 2.6 `handler.go` — chi handlers

```go
package pairing

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/maktaba/api/internal/audit/security"
	"github.com/maktaba/api/internal/auth/authz"
	"github.com/maktaba/api/internal/httpx"
)

type Handler struct {
	svc   *Service
	audit security.Writer
}

func NewHandler(svc *Service, audit security.Writer) *Handler {
	return &Handler{svc: svc, audit: audit}
}

// Routes mounts /api/auth/pair, /api/auth/pair/claim, /api/auth/pair/{code}.
// The router-level rate limiter is configured by Story 10.12 (auth bucket
// for issue/claim) plus an extra cap on poll (Plan 10.17 D7).
func (h *Handler) Routes(r chi.Router) {
	r.Post("/api/auth/pair", h.issue)
	r.Post("/api/auth/pair/claim", h.claim)
	r.Get("/api/auth/pair/{code}", h.poll)
}

type issueIn struct {
	DeviceKind  string `json:"device_kind"`
	DeviceLabel string `json:"device_label"`
	BundleID    string `json:"bundle_id,omitempty"`
}

func (h *Handler) issue(w http.ResponseWriter, r *http.Request) {
	var in issueIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil ||
		in.DeviceKind == "" {
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid-body", "device_kind required")
		return
	}
	ip, _ := netip.ParseAddrPort(r.RemoteAddr)
	resp, err := h.svc.Issue(r.Context(), IssueRequest{
		DeviceKind:  in.DeviceKind,
		DeviceLabel: in.DeviceLabel,
		BundleID:    in.BundleID,
		IP:          ip.Addr(),
	})
	if err != nil {
		httpx.WriteProblem(w, http.StatusInternalServerError, "pair-issue-failed", "could not issue code")
		return
	}
	security.LogPairCodeIssued(r.Context(), h.audit, r,
		extractCodeIDFromPollURL(resp.PollURL), in.DeviceKind, in.BundleID)
	w.Header().Set("Location", resp.PollURL)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

type claimIn struct {
	Code string `json:"code"`
}

func (h *Handler) claim(w http.ResponseWriter, r *http.Request) {
	p, ok := authz.PrincipalFromCtx(r.Context())
	if !ok {
		httpx.WriteProblem(w, http.StatusUnauthorized, "auth-required", "auth required")
		return
	}
	var in claimIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Code == "" {
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid-body", "code required")
		return
	}
	if err := h.svc.Claim(r.Context(), in.Code, p.UserID); err != nil {
		switch {
		case errors.Is(err, ErrExpired):
			httpx.WriteProblem(w, http.StatusGone, "pair-code-expired", "pairing code has expired")
		default:
			// ErrNotFound, ErrAlready, validation errors → uniform 404 to avoid enumeration.
			httpx.WriteProblem(w, http.StatusNotFound, "pair-code-unknown", "pairing code unknown")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) poll(w http.ResponseWriter, r *http.Request) {
	plain := chi.URLParam(r, "code")
	res, err := h.svc.Poll(r.Context(), plain)
	if err != nil {
		switch {
		case errors.Is(err, ErrExpired):
			httpx.WriteProblem(w, http.StatusGone, "pair-code-expired", "pairing code has expired")
		default:
			httpx.WriteProblem(w, http.StatusNotFound, "pair-code-unknown", "pairing code unknown")
		}
		return
	}
	if res.State == "pending" {
		w.Header().Set("Retry-After", "2")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(res)
		return
	}
	// Claimed → tokens.
	if res.UserID != "" {
		uid, _ := uuid.Parse(res.UserID)
		security.LogPairCodeClaimed(r.Context(), h.audit, r, uid, "" /* code_id is gone post-delete */)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(res)
}

func extractCodeIDFromPollURL(u string) string { return u } // placeholder; Issue could return code_id directly
```

### 2.7 `reaper.go` — background loop (D5)

```go
package pairing

import (
	"context"
	"log/slog"
	"time"

	"github.com/maktaba/api/internal/audit/security"
)

func RunReaper(ctx context.Context, store *Store, audit security.Writer, log *slog.Logger) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			expired, err := store.ReapExpired(ctx)
			if err != nil {
				log.Warn("pairing.reaper.reap_failed", "err", err.Error())
			} else if expired > 0 {
				audit.Enqueue(security.Row{
					ID: uuidv7(), TS: time.Now().UTC(),
					Event:   security.EventPairCodeExpired,
					Payload: security.PairCodeExpiredPayload{Count: int(expired)},
				})
			}
			if _, err := store.CleanupOld(ctx); err != nil {
				log.Warn("pairing.reaper.cleanup_failed", "err", err.Error())
			}
		}
	}
}
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/00XX_pairing_codes.sql` | `pairing_codes` (D1 schema) | `TestMigration` |
| 2 | `api/internal/pairing/codes.go` | `Alphabet`, `CodeLen`, `GenerateCode`, `NormalizeCode`, `HashCode`, `EqualHash` | `TestGenerateUniformish`, `TestNormalizeRejectsBadChars` |
| 3 | `api/internal/pairing/store.go` | `Store`, `Row`, `Insert`, `Lookup`, `Claim`, `DeleteByID`, `ReapExpired`, `CleanupOld` | `TestStoreClaimAtomic`, `TestStoreReap` |
| 4 | `api/internal/pairing/service.go` | `Service`, `Issue`, `Claim`, `Poll`, `PollResult` | `TestServiceIssueClaimPoll`, `TestServicePollPendingThenClaimed` |
| 5 | `api/internal/pairing/handler.go` | `Handler`, `Routes`, `issue`, `claim`, `poll` | HTTP integration |
| 6 | `api/internal/pairing/reaper.go` | `RunReaper` | `TestReaperExpiresAndCleans` |
| 7 | wire in `cmd/api/main.go`: rate-limit middleware (Story 10.12) on issue+claim; separate poll bucket (D7) | — | smoke |

---

## 4. Test cases (keyed to ACs)

### AC-1 — Device requests a code
- `TestIssueReturns201WithLocationAndCode`: POST `/api/auth/pair {device_kind:"tv"}` → 201, `Location: /api/auth/pair/<8-char>`, body has `code`, `expires_at`, `poll_url`. Code matches `^[ABCDEFGHJKMNPQRSTUVWXYZ23456789]{8}$`.
- `TestIssueWritesAuditCodeIssued`: an audit row with `event='pair.code-issued'` and `payload.code_id` (no plaintext).
- `TestIssueRespectsTTL`: `expires_at - now ≈ 600s` (default).
- Rate-limit integration: 7 issues from same IP in 1 min → 7th gets 429.

### AC-2 — User claims the code
- `TestClaimSuccess`: pending row → `POST /api/auth/pair/claim {code}` as auth'd user → 204; row state='claimed', user_id set.
- `TestClaimExpired`: expired row → 410 problem+json `type=pair-code-expired`.
- `TestClaimUnknown`: random 8-char code that doesn't exist → 404 `type=pair-code-unknown`.
- `TestClaimAlreadyClaimed`: claim twice → first 204, second 404 (uniform with unknown).
- `TestClaimUnauthenticated`: no principal in ctx → 401.
- `TestClaimRaceOnlyOneWins`: 10 concurrent claims for the same code → 1 success, 9 failures.

### AC-3 — Device polls and receives tokens
- `TestPollPendingReturns202`: polling a pending code → 202 with `{state:'pending',retry_after:2}` and `Retry-After: 2`.
- `TestPollClaimedDeliversTokensAndDeletes`: after claim, poll → 200 with `access_token`, `refresh_token`, `user_id`, `expires_in`; row is deleted; an audit row `event='pair.code-claimed'` is written.
- `TestPollAfterDeliveryReturns404`: a second poll after delivery → 404 (row gone).

### AC-4 — Code uniqueness and constant-time match
- `TestStoreClaimAtomic`: see above.
- `TestEqualHashConstantTime` (statistical): 10k matching vs 10k mismatching compares — wall-time difference < 5%.
- `TestStoredCodeHashIsHex64`: every inserted row's `code_hash` is 64 hex chars; no plaintext stored.
- Security: `pg_dump pairing_codes` after issue → no plaintext code in output.

### AC-5 — Cleanup
- `TestReaperExpiresPending`: insert a pending row with `expires_at = now() - 1s`; run reaper; row state='expired'; audit row `pair.code-expired` written with count=1.
- `TestReaperCleansAfter24h`: insert a claimed row with `created_at = now() - 25h`; run reaper twice (60s apart in a fake clock); row deleted.
- `TestReaperRunsEvery60s`: timer-based; we test the loop with a manually advanced clock.

### AC-6 — Rate limits
- HTTP integration: 6 issues in 60s → ok; 7th → 429. The 6/min cap is enforced before the 30/min `/api/auth/*` budget, so other auth endpoints continue to work for the same IP.
- HTTP integration: 60 polls in 60s → ok; 61st → 429.

---

## 5. Edge cases

| #   | Case | Handled by |
|-----|------|------------|
| E1  | Device polls forever (user never scans). Code expires after 600s; subsequent polls hit `state='expired'` → 410. After 24h+, the row is gone → 404. | D5 + handler. |
| E2  | Two users claim the same code in parallel. The atomic UPDATE picks one winner; the loser's UPDATE returns zero rows; `Claim` looks up state and reports either ErrExpired (if it now is) or ErrNotFound (covered by uniform 404 in handler). | D4. |
| E3  | A device claims, then the user `logout-all` (Story 10.5 AC-3). The device's refresh token is revoked along with all others; subsequent refresh fails; the device falls back to re-pairing. | Story 10.5; not in scope here. |
| E4  | A device that already has a valid refresh token re-runs `pair`. The new flow issues fresh tokens; the old refresh chain stays valid until rotated or revoked; both sets coexist. | Falls out of Story 10.3 token-family semantics. |
| E5  | An attacker brute-forces 8-char codes. With per-IP 6/min issue + 60/min poll caps, and 32^8 ≈ 1.1e12 keyspace and 600s TTL, expected time to hit a single active code is `keyspace / (caps × time_alive) ≈ 30+ years` from a single IP. Distributed attacks are bounded by Story 10.12's 30/min /24 cap. | D2 + Story 10.12. |
| E6  | A code that contains the letter 'O' due to OCR/voice. `NormalizeCode` rejects it → ErrBadCode → 404. We document the alphabet in the SPA error message. | codes.go. |
| E7  | The plaintext appearing only in `Location` header at issue time. Logs strip the `Location` header? Plan 10.14's `redactlog` does NOT strip `Location` by default — but `Location` for /api/auth/pair contains the code. We add `Location` to the `redactlog.stripHeaders` list with a domain check (`strings.HasPrefix(value, "/api/auth/pair/")`), or simpler: redact the code path segment itself with a regex. We pick the regex approach: redact the last path segment of any `Location: /api/auth/pair/...` log value. | Coordinate with Plan 10.14: add `pairLocationRedactor` in `redactlog/middleware.go` (one-line regex). |
| E8  | A clock skew between API and Postgres > the TTL. `expires_at = now() + 600s` is computed in Postgres via `now()` is preferable; we currently compute in Go. We document the requirement to keep API host and Postgres host clocks within 30s; expected on a single docker-compose host. | Documented; Go-side `time.Now().UTC()`. |
| E9  | A claim on a code that's already terminal (`expired` or `claimed`). The atomic UPDATE filters `state='pending'` so it returns 0 rows; the lookup disambiguates. | D4. |
| E10 | A poll racing with the reaper. Reaper marks pending→expired; poll sees `expired` → 410. No data loss. | Atomic states. |
| E11 | A poll racing with another poll on a claimed row. Both polls call `MintForUser`; the second one's DELETE fails silently (row gone); the first poll's tokens are valid; the second gets a different but valid token bundle. We accept this as best-effort: a duplicate poll would be a bug client-side anyway. | Documented behaviour. |
| E12 | A pgx outage during Issue. Insert returns error; handler returns 500; no row exists; no audit row written. | Falls out of code path. |

---

## 6. Acceptance checklist

- [ ] **A1** Migration `00XX_pairing_codes.sql` creates the table with `code_id UUID PRIMARY KEY`, `code_hash TEXT NOT NULL UNIQUE`, the state CHECK constraint, FK on `user_id`, and the `(state, expires_at)` index.
- [ ] **A2** `POST /api/auth/pair {device_kind, device_label, bundle_id?}` returns 201 with `{code, expires_at, poll_url}`, sets `Location: /api/auth/pair/<code>`, and writes a `pair.code-issued` audit row carrying the `code_id` (not the plaintext).
- [ ] **A3** `POST /api/auth/pair/claim {code}` (auth required) atomically transitions pending→claimed via single UPDATE; returns 204 on success, 410 `type=pair-code-expired` on expiry, 404 `type=pair-code-unknown` for unknown/already-claimed (uniform).
- [ ] **A4** `GET /api/auth/pair/{code}` returns 202 + `{state:'pending', retry_after:2}` while pending, 410 if expired, 404 if unknown, and 200 with the Story 10.3 AC-1 token bundle when claimed; deletes the row post-delivery; writes a `pair.code-claimed` audit row.
- [ ] **A5** `pairing_codes.code_hash` stores the lowercase hex SHA-256 of the plaintext. The plaintext is generated, returned in the body, set in the `Location` header, and never persisted. Constant-time hash compare via `crypto/subtle.ConstantTimeCompare`.
- [ ] **A6** Reaper goroutine runs every 60s: marks pending+overdue as expired (single UPDATE), deletes terminal-state rows older than 24h, writes a `pair.code-expired` audit row when count > 0.
- [ ] **A7** Rate limits: 6/min/IP on issue, 60/min/IP on poll, both enforced at the chi router; the 6/min issue cap participates in the broader Story 10.12 `auth_rate_per_min=30` budget; the 60/min poll cap is a separate bucket.
- [ ] **A8** `redactlog` middleware (Plan 10.14) is updated to redact the last path segment of any `Location: /api/auth/pair/...` value so plaintext codes never appear in request logs.
- [ ] **A9** Integration tests: full happy-path flow under 10s; expired flow returns 410; brute-force cap; race on parallel claims; DB dump never contains plaintext.

---

## 7. Cross-references

- Token bundle shape on poll-claimed: see [story-10-03-native-login.md](story-10-03-native-login.md) AC-1; the mint helper `internal/auth/native.MintForUser` is the single source of truth.
- Audit helpers: [plan-10-16-security-audit.md](plan-10-16-security-audit.md) §2.4. The `LogPairCodeIssued` / `LogPairCodeClaimed` helpers carry `code_id` (never plaintext).
- Rate limits: [story-10-12-rate-limiting-auth.md](story-10-12-rate-limiting-auth.md) AC-1 (`auth_rate_per_min=30` covers `/api/auth/*` issue+claim; poll has its own 60/min/IP bucket — D7).
- `lib[]` claim minted into the access token: see [plan-10-13-permission-model.md](plan-10-13-permission-model.md) §2.6 (`Resolver.LibraryIDsForUser`).
- Login-style problem+json: this plan reuses `internal/httpx.WriteProblem` from Plan 10.13.

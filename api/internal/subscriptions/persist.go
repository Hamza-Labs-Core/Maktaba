// persist.go adds durable storage for the *applied* license so a
// premium entitlement survives a process restart (Epic 16; gap-closure
// HLB-287).
//
// Before this, Store kept the active entitlement in-memory only — a
// restart silently dropped the customer back to the free tier. The
// fix backs Store with the `licenses` table (migration slot 0056):
//
//   - SetLicense persists the signed license JSON + decoded snapshot,
//     revoking any prior active row (the slot-0056 partial unique index
//     `licenses_only_one_active` enforces one-active-at-a-time).
//   - Revoke stamps revoked_at on the active row.
//   - On construction, NewPersistentStore re-loads the active row and
//     re-verifies the stored signed license against the build-time
//     public key, so a tampered/rotated key fails closed (free tier)
//     rather than trusting a stale snapshot.
//
// The in-memory cache in Store is retained: Current()/Allows() stay
// lock-only with no per-request DB hit. The DB is the source of truth
// only across restarts.
package subscriptions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// persistedLicense is the `licenses` row shape (slot 0056). RevokedAt
// nil means this is the single active license.
type persistedLicense struct {
	LicenseID string
	Tier      Tier
	Seats     int
	IssuedAt  time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
	RawJWT    string // the signed License JSON as POSTed by the admin
	Features  []Feature
}

// LicensePersistence is the durable backend Store writes through to.
// The Postgres implementation (sqlLicenseStore) is the production
// path; tests inject an in-process fake exercising the same
// one-active-row contract. This mirrors the interface-seam convention
// used by authz and the idempotency package.
type LicensePersistence interface {
	// SaveActive durably records rec as the sole active license,
	// revoking any previously-active row first.
	SaveActive(ctx context.Context, rec persistedLicense) error
	// RevokeActive stamps revoked_at on the active row (no-op if none).
	RevokeActive(ctx context.Context) error
	// LoadActive returns the single active license, or nil if the
	// instance is on the free tier.
	LoadActive(ctx context.Context) (*persistedLicense, error)
}

// StatePersistence keeps what the license server last said (slot
// 0155). The SQL backend implements it; a backend without it keeps that
// state in memory only.
type StatePersistence interface {
	SaveState(ctx context.Context, st persistedState) error
	LoadState(ctx context.Context) (*persistedState, error)
}

// persistedState is the `license_state` row (slot 0155).
type persistedState struct {
	Revocations      string
	RevocationsAt    time.Time
	ClockHighWater   time.Time
	LastRefreshAt    time.Time
	LastRefreshError string
	LockReason       string
}

// NewPersistentStore builds a Store whose Set/Revoke writes are
// durable and whose initial state is recovered from the backend. The
// stored signed license is re-verified against v so a key rotation or
// tampered row fails closed (free tier) instead of trusting the
// persisted snapshot. An expired license is kept (Settings says
// "expired") but grants nothing past SkewGrace.
//
// A recovery error is returned: callers (router wiring) decide whether
// to fail boot or degrade to free tier; we surface it rather than
// silently swallow a corrupt store.
func NewPersistentStore(ctx context.Context, db LicensePersistence, v *Verifier) (*Store, error) {
	s := &Store{persist: db}
	// An unreadable state row costs the revocation list and the clock's
	// high-water mark until the next refresh, never the license.
	var stateErr error
	if sp, ok := db.(StatePersistence); ok {
		st, err := sp.LoadState(ctx)
		if err != nil {
			stateErr = fmt.Errorf("subscriptions: load license state: %w", err)
		}
		if st != nil {
			s.lockReason, s.highWater = st.LockReason, st.ClockHighWater.UTC()
			s.lastRefreshAt, s.lastRefreshError = st.LastRefreshAt, st.LastRefreshError
			if st.Revocations != "" && v != nil {
				var list RevocationList
				if json.Unmarshal([]byte(st.Revocations), &list) == nil && v.VerifyRevocations(&list) == nil {
					s.revoked = map[string]bool{}
					for _, id := range list.Revocations.LicenseIDs {
						s.revoked[id] = true
					}
					s.revocationsRaw, s.revocationsAt = st.Revocations, st.RevocationsAt
				}
			}
		}
	}
	rec, err := db.LoadActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("subscriptions: load active license: %w", err)
	}
	if rec == nil {
		return s, stateErr // free tier
	}
	lic, ent, err := s.recover(rec, v)
	if err != nil {
		// A persisted-but-unverifiable license must not silently grant
		// premium; leave the store on the free tier and report.
		return s, fmt.Errorf("subscriptions: recover active license %q: %w", rec.LicenseID, err)
	}
	s.license, s.raw = ent, lic
	return s, stateErr
}

// recover re-derives the Entitlements from the persisted signed
// license. Re-verifying (rather than trusting the snapshot columns)
// keeps the build-time public key the single source of truth.
func (s *Store) recover(rec *persistedLicense, v *Verifier) (*License, *Entitlements, error) {
	if v == nil {
		return nil, nil, errNoVerifier
	}
	var lic License
	if err := json.Unmarshal([]byte(rec.RawJWT), &lic); err != nil {
		return nil, nil, fmt.Errorf("decode stored license: %w", err)
	}
	ent, err := v.VerifySignature(&lic)
	if err != nil {
		return nil, nil, err
	}
	return &lic, ent, nil
}

func recordOf(lic *License) persistedLicense {
	raw, _ := json.Marshal(lic) // a License always marshals
	return persistedLicense{
		LicenseID: lic.License.LicenseID,
		Tier:      lic.License.Tier,
		Seats:     lic.License.Seats,
		IssuedAt:  lic.License.IssuedAt,
		ExpiresAt: lic.License.ExpiresAt,
		RawJWT:    string(raw),
		Features:  lic.License.Features,
	}
}

// sqlLicenseStore is the Postgres-backed LicensePersistence. It maps
// directly onto the `licenses` table (slot 0056). The partial unique
// index `licenses_only_one_active` (WHERE revoked_at IS NULL)
// guarantees the one-active invariant at the DB level; SaveActive
// additionally revokes the prior row inside a transaction so the
// invariant holds even without that index (e.g. the SQLite sibling).
type sqlLicenseStore struct {
	db *sql.DB
}

// NewSQLLicensePersistence wraps an *sql.DB as a LicensePersistence.
// This is the production backend wired by the router when a DB handle
// is available; it follows the same *sql.DB pattern as ACLStore /
// sessions.Store.
func NewSQLLicensePersistence(db *sql.DB) LicensePersistence {
	return &sqlLicenseStore{db: db}
}

func (p *sqlLicenseStore) SaveActive(ctx context.Context, rec persistedLicense) error {
	feats, err := json.Marshal(rec.Features)
	if err != nil {
		return err
	}
	if rec.Features == nil {
		feats = []byte("[]")
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Drop the prior active license so the partial unique index admits
	// the new row (and so the invariant holds on the SQLite sibling,
	// which has no partial unique index).
	if _, err := tx.ExecContext(ctx,
		`UPDATE licenses SET revoked_at = $1 WHERE revoked_at IS NULL`,
		time.Now().UTC(),
	); err != nil {
		return err
	}
	// Upsert by license_id: re-applying a previously-revoked key
	// re-activates that same row rather than colliding on the PK.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO licenses
		   (license_id, tier, seats, issued_at, expires_at, revoked_at, raw_jwt, features)
		 VALUES ($1, $2, $3, $4, $5, NULL, $6, $7)
		 ON CONFLICT (license_id) DO UPDATE SET
		   tier       = EXCLUDED.tier,
		   seats      = EXCLUDED.seats,
		   issued_at  = EXCLUDED.issued_at,
		   expires_at = EXCLUDED.expires_at,
		   revoked_at = NULL,
		   raw_jwt    = EXCLUDED.raw_jwt,
		   features   = EXCLUDED.features`,
		rec.LicenseID, string(rec.Tier), rec.Seats,
		rec.IssuedAt, rec.ExpiresAt, rec.RawJWT, string(feats),
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *sqlLicenseStore) RevokeActive(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE licenses SET revoked_at = $1 WHERE revoked_at IS NULL`,
		time.Now().UTC())
	return err
}

func (p *sqlLicenseStore) LoadActive(ctx context.Context) (*persistedLicense, error) {
	var rec persistedLicense
	var tier string
	var featsRaw []byte
	err := p.db.QueryRowContext(ctx,
		`SELECT license_id, tier, seats, issued_at, expires_at, raw_jwt, features
		   FROM licenses
		  WHERE revoked_at IS NULL
		  ORDER BY issued_at DESC
		  LIMIT 1`,
	).Scan(&rec.LicenseID, &tier, &rec.Seats, &rec.IssuedAt,
		&rec.ExpiresAt, &rec.RawJWT, &featsRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rec.Tier = Tier(tier)
	if len(featsRaw) > 0 {
		if err := json.Unmarshal(featsRaw, &rec.Features); err != nil {
			return nil, fmt.Errorf("decode features: %w", err)
		}
	}
	return &rec, nil
}

func (p *sqlLicenseStore) SaveState(ctx context.Context, st persistedState) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO license_state
		   (id, revocations, revocations_at, clock_high_water, last_refresh_at, last_refresh_error, lock_reason)
		 VALUES (1, $1, $2, $3, $4, $5, $6)
		 ON CONFLICT (id) DO UPDATE SET
		   revocations        = EXCLUDED.revocations,
		   revocations_at     = EXCLUDED.revocations_at,
		   clock_high_water   = EXCLUDED.clock_high_water,
		   last_refresh_at    = EXCLUDED.last_refresh_at,
		   last_refresh_error = EXCLUDED.last_refresh_error,
		   lock_reason        = EXCLUDED.lock_reason`,
		st.Revocations, nullTime(st.RevocationsAt), nullTime(st.ClockHighWater),
		nullTime(st.LastRefreshAt), st.LastRefreshError, st.LockReason)
	return err
}

func (p *sqlLicenseStore) LoadState(ctx context.Context) (*persistedState, error) {
	var st persistedState
	var revAt, hw, refAt sql.NullTime
	err := p.db.QueryRowContext(ctx,
		`SELECT revocations, revocations_at, clock_high_water, last_refresh_at, last_refresh_error, lock_reason
		   FROM license_state WHERE id = 1`,
	).Scan(&st.Revocations, &revAt, &hw, &refAt, &st.LastRefreshError, &st.LockReason)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st.RevocationsAt, st.ClockHighWater, st.LastRefreshAt = revAt.Time, hw.Time, refAt.Time
	return &st, nil
}

func nullTime(t time.Time) sql.NullTime {
	return sql.NullTime{Time: t.UTC(), Valid: !t.IsZero()}
}

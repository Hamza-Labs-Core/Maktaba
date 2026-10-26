package subscriptions

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// SkewGrace is how long an expired license keeps working (story 16.2
// EC: clock skew between the home and the license server). A license
// the cloud issues lasts 31 days and is refreshed daily, so a home cut
// off from the license server locks about 34 days after its last
// refresh (story 16.4: 30 d offline grace, locked by 35 d).
const SkewGrace = 72 * time.Hour

// Status is where the applied license stands.
type Status string

const (
	// StatusNone: no license applied; the free tier.
	StatusNone Status = "none"
	// StatusActive: the license grants its tier.
	StatusActive Status = "active"
	// StatusGrace: past expires_at, inside SkewGrace; still granted.
	StatusGrace Status = "grace"
	// StatusExpired: past expires_at + SkewGrace; the free tier.
	StatusExpired Status = "expired"
	// StatusRevoked: the license server revoked it; the free tier.
	StatusRevoked Status = "revoked"
)

// LockRevoked is the lock reason a revocation records.
const LockRevoked = "revoked"

// State is the full picture Settings shows an admin.
type State struct {
	// License is the applied license, whether or not it grants anything
	// now (nil: none applied).
	License *Entitlements
	// Effective is what the gates use (nil: free tier).
	Effective        *Entitlements
	Status           Status
	LockReason       string
	LastRefreshAt    time.Time
	LastRefreshError string
	RevocationsAt    time.Time
	Now              time.Time
}

// Store is the runtime cache of the applied license and what the
// license server last said about it. Handlers go through Current()
// rather than reading the disk every request.
type Store struct {
	mu sync.RWMutex
	// license is the applied, signature-checked license (nil: none).
	license *Entitlements
	// raw is the applied license as signed, for persistence.
	raw *License

	lockReason       string
	revoked          map[string]bool
	revocationsRaw   string
	revocationsAt    time.Time
	lastRefreshAt    time.Time
	lastRefreshError string
	// highWater is the latest time seen (the local clock or the license
	// server's): a clock set back cannot revive an expired license.
	highWater time.Time

	// clock is time.Now in production; tests set it.
	clock func() time.Time

	// persist, when non-nil, makes Set/Revoke durable so an applied
	// license survives a process restart (HLB-287). Nil keeps the
	// store in-memory only — the original behaviour, retained for
	// tests and the no-DB dev path.
	persist LicensePersistence
}

// NewStore creates a Store seeded with the free tier.
func NewStore() *Store {
	return &Store{}
}

// SetClock replaces the store's clock (tests).
func (s *Store) SetClock(f func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock = f
}

// now is the store's time: the local clock, never earlier than the
// high-water mark. Callers hold s.mu.
func (s *Store) nowLocked() time.Time {
	c := s.clock
	if c == nil {
		c = time.Now
	}
	t := c().UTC()
	if t.Before(s.highWater) {
		return s.highWater
	}
	return t
}

// Now returns the store's notion of the current time.
func (s *Store) Now() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nowLocked()
}

// Set replaces the applied entitlement in memory. Pass nil to revert to
// the free tier. It clears any lock (tests and the no-DB path).
func (s *Store) Set(e *Entitlements) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.license = e
	s.lockReason = ""
}

// Current returns the effective entitlement. nil means free tier: no
// license, an expired one, or a revoked one.
func (s *Store) Current() *Entitlements {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ent, _ := s.effectiveLocked()
	return ent
}

func (s *Store) effectiveLocked() (*Entitlements, Status) {
	if s.license == nil {
		return nil, StatusNone
	}
	if s.lockReason == LockRevoked || s.revoked[s.license.LicenseID] {
		return nil, StatusRevoked
	}
	now := s.nowLocked()
	if s.license.ExpiresAt.IsZero() || now.Before(s.license.ExpiresAt) {
		return s.license, StatusActive
	}
	if now.Before(s.license.ExpiresAt.Add(SkewGrace)) {
		return s.license, StatusGrace
	}
	return nil, StatusExpired
}

// State reports the applied license, what it grants now, and the last
// word from the license server.
func (s *Store) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	eff, st := s.effectiveLocked()
	lock := s.lockReason
	if st == StatusRevoked {
		lock = LockRevoked
	}
	return State{
		License: s.license, Effective: eff, Status: st, LockReason: lock,
		LastRefreshAt: s.lastRefreshAt, LastRefreshError: s.lastRefreshError,
		RevocationsAt: s.revocationsAt, Now: s.nowLocked(),
	}
}

// Allows is a convenience wrapper.
func (s *Store) Allows(f Feature) bool {
	return s.Current().Allows(f)
}

// SeatLimit reports the current entitlement's seat cap (Story 16.2). A
// nil/free store is single-user (1). This is the value the user-create
// gate enforces against.
func (s *Store) SeatLimit() int {
	return s.Current().SeatLimit()
}

// TierName is the effective tier (free when nothing is granted).
func (s *Store) TierName() string {
	if e := s.Current(); e != nil {
		return string(e.Tier)
	}
	return string(TierFree)
}

// SetLicense applies lic (already verified by the caller into ent),
// caching it in-memory and, when a persistence backend is configured,
// durably recording it as the sole active license. Applying a license
// clears a revocation lock: a revoked id stays refused by the list.
func (s *Store) SetLicense(ctx context.Context, lic *License, ent *Entitlements) error {
	if s.persist != nil {
		if err := s.persist.SaveActive(ctx, recordOf(lic)); err != nil {
			return fmt.Errorf("subscriptions: persist license: %w", err)
		}
	}
	s.mu.Lock()
	s.license, s.raw, s.lockReason = ent, lic, ""
	s.mu.Unlock()
	return s.saveState(ctx)
}

// Revoke durably reverts the instance to the free tier (an admin
// removed the license, or the cloud says the plan ended). With no
// persistence backend it is equivalent to Set(nil).
func (s *Store) Revoke(ctx context.Context) error {
	if s.persist != nil {
		if err := s.persist.RevokeActive(ctx); err != nil {
			return fmt.Errorf("subscriptions: persist revoke: %w", err)
		}
	}
	s.mu.Lock()
	s.license, s.raw, s.lockReason = nil, nil, ""
	s.mu.Unlock()
	return s.saveState(ctx)
}

// Lock records that the license server revoked the applied license: the
// home drops to the free tier at once and keeps the license on record so
// Settings can say why (story 16.4).
func (s *Store) Lock(ctx context.Context, reason string) error {
	s.mu.Lock()
	s.lockReason = reason
	s.mu.Unlock()
	return s.saveState(ctx)
}

// SetRevocations replaces the revocation list (already verified). A
// license on it is refused from now on; one taken off it works again.
func (s *Store) SetRevocations(ctx context.Context, list *RevocationList, raw string, at time.Time) error {
	ids := map[string]bool{}
	for _, id := range list.Revocations.LicenseIDs {
		ids[id] = true
	}
	s.mu.Lock()
	s.revoked, s.revocationsRaw, s.revocationsAt = ids, raw, at.UTC()
	s.mu.Unlock()
	return s.saveState(ctx)
}

// ObserveServerTime moves the clock to the license server's time: when
// it is reachable its time wins over the local clock (story 16.4 EC),
// earlier or later.
func (s *Store) ObserveServerTime(t time.Time) {
	if t.IsZero() {
		return
	}
	s.mu.Lock()
	s.highWater = t.UTC()
	s.mu.Unlock()
}

// RecordRefresh notes the outcome of a refresh with the license server.
func (s *Store) RecordRefresh(ctx context.Context, err error) error {
	s.mu.Lock()
	if err == nil {
		s.lastRefreshAt, s.lastRefreshError = s.nowLocked(), ""
	} else {
		s.lastRefreshError = err.Error()
	}
	s.mu.Unlock()
	return s.saveState(ctx)
}

// TouchClock persists the clock's high-water mark (it moves on with the
// local clock), with nothing else changed.
func (s *Store) TouchClock(ctx context.Context) error {
	return s.saveState(ctx)
}

// Fingerprint identifies the applied license's signed content, so a
// refresh that fetched the same license changes nothing.
func (s *Store) Fingerprint() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.raw == nil {
		return ""
	}
	return s.raw.Signature
}

// saveState persists the license server's last word, when the backend
// keeps it.
func (s *Store) saveState(ctx context.Context) error {
	sp, ok := s.persist.(StatePersistence)
	if !ok {
		return nil
	}
	s.mu.Lock()
	// the clock's high-water moves on with the local clock too
	s.highWater = s.nowLocked()
	st := persistedState{
		Revocations: s.revocationsRaw, RevocationsAt: s.revocationsAt,
		ClockHighWater: s.highWater, LastRefreshAt: s.lastRefreshAt,
		LastRefreshError: s.lastRefreshError, LockReason: s.lockReason,
	}
	s.mu.Unlock()
	if err := sp.SaveState(ctx, st); err != nil {
		return fmt.Errorf("subscriptions: persist license state: %w", err)
	}
	return nil
}

// errNoVerifier: a persisted license cannot be checked without the key.
var errNoVerifier = errors.New("no verifier configured")

// seatLimiter adapts a *Store to the auth handler's SeatLimiter seam. It
// reads the live entitlement on every call so a mid-process license
// change (apply/revoke/expiry) takes effect immediately.
type seatLimiter struct{ s *Store }

func (l seatLimiter) SeatLimit() int { return l.s.SeatLimit() }

// TierName lets the seat gate name the plan in its refusal.
func (l seatLimiter) TierName() string { return l.s.TierName() }

// NewSeatLimiter wraps the live entitlement Store as the seat-cap
// source for Epic 16 Story 16.2 enforcement on user creation. A nil
// store yields a limiter that always reports the free-tier cap (1),
// which fails closed rather than panicking.
func NewSeatLimiter(s *Store) interface {
	SeatLimit() int
	TierName() string
} {
	if s == nil {
		s = NewStore()
	}
	return seatLimiter{s: s}
}

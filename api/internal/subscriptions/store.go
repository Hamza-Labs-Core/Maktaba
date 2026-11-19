package subscriptions

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Grace is how long a license's paid features keep working, fully, after it
// expires or is replaced by a lower plan (story 16.2, the owner's decision of
// 2026-11-19): counted from the license's expiry (a replaced license ends when it is
// replaced, if that is earlier), then they lock. It covers the clock skew between the
// home and the license server too (the 72 h of the EC). A license the cloud issues
// lasts 31 days and is refreshed daily, so a home cut off from the license server keeps
// its plan about 61 days after its last refresh. A revocation (fraud) locks at once.
const Grace = 30 * 24 * time.Hour

// Status is where the applied license stands.
type Status string

const (
	// StatusNone: no license applied; the free tier.
	StatusNone Status = "none"
	// StatusActive: the license grants its tier.
	StatusActive Status = "active"
	// StatusGrace: past expires_at (or replaced by a lower plan), inside Grace; still
	// granted, fully, until State.GraceUntil.
	StatusGrace Status = "grace"
	// StatusExpired: past expires_at + Grace; the free tier.
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
	// GraceUntil is when the features granted only by Grace lock (an expired license's,
	// a replaced one's): zero when nothing is in its grace.
	GraceUntil time.Time
}

// ended is a license this home had before the applied one (replaced by another, or
// removed): what it granted keeps working until until (Grace after its end).
type ended struct {
	ent   *Entitlements
	until time.Time
}

// Store is the runtime cache of the applied license and what the
// license server last said about it. Handlers go through Current()
// rather than reading the disk every request.
type Store struct {
	mu sync.RWMutex
	// license is the applied, signature-checked license (nil: none).
	license *Entitlements
	// previous are the licenses replaced or removed less than Grace ago (story 16.2:
	// a downgrade keeps the old plan's features for Grace).
	previous []ended
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

	// onChange hear every change of the applied license or its standing (OnChange).
	onChange []func()
}

// OnChange adds f, called after every change of the applied license or its standing: an
// apply, a refresh's new license, a take-up of one another process recorded, a
// revocation, a lock, a new revocation list (Epic 51 story 51.19: the components'
// desired rows publish what the license allows). f runs on the caller's goroutine,
// after the store's lock is released.
func (s *Store) OnChange(f func()) {
	s.mu.Lock()
	s.onChange = append(s.onChange, f)
	s.mu.Unlock()
}

func (s *Store) changed() {
	s.mu.RLock()
	fs := append([]func(){}, s.onChange...)
	s.mu.RUnlock()
	for _, f := range fs {
		f()
	}
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
	s.license = e
	s.lockReason = ""
	s.mu.Unlock()
	s.changed()
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
	ent, st, _ := s.grantLocked()
	return ent, st
}

// grantLocked is what the gates use now, the license's standing, and when what only
// Grace grants locks (zero: nothing). The applied license grants its plan while it is
// valid and for Grace after its expiry; each license replaced or removed less than Grace
// ago adds its plan until its own end + Grace (the higher plan wins: a downgrade keeps
// working fully). A revocation (the lock, or the list) grants nothing, at once.
func (s *Store) grantLocked() (*Entitlements, Status, time.Time) {
	if s.lockReason == LockRevoked || (s.license != nil && s.revoked[s.license.LicenseID]) {
		return nil, StatusRevoked, time.Time{}
	}
	now := s.nowLocked()
	var parts []*Entitlements
	var graceUntil time.Time
	grace := func(until time.Time) {
		if until.After(graceUntil) {
			graceUntil = until
		}
	}
	st := StatusNone
	if l := s.license; l != nil {
		switch {
		case l.ExpiresAt.IsZero() || now.Before(l.ExpiresAt):
			parts, st = append(parts, l), StatusActive
		case now.Before(l.ExpiresAt.Add(Grace)):
			parts, st = append(parts, l), StatusGrace
			grace(l.ExpiresAt.Add(Grace))
		default:
			st = StatusExpired
		}
	}
	for _, p := range s.previous {
		if s.revoked[p.ent.LicenseID] || !now.Before(p.until) {
			continue
		}
		if st != StatusActive || addsTo(s.license, p.ent) {
			grace(p.until)
		}
		parts = append(parts, p.ent)
		if st != StatusActive {
			st = StatusGrace
		}
	}
	switch len(parts) {
	case 0:
		return nil, st, time.Time{}
	case 1:
		return parts[0], st, graceUntil
	}
	return merge(parts), st, graceUntil
}

// addsTo: prev grants something cur does not (a feature, or seats).
func addsTo(cur, prev *Entitlements) bool {
	if cur == nil {
		return true
	}
	for f, on := range prev.Features {
		if on && !cur.Features[f] {
			return true
		}
	}
	cs, ps := cur.SeatLimit(), prev.SeatLimit()
	return cs != SeatsUnlimited && (ps == SeatsUnlimited || ps > cs)
}

// merge is the union of what parts grant: every feature, the most seats, the highest
// tier. The first part (the applied license, when it grants) names the license.
func merge(parts []*Entitlements) *Entitlements {
	out := *parts[0]
	out.Features = map[Feature]bool{}
	seats := 0
	for _, p := range parts {
		for f, on := range p.Features {
			if on {
				out.Features[f] = true
			}
		}
		if tierRank[p.Tier] > tierRank[out.Tier] {
			out.Tier = p.Tier
		}
		switch n := p.SeatLimit(); {
		case seats == SeatsUnlimited:
		case n == SeatsUnlimited || n > seats:
			seats = n
		}
	}
	out.seatFloor = seats
	return &out
}

// retire moves the applied license to previous (it was replaced or removed now): what it
// granted keeps working until its end (its expiry, or now if that is earlier) + Grace.
// A revoked license is not kept. Callers hold s.mu.
func (s *Store) retireLocked() {
	l := s.license
	if l == nil || s.lockedLocked() {
		return
	}
	end := s.nowLocked()
	if !l.ExpiresAt.IsZero() && l.ExpiresAt.Before(end) {
		end = l.ExpiresAt
	}
	s.keepEndedLocked(l, end.Add(Grace))
}

// lockedLocked: the applied license is revoked (the cloud's lock, or the list). Callers
// hold s.mu.
func (s *Store) lockedLocked() bool {
	return s.license != nil && (s.lockReason == LockRevoked || s.revoked[s.license.LicenseID])
}

// keepEndedLocked records an ended license (dropping those whose grace is over).
func (s *Store) keepEndedLocked(e *Entitlements, until time.Time) {
	now := s.nowLocked()
	kept := s.previous[:0]
	for _, p := range s.previous {
		if now.Before(p.until) && (p.ent.LicenseID != e.LicenseID || !p.ent.IssuedAt.Equal(e.IssuedAt)) {
			kept = append(kept, p)
		}
	}
	s.previous = kept
	if now.Before(until) {
		s.previous = append(s.previous, ended{ent: e, until: until})
	}
}

// Until is when f stops being granted: the latest end of a grant of it (the applied
// license's expiry + Grace, an ended one's grace). ok is false when f is not granted;
// a zero time with ok is a grant that does not end.
func (s *Store) Until(f Feature) (time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	eff, _, _ := s.grantLocked()
	if !eff.Allows(f) {
		return time.Time{}, false
	}
	if FreeFeatures[f] {
		return time.Time{}, true
	}
	now := s.nowLocked()
	var until time.Time
	if l := s.license; l != nil && l.Features[f] && (l.ExpiresAt.IsZero() || now.Before(l.ExpiresAt.Add(Grace))) {
		if l.ExpiresAt.IsZero() {
			return time.Time{}, true
		}
		until = l.ExpiresAt.Add(Grace)
	}
	for _, p := range s.previous {
		if p.ent.Features[f] && !s.revoked[p.ent.LicenseID] && now.Before(p.until) && p.until.After(until) {
			until = p.until
		}
	}
	return until, true
}

// State reports the applied license, what it grants now, and the last
// word from the license server.
func (s *Store) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	eff, st, until := s.grantLocked()
	lock := s.lockReason
	if st == StatusRevoked {
		lock = LockRevoked
	}
	return State{
		License: s.license, Effective: eff, Status: st, LockReason: lock,
		LastRefreshAt: s.lastRefreshAt, LastRefreshError: s.lastRefreshError,
		RevocationsAt: s.revocationsAt, Now: s.nowLocked(), GraceUntil: until,
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
	s.mu.RLock()
	old, oldRaw, locked := s.license, s.raw, s.lockedLocked()
	s.mu.RUnlock()
	// a plan license keeps its id from plan to plan (the cloud's cloud-<server>): one that
	// now grants less is a downgrade all the same, kept in its grace
	sameIDDown := old != nil && ent != nil && old.LicenseID == ent.LicenseID && !locked && addsTo(ent, old)
	ep, _ := s.persist.(EndedPersistence)
	if sameIDDown && ep != nil && oldRaw != nil {
		// its row is about to be overwritten: the ended one is kept as a row of its own,
		// ended at the store's time (as the grace in memory counts it)
		rec := recordOf(oldRaw)
		s.mu.RLock()
		end := s.nowLocked()
		s.mu.RUnlock()
		rec.RevokedAt = &end
		if err := ep.SaveEnded(ctx, rec); err != nil {
			return fmt.Errorf("subscriptions: persist the ended license: %w", err)
		}
	}
	if s.persist != nil {
		if err := s.persist.SaveActive(ctx, recordOf(lic)); err != nil {
			return fmt.Errorf("subscriptions: persist license: %w", err)
		}
	}
	if locked && ep != nil && old != nil && (ent == nil || old.LicenseID != ent.LicenseID) {
		// a revoked license replaced: its row's end is no grace after a restart
		if err := ep.DropGrace(ctx, old.LicenseID); err != nil {
			return fmt.Errorf("subscriptions: persist the revoked license: %w", err)
		}
	}
	s.mu.Lock()
	if old == nil || ent == nil || old.LicenseID != ent.LicenseID || sameIDDown {
		s.retireLocked()
	}
	s.license, s.raw, s.lockReason = ent, lic, ""
	s.mu.Unlock()
	defer s.changed()
	return s.saveState(ctx)
}

// ReloadActive takes up the active license as another process recorded it (the
// `maktaba-api license apply` command, another replica): it reports whether the
// license changed. One the build's key cannot verify is not taken up.
func (s *Store) ReloadActive(ctx context.Context, v *Verifier) (bool, error) {
	if s.persist == nil {
		return false, nil
	}
	rec, err := s.persist.LoadActive(ctx)
	if err != nil {
		return false, err
	}
	s.mu.RLock()
	cur := ""
	if s.license != nil {
		cur = s.license.LicenseID
	}
	s.mu.RUnlock()
	if rec == nil {
		if cur == "" {
			return false, nil
		}
		s.mu.Lock()
		s.retireLocked()
		s.license, s.raw = nil, nil
		s.mu.Unlock()
		s.changed()
		return true, nil
	}
	if rec.LicenseID == cur {
		return false, nil
	}
	lic, ent, err := s.recover(rec, v)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	s.retireLocked()
	s.license, s.raw, s.lockReason = ent, lic, ""
	s.mu.Unlock()
	s.changed()
	return true, nil
}

// Revoke durably reverts the instance to the free tier (an admin
// removed the license, or the cloud says the plan ended): what the license
// granted keeps working for Grace from its end (story 16.2). With no
// persistence backend it is equivalent to Set(nil).
func (s *Store) Revoke(ctx context.Context) error {
	s.mu.RLock()
	old, oldRaw, locked, now := s.license, s.raw, s.lockedLocked(), s.nowLocked()
	s.mu.RUnlock()
	if ep, ok := s.persist.(EndedPersistence); ok && old != nil && oldRaw != nil && !locked {
		// kept as a row of its own: the plan license's next issue (the same id, a lower
		// plan) overwrites the plain row, and with it this end
		rec := recordOf(oldRaw)
		rec.RevokedAt = &now
		if err := ep.SaveEnded(ctx, rec); err != nil {
			return fmt.Errorf("subscriptions: persist the ended license: %w", err)
		}
	}
	if s.persist != nil {
		if err := s.persist.RevokeActive(ctx); err != nil {
			return fmt.Errorf("subscriptions: persist revoke: %w", err)
		}
	}
	if ep, ok := s.persist.(EndedPersistence); ok && locked && old != nil {
		if err := ep.DropGrace(ctx, old.LicenseID); err != nil {
			return fmt.Errorf("subscriptions: persist the revoked license: %w", err)
		}
	}
	s.mu.Lock()
	s.retireLocked()
	s.license, s.raw, s.lockReason = nil, nil, ""
	s.mu.Unlock()
	defer s.changed()
	return s.saveState(ctx)
}

// Lock records that the license server revoked the applied license: the
// home drops to the free tier at once and keeps the license on record so
// Settings can say why (story 16.4).
func (s *Store) Lock(ctx context.Context, reason string) error {
	s.mu.Lock()
	s.lockReason = reason
	var id string
	if reason == LockRevoked && s.license != nil {
		// a revocation (fraud) leaves no grace of that license behind: the plans it had
		// before (a same-id downgrade's) end with it, and never come back when another
		// license clears the lock
		id = s.license.LicenseID
		kept := s.previous[:0]
		for _, p := range s.previous {
			if p.ent.LicenseID != id {
				kept = append(kept, p)
			}
		}
		s.previous = kept
	}
	s.mu.Unlock()
	defer s.changed()
	// the lock first: a restart after a failure below still finds the license locked
	if err := s.saveState(ctx); err != nil {
		return err
	}
	if ep, ok := s.persist.(EndedPersistence); ok && id != "" {
		if err := ep.DropGrace(ctx, id); err != nil {
			return fmt.Errorf("subscriptions: persist the revoked license: %w", err)
		}
	}
	return nil
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
	defer s.changed()
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

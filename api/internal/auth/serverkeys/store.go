package serverkeys

import (
	"crypto/ed25519"
	"errors"
	"sync"
	"time"
)

// DefaultOverlapDuration is the window after a rotation during
// which the previous key's signatures still verify (Story 10.18
// AC-6). 72 hours.
const DefaultOverlapDuration = 72 * time.Hour

// Clock is a minimal monotonic-aware time source so tests can
// inject deterministic timing.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// SystemClock is the production clock.
var SystemClock Clock = realClock{}

// Store holds the active server-identity key plus any overlap-
// predecessor key. Concurrent reads + verifications are safe; the
// only mutator is `Rotate`.
type Store struct {
	mu              sync.RWMutex
	active          *Key
	overlap         *Key // verify-only predecessor; nil outside rotation overlap
	overlapExpires  time.Time
	overlapDuration time.Duration
	clock           Clock
}

// NewStore constructs a store seeded with `active`. Pass
// `overlapDuration <= 0` to use `DefaultOverlapDuration`.
func NewStore(active *Key, overlapDuration time.Duration, clock Clock) (*Store, error) {
	if active == nil {
		return nil, errors.New("serverkeys: active key required")
	}
	if active.PrivateKey == nil {
		return nil, errors.New("serverkeys: active key must include private bytes")
	}
	if clock == nil {
		clock = SystemClock
	}
	if overlapDuration <= 0 {
		overlapDuration = DefaultOverlapDuration
	}
	return &Store{active: active, overlapDuration: overlapDuration, clock: clock}, nil
}

// Active returns the currently signing key.
func (s *Store) Active() *Key {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

// Lookup returns the key matching `kid` (active or overlap
// predecessor) and a boolean indicating presence. The overlap
// predecessor is excluded once `overlapExpires` is past.
func (s *Store) Lookup(kid string) (*Key, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.active != nil && s.active.Kid == kid {
		return s.active, true
	}
	if s.overlap != nil && s.overlap.Kid == kid && s.clock.Now().Before(s.overlapExpires) {
		return s.overlap, true
	}
	return nil, false
}

// Sign returns an Ed25519 signature over `payload` plus the kid of
// the signing key.
func (s *Store) Sign(payload []byte) ([]byte, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ed25519.Sign(s.active.PrivateKey, payload), s.active.Kid
}

// Verify checks `sig` against `payload` using the public key
// matching `kid`. Returns `ErrUnknownKid` if `kid` is not known
// (including expired overlap predecessors) and `ErrBadSig` if the
// signature does not verify.
func (s *Store) Verify(payload, sig []byte, kid string) error {
	key, ok := s.Lookup(kid)
	if !ok {
		return ErrUnknownKid
	}
	if !ed25519.Verify(key.PublicKey, payload, sig) {
		return ErrBadSig
	}
	return nil
}

// RotateOptions controls a single rotation.
type RotateOptions struct {
	Immediate bool
	Reason    string
}

// RotateResult reports the outcome of a Rotate call for the
// caller's audit logging.
type RotateResult struct {
	OldKid          string
	NewKid          string
	OverlapSeconds  int
	OverlapExpiresAt time.Time
}

// Rotate generates a fresh key, makes it active, and (unless
// `Immediate` is true) keeps the previous key around as a
// verify-only predecessor for `overlapDuration`.
func (s *Store) Rotate(opts RotateOptions) (*RotateResult, error) {
	now := s.clock.Now()
	next, err := Generate(now, "generated")
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.active
	s.active = next
	res := &RotateResult{OldKid: old.Kid, NewKid: next.Kid}
	if opts.Immediate {
		s.overlap = nil
		s.overlapExpires = time.Time{}
	} else {
		// Strip the private bytes from the overlap predecessor to
		// make it verify-only.
		s.overlap = &Key{
			Kid:       old.Kid,
			PublicKey: old.PublicKey,
			CreatedAt: old.CreatedAt,
			Source:    old.Source,
		}
		s.overlapExpires = now.Add(s.overlapDuration)
		res.OverlapSeconds = int(s.overlapDuration.Seconds())
		res.OverlapExpiresAt = s.overlapExpires
	}
	return res, nil
}

// OverlapKey returns the predecessor key if one is currently in
// its overlap window, otherwise nil. Test helper / JWKS publisher.
func (s *Store) OverlapKey() (*Key, time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.overlap == nil || !s.clock.Now().Before(s.overlapExpires) {
		return nil, time.Time{}, false
	}
	return s.overlap, s.overlapExpires, true
}

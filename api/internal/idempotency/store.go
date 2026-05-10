// Package idempotency stores the per-key request/response pair the
// API replays when a client retries a mutation with the same
// Idempotency-Key header. The interface is implemented in-memory here
// (single-process correctness) and will gain a Postgres backing in a
// later story so retries survive process restarts.
//
// Story 7.1 AC-4.
package idempotency

import (
	"context"
	"sync"
	"time"
)

// Record is one cached entry. RequestHash is the sha256 of the request
// body so the middleware can detect a retry that changed the body
// (which is a 409, never a replay).
type Record struct {
	Key         string
	UserID      string
	RequestHash string
	Status      int
	Body        []byte
	Headers     map[string]string
	CreatedAt   time.Time
}

// Store is the abstract surface every backend implements.
type Store interface {
	Lookup(ctx context.Context, key, userID string) (Record, bool)
	Save(ctx context.Context, r Record) error
	SweepExpired(ctx context.Context, ttl time.Duration) (int, error)
}

// MemoryStore is an in-memory Store. Safe for concurrent use; entries
// are dropped on process restart. Suitable for the skeleton-stage
// API; Postgres backing lands when AC-4 acquires the migration slot.
type MemoryStore struct {
	mu sync.RWMutex
	m  map[string]Record
}

// NewMemoryStore returns an empty MemoryStore. The caller is
// responsible for periodically calling SweepExpired so the map doesn't
// grow without bound.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{m: map[string]Record{}}
}

func compositeKey(key, userID string) string {
	// userID is empty for unauthenticated routes; the empty-string
	// value is fine because the caller's contract is "same user-key
	// pair = replay" and unauthenticated callers all share one bucket.
	return userID + "\x00" + key
}

// Lookup returns a previously cached record, if any. The bool is false
// when no record exists; the caller MUST process the request from
// scratch in that case.
func (s *MemoryStore) Lookup(_ context.Context, key, userID string) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.m[compositeKey(key, userID)]
	return r, ok
}

// Save stores the response so future requests with the same key can
// replay it. The CreatedAt field is stamped here when zero so callers
// don't have to remember.
func (s *MemoryStore) Save(_ context.Context, r Record) error {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[compositeKey(r.Key, r.UserID)] = r
	return nil
}

// SweepExpired removes every record older than ttl. The returned count
// is the number of rows dropped; useful for observability.
func (s *MemoryStore) SweepExpired(_ context.Context, ttl time.Duration) (int, error) {
	cutoff := time.Now().Add(-ttl)
	dropped := 0
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, r := range s.m {
		if r.CreatedAt.Before(cutoff) {
			delete(s.m, k)
			dropped++
		}
	}
	return dropped, nil
}

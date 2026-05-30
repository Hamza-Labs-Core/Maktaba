// Package discovery implements LAN service discovery (Epic 15 plans 15.1/15.5/15.6).
//
// Two surfaces:
//
//  1. mDNS / DNS-SD service publication so clients on the same LAN can
//     find the API server without manual entry. Each Service captures
//     the type ("_maktaba._tcp"), the instance name, port, and a set of
//     TXT records describing the offered protocols.
//
//  2. QR-code pairing: short-lived numeric codes that map to a one-shot
//     device-registration ticket. The TV displays the code; the phone
//     scans/types it; the phone exchanges it for a device token via
//     POST /api/pairing/exchange (Story 15.6).
//
// The package is *transport-agnostic*: the mDNS Publisher and the
// PairingStore are interfaces so production uses (zeroconf, postgres)
// or tests (in-memory) can be swapped in.
package discovery

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ServiceType is the DNS-SD service identifier Maktaba registers.
const ServiceType = "_maktaba._tcp"

// Service describes the announcement payload.
type Service struct {
	Instance string            // e.g. "Maktaba @ kitchen"
	Domain   string            // "local."
	Port     int               // 8080
	TXT      map[string]string // protocol, version, schema_rev, tls
}

// Publisher is the mDNS/DNS-SD publication side. Implementations
// register the service and keep the announcement alive until Close.
type Publisher interface {
	Publish(ctx context.Context, svc Service) error
	Close() error
}

// Browser discovers other Maktaba instances on the LAN. Used by client
// apps; here on the server side it powers a "peers" admin endpoint.
type Browser interface {
	Browse(ctx context.Context) (<-chan Service, error)
	Close() error
}

// NoopPublisher is the fallback when mDNS is disabled. It records the
// last advertised service so tests can assert what would have been
// published.
type NoopPublisher struct {
	mu   sync.Mutex
	last *Service
}

// Publish records the service.
func (n *NoopPublisher) Publish(_ context.Context, s Service) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	cp := s
	n.last = &cp
	return nil
}

// Close is a no-op.
func (n *NoopPublisher) Close() error { return nil }

// Last returns the most recently advertised service, or nil.
func (n *NoopPublisher) Last() *Service {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.last == nil {
		return nil
	}
	cp := *n.last
	return &cp
}

// --- pairing ----------------------------------------------------------------

// PairingTicket is the server-side state for a pairing code.
type PairingTicket struct {
	Code       string
	UserID     string
	IssuedAt   time.Time
	ExpiresAt  time.Time
	ConsumedAt *time.Time
}

// PairingStore persists tickets. Production-backed by Postgres; tests
// use the in-memory implementation.
type PairingStore interface {
	Put(ctx context.Context, t PairingTicket) error
	Get(ctx context.Context, code string) (PairingTicket, error)
	Consume(ctx context.Context, code string) (PairingTicket, error)
	// Sweep is the Story 15.6 reaper query: hard-delete every ticket
	// whose expiry is older than `before`, returning the rows removed.
	// A boot goroutine drives it on a 30 s tick (see main.runPairingSweep)
	// so expired/consumed tickets do not accrete unbounded. The expiry
	// "flip" is implicit: Get/Consume already treat any ticket past its
	// ExpiresAt as expired, and Sweep then hard-deletes it once it is
	// older than the 7-day retention horizon the caller passes.
	Sweep(ctx context.Context, before time.Time) (int64, error)
}

// ErrCodeNotFound is returned by PairingStore.Get/Consume when the
// code does not exist (or has expired and been swept).
var ErrCodeNotFound = errors.New("pairing code not found")

// ErrCodeExpired is returned when the code is present but past TTL.
var ErrCodeExpired = errors.New("pairing code expired")

// ErrCodeConsumed is returned when the code has already been redeemed.
var ErrCodeConsumed = errors.New("pairing code already consumed")

// MemoryPairingStore is an in-memory store for tests and dev.
type MemoryPairingStore struct {
	mu      sync.Mutex
	tickets map[string]PairingTicket
	now     func() time.Time
}

// NewMemoryPairingStore creates an empty store.
func NewMemoryPairingStore() *MemoryPairingStore {
	return &MemoryPairingStore{
		tickets: map[string]PairingTicket{},
		now:     time.Now,
	}
}

// SetNow overrides the time source. Tests use it to freeze the clock.
func (m *MemoryPairingStore) SetNow(fn func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = fn
}

// Put stores or replaces a ticket.
func (m *MemoryPairingStore) Put(_ context.Context, t PairingTicket) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tickets[t.Code] = t
	return nil
}

// Get fetches a ticket without consuming it.
func (m *MemoryPairingStore) Get(_ context.Context, code string) (PairingTicket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tickets[code]
	if !ok {
		return PairingTicket{}, ErrCodeNotFound
	}
	if m.now().After(t.ExpiresAt) {
		return PairingTicket{}, ErrCodeExpired
	}
	return t, nil
}

// Consume atomically marks a ticket as redeemed and returns its prior state.
func (m *MemoryPairingStore) Consume(_ context.Context, code string) (PairingTicket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tickets[code]
	if !ok {
		return PairingTicket{}, ErrCodeNotFound
	}
	if m.now().After(t.ExpiresAt) {
		return PairingTicket{}, ErrCodeExpired
	}
	if t.ConsumedAt != nil {
		return PairingTicket{}, ErrCodeConsumed
	}
	now := m.now()
	t.ConsumedAt = &now
	m.tickets[code] = t
	return t, nil
}

// Sweep hard-deletes tickets past the retention horizon. It mirrors
// SQLPairingStore.Sweep's two-predicate selection exactly so the
// in-memory dev/no-DB path bounds its growth under the same reaper
// goroutine with identical semantics: a ticket is removed when it is
// either (a) still unconsumed and expired before `before`, or (b)
// consumed before `before`. (The split in the SQL store is purely an
// index-alignment concern; the net selected set is the union, which is
// what this loop computes.) Returns rows removed.
func (m *MemoryPairingStore) Sweep(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for code, t := range m.tickets {
		unconsumedExpired := t.ConsumedAt == nil && t.ExpiresAt.Before(before)
		consumedStale := t.ConsumedAt != nil && t.ConsumedAt.Before(before)
		if unconsumedExpired || consumedStale {
			delete(m.tickets, code)
			n++
		}
	}
	return n, nil
}

// GenerateCode mints a short, human-typable pairing code. The format is
// XXXX-XXXX (32 bits of entropy, base-32, dashed).
func GenerateCode() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	v := binary.BigEndian.Uint32(b[:])
	// 8 chars of Crockford base-32 alphabet → 40 bits expressed in 8.
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var out [8]byte
	for i := 0; i < 8; i++ {
		out[7-i] = alphabet[v&0x1F]
		v >>= 5
	}
	return fmt.Sprintf("%s-%s", out[0:4], out[4:8]), nil
}

// NormalizeCode upper-cases and strips spaces / dashes from user input.
func NormalizeCode(s string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(s))
}

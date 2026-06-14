// Package session implements Story 8.9 — the streaming_sessions store
// and the idle-session reaper. The schema lives in
// shared/db/migrations/0039_streaming_sessions.{sql,sqlite.sql}.
//
// The Streaming binary is the only writer. The API reads via the
// gRPC OpenSession/CloseSession surface (Story 8.8). All sessions
// are pinned to one host (consistent-hash routing); the reaper kills
// FFmpeg subprocesses for sessions that have stopped fetching segments.
package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Mode mirrors the streaming_sessions.mode column.
type Mode string

const (
	ModeDirect         Mode = "direct"
	ModeRemux          Mode = "remux"
	ModeTranscode      Mode = "transcode"
	ModeDirectDegraded Mode = "direct-degraded"
	// ModeChannel is a long-lived virtual session whose input is a
	// channel schedule rather than one video (Epic 27 / Story 27.3,
	// slot 0083 widens the streaming_sessions.mode CHECK to admit it).
	ModeChannel Mode = "channel"
)

// Format mirrors the streaming_sessions.format column.
type Format string

const (
	FormatHLS  Format = "hls"
	FormatDASH Format = "dash"
)

// State distinguishes active sessions from queued ones (Story 8.10).
type State string

const (
	StateActive State = "active"
	StateQueued State = "queued"
)

// CloseReason is the closed_reason column.
type CloseReason string

const (
	ReasonAPI               CloseReason = "api"
	ReasonIdle              CloseReason = "idle"
	ReasonCrash             CloseReason = "crash"
	ReasonEvicted           CloseReason = "evicted"
	ReasonUserStop          CloseReason = "user-stop"
	ReasonAdminEvict        CloseReason = "admin-evict"
	ReasonHWFailedSWFailed  CloseReason = "hw_failed_software_failed"
	ReasonStoreInsertFailed CloseReason = "store-insert-failed"
)

// Row is the in-memory shape mirroring the streaming_sessions row.
type Row struct {
	ID            uuid.UUID
	VideoID       uuid.UUID
	UserID        uuid.UUID
	ClientProfile string
	Mode          Mode
	Format        Format
	Host          string
	PID           int
	StartedAt     time.Time
	LastSegmentAt time.Time
	ClosedAt      *time.Time
	ClosedReason  CloseReason
	State         State

	// Transcoder is the FFmpeg controller pinned to this session.
	// Nil for direct/remux sessions. Owned by the session store —
	// the reaper kills it on idle.
	Transcoder Transcoder
}

// Transcoder is the small surface the reaper needs to terminate an
// in-flight FFmpeg. The ffmpeg package implements it.
type Transcoder interface {
	Stop(ctx context.Context) error
	PID() int
	Active() bool
}

// Store is the persistence + index layer for sessions. Production
// wires a Postgres-backed implementation; tests use the in-memory
// store from this file.
type Store interface {
	Insert(ctx context.Context, row *Row) error
	Get(ctx context.Context, id uuid.UUID) (*Row, bool, error)
	Touch(ctx context.Context, id uuid.UUID, at time.Time) error
	Close(ctx context.Context, id uuid.UUID, reason CloseReason, at time.Time) error
	ListIdle(ctx context.Context, before time.Time) ([]*Row, error)
	List(ctx context.Context) ([]*Row, error)
	CountActive(ctx context.Context, mode Mode) (int, error)
	ActiveByUserVideo(ctx context.Context, userID, videoID uuid.UUID) (*Row, bool, error)
}

// MemoryStore is a thread-safe in-memory Store. Suitable for tests
// and single-host dev installs; production swaps in a Postgres-backed
// store with the same interface.
type MemoryStore struct {
	mu         sync.RWMutex
	rows       map[uuid.UUID]*Row
	touchEvery time.Duration
	lastTouch  map[uuid.UUID]time.Time
}

// NewMemoryStore builds an empty store. Touches are coalesced to one
// write per touchEvery per session — Story 8.9 AC-2's "at most one
// write per 5 s" rule.
func NewMemoryStore(touchEvery time.Duration) *MemoryStore {
	if touchEvery <= 0 {
		touchEvery = 5 * time.Second
	}
	return &MemoryStore{
		rows:       map[uuid.UUID]*Row{},
		touchEvery: touchEvery,
		lastTouch:  map[uuid.UUID]time.Time{},
	}
}

// Insert adds a new row.
func (m *MemoryStore) Insert(_ context.Context, row *Row) error {
	if row.ID == uuid.Nil {
		row.ID = uuid.New()
	}
	if row.StartedAt.IsZero() {
		row.StartedAt = time.Now().UTC()
	}
	if row.LastSegmentAt.IsZero() {
		row.LastSegmentAt = row.StartedAt
	}
	if row.State == "" {
		row.State = StateActive
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.rows[row.ID]; exists {
		return fmt.Errorf("duplicate session id %s", row.ID)
	}
	m.rows[row.ID] = row
	return nil
}

// Get returns the row, or (_, false, nil) when absent.
func (m *MemoryStore) Get(_ context.Context, id uuid.UUID) (*Row, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.rows[id]
	if !ok {
		return nil, false, nil
	}
	cp := *r
	return &cp, true, nil
}

// Touch updates last_segment_at, debounced per Story 8.9 AC-2.
func (m *MemoryStore) Touch(_ context.Context, id uuid.UUID, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	last, ok := m.lastTouch[id]
	if ok && at.Sub(last) < m.touchEvery {
		return nil
	}
	row, exists := m.rows[id]
	if !exists {
		return errors.New("session not found")
	}
	row.LastSegmentAt = at
	m.lastTouch[id] = at
	return nil
}

// Close marks a session closed. Idempotent — re-closing a closed
// session is a no-op (the API may close after the reaper already did).
func (m *MemoryStore) Close(_ context.Context, id uuid.UUID, reason CloseReason, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[id]
	if !ok {
		return errors.New("session not found")
	}
	if row.ClosedAt != nil {
		return nil
	}
	t := at
	row.ClosedAt = &t
	row.ClosedReason = reason
	if row.Transcoder != nil {
		_ = row.Transcoder.Stop(context.Background())
	}
	return nil
}

// ListIdle returns active sessions whose last_segment_at is before
// the cutoff. The reaper uses this with cutoff = now - 90 s.
func (m *MemoryStore) ListIdle(_ context.Context, before time.Time) ([]*Row, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []*Row{}
	for _, r := range m.rows {
		if r.ClosedAt != nil {
			continue
		}
		if r.LastSegmentAt.Before(before) {
			cp := *r
			out = append(out, &cp)
		}
	}
	return out, nil
}

// List returns all sessions (closed and open) — used by the gRPC
// status surface.
func (m *MemoryStore) List(_ context.Context) ([]*Row, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Row, 0, len(m.rows))
	for _, r := range m.rows {
		cp := *r
		out = append(out, &cp)
	}
	return out, nil
}

// CountActive returns the number of open sessions for a given mode —
// used by the slot allocator (Story 8.10) to decide if there's room.
func (m *MemoryStore) CountActive(_ context.Context, mode Mode) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, r := range m.rows {
		if r.ClosedAt != nil || r.State != StateActive {
			continue
		}
		if r.Mode == mode {
			n++
		}
	}
	return n, nil
}

// ActiveByUserVideo finds an open session for the (user, video) tuple.
// Used to dedupe — re-opening the same session within the TTL returns
// the existing row instead of churning FFmpeg.
func (m *MemoryStore) ActiveByUserVideo(_ context.Context, userID, videoID uuid.UUID) (*Row, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, r := range m.rows {
		if r.ClosedAt != nil {
			continue
		}
		if r.UserID == userID && r.VideoID == videoID {
			cp := *r
			return &cp, true, nil
		}
	}
	return nil, false, nil
}

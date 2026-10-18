package channel

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// fakeClock is a manually-advanced clock for deterministic tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock(t time.Time) *fakeClock { return &fakeClock{t: t} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeRepo serves a fixed block list and records runtime writes.
type fakeRepo struct {
	blocks   []ProgramBlock
	setCalls int
	cleared  []uuid.UUID
	missing  bool
}

func (f *fakeRepo) ProgramsFrom(_ context.Context, _ uuid.UUID, from time.Time, limit int) ([]ProgramBlock, error) {
	var out []ProgramBlock
	for _, b := range f.blocks {
		if b.EndAt.After(from) {
			out = append(out, b)
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeRepo) Look(_ context.Context, id uuid.UUID) (Look, error) {
	if f.missing {
		return Look{}, ErrChannelNotFound
	}
	l := DefaultLook()
	l.Name = "Test"
	return l, nil
}

func (f *fakeRepo) SetRuntime(_ context.Context, _ Runtime) error { f.setCalls++; return nil }

func (f *fakeRepo) ClearRuntime(_ context.Context, id uuid.UUID) error {
	f.cleared = append(f.cleared, id)
	return nil
}

// fakeLayout returns a fixed dir per session under a temp root.
type fakeLayout struct{ root string }

func (l fakeLayout) HLSDir(sessionID string) string { return l.root + "/" + sessionID }

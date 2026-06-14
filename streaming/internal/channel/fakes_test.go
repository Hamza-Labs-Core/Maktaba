package channel

import (
	"context"
	"io"
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

func (f *fakeRepo) SetRuntime(_ context.Context, _ Runtime) error { f.setCalls++; return nil }

func (f *fakeRepo) ClearRuntime(_ context.Context, id uuid.UUID) error {
	f.cleared = append(f.cleared, id)
	return nil
}

// fakeController is a stand-in encoder handle.
type fakeController struct {
	stopped bool
	pid     int
}

func (c *fakeController) Stop(_ context.Context) error { c.stopped = true; return nil }
func (c *fakeController) PID() int                     { return c.pid }
func (c *fakeController) Active() bool                 { return !c.stopped }

// fakeRunner records StartHLS calls and the concat scripts it was given.
type fakeRunner struct {
	starts   int
	lastJob  Job
	controls []*fakeController
}

func (r *fakeRunner) StartHLS(_ context.Context, job Job) (Controller, error) {
	r.starts++
	r.lastJob = job
	c := &fakeController{pid: 1000 + r.starts}
	r.controls = append(r.controls, c)
	return c, nil
}

// fakeTS records StreamMPEGTS calls and writes a canned packet.
type fakeTS struct {
	calls      int
	lastConcat string
}

func (t *fakeTS) StreamMPEGTS(_ context.Context, concatPath string, w io.Writer) error {
	t.calls++
	t.lastConcat = concatPath
	_, _ = w.Write([]byte("TSPACKET"))
	return nil
}

// fakeLayout returns a fixed dir per session under a temp root.
type fakeLayout struct{ root string }

func (l fakeLayout) HLSDir(sessionID string) string { return l.root + "/" + sessionID }

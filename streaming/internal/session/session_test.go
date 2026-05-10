package session

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeTranscoder struct {
	stopped atomic.Bool
	pid     int
}

func (f *fakeTranscoder) Stop(_ context.Context) error { f.stopped.Store(true); return nil }
func (f *fakeTranscoder) PID() int                     { return f.pid }
func (f *fakeTranscoder) Active() bool                 { return !f.stopped.Load() }

func TestStore_InsertGet(t *testing.T) {
	s := NewMemoryStore(time.Second)
	row := &Row{VideoID: uuid.New(), UserID: uuid.New(), Mode: ModeTranscode, Format: FormatHLS}
	if err := s.Insert(context.Background(), row); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if row.ID == uuid.Nil {
		t.Fatal("id not assigned")
	}
	got, ok, err := s.Get(context.Background(), row.ID)
	if err != nil || !ok {
		t.Fatalf("get: %v %v", err, ok)
	}
	if got.VideoID != row.VideoID {
		t.Fatalf("video=%v want %v", got.VideoID, row.VideoID)
	}
}

func TestStore_TouchDebounced(t *testing.T) {
	s := NewMemoryStore(5 * time.Second)
	row := &Row{Mode: ModeTranscode, Format: FormatHLS}
	_ = s.Insert(context.Background(), row)

	first := time.Now()
	if err := s.Touch(context.Background(), row.ID, first); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.Get(context.Background(), row.ID)
	stored1 := got.LastSegmentAt

	// Touch within 5s should be no-op.
	if err := s.Touch(context.Background(), row.ID, first.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Get(context.Background(), row.ID)
	if !got.LastSegmentAt.Equal(stored1) {
		t.Fatalf("touch wasn't debounced: %v vs %v", got.LastSegmentAt, stored1)
	}

	// Touch beyond 5s should land.
	later := first.Add(10 * time.Second)
	if err := s.Touch(context.Background(), row.ID, later); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Get(context.Background(), row.ID)
	if !got.LastSegmentAt.Equal(later) {
		t.Fatalf("touch didn't land: %v vs %v", got.LastSegmentAt, later)
	}
}

func TestStore_CloseIdempotent(t *testing.T) {
	s := NewMemoryStore(time.Second)
	row := &Row{Mode: ModeRemux, Format: FormatHLS}
	_ = s.Insert(context.Background(), row)
	now := time.Now()
	if err := s.Close(context.Background(), row.ID, ReasonAPI, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(context.Background(), row.ID, ReasonIdle, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.Get(context.Background(), row.ID)
	if got.ClosedReason != ReasonAPI {
		t.Fatalf("reason=%s; second close should not overwrite", got.ClosedReason)
	}
}

func TestStore_CloseStopsTranscoder(t *testing.T) {
	s := NewMemoryStore(time.Second)
	tc := &fakeTranscoder{pid: 4242}
	row := &Row{Mode: ModeTranscode, Format: FormatHLS, Transcoder: tc}
	_ = s.Insert(context.Background(), row)
	_ = s.Close(context.Background(), row.ID, ReasonAPI, time.Now())
	if !tc.stopped.Load() {
		t.Fatal("transcoder not stopped")
	}
}

func TestReaper_ClosesIdleSessions(t *testing.T) {
	s := NewMemoryStore(time.Second)
	now := time.Now().UTC()
	idle := &Row{Mode: ModeTranscode, Format: FormatHLS, LastSegmentAt: now.Add(-2 * time.Minute), StartedAt: now.Add(-time.Hour)}
	fresh := &Row{Mode: ModeTranscode, Format: FormatHLS, LastSegmentAt: now.Add(-30 * time.Second), StartedAt: now.Add(-time.Hour)}
	_ = s.Insert(context.Background(), idle)
	_ = s.Insert(context.Background(), fresh)

	rsm := &reapSeenMu{}
	r := NewReaper(s, ReaperConfig{IdleAfter: 90 * time.Second, Interval: time.Second, OnReap: rsm.append})
	r.SetClock(func() time.Time { return now })

	if err := r.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.Reaped() != 1 {
		t.Fatalf("reaped=%d", r.Reaped())
	}

	got, _, _ := s.Get(context.Background(), idle.ID)
	if got.ClosedReason != ReasonIdle {
		t.Fatalf("reason=%s", got.ClosedReason)
	}
	got, _, _ = s.Get(context.Background(), fresh.ID)
	if got.ClosedAt != nil {
		t.Fatal("fresh session was reaped")
	}
}

type reapSeenMu struct {
	rows []*Row
}

func (r *reapSeenMu) append(row *Row) { r.rows = append(r.rows, row) }

func TestStore_CountActive(t *testing.T) {
	s := NewMemoryStore(time.Second)
	for i := 0; i < 3; i++ {
		_ = s.Insert(context.Background(), &Row{Mode: ModeTranscode, Format: FormatHLS})
	}
	for i := 0; i < 2; i++ {
		_ = s.Insert(context.Background(), &Row{Mode: ModeDirect, Format: FormatHLS})
	}
	n, _ := s.CountActive(context.Background(), ModeTranscode)
	if n != 3 {
		t.Fatalf("transcode count=%d", n)
	}
	n, _ = s.CountActive(context.Background(), ModeDirect)
	if n != 2 {
		t.Fatalf("direct count=%d", n)
	}
}

func TestStore_ActiveByUserVideo(t *testing.T) {
	s := NewMemoryStore(time.Second)
	user, video := uuid.New(), uuid.New()
	row := &Row{UserID: user, VideoID: video, Mode: ModeTranscode, Format: FormatHLS}
	_ = s.Insert(context.Background(), row)

	r, ok, err := s.ActiveByUserVideo(context.Background(), user, video)
	if err != nil || !ok || r.ID != row.ID {
		t.Fatalf("not found: ok=%v err=%v", ok, err)
	}

	_ = s.Close(context.Background(), row.ID, ReasonAPI, time.Now())
	_, ok, _ = s.ActiveByUserVideo(context.Background(), user, video)
	if ok {
		t.Fatal("closed session should not appear")
	}
}

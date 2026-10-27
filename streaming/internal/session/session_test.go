package session

import (
	"context"
	"errors"
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

// slowTranscoder's Stop waits for release (an ffmpeg slow to exit), then fails with err.
type slowTranscoder struct {
	entered chan struct{}
	release chan struct{}
	err     error
}

func (s *slowTranscoder) Stop(context.Context) error {
	close(s.entered)
	<-s.release
	return s.err
}
func (s *slowTranscoder) PID() int     { return 1 }
func (s *slowTranscoder) Active() bool { return true }

// A transcode slow to stop holds up only its own Close: the store answers every other
// session (each playlist and segment fetch) meanwhile, and a stop that failed is
// returned, the session closed all the same.
func TestStore_CloseStopsOutsideTheLock(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore(time.Second)
	slow := &slowTranscoder{entered: make(chan struct{}), release: make(chan struct{}), err: errors.New("ffmpeg: stop timeout")}
	row := &Row{Mode: ModeTranscode, Format: FormatHLS, Transcoder: slow}
	other := &Row{Mode: ModeDirect, Format: FormatHLS}
	if err := s.Insert(ctx, row); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert(ctx, other); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close(ctx, row.ID, ReasonAPI, time.Now()) }()
	<-slow.entered
	got := make(chan struct{})
	go func() {
		_, _, _ = s.Get(ctx, other.ID)
		_ = s.Touch(ctx, other.ID, time.Now())
		close(got)
	}()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("the store waited on a transcode's stop")
	}
	if r, _, _ := s.Get(ctx, row.ID); r.ClosedAt == nil {
		t.Error("the session is not closed while its transcode stops")
	}
	close(slow.release)
	if err := <-closed; !errors.Is(err, ErrTranscodeNotStopped) {
		t.Errorf("Close = %v, want ErrTranscodeNotStopped", err)
	}
	if err := s.Close(ctx, row.ID, ReasonAPI, time.Now()); err != nil {
		t.Errorf("closing again = %v, want nil", err)
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

// While its transcode runs, a session is reaped only after ActiveIdleAfter
// without a fetch (a background tab comes back to it going on); one whose
// transcode is over, after IdleAfter.
func TestReaper_ActiveTranscodeWaitsLonger(t *testing.T) {
	s := NewMemoryStore(time.Second)
	now := time.Now().UTC()
	rows := map[string]*Row{}
	for name, quiet := range map[string]time.Duration{"active 5 min": 5 * time.Minute, "active 11 min": 11 * time.Minute,
		"over 2 min": 2 * time.Minute} {
		tc := &fakeTranscoder{}
		if name == "over 2 min" {
			tc.stopped.Store(true)
		}
		rows[name] = &Row{Mode: ModeTranscode, Format: FormatHLS, Transcoder: tc, LastSegmentAt: now.Add(-quiet),
			StartedAt: now.Add(-time.Hour)}
		_ = s.Insert(context.Background(), rows[name])
	}
	r := NewReaper(s, ReaperConfig{IdleAfter: 90 * time.Second, ActiveIdleAfter: 10 * time.Minute})
	r.SetClock(func() time.Time { return now })
	if err := r.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	for name, closed := range map[string]bool{"active 5 min": false, "active 11 min": true, "over 2 min": true} {
		got, _, _ := s.Get(context.Background(), rows[name].ID)
		if (got.ClosedAt != nil) != closed {
			t.Errorf("%s: closed %v, want %v", name, got.ClosedAt != nil, closed)
		}
	}
}

// How a transcode ended, as the row tells it: cut short only once it is over
// without having encoded the whole source.
func TestRow_TranscodeEnd(t *testing.T) {
	for name, c := range map[string]struct {
		tr            Transcoder
		finished, cut bool
	}{
		"none":     {nil, false, false},
		"no ender": {&fakeTranscoder{}, false, false},
		"running":  {&enderTranscoder{}, false, false},
		"finished": {&enderTranscoder{ended: true, finished: true}, true, false},
		"stopped":  {&enderTranscoder{ended: true}, false, true},
	} {
		r := &Row{Transcoder: c.tr}
		if r.TranscodeFinished() != c.finished || r.TranscodeCut() != c.cut {
			t.Errorf("%s: finished %v, cut %v", name, r.TranscodeFinished(), r.TranscodeCut())
		}
	}
}

type enderTranscoder struct {
	fakeTranscoder
	ended, finished bool
}

func (e *enderTranscoder) Ended() bool    { return e.ended }
func (e *enderTranscoder) Finished() bool { return e.finished }

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

func TestStore_OpenByUserVideo(t *testing.T) {
	s := NewMemoryStore(time.Second)
	user, video := uuid.New(), uuid.New()
	now := time.Now()
	second := &Row{UserID: user, VideoID: video, Mode: ModeTranscode, Format: FormatHLS, StartedAt: now}
	first := &Row{UserID: user, VideoID: video, Mode: ModeTranscode, Format: FormatHLS, StartedAt: now.Add(-time.Minute)}
	other := &Row{UserID: uuid.New(), VideoID: video, Mode: ModeTranscode, Format: FormatHLS}
	for _, r := range []*Row{second, first, other} {
		_ = s.Insert(context.Background(), r)
	}

	rows, err := s.OpenByUserVideo(context.Background(), user, video)
	if err != nil || len(rows) != 2 || rows[0].ID != first.ID || rows[1].ID != second.ID {
		t.Fatalf("open: %v %v", rows, err)
	}

	_ = s.Close(context.Background(), first.ID, ReasonAPI, time.Now())
	rows, _ = s.OpenByUserVideo(context.Background(), user, video)
	if len(rows) != 1 || rows[0].ID != second.ID {
		t.Fatalf("a closed session listed: %v", rows)
	}
}

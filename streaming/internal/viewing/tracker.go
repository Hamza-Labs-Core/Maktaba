// Package viewing records who watches the live channels (Epic 44 story 44.4): one
// tv_watch_sessions row per viewing, in a Maktaba app or the web (surface app: the
// home's front door sees each channel segment, by person and app) and through
// Plex's Live TV (surface tuner: an HDHomeRun lease, no person, the address
// hashed). It also keeps hdhr_tuner_leases (the tuner page) and, behind the front
// door, channel_runtime.viewer_count.
//
// The tracker is in memory: a segment fetch never waits on the database. The rows
// are written every FlushEvery and at shutdown.
package viewing

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Surfaces and end reasons (the slot-0158 CHECKs).
const (
	SurfaceApp   = "app"
	SurfaceTuner = "tuner"
	EndClosed    = "closed"
	EndIdle      = "idle"
)

const (
	// DefaultIdle: an app viewing with no segment this long has ended.
	DefaultIdle = 60 * time.Second
	// DefaultFlushEvery is how often the rows are written.
	DefaultFlushEvery = 15 * time.Second
	// prefsTTL: a person's tracking switch is read again after this.
	prefsTTL = time.Minute
	// maxEnded bounds the ended viewings kept for a database that is down.
	maxEnded = 10000
)

// Row is one viewing as written.
type Row struct {
	ID      uuid.UUID
	Channel uuid.UUID
	// User: uuid.Nil for none (the tuner).
	User    uuid.UUID
	Surface string
	Client  Client
	// Addr is the tuner client's address: kept in hdhr_tuner_leases, only
	// hashed in tv_watch_sessions.
	Addr      string
	Started   time.Time
	LastSeen  time.Time
	Ended     time.Time // zero: still open
	EndReason string
}

// Seconds is the time watched: from the start to the last segment (the tuner:
// to the last flush, or its end).
func (r Row) Seconds() int {
	if d := r.LastSeen.Sub(r.Started); d > 0 {
		return int(d / time.Second)
	}
	return 0
}

// Batch is one flush.
type Batch struct {
	// Rows are the viewings to write (new, moved on or ended; written whole, by id).
	Rows []Row
	// Viewers, when not nil, is every channel's viewers now for
	// channel_runtime.viewer_count (a channel not in it has none).
	Viewers map[uuid.UUID]int
}

// Store writes the viewings (PGStore).
type Store interface {
	// Begin ends what a previous run left open (once, before the first write).
	Begin(ctx context.Context) error
	Write(ctx context.Context, b Batch) error
	// TrackingOff reports which of users turned tracking off
	// (user_analytics_prefs.track_enabled = false).
	TrackingOff(ctx context.Context, users []uuid.UUID) (map[uuid.UUID]bool, error)
}

type appKey struct {
	ch, user uuid.UUID
	client   string
	addr     string // only for a viewer with no person
}

type session struct {
	Row
	key appKey
}

type pref struct {
	off bool
	at  time.Time
}

// Tracker keeps the open viewings. The zero value is not usable: NewTracker.
type Tracker struct {
	Store  Store // nil: nothing is recorded (logged once by Run)
	Now    func() time.Time
	Idle   time.Duration // 0: DefaultIdle
	Every  time.Duration // 0: DefaultFlushEvery
	Logger *slog.Logger
	// Viewers, when set, is each channel's viewers for channel_runtime.viewer_count
	// (the front door: its TS clients plus AppViewers); nil leaves the column alone.
	Viewers func() map[uuid.UUID]int

	mu      sync.Mutex
	app     map[appKey]*session
	tuner   map[uuid.UUID]*session
	ended   []*session
	prefs   map[uuid.UUID]pref
	begun   bool
	failing bool
}

// NewTracker builds an empty tracker writing to store.
func NewTracker(store Store) *Tracker {
	return &Tracker{Store: store, app: map[appKey]*session{}, tuner: map[uuid.UUID]*session{},
		prefs: map[uuid.UUID]pref{}}
}

func (t *Tracker) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (t *Tracker) idle() time.Duration {
	if t.Idle > 0 {
		return t.Idle
	}
	return DefaultIdle
}

func (t *Tracker) log() *slog.Logger {
	if t.Logger != nil {
		return t.Logger
	}
	return slog.Default()
}

// Segment records a channel segment served to v: the first opens a viewing, each
// one moves it on. A viewing that has gone quiet for Idle ends, and the next
// segment opens a new one. Playlists do not count: the web player fetches its
// neighbours' playlists every 10 s (tvplayer prefetch.ts) without watching them.
func (t *Tracker) Segment(ch uuid.UUID, v Viewer) {
	if t == nil {
		return
	}
	k := appKey{ch: ch, user: v.User, client: v.Client.label()}
	if v.User == uuid.Nil {
		k.addr = v.Addr
	}
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if s := t.app[k]; s != nil {
		if now.Sub(s.LastSeen) < t.idle() {
			s.LastSeen = now
			return
		}
		t.endLocked(s, EndIdle, s.LastSeen)
		delete(t.app, k)
	}
	t.app[k] = &session{key: k, Row: Row{ID: uuid.New(), Channel: ch, User: v.User, Surface: SurfaceApp,
		Client: v.Client, Started: now, LastSeen: now}}
}

// OpenTuner records a tuner stream starting (one per HDHomeRun lease); the
// returned func ends it (closed). addr is the tuner client's address.
func (t *Tracker) OpenTuner(ch uuid.UUID, addr string) func() {
	if t == nil {
		return func() {}
	}
	now := t.now()
	s := &session{Row: Row{ID: uuid.New(), Channel: ch, Surface: SurfaceTuner, Addr: addr, Started: now, LastSeen: now}}
	t.mu.Lock()
	t.tuner[s.ID] = s
	t.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			end := t.now()
			t.mu.Lock()
			defer t.mu.Unlock()
			if t.tuner[s.ID] == s {
				delete(t.tuner, s.ID)
				t.endLocked(s, EndClosed, end)
			}
		})
	}
}

// endLocked ends s at `at` and queues it for the next flush.
func (t *Tracker) endLocked(s *session, reason string, at time.Time) {
	s.LastSeen = at
	s.Ended = at
	s.EndReason = reason
	t.ended = append(t.ended, s)
	if over := len(t.ended) - maxEnded; over > 0 {
		t.ended = append([]*session(nil), t.ended[over:]...)
	}
}

// AppViewers is the open app viewings per channel (every viewer, recorded or not).
func (t *Tracker) AppViewers() map[uuid.UUID]int {
	out := map[uuid.UUID]int{}
	if t == nil {
		return out
	}
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, s := range t.app {
		if now.Sub(s.LastSeen) < t.idle() {
			out[s.Channel]++
		}
	}
	return out
}

// sweepLocked ends the app viewings that went quiet.
func (t *Tracker) sweepLocked(now time.Time) {
	for k, s := range t.app {
		if now.Sub(s.LastSeen) >= t.idle() {
			t.endLocked(s, EndIdle, s.LastSeen)
			delete(t.app, k)
		}
	}
}

// Flush ends the quiet viewings and writes every open and ended one. A failed
// write keeps them for the next flush.
func (t *Tracker) Flush(ctx context.Context) error {
	if t.Store == nil {
		// nothing to write: only forget the quiet viewers (the counts stay right)
		t.mu.Lock()
		t.sweepLocked(t.now())
		t.ended = nil
		t.mu.Unlock()
		return nil
	}
	t.mu.Lock()
	begun := t.begun
	t.mu.Unlock()
	if !begun {
		if err := t.Store.Begin(ctx); err != nil {
			return t.failed(err)
		}
		t.mu.Lock()
		t.begun = true
		t.mu.Unlock()
	}
	now := t.now()
	t.mu.Lock()
	t.sweepLocked(now)
	for _, s := range t.tuner {
		s.LastSeen = now
	}
	ask := t.unknownPrefsLocked(now)
	t.mu.Unlock()

	if len(ask) > 0 {
		off, err := t.Store.TrackingOff(ctx, ask)
		if err == nil {
			t.mu.Lock()
			for _, u := range ask {
				t.prefs[u] = pref{off: off[u], at: now}
			}
			t.mu.Unlock()
		} else {
			t.log().Warn("viewing: reading the tracking switches failed", "err", err.Error(),
				"event", "tv_viewing_prefs_failed")
		}
	}

	var viewers map[uuid.UUID]int
	if t.Viewers != nil {
		viewers = t.Viewers()
	}
	t.mu.Lock()
	b := Batch{Viewers: viewers}
	for _, s := range t.app {
		if t.recordLocked(s) {
			b.Rows = append(b.Rows, s.Row)
		}
	}
	for _, s := range t.tuner {
		b.Rows = append(b.Rows, s.Row)
	}
	sent := map[*session]bool{}
	for _, s := range t.ended {
		switch {
		case s.Surface == SurfaceTuner || t.recordLocked(s):
			b.Rows = append(b.Rows, s.Row)
			sent[s] = true
		case t.knownLocked(s):
			sent[s] = true // never recorded (no person, or tracking off): dropped
		}
	}
	t.mu.Unlock()

	if err := t.Store.Write(ctx, b); err != nil {
		return t.failed(err)
	}
	t.mu.Lock()
	kept := t.ended[:0]
	for _, s := range t.ended {
		if !sent[s] {
			kept = append(kept, s)
		}
	}
	for i := len(kept); i < len(t.ended); i++ {
		t.ended[i] = nil
	}
	t.ended = kept
	for u, p := range t.prefs {
		if now.Sub(p.at) > 10*prefsTTL {
			delete(t.prefs, u)
		}
	}
	if t.failing {
		t.failing = false
		t.log().Info("viewing: writing the viewings works again", "event", "tv_viewing_write_ok")
	}
	t.mu.Unlock()
	return nil
}

// unknownPrefsLocked lists the people with a viewing whose switch is not read or
// is older than prefsTTL.
func (t *Tracker) unknownPrefsLocked(now time.Time) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	add := func(s *session) {
		u := s.User
		if u == uuid.Nil || seen[u] {
			return
		}
		seen[u] = true
		if p, ok := t.prefs[u]; !ok || now.Sub(p.at) >= prefsTTL {
			out = append(out, u)
		}
	}
	for _, s := range t.app {
		add(s)
	}
	for _, s := range t.ended {
		if s.Surface == SurfaceApp {
			add(s)
		}
	}
	return out
}

// knownLocked reports whether s's recording is decided (no person, or the
// person's switch read).
func (t *Tracker) knownLocked(s *session) bool {
	if s.User == uuid.Nil {
		return true
	}
	_, ok := t.prefs[s.User]
	return ok
}

// recordLocked reports whether s is written: a known person with tracking on.
func (t *Tracker) recordLocked(s *session) bool {
	if s.User == uuid.Nil {
		return false
	}
	p, ok := t.prefs[s.User]
	return ok && !p.off
}

// failed logs a failed write once until one works again.
func (t *Tracker) failed(err error) error {
	t.mu.Lock()
	first := !t.failing
	t.failing = true
	t.mu.Unlock()
	if first {
		t.log().Warn("viewing: writing the viewings failed; kept for the next try", "err", err.Error(),
			"event", "tv_viewing_write_failed")
	}
	return err
}

// Close ends every open viewing (closed): the process is stopping.
func (t *Tracker) Close() {
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sweepLocked(now)
	for k, s := range t.app {
		t.endLocked(s, EndClosed, s.LastSeen)
		delete(t.app, k)
	}
	for id, s := range t.tuner {
		t.endLocked(s, EndClosed, now)
		delete(t.tuner, id)
	}
}

// Run flushes every Every until ctx ends, then ends every open viewing and
// writes them a last time.
func (t *Tracker) Run(ctx context.Context) {
	if t.Store == nil {
		t.log().Info("viewing: no database here; live TV viewings are not recorded", "event", "tv_viewing_off")
	}
	every := t.Every
	if every <= 0 {
		every = DefaultFlushEvery
	}
	tk := time.NewTicker(every)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Close()
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			_ = t.Flush(fctx)
			cancel()
			return
		case <-tk.C:
			fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_ = t.Flush(fctx)
			cancel()
		}
	}
}

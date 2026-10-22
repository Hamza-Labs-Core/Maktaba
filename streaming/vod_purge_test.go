package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/handlers"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
)

// sessionFolder writes a transcode's folder (a variant playlist and a
// segment), last changed at changed.
func sessionFolder(t *testing.T, layout *cache.Layout, name string, changed time.Time) string {
	t.Helper()
	dir := layout.HLSDir(name)
	v0 := filepath.Join(dir, "v0")
	if err := os.MkdirAll(v0, 0o755); err != nil {
		t.Fatal(err)
	}
	for f, body := range map[string]string{
		"index.m3u8": "#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXTINF:6.000000,\nseg-0.ts\n",
		"seg-0.ts":   "ts",
	} {
		if err := os.WriteFile(filepath.Join(v0, f), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{v0, dir} {
		if err := os.Chtimes(d, changed, changed); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// transcode is a session's transcode in one of its states.
type transcode struct{ active, ended, finished atomic.Bool }

func (f *transcode) Stop(context.Context) error {
	if f.active.Swap(false) {
		f.ended.Store(true)
	}
	return nil
}
func (f *transcode) PID() int       { return 0 }
func (f *transcode) Active() bool   { return f.active.Load() }
func (f *transcode) Ended() bool    { return f.ended.Load() }
func (f *transcode) Finished() bool { return f.finished.Load() }

func running() *transcode {
	t := &transcode{}
	t.active.Store(true)
	return t
}

func finished() *transcode {
	t := &transcode{}
	t.ended.Store(true)
	t.finished.Store(true)
	return t
}

// noFloor is a disk with room to spare.
var noFloor = vodDisk{minFreeGiB: -1}

// The sweep removes a session's folder closed by its player at once; one
// closed for being idle 4 h after its last fetch when its transcode was
// finished, 10 min after when it was cut short; a previous run's (no session
// here) once it has not changed for the idle horizon. An open session's, one
// within its grace (a fetch after the close counts), one being opened (its
// row not stored yet) and a channel's stay.
func TestPurgeVODSessions(t *testing.T) {
	ctx := context.Background()
	layout := cache.New(t.TempDir())
	if err := layout.EnsureTiers(); err != nil {
		t.Fatal(err)
	}
	store := session.NewMemoryStore(time.Second)
	now := time.Now()
	stale := now.Add(-2 * vodSessionIdle)
	type sess struct {
		tr      session.Transcoder
		reason  session.CloseReason // "": open
		closed  time.Time
		watched time.Time // its last fetch
		gone    bool
	}
	sessions := map[string]sess{
		"open, paused long ago":       {tr: running(), watched: now.Add(-time.Hour)},
		"closed by its player":        {tr: finished(), reason: session.ReasonAPI, closed: now, watched: now, gone: true},
		"finished, idle 3 h":          {tr: finished(), reason: session.ReasonIdle, closed: now.Add(-3 * time.Hour), watched: now.Add(-3 * time.Hour)},
		"finished, idle 4 h":          {tr: finished(), reason: session.ReasonIdle, closed: now.Add(-vodFinishedGrace), watched: now.Add(-vodFinishedGrace), gone: true},
		"cut, idle 9 min":             {tr: &transcode{}, reason: session.ReasonIdle, closed: now.Add(-9 * time.Minute), watched: now.Add(-9 * time.Minute)},
		"cut, idle 10 min":            {tr: &transcode{}, reason: session.ReasonIdle, closed: now.Add(-vodSessionGrace), watched: now.Add(-vodSessionGrace), gone: true},
		"cut, closed 1 h, fetched 9m": {tr: &transcode{}, reason: session.ReasonIdle, closed: now.Add(-time.Hour), watched: now.Add(-9 * time.Minute)},
	}
	dirs := map[string]string{}
	for name, s := range sessions {
		id := uuid.New()
		if err := store.Insert(ctx, &session.Row{ID: id, Mode: session.ModeTranscode, Transcoder: s.tr,
			StartedAt: s.watched, LastSegmentAt: s.watched}); err != nil {
			t.Fatal(err)
		}
		if s.reason != "" {
			if err := store.Close(ctx, id, s.reason, s.closed); err != nil {
				t.Fatal(err)
			}
		}
		dirs[name] = sessionFolder(t, layout, id.String(), stale)
	}
	keep := []string{
		sessionFolder(t, layout, uuid.NewString(), now),              // being opened
		sessionFolder(t, layout, "channel-"+uuid.NewString(), stale), // a channel's
	}
	previous := sessionFolder(t, layout, uuid.NewString(), stale)
	var log bytes.Buffer
	purgeVODSessions(ctx, layout, store, noFloor, now, slog.New(slog.NewTextHandler(&log, nil)))
	for name, s := range sessions {
		if _, err := os.Stat(dirs[name]); (err != nil) != s.gone {
			t.Errorf("%s: removed %v, want %v", name, err != nil, s.gone)
		}
	}
	for _, d := range keep {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s removed", filepath.Base(d))
		}
	}
	if _, err := os.Stat(previous); err == nil {
		t.Error("a previous run's folder kept")
	}
	s := log.String()
	if strings.Count(s, "event=vod_session_purged") != 4 {
		t.Errorf("log %q", s)
	}
	for _, reason := range []string{"closed", "idle", "orphaned"} {
		if !strings.Contains(s, "reason="+reason) {
			t.Errorf("no %s removal logged: %q", reason, s)
		}
	}
}

// With less than the floor free, the sweep removes the folders it keeps for
// closed sessions, the longest unwatched first, until the floor is free; never
// an open session's.
func TestPurgeVODSessionsDiskLow(t *testing.T) {
	ctx := context.Background()
	layout := cache.New(t.TempDir())
	if err := layout.EnsureTiers(); err != nil {
		t.Fatal(err)
	}
	store := session.NewMemoryStore(time.Second)
	now := time.Now()
	var dirs []string
	for _, watched := range []time.Duration{time.Hour, 3 * time.Hour, 2 * time.Hour} {
		id := uuid.New()
		_ = store.Insert(ctx, &session.Row{ID: id, Mode: session.ModeTranscode, Transcoder: finished(),
			LastSegmentAt: now.Add(-watched)})
		_ = store.Close(ctx, id, session.ReasonIdle, now.Add(-watched))
		dirs = append(dirs, sessionFolder(t, layout, id.String(), now))
	}
	open := uuid.New()
	_ = store.Insert(ctx, &session.Row{ID: open, Mode: session.ModeTranscode, Transcoder: running(), LastSegmentAt: now})
	openDir := sessionFolder(t, layout, open.String(), now)
	// 6 GiB free, a 10 GiB floor: each folder removed frees 3 GiB
	disk := vodDisk{root: layout.Root, minFreeGiB: 10, diskSpace: func(string) (uint64, uint64, error) {
		removed := 0
		for _, d := range dirs {
			if _, err := os.Stat(d); err != nil {
				removed++
			}
		}
		return uint64(6+3*removed) * gib, 100 * gib, nil
	}}
	var log bytes.Buffer
	purgeVODSessions(ctx, layout, store, disk, now, slog.New(slog.NewTextHandler(&log, nil)))
	for i, gone := range []bool{false, true, true} { // 1 h, 3 h, 2 h unwatched: the 3 h, then the 2 h
		if _, err := os.Stat(dirs[i]); (err != nil) != gone {
			t.Errorf("folder %d: removed %v, want %v", i, err != nil, gone)
		}
	}
	if _, err := os.Stat(openDir); err != nil {
		t.Error("an open session's folder removed")
	}
	if strings.Count(log.String(), "reason=disk_low") != 2 {
		t.Errorf("log %q", log.String())
	}
}

// The floor: the configured GiB, else a tenth of the disk between 3 and 20
// GiB; none when it is below 0.
func TestVODDiskFloor(t *testing.T) {
	for _, c := range []struct {
		total     uint64
		minFree   int
		wantFloor uint64
	}{
		{100 * gib, 0, 10 * gib},
		{1000 * gib, 0, 20 * gib},
		{16 * gib, 0, 3 * gib},
		{1000 * gib, 5, 5 * gib},
		{1000 * gib, -1, 0},
	} {
		d := vodDisk{minFreeGiB: c.minFree, diskSpace: func(string) (uint64, uint64, error) { return gib, c.total, nil }}
		if _, floor, err := d.space(); err != nil || floor != c.wantFloor {
			t.Errorf("%d GiB disk, %d configured: floor %d GiB (%v), want %d", c.total/gib, c.minFree, floor/gib, err,
				c.wantFloor/gib)
		}
	}
}

// A viewer pauses for minutes: the paused player's playlist reloads keep the
// session open while its transcode runs. A player gone quiet (a background
// tab) keeps a running transcode for vodActiveIdle, then the reaper stops it.
// Once the transcode is finished, reloads are no activity: the reaper closes
// the session, and its folder stays (its segments still served, a fetch
// pushing its grace on) for vodFinishedGrace after the last fetch.
func TestVODSessionThroughAPause(t *testing.T) {
	ctx := context.Background()
	layout := cache.New(t.TempDir())
	if err := layout.EnsureTiers(); err != nil {
		t.Fatal(err)
	}
	clock := time.Now().UTC()
	now := func() time.Time { return clock }
	store := session.NewMemoryStore(5 * time.Second)
	reaper := session.NewReaper(store, session.ReaperConfig{IdleAfter: vodSessionIdle, ActiveIdleAfter: vodActiveIdle,
		Interval: vodSessionSweep})
	reaper.SetClock(now)
	h := &handlers.ManifestHandler{Sessions: store, Layout: layout, Now: now}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sweep := func() {
		if err := reaper.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
		purgeVODSessions(ctx, layout, store, noFloor, clock, logger)
	}
	open := func(tr session.Transcoder) (uuid.UUID, func(string) int) {
		id := uuid.New()
		if err := store.Insert(ctx, &session.Row{ID: id, Mode: session.ModeTranscode, Format: session.FormatHLS,
			StartedAt: clock, LastSegmentAt: clock, Transcoder: tr}); err != nil {
			t.Fatal(err)
		}
		sessionFolder(t, layout, id.String(), clock)
		r := chi.NewRouter()
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{Sub: id.String()})))
			})
		})
		r.Get("/stream/{session_id}/{rendition}/index.m3u8", h.ServeRenditionIndex)
		r.Get("/stream/{session_id}/{rendition}/{segment}", h.ServeSegment)
		return id, func(path string) int {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest("GET", "/stream/"+id.String()+path, nil))
			return rec.Code
		}
	}
	isOpen := func(id uuid.UUID) bool {
		row, ok, _ := store.Get(ctx, id)
		return ok && row.ClosedAt == nil
	}
	exists := func(id uuid.UUID) bool {
		_, err := os.Stat(layout.HLSDir(id.String()))
		return err == nil
	}

	// transcoding, paused for 15 min: only the playlist is reloaded
	tr := running()
	id, get := open(tr)
	for i := 0; i < 150; i++ {
		clock = clock.Add(6 * time.Second)
		if code := get("/v0/index.m3u8"); code != 200 {
			t.Fatalf("playlist %d", code)
		}
		sweep()
	}
	if !isOpen(id) || !tr.Active() {
		t.Fatal("the paused session was reaped")
	}
	// the tab in the background: nothing fetched; the transcode goes on for
	// vodActiveIdle, then is stopped, and the playlist answers 410 (the player
	// opens the video again)
	clock = clock.Add(vodActiveIdle - time.Minute)
	sweep()
	if !isOpen(id) || !tr.Active() {
		t.Fatal("reaped while its transcode ran, within vodActiveIdle")
	}
	clock = clock.Add(2 * time.Minute)
	sweep()
	if isOpen(id) || tr.Active() {
		t.Fatal("a quiet session kept past vodActiveIdle")
	}
	if code := get("/v0/index.m3u8"); code != 410 {
		t.Errorf("a cut transcode's playlist: %d, want 410", code)
	}

	// finished, then paused: reloads are no activity, the session is reaped
	// after vodSessionIdle, its folder kept for vodFinishedGrace after the
	// last fetch (a fetch during it pushes it on)
	id, get = open(finished())
	for i := 0; i < 20; i++ {
		clock = clock.Add(6 * time.Second)
		get("/v0/index.m3u8")
		sweep()
	}
	if isOpen(id) {
		t.Fatal("a finished transcode's reloads kept its session open")
	}
	clock = clock.Add(vodFinishedGrace - time.Hour)
	sweep()
	if code := get("/v0/seg-0.ts"); code != 200 || !exists(id) {
		t.Fatalf("a segment within the grace: %d", code)
	}
	clock = clock.Add(vodFinishedGrace - time.Minute)
	sweep()
	if !exists(id) {
		t.Fatal("removed within the grace of its last fetch")
	}
	clock = clock.Add(2 * time.Minute)
	sweep()
	if exists(id) {
		t.Fatal("kept past the grace of its last fetch")
	}
}

// transcodeRoom: room above the floor starts a transcode; less does not, and
// says so; a disk that cannot be read does not stop one.
func TestTranscodeRoom(t *testing.T) {
	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, nil))
	disk := func(free uint64, err error) vodDisk {
		return vodDisk{diskSpace: func(string) (uint64, uint64, error) { return free, 100 * gib, err }}
	}
	if err := transcodeRoom(disk(11*gib, nil), logger); err != nil {
		t.Errorf("11 GiB free over a 10 GiB floor: %v", err)
	}
	if err := transcodeRoom(disk(9*gib, nil), logger); err == nil || !strings.Contains(log.String(), "event=vod_disk_low") {
		t.Errorf("9 GiB free: %v, log %q", err, log.String())
	}
	if err := transcodeRoom(disk(0, errors.New("no disk")), logger); err != nil {
		t.Errorf("unreadable: %v", err)
	}
	if err := transcodeRoom(vodDisk{root: t.TempDir(), minFreeGiB: -1}, logger); err != nil {
		t.Errorf("no floor: %v", err)
	}
}

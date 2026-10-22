package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// The sweep removes a closed session's folder at once, one closed for being
// idle after the grace, and a previous run's (no session here) once it has not
// changed for the idle horizon; an open session's, one being opened (its row
// not stored yet) and a channel's stay.
func TestPurgeVODSessions(t *testing.T) {
	ctx := context.Background()
	layout := cache.New(t.TempDir())
	if err := layout.EnsureTiers(); err != nil {
		t.Fatal(err)
	}
	store := session.NewMemoryStore(time.Second)
	now := time.Now()
	stale := now.Add(-2 * vodSessionIdle)
	open, closed, idle, idleLong := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{open, closed, idle, idleLong} {
		if err := store.Insert(ctx, &session.Row{ID: id, Mode: session.ModeTranscode}); err != nil {
			t.Fatal(err)
		}
	}
	for id, c := range map[uuid.UUID]struct {
		reason session.CloseReason
		at     time.Time
	}{
		closed:   {session.ReasonAPI, now},
		idle:     {session.ReasonIdle, now.Add(-vodSessionGrace + time.Minute)},
		idleLong: {session.ReasonIdle, now.Add(-vodSessionGrace)},
	} {
		if err := store.Close(ctx, id, c.reason, c.at); err != nil {
			t.Fatal(err)
		}
	}
	keep := []string{
		sessionFolder(t, layout, open.String(), stale),               // open, if paused
		sessionFolder(t, layout, idle.String(), stale),               // idle, within the grace
		sessionFolder(t, layout, uuid.NewString(), now),              // being opened
		sessionFolder(t, layout, "channel-"+uuid.NewString(), stale), // a channel's
	}
	gone := []string{
		sessionFolder(t, layout, closed.String(), now),
		sessionFolder(t, layout, idleLong.String(), now),
		sessionFolder(t, layout, uuid.NewString(), stale), // a previous run's
	}
	var log bytes.Buffer
	purgeVODSessions(ctx, layout, store, now, slog.New(slog.NewTextHandler(&log, nil)))
	for _, d := range keep {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s removed", filepath.Base(d))
		}
	}
	for _, d := range gone {
		if _, err := os.Stat(d); err == nil {
			t.Errorf("%s kept", filepath.Base(d))
		}
	}
	s := log.String()
	if strings.Count(s, "event=vod_session_purged") != 3 {
		t.Errorf("log %q", s)
	}
	for _, reason := range []string{"closed", "idle", "orphaned"} {
		if !strings.Contains(s, "reason="+reason) {
			t.Errorf("no %s removal logged: %q", reason, s)
		}
	}
}

// A viewer pauses for minutes: the paused player's playlist reloads keep the
// session open. Once nothing is fetched the reaper closes it, and its folder
// stays (its segments still served, so a resume plays and seeks within them)
// until the grace ends.
func TestVODSessionThroughAPause(t *testing.T) {
	ctx := context.Background()
	layout := cache.New(t.TempDir())
	if err := layout.EnsureTiers(); err != nil {
		t.Fatal(err)
	}
	clock := time.Now().UTC()
	now := func() time.Time { return clock }
	store := session.NewMemoryStore(5 * time.Second)
	reaper := session.NewReaper(store, session.ReaperConfig{IdleAfter: vodSessionIdle, Interval: vodSessionSweep})
	reaper.SetClock(now)
	id := uuid.New()
	if err := store.Insert(ctx, &session.Row{ID: id, Mode: session.ModeTranscode, Format: session.FormatHLS,
		StartedAt: clock, LastSegmentAt: clock}); err != nil {
		t.Fatal(err)
	}
	sessionFolder(t, layout, id.String(), clock)
	h := &handlers.ManifestHandler{Sessions: store, Layout: layout, Now: now}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{Sub: id.String()})))
		})
	})
	r.Get("/stream/{session_id}/{rendition}/index.m3u8", h.ServeRenditionIndex)
	r.Get("/stream/{session_id}/{rendition}/{segment}", h.ServeSegment)
	get := func(path string) int {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/stream/"+id.String()+path, nil))
		return rec.Code
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sweep := func() {
		if err := reaper.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
		purgeVODSessions(ctx, layout, store, clock, logger)
	}
	isOpen := func() bool {
		row, ok, _ := store.Get(ctx, id)
		return ok && row.ClosedAt == nil
	}

	// paused for 5 min: only the playlist is reloaded, every target duration
	for i := 0; i < 50; i++ {
		clock = clock.Add(6 * time.Second)
		if code := get("/v0/index.m3u8"); code != 200 {
			t.Fatalf("playlist %d", code)
		}
		sweep()
	}
	if !isOpen() {
		t.Fatal("the paused session was reaped")
	}
	// the player gone: reaped once idle, its folder kept for the grace
	clock = clock.Add(vodSessionIdle + time.Second)
	sweep()
	if isOpen() {
		t.Fatal("not reaped when nothing was fetched")
	}
	clock = clock.Add(vodSessionGrace - time.Minute)
	sweep()
	if code := get("/v0/seg-0.ts"); code != 200 {
		t.Errorf("a segment within the grace: %d", code)
	}
	clock = clock.Add(time.Minute)
	sweep()
	if code := get("/v0/seg-0.ts"); code != 404 {
		t.Errorf("a segment after the grace: %d", code)
	}
}

// transcodeRoom: no floor, or room above it, starts a transcode; less free
// than the floor does not.
func TestTranscodeRoom(t *testing.T) {
	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, nil))
	root := t.TempDir()
	if err := transcodeRoom(root, 0, logger); err != nil {
		t.Errorf("no floor: %v", err)
	}
	if err := transcodeRoom(root, 1, logger); err != nil {
		t.Errorf("one byte: %v", err)
	}
	if err := transcodeRoom(root, math.MaxUint64, logger); err == nil || !strings.Contains(log.String(), "event=vod_disk_low") {
		t.Errorf("the whole of uint64: %v, log %q", err, log.String())
	}
}

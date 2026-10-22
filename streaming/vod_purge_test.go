package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
)

// The sweep removes a closed session's folder at once and a previous run's
// (no session here) once it has not changed for the idle horizon; an open
// session's, one being opened (its row not stored yet) and a channel's stay.
func TestPurgeVODSessions(t *testing.T) {
	ctx := context.Background()
	layout := cache.New(t.TempDir())
	if err := layout.EnsureTiers(); err != nil {
		t.Fatal(err)
	}
	store := session.NewMemoryStore(time.Second)
	now := time.Now()
	stale := now.Add(-2 * vodSessionIdle)
	folder := func(name string, changed time.Time) string {
		dir := layout.HLSDir(name)
		v0 := filepath.Join(dir, "v0")
		if err := os.MkdirAll(v0, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(v0, "seg-0.ts"), []byte("ts"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, d := range []string{v0, dir} {
			if err := os.Chtimes(d, changed, changed); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	open, closed := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{open, closed} {
		if err := store.Insert(ctx, &session.Row{ID: id, Mode: session.ModeTranscode}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(ctx, closed, session.ReasonAPI, now); err != nil {
		t.Fatal(err)
	}
	keep := []string{
		folder(open.String(), stale),               // open, if paused
		folder(uuid.NewString(), now),              // being opened
		folder("channel-"+uuid.NewString(), stale), // a channel's
	}
	gone := []string{
		folder(closed.String(), now),
		folder(uuid.NewString(), stale), // a previous run's
	}
	var log bytes.Buffer
	purgeVODSessions(ctx, layout, store, vodSessionIdle, now, slog.New(slog.NewTextHandler(&log, nil)))
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
	if s := log.String(); strings.Count(s, "event=vod_session_purged") != 2 ||
		!strings.Contains(s, "reason=closed") || !strings.Contains(s, "reason=orphaned") {
		t.Errorf("log %q", s)
	}
}

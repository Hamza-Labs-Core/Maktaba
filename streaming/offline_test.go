package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestDBWatchFollowsTheDatabase(t *testing.T) {
	var down atomic.Bool
	w := &dbWatch{logger: quietLogger(), ping: func(context.Context) error {
		if down.Load() {
			return errors.New("dial tcp: connection refused")
		}
		return nil
	}}
	ctx := context.Background()
	w.check(ctx)
	if w.Down() {
		t.Fatal("down with the database answering")
	}
	back := w.backCh()
	down.Store(true)
	w.check(ctx)
	if !w.Down() {
		t.Fatal("up with the database gone")
	}
	waited := make(chan error, 1)
	go func() { waited <- w.wait(ctx) }()
	down.Store(false)
	w.check(ctx)
	select {
	case <-back:
	case <-time.After(time.Second):
		t.Fatal("the database answering again was not signalled")
	}
	if err := <-waited; err != nil {
		t.Fatalf("wait: %v", err)
	}
	w.Failed(errors.New("a lookup timed out"))
	if !w.Down() {
		t.Fatal("a failed lookup does not mark the database down")
	}
}

type countingSource struct {
	calls atomic.Int32
	rows  []probe.Row
}

func (s *countingSource) AllRows(context.Context) ([]probe.Row, error) {
	s.calls.Add(1)
	return s.rows, nil
}
func (s *countingSource) PathMap(context.Context) (map[string]string, error) {
	return map[string]string{"/Volumes/Originals/": "O:/"}, nil
}

// The snapshot is not read from a database that does not answer, and is as soon as it
// answers again.
func TestRefreshSnapshotWaitsForTheDatabase(t *testing.T) {
	id := uuid.New()
	src := &countingSource{rows: []probe.Row{{VideoID: id, Path: "/Volumes/Originals/a.mp4", Probed: true}}}
	w := &dbWatch{logger: quietLogger(), ping: func(context.Context) error { return nil }}
	w.Failed(errors.New("not answering at start"))
	snap := probe.OpenSnapshot("", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { refreshSnapshot(ctx, quietLogger(), snap, src, w); close(done) }()
	time.Sleep(50 * time.Millisecond)
	if src.calls.Load() != 0 {
		t.Fatal("the snapshot was read from a database that does not answer")
	}
	w.check(ctx) // it answers
	deadline := time.Now().Add(2 * time.Second)
	for snap.Len() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := snap.Lookup(id); !ok || snap.PathMap()["/Volumes/Originals/"] != "O:/" {
		t.Fatal("the snapshot was not refreshed once the database answered")
	}
	cancel()
	<-done
}

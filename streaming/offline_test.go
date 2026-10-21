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

// Without a copy (none made yet) the home's database stays the only source: a lookup it
// cannot answer is unavailable, not a crash.
func TestWithoutACopy(t *testing.T) {
	t.Setenv(copyEnv, "")
	bg := newBackground(context.Background())
	defer bg.Stop(time.Second)
	w := &dbWatch{logger: quietLogger(), ping: func(context.Context) error { return errors.New("down") }}
	w.Failed(errors.New("not answering at start"))
	b := withCopy(bg, quietLogger(), nil, "node-1", w)
	o, ok := b.(*probe.Offline)
	if !ok || o.Copy != nil {
		t.Fatalf("backend %T copy %v", b, o)
	}
	if _, err := o.Lookup(context.Background(), uuid.New()); !errors.Is(err, probe.ErrUnavailable) {
		t.Fatalf("a lookup with neither answering: %v", err)
	}
}

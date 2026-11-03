package main

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner/loopcheck"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// T09 (F1, Go loops): every loop streaming declares is in the catalog, every catalog
// loop of streaming is started, and a bare bg.Go in a start file says why it is not a
// loop.
func TestStreamingLoopsMatchTheCatalog(t *testing.T) {
	loopcheck.Check(t, loopcheck.Spec{
		Host: componentHost, Root: ".", StartFiles: []string{"main.go", "tvcluster_wire.go", "slots_wire.go"},
		Declare: []string{"Loop"}, Plain: []loopcheck.Plain{{Pkg: "bg", Func: "Go", NameArg: -1}},
	})
}

// With a runner, bg.Loop stops with its component, and bg.Stop still waits for every
// loop before the pool closes (T11 for streaming's loops).
func TestBackgroundLoopFollowsItsComponent(t *testing.T) {
	bg := newBackground(context.Background())
	r := runner.New(runner.Options{Host: componentHost, Logger: slog.Default(), Context: bg.ctx})
	bg.runner = r
	bg.Go(func(ctx context.Context) { r.Run(ctx, runner.Source{}) })
	var running atomic.Int32
	loop := func(ctx context.Context) {
		running.Add(1)
		defer running.Add(-1)
		<-ctx.Done()
	}
	bg.Loop("hdhr_ssdp", loop)
	bg.Loop("watchdog", loop)
	waitUntil(t, func() bool { return running.Load() == 2 })

	r.Apply(map[string]store.Desired{"livetv.hdhomerun": {ID: "livetv.hdhomerun", Want: components.WantInstalled}})
	waitUntil(t, func() bool { return running.Load() == 1 })
	if bg.On("livetv.hdhomerun") || !bg.On("core.streaming") {
		t.Fatal("gates: hdhomerun should be off, core on")
	}
	if !bg.Stop(5 * time.Second) {
		t.Fatal("bg.Stop did not wait for the runner's loops")
	}
	if n := running.Load(); n != 0 {
		t.Fatalf("%d loops still running after Stop", n)
	}

	// no runner (a node): Loop is a plain background goroutine
	plain := newBackground(context.Background())
	plain.Loop("hdhr_ssdp", loop)
	waitUntil(t, func() bool { return running.Load() == 1 })
	if !plain.On("livetv") || !plain.Stop(5*time.Second) {
		t.Fatal("no runner: every component on, and Stop waits")
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

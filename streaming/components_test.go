package main

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner/loopcheck"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pgtest"
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

// Epic 51 on a real Postgres: a joined machine's streaming runs its loops on a runner of
// that machine's desired state, so an override that turns livetv.hdhomerun off there stops
// its loop on that machine only; the home's keeps running.
func TestJoinedMachineLoopsFollowItsOverride(t *testing.T) {
	if testing.Short() {
		t.Skip("real Postgres")
	}
	dsn, pool := pgtest.ScratchDB(t)
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("MAKTABA_COMPONENTS_RECONCILE", "")
	nodeID := uuid.NewString()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, approved_at, approved_by)
		VALUES ($1, 'n', 'approved', 'linux', 'amd64', 'k-n', now(), 'test')`, nodeID); err != nil {
		t.Fatal(err)
	}
	var homeOn, nodeOn atomic.Int32
	loop := func(on *atomic.Int32) func(ctx context.Context) {
		return func(ctx context.Context) {
			on.Add(1)
			defer on.Add(-1)
			<-ctx.Done()
		}
	}
	home, node := newBackground(ctx), newBackground(ctx)
	startComponents(home, pool, "", slog.Default())
	startComponents(node, pool, nodeID, slog.Default())
	home.Loop("hdhr_ssdp", loop(&homeOn))
	node.Loop("hdhr_ssdp", loop(&nodeOn))
	t.Cleanup(func() { home.Stop(5 * time.Second); node.Stop(5 * time.Second) })
	waitUntil(t, func() bool { return homeOn.Load() == 1 && nodeOn.Load() == 1 })
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()
	if err := store.New(db).SetOverride(ctx, store.Override{Node: nodeID, ID: "livetv.hdhomerun", Want: components.WantInstalled},
		store.Audit{By: "admin", ActorSource: "admin", Action: "component.disable"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return nodeOn.Load() == 0 })
	time.Sleep(300 * time.Millisecond)
	if homeOn.Load() != 1 || !home.On("livetv.hdhomerun") || node.On("livetv.hdhomerun") {
		t.Fatalf("home %d (on %v), node on %v", homeOn.Load(), home.On("livetv.hdhomerun"), node.On("livetv.hdhomerun"))
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

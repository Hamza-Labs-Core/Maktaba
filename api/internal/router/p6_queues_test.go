package router_test

// The queues handler as MountP6 builds it (slot 0175): it maps a library's paths
// to this machine (libpaths.ToLocal), which the YouTube guess (its .info.json
// sidecars) and the Tartube file suggestion read through. Runs when
// MAKTABA_TEST_PG_URL is set (see access_pg_test.go).

import (
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/libpaths"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

func TestPGMountP6QueuesMapPathsToThisMachine(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	var got *queues.Handler
	r := router.New(router.Deps{IdempotencyStore: idempotency.NewMemoryStore()})
	router.MountP6(r, router.P6Deps{DB: db, StreamingClient: acStreaming{}, OnQueues: func(h *queues.Handler) { got = h }})
	if got == nil {
		t.Fatal("MountP6 never handed over the queues handler")
	}
	if got.LocalPath == nil {
		t.Fatal("the queues handler has no LocalPath: sidecars and the Tartube file are read at the database's path")
	}
	const p = "/Volumes/Originals/Backup/Youtube/x.mp4"
	if a, b := got.LocalPath(p), libpaths.ToLocal(p); a != b {
		t.Fatalf("LocalPath(%q) = %q, want libpaths.ToLocal's %q", p, a, b)
	}
}

package queues

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/swr"
)

// The queue snapshot (GET /api/queues, /api/queues/{stage}, the /ws/queues
// frames and the cluster hub's queues topic) and the library rows (GET
// /api/queues/libraries) are read through one stale-while-revalidate cache per
// database (swr): concurrent readers share one build, a stale value is served
// at once while one rebuild runs, and a failed or slow rebuild never fails a
// read that has a previous value. The cache is per *sql.DB, not per Handler:
// the API's routes, the snapshot publisher and the cluster hub each build
// their own Handler on the same pool, and share these reads.
const (
	// snapshotTTL matches the /ws/queues publish cadence.
	snapshotTTL = DefaultSnapshotInterval
	// libraryCacheTTL is how long the library rows are fresh
	// (plan-06-11 §8.1: "cached 5 s").
	libraryCacheTTL = 5 * time.Second
	// readBuildTimeout bounds one build of either read.
	readBuildTimeout = 2 * time.Minute
)

type sharedReads struct {
	snapshot  swr.Cache[Snapshot]
	libraries swr.Cache[[]LibraryQueueRow]
}

var readsByDB sync.Map // *sql.DB -> *sharedReads

func readsFor(db *sql.DB) *sharedReads {
	if v, ok := readsByDB.Load(db); ok {
		return v.(*sharedReads)
	}
	r := &sharedReads{}
	r.snapshot.TTL, r.snapshot.Timeout = snapshotTTL, readBuildTimeout
	r.snapshot.OnError = func(err error) {
		slog.Default().Warn("queues: snapshot build failed; serving the last one", "err", err,
			"event", "queues_snapshot_build_failed")
	}
	r.libraries.TTL, r.libraries.Timeout = libraryCacheTTL, readBuildTimeout
	r.libraries.OnError = func(err error) {
		slog.Default().Warn("queues: library rows build failed; serving the last ones", "err", err,
			"event", "queues_libraries_build_failed")
	}
	v, _ := readsByDB.LoadOrStore(db, r)
	return v.(*sharedReads)
}

func (h *Handler) reads() *sharedReads { return readsFor(h.DB) }

// InvalidateReads marks the cached snapshot and library rows stale after a
// write that changes them (a pause, a pool size, a retry): the next read
// waits a moment for the new values instead of serving the old ones.
func (h *Handler) InvalidateReads() {
	r := h.reads()
	r.snapshot.Invalidate()
	r.libraries.Invalidate()
}

// waitReads blocks until no build of either read is in flight (tests).
func (h *Handler) waitReads(ctx context.Context) error {
	r := h.reads()
	if err := r.snapshot.Wait(ctx); err != nil {
		return err
	}
	return r.libraries.Wait(ctx)
}

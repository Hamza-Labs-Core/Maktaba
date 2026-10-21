package main

// A full node plays with the home's database down (PR #145): its probe rows come from
// the node's own snapshot (probe.Snapshot, in MAKTABA_NODE_STATE_DIR) while the database
// does not answer, its path map too, and the role starts without the database; the
// channels the home's front door places here wait for it (VOD does not).

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
)

// dbWatch tells whether the home's database answers this node: a ping every 5 s, each
// bounded to 2 s, and any lookup that failed on it.
type dbWatch struct {
	ping   func(context.Context) error
	logger *slog.Logger

	down atomic.Bool
	mu   sync.Mutex
	back chan struct{} // closed when the database answers again
}

func (w *dbWatch) Down() bool { return w.down.Load() }

func (w *dbWatch) Failed(err error) { w.set(true, err) }

func (w *dbWatch) set(down bool, err error) {
	if w.down.Swap(down) == down {
		return
	}
	if down {
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		w.logger.Warn("streaming: the home's database does not answer; playback reads this machine's snapshot",
			"err", msg, "event", "stream_home_db_down")
		return
	}
	w.logger.Info("streaming: the home's database answers again", "event", "stream_home_db_back")
	w.mu.Lock()
	if w.back != nil {
		close(w.back)
		w.back = nil
	}
	w.mu.Unlock()
}

// backCh is closed the next time the database answers after being down.
func (w *dbWatch) backCh() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.back == nil {
		w.back = make(chan struct{})
	}
	return w.back
}

func (w *dbWatch) check(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	err := w.ping(pctx)
	cancel()
	if ctx.Err() == nil {
		w.set(err != nil, err)
	}
}

func (w *dbWatch) run(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		w.check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// wait returns once the database answers (nil) or ctx ends.
func (w *dbWatch) wait(ctx context.Context) error {
	for w.Down() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.backCh():
		}
	}
	return nil
}

// snapshotSource reads the snapshot from the home's database: every probed row, and
// this node's path map (none when MAKTABA_STREAM_PATH_MAP sets it).
type snapshotSource struct {
	pg     *probe.PGBackend
	pool   *pgxpool.Pool
	nodeID string
}

func (s snapshotSource) AllRows(ctx context.Context) ([]probe.Row, error) { return s.pg.AllRows(ctx) }

func (s snapshotSource) PathMap(ctx context.Context) (map[string]string, error) {
	if os.Getenv(pathmap.EnvVar) != "" || s.nodeID == "" {
		return nil, nil
	}
	m, err := cluster.NodePathMap(ctx, s.pool, s.nodeID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, r := range m.Rules() {
		out[r.From] = r.To
	}
	return out, nil
}

// nodeStateDir is where a node keeps its own state: MAKTABA_NODE_STATE_DIR (its
// supervisor sets it), else none (the snapshot then lives in memory only).
func nodeStateDir() string { return os.Getenv("MAKTABA_NODE_STATE_DIR") }

// withSnapshot wraps a full node's probe backend with its snapshot: it opens the file,
// installs the path map it holds (until the database answers with its own), and keeps
// the watch and the refresh running on bg.
func withSnapshot(bg *background, logger *slog.Logger, pg *probe.PGBackend, pool *pgxpool.Pool, nodeID string,
	watch *dbWatch) probe.Backend {
	path := ""
	if dir := nodeStateDir(); dir != "" {
		path = filepath.Join(dir, probe.SnapshotFile)
	} else {
		logger.Warn("streaming: no state directory (MAKTABA_NODE_STATE_DIR): the playback snapshot is not kept across restarts",
			"event", "stream_snapshot_memory_only")
	}
	snap := probe.OpenSnapshot(path, logger)
	if pm := snap.PathMap(); len(pm) > 0 && os.Getenv(pathmap.EnvVar) == "" {
		if raw, err := json.Marshal(pm); err == nil {
			if m, err := pathmap.FromJSON(raw); err == nil {
				pathmap.SetDefault(m)
			}
		}
	}
	src := snapshotSource{pg: pg, pool: pool, nodeID: nodeID}
	bg.Go(watch.run)
	bg.Go(func(ctx context.Context) { refreshSnapshot(ctx, logger, snap, src, watch) })
	return &probe.Offline{Primary: pg, Snap: snap, Down: watch.Down, Failed: watch.Failed}
}

// refreshSnapshot refreshes now, every probe.SnapshotEvery while the database answers,
// and as soon as it answers again after an outage.
func refreshSnapshot(ctx context.Context, logger *slog.Logger, snap *probe.Snapshot, src probe.SnapshotSource, watch *dbWatch) {
	refresh := func() {
		if watch.Down() {
			return
		}
		rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if err := snap.Refresh(rctx, src); err != nil && ctx.Err() == nil {
			logger.Warn("streaming: this machine's playback snapshot was not refreshed", "err", err.Error(),
				"event", "stream_snapshot_refresh_failed")
		}
	}
	refresh()
	t := time.NewTicker(probe.SnapshotEvery)
	defer t.Stop()
	for {
		back := watch.backCh()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-back:
		}
		refresh()
	}
}

// homeDBDownHandler answers the channel routes while the home's database does not
// answer (they wait for it; VOD does not).
func homeDBDownHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		http.Error(w, "this node's channels wait for the home's database", http.StatusServiceUnavailable)
	})
}

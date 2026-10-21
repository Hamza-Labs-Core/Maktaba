package main

// A full node plays with the home's database down (specs/epics/37-node-replica): its
// probe rows come from this machine's copy of the home's database
// (MAKTABA_COPY_DATABASE_URL) while the home's does not answer, its path map too, and
// the role starts without the home's database; the channels the home's front door
// places here wait for it (VOD does not).

import (
	"context"
	"log/slog"
	"net/http"
	"os"
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
		w.logger.Warn("streaming: the home's database does not answer; playback reads this machine's copy of it",
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

// copyEnv is this machine's copy of the home's database (its supervisor sets it).
const copyEnv = "MAKTABA_COPY_DATABASE_URL"

// withCopy wraps a full node's probe backend with this machine's copy of the home's
// database: the copy answers while the home's does not, and gives this machine's path
// map (until the home's database answers with its own, buildNodeTV). The watch runs
// on bg; the copy's pool closes with it.
func withCopy(bg *background, logger *slog.Logger, pg *probe.PGBackend, nodeID string, watch *dbWatch) probe.Backend {
	o := &probe.Offline{Primary: pg, Down: watch.Down, Failed: watch.Failed}
	bg.Go(watch.run)
	dsn := os.Getenv(copyEnv)
	if dsn == "" {
		logger.Warn("streaming: no copy of the home's database ("+copyEnv+"); with the home's database down nothing plays",
			"event", "stream_no_copy")
		return o
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		logger.Warn("streaming: this machine's copy of the home's database is not usable", "err", err.Error(),
			"event", "stream_copy_unusable")
		return o
	}
	o.Copy = probe.NewPGBackend(pool)
	bg.Go(func(ctx context.Context) {
		<-ctx.Done()
		pool.Close()
	})
	if os.Getenv(pathmap.EnvVar) == "" && nodeID != "" {
		bg.Go(func(ctx context.Context) { copyPathMap(ctx, logger, pool, nodeID, watch) })
	}
	return o
}

// copyPathMap installs this machine's path map from the copy while the home's database
// does not answer (now, and every minute meanwhile).
func copyPathMap(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, nodeID string, watch *dbWatch) {
	load := func() {
		if !watch.Down() {
			return
		}
		qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		m, err := cluster.NodePathMap(qctx, pool, nodeID)
		if err != nil {
			logger.Debug("streaming: the path map was not read from the copy", "err", err.Error(),
				"event", "stream_copy_path_map_unavailable")
			return
		}
		pathmap.SetDefault(m)
	}
	load()
	tick(ctx, time.Minute, load)
}

// homeDBDownHandler answers the channel routes while the home's database does not
// answer (they wait for it; VOD does not).
func homeDBDownHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		http.Error(w, "this node's channels wait for the home's database", http.StatusServiceUnavailable)
	})
}

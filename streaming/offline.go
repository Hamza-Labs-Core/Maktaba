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

func (w *dbWatch) check(ctx context.Context) error {
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	err := w.ping(pctx)
	cancel()
	if ctx.Err() == nil {
		w.set(err != nil, err)
	}
	return err
}

// dbWatchEvery is the watch's ping period.
const dbWatchEvery = 5 * time.Second

// run pings every dbWatchEvery; a login the home's database refused (a password it no
// longer has) is tried again later and later (cluster.LoginBackoff), never every few
// seconds for as long as this process runs.
func (w *dbWatch) run(ctx context.Context) {
	refused := 0
	for {
		wait := dbWatchEvery
		if err := w.check(ctx); cluster.LoginRefused(err) {
			refused++
			wait = cluster.LoginBackoff(dbWatchEvery, refused)
		} else {
			refused = 0
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// wait returns once the database answers (nil) or ctx ends. The channel is taken
// before Down is read: set flips down before it closes the channel, so the database
// coming back either closes the channel held here or is seen by Down. (Read the other
// way round, a set between the two left wait on a new channel no one closes.)
func (w *dbWatch) wait(ctx context.Context) error {
	for {
		back := w.backCh()
		if !w.Down() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-back:
		}
	}
}

// copyEnv is this machine's copy of the home's database (its supervisor sets it).
const copyEnv = "MAKTABA_COPY_DATABASE_URL"

// withCopy wraps a full node's probe backend with this machine's copy of the home's
// database (openCopy; nil: none): the copy answers while the home's does not, and
// gives this machine's path map (until the home's database answers with its own,
// buildNodeTV). The watch runs on bg.
func withCopy(bg *background, pg *probe.PGBackend, pool *pgxpool.Pool, logger *slog.Logger, nodeID string, watch *dbWatch) probe.Backend {
	o := &probe.Offline{Primary: pg, Down: watch.Down, Failed: watch.Failed}
	bg.Go(watch.run)
	if pool == nil {
		return o
	}
	o.Copy = probe.NewPGBackend(pool)
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

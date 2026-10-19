package main

import (
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/watchdog"
)

// procWatchdog is this process's self-health heartbeat (Story 22.9). Its
// LiveCheck backs /healthz on the admin port, which the maktaba-server
// supervisor polls to detect a hung streaming process.
var procWatchdog = newProcWatchdog()

func newProcWatchdog() *watchdog.Watchdog {
	w := watchdog.New()
	if d := envDurationDefault("MAKTABA_WATCHDOG_STALE", 0); d > 0 {
		w.Stale = d
	}
	return w
}

// watchChannels registers the live-channel engine (a hard probe: a lock
// nobody releases wedges every tune) and the database (a soft probe:
// readiness only) with the watchdog.
func watchChannels(engine *channel.Engine, pool *pgxpool.Pool) {
	procWatchdog.Watch("channel_engine", engine.Responsive)
	if pool != nil {
		procWatchdog.Observe("db", pool.Ping)
	}
}

// envDurationDefault reads a Go duration env var, falling back to def
// when unset or unparseable.
func envDurationDefault(key string, def time.Duration) time.Duration {
	d, err := time.ParseDuration(os.Getenv(key))
	if err != nil {
		return def
	}
	return d
}

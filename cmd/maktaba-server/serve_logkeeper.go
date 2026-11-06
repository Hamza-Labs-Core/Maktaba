package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/logkeeper"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// serveKeeper is this serve's log keeper (Epic 53 story 53.02), once started: serve's own
// stderr lines go to it too (stderr_stamp.go), as the supervisor's.
var serveKeeper atomic.Pointer[logkeeper.Keeper]

// LogsRoot is where the log keeper writes: <data>/logs (the data folder, on the big disk
// on the live Mac).
func LogsRoot(cfg config.Config) string { return filepath.Join(cfg.Server.DataDir, "logs") }

// startLogKeeper starts the keeper for this machine: every supervised process's output in
// its own segments, the desktop app's app.log followed, [logs]'s retention and cap. echo
// is server.log (serve's stderr before stamping). Stop flushes and closes it.
func startLogKeeper(cfg config.Config, node *nodeServices, echo io.Writer) (k *logkeeper.Keeper, stop func()) {
	r := cfg.Logs.Resolved()
	name := "home"
	if node != nil && node.node.NodeID != "" {
		name = node.node.NodeID
	}
	// the keeper's own pass names a user by hash, as the roles do
	if path := supervisor.LogKeyPath(); supervisor.EnsureLogKey(path) == nil {
		if key, err := mlog.ReadLogKey(path); err == nil {
			mlog.SetUserHashKey(key)
		}
	}
	cat := components.Default()
	k = logkeeper.New(logkeeper.Config{
		Root:           LogsRoot(cfg),
		Node:           name,
		Retention:      r.Retention,
		DebugRetention: r.DebugRetention,
		MaxSegments:    r.MaxSegments,
		MinFree:        r.MinFree,
		Echo:           echo,
		EchoAll:        r.ServerLogAll,
		Component: func(role, instance string) string {
			if c, ok := cat.ByProcess(role, instance); ok {
				return c.ID
			}
			return ""
		},
	})
	// the desktop app's own lines (Epic 53 story 53.01: schema lines), when it runs here
	if app := filepath.Join(config.LogsDir(), "app.log"); fileExists(app) {
		k.FollowFile(app, logkeeper.Origin{Service: "desktop"})
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); k.Run(ctx) }()
	serveKeeper.Store(k)
	return k, func() {
		serveKeeper.Store(nil)
		cancel()
		<-done
		k.Close()
	}
}

// keeperSpool is the supervisor's WithSpool for k.
func keeperSpool(k *logkeeper.Keeper) supervisor.Option {
	return supervisor.WithSpool(func(r supervisor.Role, instance string) (*os.File, error) {
		return k.Spool(string(r), instance)
	})
}

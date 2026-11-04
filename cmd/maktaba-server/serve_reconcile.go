package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/packs"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store/pqlisten"
)

// Epic 51 story 51.04: the supervisor's reconciler, serve's side. The whole machine's
// supervisor runs it against the home's Postgres (a node with its own login, for its own
// machine id); a single role (containers) and a SQLite install run none, and their
// processes and loops run as server.toml builds them.

// componentsTarget is where this machine reads its desired state: the machine ("home" or
// the node's id) and the database's DSN. ok is false when there is none to read.
func componentsTarget(cfg config.Config) (machine, dsn string, ok bool) {
	n, err := supervisor.NodeOf(cfg)
	if err != nil {
		return "", "", false
	}
	if supervisor.NodeMode(cfg, n) {
		return n.NodeID, supervisor.HomeDSN(n), n.NodeID != ""
	}
	u := strings.TrimSpace(cfg.Database.URL)
	l := strings.ToLower(u)
	if !strings.HasPrefix(l, "postgres://") && !strings.HasPrefix(l, "postgresql://") {
		return "", "", false
	}
	return store.Home, u, true
}

// componentsMachine is this machine's id in the desired state: the node's, else the home.
func componentsMachine(cfg config.Config) string {
	if n, err := supervisor.NodeOf(cfg); err == nil && supervisor.NodeMode(cfg, n) && n.NodeID != "" {
		return n.NodeID
	}
	return store.Home
}

// bootConfig is cfg as the last desired state seen says for what is built (the slots,
// ollama, the relay agent: supervisor.BootConfig), said in the log when it differs from
// server.toml.
func bootConfig(cfg config.Config, log io.Writer) config.Config {
	out, changed := supervisor.BootConfig(cfg, componentsMachine(cfg))
	if len(changed) > 0 {
		fmt.Fprintf(log, "[components] the desired state wins over server.toml for this boot: %s\n",
			strings.Join(changed, ", "))
	}
	return out
}

// freeMB is this machine's memory a new process can have, in MB (0: not known).
func freeMB() int64 { return int64(hostinfo.FreeMemory() >> 20) }

// startComponents builds the reconciler for sup, primes its gates (before sup runs
// anything) and runs it with the loops, and the asset half (story 51.16: packs) beside
// it. With no reconciler the loops run as before. It returns the asset routes (their
// token set by the caller): every route answers 503 when there is no reconciler.
func startComponents(ctx context.Context, cfg config.Config, sup *supervisor.Supervisor, whole bool,
	loops []*supervisor.Loop, releaseDir string, log io.Writer) *packsAdmin {
	if log == nil {
		log = io.Discard
	}
	machine, dsn, ok := componentsTarget(cfg)
	if !whole || !ok {
		for _, l := range loops {
			if l.ConfigOn && (l.Forced == "" || l.ForcedOn) {
				go l.Run(ctx)
			}
		}
		return &packsAdmin{ctx: ctx, sup: sup}
	}
	rec := supervisor.NewReconciler(sup)
	rec.Machine = machine
	rec.Off = !cfg.Components.ReconcileOn()
	rec.CachePath = supervisor.CompCachePath(cfg.StateDir())
	rec.Loops = loops
	rec.Log = log
	// a dormant process is checked for memory before its first start (not_enough_memory)
	rec.FreeMB = freeMB
	// the rows' rss_mb (story 51.14): its processes' memory, read every few minutes
	rec.RSS = hostinfo.ProcessRSS
	// the asset half: what a crash left is tidied before anything reads it
	m := newPacksManager(cfg, releaseDir, log)
	for _, did := range m.Layout.Recover(m.Version) {
		fmt.Fprintf(log, "[packs] %s\n", did)
	}
	// story 51.18: a model revision an update staged, or the one a rollback came back to
	for _, did := range m.RecoverModels() {
		fmt.Fprintf(log, "[packs] %s\n", did)
	}
	run := &packs.Runner{M: m, Machine: machine, Off: rec.Off}
	rec.Assets, rec.OnDesired = m, run.Desired
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		fmt.Fprintf(log, "[components] level=warn the desired state cannot be read: %v\n", err)
	} else {
		db.SetMaxOpenConns(2)
		st := store.New(db)
		st.Version = version.Version
		rec.Source, rec.Status = st, st
		run.Status = st
		rec.NewListener = func(onError func(error)) (store.Listener, error) { return pqlisten.New(dsn, onError) }
		go func() { <-ctx.Done(); _ = db.Close() }()
	}
	rec.Prime()
	go run.Run(ctx)
	go rec.Run(ctx)
	// story 51.17: the Linux maktaba-full package's packs, at the first start of its version
	go importSystemPacks(ctx, m, cfg.StateDir(), rec.Off, log)
	pa := &packsAdmin{ctx: ctx, m: m, r: run, sup: sup}
	if st, ok := rec.Source.(*store.Store); ok {
		pa.metrics = &supervisor.ComponentMetrics{Source: st, Machine: machine}
	}
	return pa
}

// mdnsLoop is the supervisor's mDNS advertiser as the discovery.mdns component's loop:
// on by default when [server] mdns is, kept off for this boot by --no-mdns (boot_flag)
// or MAKTABA_DISCOVERY_MDNS=0 (env_override).
func mdnsLoop(cfg config.Config, noMDNS bool, run func(ctx context.Context)) *supervisor.Loop {
	l := &supervisor.Loop{Name: "supervisor:mdns", Run: run, ConfigOn: cfg.Server.MDNSOn()}
	v := strings.TrimSpace(strings.ToLower(os.Getenv("MAKTABA_DISCOVERY_MDNS")))
	switch {
	case noMDNS:
		l.Forced, l.ForcedParams = supervisor.ReasonBootFlag, map[string]any{"flag": "--no-mdns"}
	case v == "0" || v == "false" || v == "off":
		l.Forced, l.ForcedParams = supervisor.ReasonEnvOverride, map[string]any{"variable": "MAKTABA_DISCOVERY_MDNS"}
	}
	return l
}

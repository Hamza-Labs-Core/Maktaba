package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/componentseed"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/dsn"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/llmserver"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Epic 51 story 51.03: the components' desired state seeded once from what this home runs
// today, before any role starts (componentseed).

// componentSeedTries is how often a gated boot tries the seed before the roles start
// without it (the next boot seeds): a seed's failure never keeps the home from serving.
const componentSeedTries = 3

var componentSeedPause = 2 * time.Second

// componentsAtBoot gives the home's roles the seed step, after the database step they
// already wait on, or (a home whose API migrates the database itself) seeds in the
// background once the tables are there; a joined machine seeds its own overrides in the
// background. procs is returned with the step set.
func componentsAtBoot(ctx context.Context, cfg config.Config, opts serveOptions, releaseDir string,
	llmProc *supervisor.Process, node *nodeServices, procs []supervisor.Process, log io.Writer) []supervisor.Process {
	llm := llmProc != nil || strings.TrimSpace(cfg.LLM.URL) != ""
	if node != nil {
		go nodeComponentsSeed(ctx, cfg, node.node, llm, log)
		return procs
	}
	info, err := dsn.Classify(cfg.Database.URL)
	if err != nil {
		fmt.Fprintf(log, "[serve] level=warn components: no seed: the database url: %v\n", err)
		return procs
	}
	exe, _ := os.Executable()
	probe := cfg
	probe.LLM.Enabled, probe.LLM.URL = "auto", ""
	_, ollama, _ := llmserver.Resolve(probe, exe)
	cat := components.Default()
	home := componentseed.Home{
		Catalog: cat,
		Facts: componentseed.Facts{Config: cfg, Slots: supervisor.SlotsEnabled(cfg), LLMProcess: llmProc != nil,
			Relay: supervisor.RelayWanted(cfg),
			Assets: componentseed.Assets(cat, componentseed.Disk{Config: cfg, ReleaseDir: releaseDir, Ollama: ollama,
				Python: supervisor.PipelinePython(supervisor.Options{Config: cfg, ReleaseDir: releaseDir})})},
		Release:  version.Version,
		TomlPath: config.Path(),
		NoMDNS:   opts.noMDNS,
		Log:      log,
	}
	// story 51.17: setup's answer to "Choose what to run" seeds the first desired state
	// (no answer: Full, today's switches)
	if ch, ok, err := componentseed.ReadChoice(cfg.StateDir()); err != nil {
		fmt.Fprintf(log, "[serve] level=warn components: setup's bundle choice cannot be read (%v): the seed is Full\n", err)
	} else if ok {
		home.Facts.Choice = &ch
	}
	run := func(ctx context.Context) error {
		db, err := sql.Open(info.Driver, info.DSN)
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		if info.Backend == dsn.SQLite {
			db.SetMaxOpenConns(1)
		}
		h := home
		h.Store = store.New(db)
		return h.Run(ctx)
	}
	gated := false
	for _, p := range procs {
		gated = gated || (p.Role == supervisor.RoleAPI && p.Prepare != nil)
	}
	if !gated {
		go seedWhenMigrated(ctx, run, log)
		return procs
	}
	seed := &sharedStep{wait: detail("db_waiting", "Waiting for the library database"),
		run: func(ctx context.Context, _ func(supervisor.Detail)) error {
			seedTries(ctx, run, componentSeedTries, log)
			return nil
		}}
	for i := range procs {
		p := &procs[i]
		switch p.Role {
		case supervisor.RolePostgres, supervisor.RoleOllama:
			continue
		}
		if p.Prepare != nil {
			p.Prepare = steps(p.Prepare, seed.prepare)
		} else {
			p.Prepare = seed.prepare
		}
	}
	return procs
}

// seedTries runs the seed up to n times; a failure each time is logged, not fatal.
func seedTries(ctx context.Context, run func(context.Context) error, n int, log io.Writer) {
	var err error
	for i := 0; i < n; i++ {
		if err = run(ctx); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(componentSeedPause):
		}
	}
	fmt.Fprintf(log, "[serve] level=error components: the seed of desired state failed (%v): "+
		"the roles start without it; the next start tries again\n", err)
}

// seedWhenMigrated seeds once the API made the tables (it migrates at its own start).
func seedWhenMigrated(ctx context.Context, run func(context.Context) error, log io.Writer) {
	failures := 0
	for {
		err := run(ctx)
		if err == nil {
			return
		}
		if !missingTable(err) {
			failures++
			if failures >= 10 {
				fmt.Fprintf(log, "[serve] level=error components: the seed of desired state failed (%v): "+
					"the next start tries again\n", err)
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func missingTable(err error) bool {
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "does not exist") || strings.Contains(m, "no such table")
}

// nodeComponentsSeed writes this machine's first overrides once its home's database
// answers and its home has seeded.
func nodeComponentsSeed(ctx context.Context, cfg config.Config, n config.NodeConfig, llm bool, log io.Writer) {
	said := ""
	for {
		err := func() error {
			d, err := supervisor.HomeDSNChecked(n)
			if err != nil {
				return err
			}
			db, err := sql.Open("postgres", d)
			if err != nil {
				return err
			}
			defer func() { _ = db.Close() }()
			return componentseed.Node{Store: store.New(db), NodeID: n.NodeID, Config: cfg, LLM: llm,
				Marker: componentseed.MarkerPath(cfg.StateDir()), Log: log}.Run(ctx)
		}()
		if err == nil {
			return
		}
		if !errors.Is(err, componentseed.ErrHomeNotSeeded) && !missingTable(err) && err.Error() != said {
			said = err.Error() // said once, not every half minute
			fmt.Fprintf(log, "[serve] level=warn components: this machine's overrides are not seeded yet: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}

package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/nodecopy"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbin"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// nodeCopy is a full node's copy of its home's database (Epic 37, nodecopy) and where
// its Postgres programs come from.
type nodeCopy struct {
	copy     nodecopy.Copy
	programs func(ctx context.Context) (string, error)
}

// resolveNodeCopy resolves a full node's copy when serve runs the whole machine: its
// address goes into the environment of the api and streaming roles (cfg.NodeCopy)
// before they are built, and its database is one of the supervised processes; its
// manager starts with the supervisor (runNodeCopy). The database is this machine's own
// Postgres, where a home keeps its bundled one (so this machine can become the home).
// The programs are the Postgres this machine already has for Maktaba (Maktaba.app's,
// [postgres] home, MAKTABA_POSTGRES_HOME) when it is of the pinned major version, else
// the pinned build, fetched once. Nil for a machine that is not a full node, or a
// platform with neither.
func resolveNodeCopy(cfg *config.Config, wholeMachine bool) (*nodeCopy, error) {
	n := supervisor.NodeConfig()
	// MAKTABA_NODE_COPY=off: none (the tests that run a node without Postgres programs)
	if !wholeMachine || !supervisor.NodeMode(*cfg, n) || strings.EqualFold(os.Getenv("MAKTABA_NODE_COPY"), "off") {
		return nil, nil
	}
	port := cfg.Postgres.CopyPort
	if port <= 0 {
		port = cfg.Postgres.Port
	}
	if port <= 0 {
		port = pgbundle.DefaultPort
	}
	root := cfg.StateDir()
	c := nodecopy.Copy{NodeID: n.NodeID, HomeDSN: supervisor.HomeDSN(n),
		DB: pgbundle.Bundle{Data: filepath.Join(root, "postgres"), Keys: config.KeysDir(),
			Backups: filepath.Join(root, "backups"), Port: port}}
	nc := &nodeCopy{}
	exe, _ := os.Executable()
	if home := pgbundle.FindHome(*cfg, exe); home != "" && pgbin.Installed(home) && nodecopy.MajorOf(home) == pgbin.Major {
		c.DB.Home, nc.programs = home, nodecopy.Installed(home)
	} else if pin, ok := pgbin.Here(); ok {
		c.DB.Home = filepath.Join(copyPrograms(*cfg), pin.Dir())
		nc.programs = nodecopy.ProgramsFor(copyPrograms(*cfg), pgbin.HTTPFetch(&http.Client{Timeout: 30 * time.Minute}), os.Stderr)
	} else {
		fmt.Fprintln(os.Stderr, "[serve] no Postgres build for this platform: this machine keeps no copy of its home's database")
		return nil, nil
	}
	dsn, err := c.DSN()
	if err != nil {
		return nil, fmt.Errorf("this machine's copy of its home's database: %w", err)
	}
	cfg.NodeCopy = config.NodeCopy{DSN: dsn}
	nc.copy = c
	return nc, nil
}

// copyPrograms is where the copy's Postgres programs are kept when fetched.
func copyPrograms(cfg config.Config) string {
	return filepath.Join(cfg.StateDir(), "postgres-programs")
}

// migrateWith runs this machine's migrations on a database as the api role's binary
// does on a home (maktaba-api migrate up), in the api role's environment without node
// mode (a node never migrates the home's database; its own is another matter).
func migrateWith(api supervisor.Process) func(ctx context.Context, dsn string) error {
	return func(ctx context.Context, dsn string) error {
		if api.Cmd == nil {
			return fmt.Errorf("no api program to migrate with")
		}
		cmd := exec.CommandContext(ctx, api.Cmd.Path, "migrate", "--dsn", dsn, "up")
		for _, kv := range api.Cmd.Env {
			k, _, _ := strings.Cut(kv, "=")
			switch k {
			case "MAKTABA_NODE_MODE", "DATABASE_URL", "MAKTABA_DATABASE_URL":
				continue
			}
			cmd.Env = append(cmd.Env, kv)
		}
		cmd.Env = append(cmd.Env, "DATABASE_URL="+dsn)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(tail(out.String(), 600)))
		}
		return nil
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// runNodeCopy runs the copy's manager for as long as ctx: it makes this machine's
// database, subscribes it to the home's publication and keeps it (see nodecopy). The
// func it returns stops the manager (the copy's database runs on): a switch of the home
// takes the copy over.
func runNodeCopy(ctx context.Context, cfg config.Config, nc *nodeCopy, procs []supervisor.Process, sup *supervisor.Supervisor) func() {
	c := nc.copy
	for _, p := range procs {
		if p.Role == supervisor.RoleAPI {
			c.Migrate = migrateWith(p)
		}
	}
	m := &nodecopy.Manager{Copy: c, Programs: nc.programs,
		Want:     func(on bool) error { return sup.Want(supervisor.RoleCopy, "", on) },
		Running:  func() bool { return sup.Running(supervisor.RoleCopy, "") },
		StateDir: supervisor.NodeStateDir(cfg),
		Log:      os.Stderr,
	}
	mctx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { m.Run(mctx); close(done) }()
	return func() { stop(); <-done }
}

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
// before they are built, and the copy's process is one of the supervised ones; its
// manager starts with the supervisor (runNodeCopy). The programs are the Postgres this
// machine already has for Maktaba (Maktaba.app's, [postgres] home,
// MAKTABA_POSTGRES_HOME) when it is of the pinned major version, else the pinned build,
// fetched once. Nil for a machine that is not a full node, or a platform with neither.
func resolveNodeCopy(cfg *config.Config, wholeMachine bool) (*nodeCopy, error) {
	n := supervisor.NodeConfig()
	if !wholeMachine || !supervisor.NodeMode(*cfg, n) {
		return nil, nil
	}
	c := nodecopy.Copy{NodeID: n.NodeID, HomeDSN: n.DatabaseURL, Dir: filepath.Join(cfg.StateDir(), "copy"),
		Keys: config.KeysDir(), Port: cfg.Postgres.CopyPort}
	nc := &nodeCopy{}
	exe, _ := os.Executable()
	if home := pgbundle.FindHome(*cfg, exe); home != "" && pgbin.Installed(home) && nodecopy.MajorOf(home) == pgbin.Major {
		c.Bin, nc.programs = filepath.Join(home, "bin"), nodecopy.Installed(home)
	} else if pin, ok := pgbin.Here(); ok {
		c.Bin = filepath.Join(copyPrograms(*cfg), pin.Dir(), "bin")
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

// copyPrograms is where the copy's Postgres programs are kept.
func copyPrograms(cfg config.Config) string {
	return filepath.Join(cfg.StateDir(), "postgres-programs")
}

// runNodeCopy runs the copy's manager for as long as ctx: it fetches the programs,
// copies the home's database and keeps the copy (see nodecopy).
func runNodeCopy(ctx context.Context, cfg config.Config, nc *nodeCopy, sup *supervisor.Supervisor) {
	m := &nodecopy.Manager{Copy: nc.copy, Major: pgbin.Major, Programs: nc.programs,
		Want:     func(on bool) error { return sup.Want(supervisor.RoleCopy, "", on) },
		Running:  func() bool { return sup.Running(supervisor.RoleCopy, "") },
		StateDir: supervisor.NodeStateDir(cfg),
		Log:      os.Stderr,
	}
	go m.Run(ctx)
}

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
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// nodeCopy resolves a full node's copy of its home's database (Epic 37, nodecopy) when
// serve runs the whole machine: its address goes into the environment of the api and
// streaming roles (cfg.NodeCopy) before they are built, and the copy's process is one of
// the supervised ones; its manager starts with the supervisor (runNodeCopy). Nil for a
// machine that is not a full node, or a platform with no Postgres build.
func nodeCopy(cfg *config.Config, wholeMachine bool) (*nodecopy.Copy, error) {
	n := supervisor.NodeConfig()
	if !wholeMachine || !supervisor.NodeMode(*cfg, n) {
		return nil, nil
	}
	pin, ok := pgbin.Here()
	if !ok {
		fmt.Fprintln(os.Stderr, "[serve] no Postgres build for this platform: this machine keeps no copy of its home's database")
		return nil, nil
	}
	c := &nodecopy.Copy{NodeID: n.NodeID, HomeDSN: n.DatabaseURL, Dir: filepath.Join(cfg.StateDir(), "copy"),
		Keys: config.KeysDir(), Port: cfg.Postgres.CopyPort, Bin: filepath.Join(copyPrograms(*cfg), pin.Dir(), "bin")}
	dsn, err := c.DSN()
	if err != nil {
		return nil, fmt.Errorf("this machine's copy of its home's database: %w", err)
	}
	cfg.NodeCopy = config.NodeCopy{DSN: dsn}
	return c, nil
}

// copyPrograms is where the copy's Postgres programs are kept.
func copyPrograms(cfg config.Config) string {
	return filepath.Join(cfg.StateDir(), "postgres-programs")
}

// runNodeCopy runs the copy's manager for as long as ctx: it fetches the programs,
// copies the home's database and keeps the copy (see nodecopy).
func runNodeCopy(ctx context.Context, cfg config.Config, c nodecopy.Copy, sup *supervisor.Supervisor) {
	m := &nodecopy.Manager{Copy: c, Major: pgbin.Major,
		Programs: nodecopy.ProgramsFor(copyPrograms(cfg), pgbin.HTTPFetch(&http.Client{Timeout: 30 * time.Minute}), os.Stderr),
		Want:     func(on bool) error { return sup.Want(supervisor.RoleCopy, "", on) },
		Running:  func() bool { return sup.Running(supervisor.RoleCopy, "") },
		StateDir: supervisor.NodeStateDir(cfg),
		Log:      os.Stderr,
	}
	go m.Run(ctx)
}

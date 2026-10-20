package main

import (
	"context"
	"os"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// backupCheckInterval is how often serve checks whether the nightly dump is due.
const backupCheckInterval = 15 * time.Minute

// bundledPostgres resolves the Postgres bundled with Maktaba.app (Epic 36 story
// 36.1). When this install uses it, cfg's database URL becomes the bundled one
// (every child gets it). With prepare (serve running the whole machine) the
// cluster is created or finished first and the bundle is returned for the
// postgres role; otherwise (a single role, migrate, adduser) only the URL of an
// existing cluster is used. A machine that joined a home uses the home's database.
func bundledPostgres(ctx context.Context, cfg *config.Config, prepare bool) (*pgbundle.Bundle, error) {
	if supervisor.NodeConfig().Joined() {
		return nil, nil
	}
	exe, _ := os.Executable()
	b, ok, err := pgbundle.Resolve(*cfg, exe)
	if err != nil || !ok {
		return nil, err
	}
	if prepare {
		if err := b.Prepare(ctx, os.Stderr); err != nil {
			return nil, err
		}
	} else if !b.Initialized() {
		return nil, nil
	}
	url, err := b.URL()
	if err != nil {
		return nil, err
	}
	cfg.Database.URL = url
	if !prepare {
		return nil, nil
	}
	return &b, nil
}

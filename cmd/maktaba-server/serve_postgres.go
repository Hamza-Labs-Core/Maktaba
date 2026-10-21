package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbin"
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
// existing cluster is used, and none yet is an error. A machine that joined a home uses the home's database.
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
		// a machine's replication role may make a slot with any output plugin this
		// Postgres has, and one other than pgoutput decodes every table past what is
		// published to it: Maktaba's own builds have none, the home drops such a slot at
		// once (the API's guard), and an install of another make is named here
		if found, err := pgbin.OutputPlugins(b.Home); err == nil && len(found) > 0 && b.LAN {
			fmt.Fprintf(os.Stderr, "[postgres] warning: %s has logical decoding output plugins other than pgoutput "+
				"(%s): remove them; until then this home drops any slot made with one\n", b.Home, strings.Join(found, ", "))
		}
	} else if !b.Initialized() {
		// never fall back to SQLite here: the next full serve would then keep it
		return nil, errors.New("the bundled Postgres has no database yet: start Maktaba (`maktaba-server serve`) once first")
	}
	url, err := b.URL()
	if err != nil {
		return nil, err
	}
	cfg.Database.URL = url
	// the machines reach it on its own port (over the LAN, or on this machine with
	// loopback only), verifying its CA
	cfg.ClusterDB = config.ClusterDB{Port: b.Port}
	if b.TLS {
		cfg.ClusterDB.CA = b.CACert()
	}
	if !prepare {
		return nil, nil
	}
	return &b, nil
}

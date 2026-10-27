package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/dsn"
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
	// a forced home runs its own, whatever node.json holds; one that cannot be read
	// starts none (it may be a node's: never a home's cluster made beside it)
	n, err := supervisor.NodeOf(*cfg)
	if err != nil {
		return nil, err
	}
	if n.Joined() {
		return nil, nil
	}
	// a data folder on a disk that is not connected: never a new cluster in its place
	// (Epic 36 story 36.15; a whole machine's serve waits for it first, storageGate)
	if path, missing := cfg.StorageMissing(); missing {
		return nil, fmt.Errorf("the disk that holds Maktaba's data (%s) is not connected: connect it and start "+
			"Maktaba again (no new database is made in its place)", path)
	}
	exe, _ := os.Executable()
	b, ok, err := pgbundle.Resolve(*cfg, exe)
	if err != nil {
		return nil, err
	}
	if !ok {
		if prepare {
			warnOperatorPostgres(os.Stderr, *cfg)
		}
		return nil, nil
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
				"(%s): a machine's replication role can read every table through one, past what is published to it. "+
				"Remove them (Maktaba's own builds have none); until then this home drops any slot made with one "+
				"within 5 s\n", b.Home, strings.Join(found, ", "))
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

// warnOperatorPostgres tells, at each start of a home whose database is a Postgres of its
// own ([database] url, not the one Maktaba installs), what keeping copies of it for the
// machines would expose: such a server may have output plugins other than pgoutput, through
// which a machine's replication role reads every table, and only the home's sweep (the
// API's guard) drops a slot made with one. So the home keeps no copies there unless the
// admin turns on rules.cluster.copies_on_operator_postgres.
func warnOperatorPostgres(w io.Writer, cfg config.Config) {
	if info, err := dsn.Classify(cfg.Database.URL); err != nil || info.Backend != dsn.Postgres {
		return
	}
	fmt.Fprintln(w, "[postgres] warning: this home's database is a Postgres of its own, not the one Maktaba installs: "+
		"its machines keep no copy of it unless an admin turns on rules.cluster.copies_on_operator_postgres "+
		"(Settings, Rules, Machines)")
	fmt.Fprintln(w, "[postgres] warning: with that rule on, a machine's replication role may make a slot with any "+
		"logical decoding output plugin that server has besides pgoutput (test_decoding, wal2json and the like) and "+
		"read every table through it, past what is published to that machine; only this home drops such a slot, within "+
		"5 s, and only while its database user may end and drop it (pg_signal_backend, REPLICATION). Remove those "+
		"plugins from that server before turning the rule on")
}

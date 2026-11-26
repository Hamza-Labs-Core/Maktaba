package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/dsn"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbin"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
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
		if err := postgresPortCheck(b, *cfg, os.Stderr); err != nil {
			return nil, err
		}
		b = majorUpgradeStep(ctx, *cfg, b, os.Stderr)
		if err := b.Prepare(ctx, os.Stderr); err != nil {
			return nil, err
		}
		// the extensions to the new programs' versions, the statistics pg_upgrade did not
		// carry: never a reason not to start (pgbundle.FinishUpgrade), tried again at each
		// start until it is done
		if err := b.FinishUpgrade(ctx, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "[postgres] level=error the upgraded library database's last step failed, so its "+
				"extensions stay at their versions from PostgreSQL 16 and some of its planner statistics are missing "+
				"(it runs; the next start tries again): %v\n", err)
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
	} else if !b.HasDatabase() {
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

// pgPortFree reports whether the bundled Postgres's address can be bound (tests replace it).
var pgPortFree = config.CanBind

// postgresPortCheck (Epic 59 story 59.08): the bundled Postgres's port held by another
// program, before anything starts on it. The log says so in both languages, naming the
// holder and the setting that changes the port. In a container, or when MAKTABA_POSTGRES_PORT
// set the port, the start stops there (the household chose the port, or nothing outside the
// container's settings can change it); elsewhere the supervisor's own check holds the role
// until the port is free (port_taken). A cluster with a postmaster.pid is left to the
// supervisor too: an earlier Postgres of this home may still be exiting there, and the
// stale-lock step ends it.
func postgresPortCheck(b pgbundle.Bundle, cfg config.Config, out io.Writer) error {
	if _, err := os.Stat(filepath.Join(b.Data, "postmaster.pid")); err == nil {
		return nil
	}
	if pgPortFree(b.Addr()) {
		return nil
	}
	setting := config.EnvPostgresPort
	pinned := cfg.PinnedBy(config.PinPostgres) != ""
	if !pinned {
		setting += " / server.toml [postgres] port"
	}
	en, ar := portSettingTakenText(b.Port, portHolder(b.Port), setting)
	fmt.Fprintf(out, "[postgres] %s\n[postgres] %s\n", en, ar)
	if pinned || config.Container() != "" {
		return errors.New(en + "\n" + ar)
	}
	return nil
}

// majorUpgradeStep (Epic 49 story 49.2): a payload that runs a newer PostgreSQL major
// (postgres-18/) than the cluster this machine has (<state>/postgres, made by 16) upgrades
// it into a directory of its own with pg_upgrade (pgbundle.MajorUpgrade), leaving the old
// cluster as it was for a switch back to the release before. The upgraded cluster has its
// own database record, seeded from the old one's when it is made. When the upgrade cannot
// run (no room for a copy, a failed pg_upgrade), the previous major runs on its cluster as
// before, held until a start upgrades it (pgbundle.HoldMajor): the home keeps working.
func majorUpgradeStep(ctx context.Context, cfg config.Config, b pgbundle.Bundle, log io.Writer) pgbundle.Bundle {
	root := cfg.StateDir()
	old := pgbundle.PreviousHome(b.Home)
	if old == "" {
		// the payload's previous major runs (held), or the payload has only one
		if held := pgbundle.HeldMajor(root); held != "" && pgbundle.HomeMajor(b.Home) != "" {
			if exe, _ := os.Executable(); exe != "" {
				if nb, ok := retryHeldUpgrade(ctx, cfg, b, exe, held, log); ok {
					return nb
				}
			}
			return b
		}
		return b
	}
	return upgradeTo(ctx, cfg, b, old, log)
}

// retryHeldUpgrade tries a held upgrade again at this start: b runs the previous major.
func retryHeldUpgrade(ctx context.Context, cfg config.Config, b pgbundle.Bundle, exe, held string, log io.Writer) (pgbundle.Bundle, bool) {
	root := cfg.StateDir()
	if err := pgbundle.ReleaseMajor(root); err != nil {
		return b, false
	}
	nb, ok, err := pgbundle.Resolve(cfg, exe)
	if err != nil || !ok || pgbundle.PreviousHome(nb.Home) == "" {
		_ = pgbundle.HoldMajor(root, held)
		return b, false
	}
	fmt.Fprintf(log, "[postgres] the library database's upgrade was held (%s): trying it again\n", held)
	got := upgradeTo(ctx, cfg, nb, pgbundle.PreviousHome(nb.Home), log)
	return got, true
}

// upgradeTo upgrades the cluster old's major made into b's, or holds it and runs old.
func upgradeTo(ctx context.Context, cfg config.Config, b pgbundle.Bundle, old string, log io.Writer) pgbundle.Bundle {
	root := cfg.StateDir()
	newMajor, oldMajor := pgbundle.HomeMajor(b.Home), pgbundle.HomeMajor(old)
	ob := b
	ob.Home, ob.Data = old, pgbundle.DataDirFor(root, oldMajor)
	u := pgbundle.MajorUpgrade{Old: ob, New: b}
	need, why, err := u.Needed(ctx)
	if err == nil && need {
		err = u.Run(ctx, log)
	} else if err == nil {
		fmt.Fprintf(log, "[postgres] the library database runs on PostgreSQL %s (%s)\n", newMajor, why)
	}
	if err == nil {
		// Seed and reconcile the new cluster's record ONLY when the upgrade wrote
		// RecordSeedPending and the source cluster still matches UpgradeMarker (fail closed
		// on error). Legacy clusters upgraded before this marker existed have no pending
		// file, so their live database record is never touched.
		err = seedClusterRecordIfPending(ctx, root, ob, b, oldMajor, newMajor, log)
	}
	if err != nil {
		fmt.Fprintf(log, "[postgres] level=error the library database could not be upgraded to PostgreSQL %s, so it "+
			"stays on PostgreSQL %s for now (search works as before; the next start tries again): %v\n",
			newMajor, oldMajor, err)
		if herr := pgbundle.HoldMajor(root, err.Error()); herr != nil {
			fmt.Fprintf(log, "[postgres] level=error the hold cannot be recorded: %v\n", herr)
		}
		return ob
	}
	_ = pgbundle.ReleaseMajor(root)
	return b
}

// seedClusterRecordIfPending copies and reconciles the old cluster's database record into
// b's major record ONLY when pgbundle.RecordSeedPending is present (written by MajorUpgrade
// before finalizing UpgradeMarker) AND the old cluster directory still holds the exact
// cluster (SystemID and Checkpoint) named by UpgradeMarker. Legacy PG 18 homes upgraded by
// earlier releases have no pending marker and keep their live record untouched; if the old
// cluster was replaced or moved on before seeding ran, the pending marker is cleared without
// touching PG 18's record. Any I/O or control-read failure fails closed so the pending marker
// stays for the next start and the upgrade does not proceed unseeded.
func seedClusterRecordIfPending(ctx context.Context, root string, ob, b pgbundle.Bundle, oldMajor, newMajor string, log io.Writer) error {
	pendingPath := filepath.Join(b.Data, pgbundle.RecordSeedPending)
	if _, err := os.Stat(pendingPath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("check pending cluster record seed %s: %w", pendingPath, err)
	}
	m, ok, err := pgbundle.ReadUpgradedFrom(b.Data)
	if err != nil {
		return fmt.Errorf("read upgrade marker for pending record seed: %w", err)
	}
	if !ok || m.Checkpoint == "" {
		return fmt.Errorf("pending record seed marker present in %s without completed %s", b.Data, pgbundle.UpgradeMarker)
	}
	pgVer := filepath.Join(ob.Data, "PG_VERSION")
	if _, err := os.Stat(pgVer); errors.Is(err, os.ErrNotExist) {
		// Only treat the source cluster as gone when PG_VERSION is genuinely absent (and
		// ob.Data itself is missing or a directory, never a stat/permission error or file).
		if di, derr := os.Stat(ob.Data); derr != nil && !errors.Is(derr, os.ErrNotExist) {
			return fmt.Errorf("stat source cluster %s for pending record seed: %w", ob.Data, derr)
		} else if derr == nil && !di.IsDir() {
			return fmt.Errorf("source cluster path %s is not a directory", ob.Data)
		}
		fmt.Fprintf(log, "[postgres] level=warn old cluster %s is gone before record seeding: leaving PostgreSQL %s's record as it is\n", ob.Data, newMajor)
		if err := os.Remove(pendingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clear pending cluster record seed %s: %w", pendingPath, err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("stat source cluster %s for pending record seed: %w", pgVer, err)
	}
	oc, oerr := pgbundle.ReadControl(ctx, ob.Bin("pg_controldata"), ob.Data)
	if oerr != nil {
		return fmt.Errorf("read source cluster identity for pending record seed: %w", oerr)
	}
	if oc.SystemID != m.SystemID || oc.Checkpoint != m.Checkpoint {
		fmt.Fprintf(log, "[postgres] level=warn old cluster %s no longer matches upgrade marker (%s@%s vs %s@%s): leaving PostgreSQL %s's record as it is\n",
			ob.Data, oc.SystemID, oc.Checkpoint, m.SystemID, m.Checkpoint, newMajor)
		if err := os.Remove(pendingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clear pending cluster record seed %s: %w", pendingPath, err)
		}
		return nil
	}
	nc, cerr := pgbundle.ReadControl(ctx, b.Bin("pg_controldata"), b.Data)
	if cerr != nil {
		return fmt.Errorf("read upgraded cluster identity for pending record seed: %w", cerr)
	}
	if err := seedClusterRecord(root, oldMajor, newMajor, m.Identity, m.Table, nc.SystemID, log); err != nil {
		return err
	}
	if err := os.Remove(pendingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear pending cluster record seed %s: %w", pendingPath, err)
	}
	return nil
}

// seedClusterRecord copies the old cluster's database record to the new one's. When a
// pending restore recorded untouched data matching the verified old cluster's full identity
// (oldID = <sys>/<db_oid>/<public_ns_oid> and oldTable = goose_db_version OID), its old
// DataID/DataTable are cleared so the upgraded cluster (newSys, with a new system
// identifier) adopts its own live identity on the next settleData probe instead of falsely
// classifying untouched data as replaced.
func seedClusterRecord(root, oldMajor, newMajor, oldID, oldTable, newSys string, log io.Writer) error {
	dir := filepath.Join(root, "state")
	from, to := filepath.Join(dir, apppayload.ClusterRecordName(oldMajor)), filepath.Join(dir, apppayload.ClusterRecordName(newMajor))
	raw, err := os.ReadFile(from)
	if errors.Is(err, os.ErrNotExist) {
		if rerr := os.Remove(to); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return rerr
		}
		return nil
	}
	if err == nil {
		raw = reconcileUpgradedRecord(raw, oldID, oldTable, newSys, log)
		err = layout.WriteFileAtomic(to, raw, 0o644)
	}
	if err != nil {
		fmt.Fprintf(log, "[postgres] level=warn the upgraded library database's record is not seeded (%v): "+
			"this start takes it as an update from the release that ran last\n", err)
		return err
	}
	return nil
}

func reconcileUpgradedRecord(raw []byte, oldID, oldTable, newSys string, log io.Writer) []byte {
	if oldID == "" || newSys == "" {
		return raw
	}
	oldSys, oldRest, ok := strings.Cut(oldID, "/")
	if !ok || oldSys == "" || oldRest == "" || oldSys == newSys {
		return raw
	}
	var rec apppayload.Database
	if err := json.Unmarshal(raw, &rec); err != nil {
		return raw
	}
	if !asked(rec) || rec.Data != apppayload.DataUntouched || rec.DataID == "" || strings.HasSuffix(rec.DataID, insideMark) {
		return raw
	}
	// Verify the full old identity (cluster system ID, database OID, public schema OID,
	// and migration-table OID when recorded), never system ID alone.
	if rec.DataID != oldID {
		return raw
	}
	if rec.DataTable != "" && (oldTable == "" || rec.DataTable != oldTable) {
		return raw
	}
	rec.DataID, rec.DataTable = "", ""
	updated, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return raw
	}
	fmt.Fprintf(log, "[postgres] reconciled pending untouched restore identity across cluster upgrade (%s -> %s)\n", oldSys, newSys)
	return append(updated, '\n')
}

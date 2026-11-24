//go:build unix

package main

// Scenario 19 of the update rehearsal (docs/update-rehearsal.md): an update whose migration
// builds indexes CONCURRENTLY while a joined machine's copy holds an old snapshot.
//
// The outages of 2026-11-14 (v0.3.140, 0220_age_tiers.sql) and 2026-11-23 (v0.3.158,
// 0235_search_terms.sql) on the owner's home: a machine's copy kept falling too far behind
// and making itself anew, and its table sync's walsender held a REPEATABLE READ snapshot
// for the whole COPY of each large table (23 to 36 minutes, seen on the home). A CREATE
// INDEX CONCURRENTLY waits for every transaction older than it, and that wait is a lock
// wait: the migrations' 2-minute lock timeout cancelled it, three tries running (the
// database step ends the other client sessions before each, and leaves replication alone),
// and the update put the whole 35 GB dump back, 2 h 44 min with the library stopped.
//
// Here the small seed's home on the previous release; a copy's replication role whose
// walsender holds a REPEATABLE READ snapshot, connecting again whenever it is ended or the
// home's Postgres restarts, as the machine's table sync does; a client session that does
// the same (a machine's api session coming straight back); the update to this commit, with
// a NO TRANSACTION migration that builds two indexes CONCURRENTLY (a new table's and one on
// videos), its backup taken while the previous release serves. The update must commit with
// no restore, both indexes valid, and the log naming the copy's walsender it ended. On the
// code from before the fix (rehearsed with cur_ref = main on 2026-11-23) the lock timeout
// hit and the next tries took the INVALID indexes for done: the update committed with both
// indexes INVALID after 4 min 11 s of downtime, as 0232 was recorded on the owner's home.
//
// Since research item 5 (2026-11-24) the database step also waits for the machines' table
// syncs (a slot named as one, made on the home before the update and dropped while the
// step waits), pauses the copies for the migrations (the copy's role, a member of
// maktaba_replicas, may not log in and its walsender is ended), runs the index builds
// plainly with the library stopped (no wait on the old snapshots at all), and lets the copy
// log in again after: the copy's role can log in once the update is done.

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// cicMigration is 0235's shape: NO TRANSACTION, a new table and an index on it, and an
// index on a table every home has, each built CONCURRENTLY IF NOT EXISTS.
func cicMigration(slot int) (string, string) {
	return fmt.Sprintf("%04d_rehearsal_cic.sql", slot), `-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS rehearsal_cic (id bigint, term text);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS rehearsal_cic_term ON rehearsal_cic (term text_pattern_ops);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS rehearsal_cic_videos ON videos (created_at, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS rehearsal_cic_videos;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS rehearsal_cic;
-- +goose StatementEnd
`
}

// curCICSpec is this commit as the next release, with the CONCURRENTLY migration.
func (k *rehearsalKit) curCICSpec() payloadSpec {
	n, body := cicMigration(k.rev + 1)
	return payloadSpec{variant: "cur", version: k.cur, extra: map[string]string{n: body}}
}

// snapshotHolder is a session that holds a REPEATABLE READ snapshot on the home's library
// database, and connects again a second after it is ended or the server goes away.
type snapshotHolder struct {
	stop  chan struct{}
	done  chan struct{}
	held  atomic.Int64 // snapshots taken
	ended atomic.Int64 // sessions ended by the server's administrator command
	mu    sync.Mutex
	last  string
}

// holdSnapshots starts a holder as role: a walsender (replication=database, as a copy's
// table sync connects) when replication, else a client session.
func (m *appMachine) holdSnapshots(role, pw string, replication bool) *snapshotHolder {
	h := &snapshotHolder{stop: make(chan struct{}), done: make(chan struct{})}
	conn := fmt.Sprintf("host=127.0.0.1 port=%d user=%s dbname=maktaba application_name=%s connect_timeout=5",
		m.pg, role, role)
	if replication {
		conn += " replication=database"
	}
	psql := filepath.Join(m.r.kit.pgHome, "bin", "psql")
	go func() {
		defer close(h.done)
		for {
			select {
			case <-h.stop:
				return
			default:
			}
			var out bytes.Buffer
			cmd := exec.Command(psql, "-X", "-At", "-v", "ON_ERROR_STOP=1", conn)
			cmd.Env = append(os.Environ(), "PGPASSWORD="+pw)
			cmd.Stdin = strings.NewReader("BEGIN ISOLATION LEVEL REPEATABLE READ;\nSELECT 'held', count(*) FROM videos;\n" +
				"SELECT pg_sleep(1800);\n")
			cmd.Stdout, cmd.Stderr = &out, &out
			if err := cmd.Start(); err == nil {
				exited := make(chan struct{})
				go func() { _ = cmd.Wait(); close(exited) }()
				select {
				case <-h.stop:
					_ = cmd.Process.Kill()
					<-exited
					return
				case <-exited:
				}
			}
			s := out.String()
			if strings.Contains(s, "held|") {
				h.held.Add(1)
			}
			if strings.Contains(s, "terminating connection due to administrator command") {
				h.ended.Add(1)
			}
			h.mu.Lock()
			h.last = s
			h.mu.Unlock()
			select {
			case <-h.stop:
				return
			case <-time.After(time.Second):
			}
		}
	}()
	return h
}

func (h *snapshotHolder) end() {
	select {
	case <-h.stop:
	default:
		close(h.stop)
	}
	<-h.done
}

func (h *snapshotHolder) String() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return fmt.Sprintf("%d snapshots held, %d ended (last: %q)", h.held.Load(), h.ended.Load(), strings.TrimSpace(h.last))
}

// =====================================================================================
// 19 A CREATE INDEX CONCURRENTLY migration behind a machine's copy.
// =====================================================================================

func TestUpdateRehearsal19ConcurrentIndexBehindACopy(t *testing.T) {
	r := newRehearsal(t)
	k := r.kit
	home, _ := r.homeOnPrev(profile("small"))
	db := home.superDB("maktaba")
	const copyRole, nodeRole, pw = "maktaba_rep_rehearsal19", "maktaba_node_rehearsal19", "rehearsal-19-pw"
	for _, q := range []string{
		`DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'maktaba_replicas') THEN
		    CREATE ROLE maktaba_replicas NOLOGIN; END IF; END $$`,
		`CREATE ROLE ` + copyRole + ` LOGIN REPLICATION PASSWORD '` + pw + `' IN ROLE maktaba_replicas`,
		`CREATE ROLE ` + nodeRole + ` LOGIN PASSWORD '` + pw + `'`,
		`GRANT CONNECT ON DATABASE maktaba TO ` + copyRole + `, ` + nodeRole,
		`GRANT SELECT ON videos TO ` + copyRole + `, ` + nodeRole,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	copyHolder := home.holdSnapshots(copyRole, pw, true)
	nodeHolder := home.holdSnapshots(nodeRole, pw, false)
	defer copyHolder.end()
	defer nodeHolder.end()
	waitFor(t, "the copy's walsender holding a snapshot", 2*time.Minute, func() bool {
		return queryInt(t, db, `SELECT count(*) FROM pg_stat_activity WHERE backend_type = 'walsender'
		    AND usename = $1 AND backend_xmin IS NOT NULL`, copyRole) > 0
	})

	// a machine's table sync, as the home sees it: its slot (pg_<sub>_sync_<table>_<system>),
	// dropped once the database step says it waits for it
	const syncSlot = "pg_16999_sync_16384_7300000000000000001"
	if _, err := db.Exec(`SELECT pg_create_logical_replication_slot($1, 'pgoutput')`, syncSlot); err != nil {
		t.Fatalf("the table sync's slot: %v", err)
	}
	syncDone := make(chan struct{})
	go func() {
		defer close(syncDone)
		deadline := time.Now().Add(30 * time.Minute)
		for time.Now().Before(deadline) && !strings.Contains(home.log.String(), "table syncs run (a machine copying") {
			time.Sleep(time.Second)
		}
		time.Sleep(10 * time.Second) // the step waits on it a while
		if _, err := db.Exec(`SELECT pg_drop_replication_slot($1)`, syncSlot); err != nil {
			r.logf("dropping the table sync's slot: %v", err)
		}
	}()

	run, mon, states := r.update(home, k.curCICSpec(), 40*time.Minute)
	<-syncDone
	if run.State != "done" {
		t.Fatalf("the update to %s ended %s: %s (steps %+v)\ncopy: %s\nnode: %s", k.cur, run.State, run.Error,
			run.Steps, copyHolder, nodeHolder)
	}
	home.waitServing(k.cur, 3*time.Minute)
	r.budget(mon, "an update whose CREATE INDEX CONCURRENTLY waits on a machine's copy", 4*time.Minute)
	home.assertServing(k.cur)
	r.noRestore(home, states)
	r.logf("copy: %s; node: %s", copyHolder, nodeHolder)

	for _, idx := range []string{"rehearsal_cic_term", "rehearsal_cic_videos"} {
		var valid sql.NullBool
		if err := db.QueryRow(`SELECT indisvalid FROM pg_index WHERE indexrelid = to_regclass($1)`, idx).Scan(&valid); err != nil {
			t.Fatalf("%s: %v", idx, err)
		}
		if !valid.Valid || !valid.Bool {
			t.Errorf("%s is not a valid index after the update", idx)
		}
	}
	if n := queryInt(t, db, `SELECT count(*) FROM pg_index WHERE NOT indisvalid`); n != 0 {
		t.Errorf("%d INVALID indexes left", n)
	}
	if n := queryInt(t, db, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`); n != int64(k.rev+1) {
		t.Errorf("the schema is at %d, want %d", n, k.rev+1)
	}
	log := home.log.String()
	for _, want := range []string{
		"table syncs run (a machine copying the database): the migrations wait",
		"the machines' table syncs ended",
		"index builds run plainly, not CONCURRENTLY",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q", want)
		}
	}
	// the seed's own machine has a replication role too: the copy's is one of those named
	for _, said := range []string{"the machines' copies are paused for the migrations (",
		"the machines' copies may log in again ("} {
		if line := lineWith(log, said); !strings.Contains(line, copyRole) {
			t.Errorf("the log does not name %s in %q (line %q)", copyRole, said, line)
		}
	}
	if strings.Contains(log, "waited too long for a lock another session holds") {
		t.Errorf("a migration still reached its lock timeout")
	}
	if copyHolder.ended.Load() == 0 {
		t.Errorf("the copy's walsender was never ended: %s", copyHolder)
	}
	// the copy logs in again once the migrations are done
	var canLogin bool
	if err := db.QueryRow(`SELECT rolcanlogin FROM pg_roles WHERE rolname = $1`, copyRole).Scan(&canLogin); err != nil || !canLogin {
		t.Errorf("the copy's role may not log in after the update (%v)", err)
	}
	waitFor(t, "the copy's walsender back", 2*time.Minute, func() bool {
		return queryInt(t, db, `SELECT count(*) FROM pg_stat_activity WHERE backend_type = 'walsender'
		    AND usename = $1`, copyRole) > 0
	})
	if _, err := os.Stat(filepath.Join(home.root, "state", "copies-paused.json")); err == nil {
		t.Error("the paused copies' record is left after the update")
	}
	if rec, _ := home.record(); rec.State != "migrated" || rec.Version != k.cur {
		t.Errorf("the database's record is %+v, want migrated for %s", rec, k.cur)
	}
}

// lineWith is the first line of log that holds s ("" when none).
func lineWith(log, s string) string {
	for _, l := range strings.Split(log, "\n") {
		if strings.Contains(l, s) {
			return l
		}
	}
	return ""
}

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/updater"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// exitsRig is a home whose restore cannot finish: its database step (any release of it) and
// its restorer, over one record and one fake database.
type exitsRig struct {
	*restoreRig
	t     *testing.T
	f     *fakeDB
	log   *syncBuf
	dir   string // the state dir
	start func(version string) error
}

const firstBackup, otherBackup = "pre-v0.2.0-to-v0.3.0.dump", "pre-v0.2.1-to-v0.3.0.dump"

func newExitsRig(t *testing.T) *exitsRig {
	t.Helper()
	t.Setenv(envLauncherRoot, "")
	cfg, _ := appEnv(t, "v0.3.0", "")
	prevRoom := makeRoom
	t.Cleanup(func() { makeRoom = prevRoom })
	makeRoom = func(string, pgbundle.RoomPolicy) error { return nil }
	prevKeeping, prevCheck := makeRoomKeeping, roomKeeping
	t.Cleanup(func() { makeRoomKeeping, roomKeeping = prevKeeping, prevCheck })
	makeRoomKeeping = func(string, pgbundle.RoomPolicy, ...string) error { return nil }
	roomKeeping = makeRoomKeeping
	rig := newRestoreRig(t, map[string]int64{firstBackup: 140, otherBackup: 140})
	e := &exitsRig{restoreRig: rig, t: t, f: installFakeDB(t, cfg), log: &syncBuf{}, dir: cfg.StateDir()}
	rig.r.stateDir = e.dir
	b := *testBundle(cfg)
	b.Backups = rig.backups
	rig.r.identity = func(ctx context.Context) (string, int64, error) { return dataIdentity(ctx, nil) }
	e.start = func(version string) error {
		g := &migrationGate{stateDir: e.dir, version: version, pg: &b, api: exec.Command("maktaba-api"), keep: 3, log: e.log}
		return g.run(context.Background(), noReport)
	}
	return e
}

func (e *exitsRig) rec() apppayload.Database {
	e.t.Helper()
	rec, ok, err := apppayload.ReadDatabase(e.dir)
	if err != nil || !ok {
		e.t.Fatalf("record: %v %v", ok, err)
	}
	return rec
}

func (e *exitsRig) write(rec apppayload.Database) {
	e.t.Helper()
	if err := apppayload.WriteDatabase(e.dir, rec); err != nil {
		e.t.Fatal(err)
	}
}

// asked is the record of firstBackup's restore for v0.2.0, over a home on v0.3.0.
func (e *exitsRig) asked() apppayload.Database {
	return apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseRestoring, From: "v0.2.0", By: "admin",
		Dump: filepath.Join(e.backups, firstBackup), Data: apppayload.DataUntouched, DataID: "1/2/3",
		Before: &apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseHeld, From: "v0.2.0", Failed: "v0.3.0",
			Dump: filepath.Join(e.backups, firstBackup), SchemaBefore: 140}}
}

// fresh is another process of the home (its lock is its own).
func (e *exitsRig) fresh() { restoreGuards.Delete(filepath.Clean(e.dir)) }

func haltCode(t *testing.T, err error) string {
	t.Helper()
	if !supervisor.Halted(err) {
		t.Fatalf("not a halt: %v", err)
	}
	return supervisor.DetailOf(err).Code
}

// Every way out, for every state of the data: the record alone says.
func TestWaysOutByTheData(t *testing.T) {
	held := &apppayload.Database{Version: "0.3.0", State: apppayload.DatabaseHeld, Failed: "0.3.0"}
	ran := &apppayload.Database{Version: "0.3.0", State: apppayload.DatabaseMigrated}
	restored := &apppayload.Database{Version: "0.2.0", State: apppayload.DatabaseRestored}
	for _, data := range []string{apppayload.DataUntouched, apppayload.DataReplaced, apppayload.DataGone, apppayload.DataUnknown} {
		ok := data == apppayload.DataUntouched
		for _, c := range []struct {
			name, from, tried, running string
			before                     *apppayload.Database
			giveUp, behind             bool
			release                    string // what giving up starts ("": the running one, or a held one)
		}{
			{"held, the target runs", "v0.2.0", "v0.2.0", "v0.2.0", held, true, false, ""},
			{"held, the target was restarted", "v0.2.0", "v0.3.0", "v0.2.0", held, true, false, ""},
			{"held, a newer release", "v0.2.0", "v0.2.0", "v0.3.1", held, true, true, ""},
			{"held, the newer release's own try failed", "v0.2.0", "v0.3.1", "v0.3.1", held, true, false, ""},
			{"migrated, the older target runs", "v0.2.0", "v0.2.0", "v0.2.0", ran, true, false, "0.3.0"},
			{"migrated, a newer release", "v0.2.0", "v0.2.0", "v0.3.1", ran, true, true, ""},
			{"restored, the newer target runs", "v0.3.0", "v0.3.0", "v0.3.0", restored, true, false, "0.2.0"},
			{"restored, the newer target, tried by another", "v0.3.0", "v0.2.0", "v0.3.0", restored, true, false, "0.2.0"},
			{"restored, a newer release", "v0.3.0", "v0.3.0", "v0.3.1", restored, true, true, ""},
			{"never tried, a newer release", "v0.2.0", "", "v0.3.1", held, true, false, ""},
			{"not recorded, the target runs", "v0.2.0", "v0.2.0", "v0.2.0", nil, false, false, ""},
			{"not recorded, the release that ran", "v0.2.0", "v0.3.0", "v0.3.0", nil, false, false, ""},
			{"not recorded, a newer release", "v0.2.0", "v0.2.0", "v0.4.0", nil, true, true, ""},
			{"not recorded, the newer release's own try failed", "v0.2.0", "v0.4.0", "v0.4.0", nil, true, false, ""},
			{"not recorded, a newer target", "v0.4.0", "v0.4.0", "v0.4.0", nil, false, false, ""},
		} {
			rec := apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseRestoring, By: "admin", From: c.from,
				TriedBy: c.tried, Before: c.before, Data: data}
			inst := &keepInstaller{running: c.running, kept: []updater.KeptRelease{{Version: "0.2.0"}, {Version: "0.3.0"}}}
			if got := (&restorer{inst: inst}).canGiveUp(rec); got != (c.giveUp && ok) {
				t.Errorf("%q, %s: give up %v, want %v", data, c.name, got, c.giveUp && ok)
			}
			if _, release, gave := giveUpTo(rec, c.running); gave && release != c.release {
				t.Errorf("%s: give up starts %q, want %q", c.name, release, c.release)
			}
			if _, got := leftBehind(rec, c.running); got != (c.behind && ok) {
				t.Errorf("%q, %s: goes on %v, want %v", data, c.name, got, c.behind && ok)
			}
		}
	}
}

// R1: a restart of the release the restore is for, or of any release whose own try failed,
// tries again; it never takes itself for a newer release.
func TestARestartTriesTheRestoreAgain(t *testing.T) {
	e := newExitsRig(t)
	rec := e.asked()
	rec.Version, rec.From = "v0.2.0", "v0.3.0"
	rec.Before = &apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseRestored}
	e.write(rec)
	e.f.restoreErr = errors.New("server closed the connection unexpectedly")
	for i := 1; i <= 2; i++ {
		if code := haltCode(t, e.start("v0.3.0")); code != "db_restore_backup_failed" {
			t.Fatalf("start %d: %s", i, code)
		}
		if d, m, r := e.f.counts(); d != 0 || m != 0 || r != i {
			t.Fatalf("start %d: %d dumps, %d migrations, %d restores", i, d, m, r)
		}
	}
	if got := e.rec(); got.State != apppayload.DatabaseRestoring || got.TriedBy != "v0.3.0" || got.Data != apppayload.DataUntouched {
		t.Fatalf("record %+v", got)
	}
	// a newer release installed since goes on from what was there before, with a backup of its own
	e.f.known, e.f.pending = true, 1
	if err := e.start("v0.3.1"); err != nil {
		t.Fatal(err)
	}
	if d, m, r := e.f.counts(); d != 1 || m != 1 || r != 2 {
		t.Fatalf("%d dumps, %d migrations, %d restores", d, m, r)
	}
	if got := e.rec(); got.Version != "v0.3.1" || got.State != apppayload.DatabaseMigrated {
		t.Fatalf("record %+v", got)
	}
	if pgbundle.BackupsHeld(e.backups) {
		t.Fatal("the backups are held though no restore waits")
	}
	// a hold a crash left goes at the next start
	if err := pgbundle.HoldBackups(e.backups); err != nil {
		t.Fatal(err)
	}
	if err := e.start("v0.3.1"); err != nil || pgbundle.BackupsHeld(e.backups) {
		t.Fatalf("a hold left behind: %v", err)
	}
}

// R2: one set-aside for a restore however often it is tried, and no backup goes meanwhile,
// whatever prunes.
func TestOneAsideAndNoBackupGoes(t *testing.T) {
	e := newExitsRig(t)
	for i := range 5 { // earlier restores' and updates' dumps
		for _, n := range []string{fmt.Sprintf("replaced-v0.1.%d-20261101T000000.dump", i), fmt.Sprintf("pre-v0.1.%d-to-v0.2.0.dump", i)} {
			if err := os.WriteFile(filepath.Join(e.backups, n), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	e.write(e.asked())
	e.f.restoreErr = errors.New("no space left on device")
	makeRoom = pgbundle.MakeRoomWith
	for range 4 {
		if code := haltCode(t, e.start("v0.2.0")); code != "db_restore_backup_failed" {
			t.Fatal(code)
		}
		if err := pgbundle.PruneBackups(e.backups, "", 1); err != nil {
			t.Fatal(err)
		}
		_ = pgbundle.MakeRoomWith(e.backups, pgbundle.RoomPolicy{ReserveShare: 0.99, MaxShare: 0.0000001}) // no room at all
	}
	if len(e.f.asides) > 1 {
		t.Fatalf("set aside %d times: %v", len(e.f.asides), e.f.asides)
	}
	if rec := e.rec(); len(e.f.asides) == 1 && filepath.Base(rec.Aside) != e.f.asides[0] {
		t.Fatalf("record's aside %q, set aside %v", rec.Aside, e.f.asides)
	}
	ents, err := os.ReadDir(e.backups)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ent := range ents {
		if strings.HasSuffix(ent.Name(), ".dump") {
			n++
		}
	}
	if want := 12 + len(e.f.asides); n != want {
		t.Fatalf("%d dumps left of %d", n, want)
	}
	// done: the rules apply again
	e.f.restoreErr = nil
	if err := e.start("v0.2.0"); err != nil {
		t.Fatal(err)
	}
	if pgbundle.BackupsHeld(e.backups) {
		t.Fatal("still held after the restore")
	}
}

// R3: a restore asked on a release that recorded nothing of it (v0.3.162's mark), that
// cannot finish: the next release sees from the database's schema that it is still the one
// from before, and every way out is there; when it cannot see that, another backup is.
func TestARestoreAskedOnAnOlderReleaseHasAWayOut(t *testing.T) {
	shipped := func(e *exitsRig) apppayload.Database {
		return apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseRestoring, From: "v0.2.0", By: "admin",
			Dump: filepath.Join(e.backups, firstBackup), Error: "restore asked by admin"}
	}
	// v0.2.0's kept migrations: the backup's schema, whatever became of the backup
	kept := filepath.Join(t.TempDir(), "migrations")
	if err := os.MkdirAll(kept, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kept, "0140_m.sql"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		schema int64
		data   string
		code   string
	}{
		{"another schema than the backup's", 149, apppayload.DataUntouched, "db_restore_backup_failed"},
		{"the backup's schema", 140, apppayload.DataUnknown, "db_restore_backup_part"},
		{"an empty database", 0, apppayload.DataGone, "db_restore_backup_gone"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newExitsRig(t)
			e.f.schema, e.f.restoreErr = c.schema, errors.New("no such file")
			e.inst.running, e.f.known, e.f.pending = "0.4.0", true, 1
			e.write(shipped(e))
			start := func() error {
				g := &migrationGate{stateDir: e.dir, version: "v0.4.0", pg: &pgbundle.Bundle{Backups: e.backups},
					api: exec.Command("maktaba-api"), keep: 3, log: e.log, prev: func(v string) string {
						if sameRelease(v, "0.2.0") {
							return kept
						}
						return ""
					}}
				return g.run(context.Background(), noReport)
			}
			for i := 1; i <= 2; i++ { // asked: it is tried, and a plain restart tries again
				if code := haltCode(t, start()); code != c.code {
					t.Fatalf("start %d: %s, want %s", i, code, c.code)
				}
			}
			if rec := e.rec(); rec.Data != c.data || rec.TriedBy != "v0.4.0" {
				t.Fatalf("record %+v", rec)
			}
			s := stuckOf(e.r)
			if s == nil || s.CanGiveUp != (c.data == apppayload.DataUntouched) {
				t.Fatalf("stuck %+v", s)
			}
			if c.data != apppayload.DataUntouched {
				if err := e.r.abandon(context.Background(), "admin"); restoreCode(err) != "restore_data_changed" {
					t.Fatalf("given up: %v", err)
				}
				// another backup, always
				if _, err := e.r.restore(context.Background(), otherBackup, "admin", true, false); err != nil {
					t.Fatal(err)
				}
				if rec := e.rec(); filepath.Base(rec.Dump) != otherBackup || rec.Before != nil || rec.Data != c.data || rec.RestoreError != "" {
					t.Fatalf("record %+v", rec)
				}
				return
			}
			if s.Release != "0.4.0" {
				t.Fatalf("stuck %+v", s)
			}
			if err := e.r.abandon(context.Background(), "admin"); err != nil {
				t.Fatal(err)
			}
			e.fresh()
			if err := start(); err != nil { // the newer release goes on, from a backup of its own
				t.Fatal(err)
			}
			if d, m, r := e.f.counts(); d != 1 || m != 1 || r != 2 || len(e.inst.ran) != 0 {
				t.Fatalf("%d dumps, %d migrations, %d restores, switched %v", d, m, r, e.inst.ran)
			}
			if rec := e.rec(); rec.Version != "v0.4.0" || rec.State != apppayload.DatabaseMigrated {
				t.Fatalf("record %+v", rec)
			}
		})
	}
}

// R4: the database gone (dropped for room and made again empty by the next start, or
// replaced by a release that keeps no track): a newer release never goes on from it.
func TestANewerReleaseNeverGoesOnFromAnotherDatabase(t *testing.T) {
	for _, c := range []struct {
		name   string
		id     string
		schema int64
		err    error
		code   string
		data   string
	}{
		{"made again empty", "1/9/9", 0, nil, "db_restore_backup_gone", apppayload.DataGone},
		{"dropped", "", 0, nil, "db_restore_backup_gone", apppayload.DataGone},
		{"another database", "1/9/9", 140, nil, "db_restore_backup_part", apppayload.DataReplaced},
		{"no answer", "", 0, errors.New("connection refused"), "db_restore_backup_failed", apppayload.DataUntouched},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newExitsRig(t)
			rec := e.asked()
			rec.RestoreError, rec.TriedBy = "no space left on device", "v0.2.0"
			e.write(rec)
			e.f.id, e.f.schema, e.f.idErr = c.id, c.schema, c.err
			e.f.restoreErr = errors.New("no space left on device")
			if code := haltCode(t, e.start("v0.3.1")); code != c.code {
				t.Fatalf("%s, want %s", code, c.code)
			}
			if d, m, r := e.f.counts(); d != 0 || m != 0 || r != 1 {
				t.Fatalf("%d dumps, %d migrations, %d restores (the restore again)", d, m, r)
			}
			if c.err == nil && len(e.f.asides) != 0 {
				t.Fatalf("set aside %v: not the database from before", e.f.asides)
			}
			got := e.rec()
			if got.State != apppayload.DatabaseRestoring || got.Data != c.data {
				t.Fatalf("record %+v", got)
			}
			e.inst.running = "0.3.1"
			want := "restore_data_changed"
			if c.err != nil {
				want = "restore_database_unanswered"
			}
			if err := e.r.abandon(context.Background(), "admin"); restoreCode(err) != want {
				t.Fatalf("given up: %v, want %s", err, want)
			}
			if again := e.rec(); again.State != apppayload.DatabaseRestoring || pgbundle.BackupsHeld(e.backups) != true {
				t.Fatalf("after the refusal: %+v", again)
			}
		})
	}
}

// R5: another backup whose release cannot be switched to leaves the failed restore as it
// was, never what was there before it.
func TestASwitchThatFailsLeavesTheFailedRestore(t *testing.T) {
	e := newExitsRig(t)
	rec := e.asked()
	rec.Before = &apppayload.Database{Version: "0.3.0", State: apppayload.DatabaseMigrated}
	rec.Data, rec.RestoreError, rec.TriedBy = apppayload.DataGone, "no space", "v0.2.0"
	e.write(rec)
	e.f.id, e.f.schema = "", 0
	e.inst.runErr = errors.New("no space left on device")
	if _, err := e.r.restore(context.Background(), otherBackup, "admin", true, false); restoreCode(err) != "restore_switch_failed" {
		t.Fatal(err)
	}
	got := e.rec()
	if got.State != apppayload.DatabaseRestoring || filepath.Base(got.Dump) != firstBackup || got.Data != apppayload.DataGone ||
		got.RestoreError != "no space" || !pgbundle.BackupsHeld(e.backups) {
		t.Fatalf("record %+v", got)
	}
	// nor does giving up, when the release it needs cannot be switched to
	e.f.id, e.f.schema = "1/2/3", 149
	e.inst.running = "0.2.0"
	if err := e.r.abandon(context.Background(), "admin"); restoreCode(err) != "restore_switch_failed" {
		t.Fatal(err)
	}
	if got := e.rec(); got.State != apppayload.DatabaseRestoring || filepath.Base(got.Dump) != firstBackup || !pgbundle.BackupsHeld(e.backups) {
		t.Fatalf("record %+v", got)
	}
	if err := e.r.abandon(context.Background(), "admin"); restoreCode(err) != "restore_switch_failed" {
		t.Fatalf("a refused give up leaves the lock: %v", err)
	}
	select {
	case c := <-e.exits:
		t.Fatalf("restarted (%d) on a switch that failed", c)
	default:
	}
}

// R6: giving up releases no step of the process that is about to end: the release it
// switches from never starts on the record it writes.
func TestGivingUpReleasesNoStep(t *testing.T) {
	e := newExitsRig(t)
	rec := e.asked()
	rec.Version, rec.From = "v0.2.0", "v0.3.0"
	rec.Before = &apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseRestored}
	e.write(rec)
	e.f.restoreErr = errors.New("corrupt")
	halt := e.start("v0.3.0")
	if haltCode(t, halt) != "db_restore_backup_failed" || supervisor.HaltCleared(halt) {
		t.Fatalf("%v", halt)
	}
	if err := e.r.abandon(context.Background(), "admin"); err != nil {
		t.Fatal(err)
	}
	if got := e.rec(); got.Version != "v0.2.0" || got.State != apppayload.DatabaseRestored || strings.Join(e.inst.ran, "") != "0.2.0" {
		t.Fatalf("record %+v, switched %v", got, e.inst.ran)
	}
	if supervisor.HaltCleared(halt) {
		t.Fatal("the halted step was released before the restart")
	}
	// asked for anything more: the process restarts for what was asked
	if _, err := e.r.restore(context.Background(), otherBackup, "admin", true, false); restoreCode(err) != "restore_under_way" {
		t.Fatal(err)
	}
	if c := <-e.exits; c != 75 {
		t.Fatal(c)
	}
	e.fresh()
	if err := e.start("v0.2.0"); err != nil {
		t.Fatal(err)
	}
	if d, m, _ := e.f.counts(); d != 0 || m != 1 {
		t.Fatalf("%d dumps, %d migrations", d, m)
	}
}

// What a restore did before it failed is in the record before it is done, and the database
// itself has the last word.
func TestTheRecordSaysWhatTheRestoreDid(t *testing.T) {
	for _, c := range []struct {
		name  string
		steps []string
		same  bool // the database turns out to be the one from before all the same
		data  string
		code  string
	}{
		{"nothing", nil, true, apppayload.DataUntouched, "db_restore_backup_failed"},
		{"dropped", []string{pgbundle.StepGone}, false, apppayload.DataGone, "db_restore_backup_gone"},
		{"dropped, then in part", []string{pgbundle.StepGone, pgbundle.StepReplaced}, false, apppayload.DataReplaced, "db_restore_backup_part"},
		{"committed, then found not whole", []string{pgbundle.StepReplaced}, false, apppayload.DataReplaced, "db_restore_backup_part"},
		{"a commit that did not happen", []string{pgbundle.StepReplaced}, true, apppayload.DataUntouched, "db_restore_backup_failed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newExitsRig(t)
			e.write(e.asked())
			e.f.steps = c.steps
			e.f.onRestore = func(context.Context) error {
				rec := e.rec()
				if n := len(c.steps); n > 0 && rec.Data != c.steps[n-1] {
					t.Errorf("the record says %q while the restore is past %v", rec.Data, c.steps)
				}
				e.f.mu.Lock()
				defer e.f.mu.Unlock()
				if c.same {
					e.f.id, e.f.schema = "1/2/3", 149
				} else if len(c.steps) == 2 || c.steps[0] == pgbundle.StepReplaced {
					e.f.schema = 140
				}
				return errors.New("3 of 4 rows")
			}
			if code := haltCode(t, e.start("v0.2.0")); code != c.code {
				t.Fatalf("%s, want %s", code, c.code)
			}
			if rec := e.rec(); rec.Data != c.data || rec.RestoreError != "3 of 4 rows" || rec.TriedBy != "v0.2.0" {
				t.Fatalf("record %+v", rec)
			}
			err := e.r.abandon(context.Background(), "admin")
			if c.same != (err == nil) {
				t.Fatalf("given up: %v", err)
			}
			if !c.same {
				// the same backup again, whatever the data is: what was there before stays known
				if _, err := e.r.restore(context.Background(), firstBackup, "admin", true, false); err != nil {
					t.Fatal(err)
				}
				rec := e.rec()
				if rec.Aside == "" || rec.Data != c.data || rec.DataID != "1/2/3" || rec.Before == nil || rec.Before.Failed != "v0.3.0" || rec.Version != "v0.3.0" {
					t.Fatalf("record %+v", rec)
				}
			}
		})
	}
}

// A try that hangs is stopped by giving the restore up; a second ask while one runs, and a
// give up that would be refused anyway, stop nothing.
func TestGivingUpStopsATryThatHangs(t *testing.T) {
	e := newExitsRig(t)
	e.write(e.asked())
	started := make(chan struct{})
	e.f.onRestore = func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	done := make(chan error, 1)
	go func() { done <- e.start("v0.2.0") }()
	<-started
	if s := stuckOf(e.r); s == nil || !s.Running || !s.CanGiveUp {
		t.Fatalf("stuck %+v", s)
	}
	if _, err := e.r.restore(context.Background(), otherBackup, "admin", true, false); restoreCode(err) != "restore_under_way" {
		t.Fatal(err)
	}
	rec := e.rec()
	rec.Data = apppayload.DataReplaced
	e.write(rec)
	if err := e.r.abandon(context.Background(), "admin"); restoreCode(err) != "restore_under_way" {
		t.Fatal(err)
	}
	rec.Data = apppayload.DataUntouched
	e.write(rec)
	select {
	case err := <-done:
		t.Fatalf("stopped by what was refused: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	e.inst.running = "0.2.0"
	if err := e.r.abandon(context.Background(), "admin"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !supervisor.Halted(err) || !strings.Contains(err.Error(), "stopped by admin") {
		t.Fatalf("the step: %v", err)
	}
	if got := e.rec(); got.State != apppayload.DatabaseHeld || got.Failed != "v0.3.0" || len(e.inst.ran) != 0 {
		t.Fatalf("record %+v, switched %v", got, e.inst.ran)
	}
}

// Two people ask at once: one is done, the other refused, and the release that starts is
// the record's.
func TestTwoAsksAtOnce(t *testing.T) {
	e := newExitsRig(t)
	e.write(apppayload.Database{Version: "0.3.0", State: apppayload.DatabaseMigrated})
	errs := make(chan error, 2)
	for _, n := range []string{firstBackup, otherBackup} {
		go func() {
			_, err := e.r.restore(context.Background(), n, "admin", true, false)
			errs <- err
		}()
	}
	a, b := <-errs, <-errs
	if (a == nil) == (b == nil) {
		t.Fatalf("%v, %v", a, b)
	}
	if rec := e.rec(); len(e.inst.ran) != 1 || e.inst.ran[0] != layout.NormalizeVersion(rec.From) {
		t.Fatalf("switched %v, record for %s", e.inst.ran, rec.From)
	}
}

// Asked offline the database does not answer: untouched is trusted only while the release
// that tries keeps track of it; that release then notes the database it found.
func TestARestoreAskedOffline(t *testing.T) {
	for _, c := range []struct{ running, data string }{{"0.3.0", apppayload.DataUnknown}, {"0.2.0", apppayload.DataUntouched}} {
		e := newExitsRig(t)
		e.write(apppayload.Database{Version: "0.3.0", State: apppayload.DatabaseHeld, Failed: "0.3.0"})
		e.r.exit, e.inst.running, e.f.idErr = nil, c.running, errors.New("connection refused")
		if _, err := e.r.restore(context.Background(), firstBackup, "the command line", true, false); err != nil {
			t.Fatal(err)
		}
		if rec := e.rec(); rec.Data != c.data || rec.DataID != "" || rec.Before == nil || !pgbundle.BackupsHeld(e.backups) {
			t.Fatalf("%s: record %+v", c.running, rec)
		}
		e.f.idErr, e.f.restoreErr = nil, errors.New("disk full")
		_ = e.start("v0.2.1") // the kept release the backup's schema is for
		want := "1/2/3"
		if c.data == apppayload.DataUnknown {
			want = "" // the backup cannot be read here, nor its release's migrations
		}
		if rec := e.rec(); rec.DataID != want || rec.Data != c.data {
			t.Fatalf("%s: record %+v", c.running, rec)
		}
	}
}

// A restore mark no person asked for is held before a restore replaces it, and giving that
// up leaves it held.
func TestARestoreOverAnUnaskedMark(t *testing.T) {
	e := newExitsRig(t)
	e.write(apppayload.Database{Version: "0.3.0", State: apppayload.DatabaseRestoring, From: "0.2.0", Failed: "0.3.0",
		Dump: filepath.Join(e.backups, firstBackup)})
	if _, err := e.r.restore(context.Background(), otherBackup, "admin", true, false); err != nil {
		t.Fatal(err)
	}
	e.fresh()
	if err := e.r.abandon(context.Background(), "admin"); err != nil {
		t.Fatal(err)
	}
	if rec := e.rec(); rec.State != apppayload.DatabaseHeld || rec.Held != "db_held_unasked" || filepath.Base(rec.Dump) != firstBackup {
		t.Fatalf("record %+v", rec)
	}
}

// The command line and the admin endpoint.
func TestAbandonEndpoint(t *testing.T) {
	e := newExitsRig(t)
	rec := e.asked()
	rec.RestoreError, rec.TriedBy = "disk full", "v0.2.0"
	e.write(rec)
	e.inst.running = "0.2.0"
	u := &updates{r: e.r}
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		u.serveRestore(w, httptest.NewRequest(http.MethodPost, "/update/restore-abandon", strings.NewReader(body)), "restore-abandon")
		return w
	}
	var out bytes.Buffer
	printBackups(&out, e.r.list())
	if !strings.Contains(out.String(), "could not finish (disk full): nothing was changed") || !strings.Contains(out.String(), "--abandon") {
		t.Fatalf("the list says:\n%s", out.String())
	}
	if w := post(`{"by":"the command line"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("not confirmed: %d %s", w.Code, w.Body)
	}
	if w := post(`{"by":"the command line","confirm":true}`); w.Code != http.StatusAccepted {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	e.fresh()
	if w := post(`{"by":"x","confirm":true}`); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "restore_none") {
		t.Fatalf("none waits: %d %s", w.Code, w.Body)
	}
}

// The review of 2b54a1565, 1: a restore of A asked on a shipped release, half done and not
// known; another backup, B, at another schema, asked after it, which fails. That the database
// is not at B's schema says nothing: it may hold A's data. It is never taken for the one from
// before, whichever backup is asked then, in whatever order.
func TestAnEarlierTrysDataIsNeverTakenForTheOneFromBefore(t *testing.T) {
	e := newExitsRig(t)
	const aside = "replaced-v0.3.0-20261120T000000.dump"
	if err := os.WriteFile(filepath.Join(e.backups, aside), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	schemas := map[string]int64{firstBackup: 140, otherBackup: 140, aside: 149}
	e.r.schema = func(_ context.Context, p string) (int64, error) { return schemas[filepath.Base(p)], nil }
	e.f.schema, e.f.restoreErr = 140, errors.New("no such file") // the database holds A, in part or whole
	e.inst.running = "0.4.0"
	e.write(apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseRestoring, From: "v0.2.0", By: "admin",
		Dump: filepath.Join(e.backups, firstBackup), Error: "restore asked by admin"})
	kept := t.TempDir()
	for v, s := range map[string]string{"0.2.0": "0140_m.sql", "0.2.1": "0140_m.sql", "0.3.0": "0149_m.sql"} {
		if err := os.MkdirAll(filepath.Join(kept, v), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(kept, v, s), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	start := func(version string) error {
		e.fresh()
		g := &migrationGate{stateDir: e.dir, version: version, pg: &pgbundle.Bundle{Backups: e.backups},
			api: exec.Command("maktaba-api"), keep: 3, log: e.log, prev: func(v string) string {
				return existingDir(filepath.Join(kept, layout.NormalizeVersion(v)))
			}}
		return g.run(context.Background(), noReport)
	}
	ask := func(name string) {
		t.Helper()
		e.fresh()
		if _, err := e.r.restore(context.Background(), name, "admin", true, false); err != nil {
			t.Fatal(err)
		}
	}
	never := func(when string) {
		t.Helper()
		if rec := e.rec(); rec.Data == apppayload.DataUntouched || rec.DataID != "" {
			t.Fatalf("%s: %+v", when, rec)
		}
		e.fresh()
		if s := stuckOf(e.r); s != nil && s.CanGiveUp {
			t.Fatalf("%s: stuck %+v", when, s)
		}
		if err := e.r.abandon(context.Background(), "admin"); restoreCode(err) != "restore_data_changed" {
			t.Fatalf("%s: given up: %v", when, err)
		}
	}
	for _, v := range []string{"v0.4.0"} {
		if code := haltCode(t, start(v)); code != "db_restore_backup_part" {
			t.Fatal(code)
		}
	}
	never("A, tried")
	ask(aside)
	never("B, asked")
	for _, v := range []string{"v0.3.0", "v0.4.0"} {
		if code := haltCode(t, start(v)); code != "db_restore_backup_part" {
			t.Fatalf("B on %s: %s", v, code)
		}
		never("B, tried on " + v)
	}
	ask(otherBackup) // A's schema again
	_ = start("v0.4.0")
	ask(aside)
	_ = start("v0.4.0")
	never("B again")
	if rec := e.rec(); !slices.Equal(rec.MaybeSchemas, []int64{140, 149}) {
		t.Fatalf("%+v", rec)
	}
}

// A backup whose schema cannot be told, asked before, may have made any: nothing is known by
// its schema after it.
func TestABackupOfNoSchemaLeavesTheDataUnknown(t *testing.T) {
	e := newExitsRig(t)
	e.r.schema = func(_ context.Context, p string) (int64, error) {
		if filepath.Base(p) == otherBackup {
			return 140, nil
		}
		return 0, errors.New("lost")
	}
	e.f.schema, e.f.restoreErr = 777, errors.New("disk full")
	e.inst.running = "0.4.0"
	e.write(apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseRestoring, From: "v0.1.0", By: "admin",
		Dump: filepath.Join(e.backups, firstBackup), Error: "restore asked by admin"})
	if _, err := e.r.restore(context.Background(), otherBackup, "admin", true, false); err != nil {
		t.Fatal(err)
	}
	e.fresh()
	if err := e.r.abandon(context.Background(), "admin"); restoreCode(err) != "restore_data_changed" {
		t.Fatal(err)
	}
	if rec := e.rec(); rec.Data != apppayload.DataUnknown || !slices.Equal(rec.MaybeSchemas, []int64{0}) {
		t.Fatalf("%+v", rec)
	}
}

// ... and a database at another schema than every backup tried is the one from before, however
// many were.
func TestTheOneFromBeforeIsStillKnownAfterAnotherBackup(t *testing.T) {
	e := newExitsRig(t)
	e.r.schema = func(context.Context, string) (int64, error) { return 140, nil }
	e.f.schema, e.f.restoreErr = 149, errors.New("disk full")
	e.inst.running = "0.4.0"
	e.write(apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseRestoring, From: "v0.2.0", By: "admin",
		Dump: filepath.Join(e.backups, firstBackup), Error: "restore asked by admin"})
	e.f.idErr = errors.New("connection refused") // asked offline: nothing can be looked at
	if _, err := e.r.restore(context.Background(), otherBackup, "admin", true, false); err != nil {
		t.Fatal(err)
	}
	if rec := e.rec(); rec.Data != apppayload.DataUnknown {
		t.Fatalf("%+v", rec)
	}
	e.f.idErr = nil
	e.fresh()
	if err := e.r.abandon(context.Background(), "admin"); err != nil {
		t.Fatal(err)
	}
}

// The review of 2b54a1565, 5: what the page says of a newer release, for a restore asked on a
// shipped release too: it goes on from the library as it was, and never puts the backup back.
func TestANewerReleaseGoesOnAfterAShippedAsk(t *testing.T) {
	e := newExitsRig(t)
	e.r.schema = func(context.Context, string) (int64, error) { return 140, nil }
	e.f.schema, e.f.restoreErr = 149, errors.New("no space left on device")
	e.inst.running, e.f.known, e.f.pending = "0.4.0", true, 1
	e.write(apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseRestoring, From: "v0.2.0", By: "admin",
		Dump: filepath.Join(e.backups, firstBackup), Error: "restore asked by admin", Data: apppayload.DataUntouched, DataID: "1/2/3"})
	if code := haltCode(t, e.start("v0.4.0")); code != "db_restore_backup_failed" {
		t.Fatal(code)
	}
	e.f.restoreErr = nil // there is room now
	e.fresh()
	if err := e.start("v0.4.0"); err != nil { // a plain restart does what was asked
		t.Fatal(err)
	}
	if _, _, r := e.f.counts(); r != 2 {
		t.Fatalf("%d restores", r)
	}
	// a newer one goes on
	e2 := newExitsRig(t)
	e2.f.schema, e2.f.restoreErr = 149, errors.New("no space left on device")
	e2.f.known, e2.f.pending = true, 1
	e2.write(apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseRestoring, From: "v0.2.0", By: "admin",
		Dump: filepath.Join(e2.backups, firstBackup), Error: "restore asked by admin", Data: apppayload.DataUntouched, DataID: "1/2/3"})
	_ = haltCode(t, e2.start("v0.4.0"))
	e2.f.restoreErr = nil
	e2.fresh()
	if err := e2.start("v0.5.0"); err != nil {
		t.Fatal(err)
	}
	if d, m, r := e2.f.counts(); d != 1 || m != 1 || r != 1 {
		t.Fatalf("%d dumps, %d migrations, %d restores", d, m, r)
	}
	if rec := e2.rec(); rec.Version != "v0.5.0" || rec.State != apppayload.DatabaseMigrated {
		t.Fatalf("%+v", rec)
	}
}

// Through the admin port, as the command line asks: past its token and its router.
func TestAbandonThroughTheAdminPort(t *testing.T) {
	e := newExitsRig(t)
	rec := e.asked()
	rec.RestoreError, rec.TriedBy = "disk full", "v0.2.0"
	e.write(rec)
	e.inst.running = "0.2.0"
	u := &updates{r: e.r}
	srv := httptest.NewServer(u.adminHandler(http.NotFoundHandler(), "tok"))
	defer srv.Close()
	var out bytes.Buffer
	if err := abandonThroughServer(context.Background(), &out, &restoreAsker{base: srv.URL, token: "wrong", http: srv.Client()}); err == nil {
		t.Fatal("given up with a wrong token")
	}
	if err := abandonThroughServer(context.Background(), &out, &restoreAsker{base: srv.URL, token: "tok", http: srv.Client()}); err != nil {
		t.Fatal(err)
	}
	if got := e.rec(); got.State != apppayload.DatabaseHeld || !strings.Contains(out.String(), "given up") {
		t.Fatalf("record %+v, said %q", got, out.String())
	}
}

// A try is stopped only for a give up that can be done: the release that has to run is kept.
func TestATryIsNotStoppedForAGiveUpThatCannotBeDone(t *testing.T) {
	e := newExitsRig(t)
	rec := e.asked()
	rec.Before = &apppayload.Database{Version: "0.2.5", State: apppayload.DatabaseMigrated} // no longer kept
	e.write(rec)
	e.inst.running = "0.2.0"
	started, free := make(chan struct{}), make(chan struct{})
	e.f.onRestore = func(ctx context.Context) error {
		close(started)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-free:
			return nil
		}
	}
	done := make(chan error, 1)
	go func() { done <- e.start("v0.2.0") }()
	<-started
	if s := stuckOf(e.r); s == nil || !s.Running || s.CanGiveUp {
		t.Fatalf("stuck %+v", s)
	}
	if err := e.r.abandon(context.Background(), "admin"); restoreCode(err) != "restore_under_way" {
		t.Fatal(err)
	}
	close(free)
	if err := <-done; err != nil {
		t.Fatalf("the try: %v", err)
	}
	if got := e.rec(); got.Version != "v0.2.0" || got.State != apppayload.DatabaseMigrated {
		t.Fatalf("%+v", got)
	}
}

// A switch that fails leaves the record as it was, on a disk with no room for another copy of
// it; when even that cannot be done, it is said.
func TestARecordGoesBackOnAFullDisk(t *testing.T) {
	e := newExitsRig(t)
	e.write(apppayload.Database{Version: "0.3.0", State: apppayload.DatabaseMigrated, Error: "as it was"})
	before, err := os.ReadFile(apppayload.DatabasePath(e.dir))
	if err != nil {
		t.Fatal(err)
	}
	prev := writeRecord
	t.Cleanup(func() { writeRecord = prev })
	writes := 0
	writeRecord = func(dir string, rec apppayload.Database) error {
		if writes++; writes > 1 {
			return errors.New("no space left on device")
		}
		return prev(dir, rec)
	}
	e.inst.runErr = errors.New("no space left on device")
	if _, err := e.r.restore(context.Background(), firstBackup, "admin", true, false); restoreCode(err) != "restore_switch_failed" {
		t.Fatal(err)
	}
	if now, err := os.ReadFile(apppayload.DatabasePath(e.dir)); err != nil || !bytes.Equal(now, before) {
		t.Fatalf("the record (%v):\n%s\nit was\n%s", err, now, before)
	}
	if ents, _ := filepath.Glob(apppayload.DatabasePath(e.dir) + "*"); len(ents) != 1 {
		t.Fatalf("left behind: %v", ents)
	}
	// with no room for its copy, nothing is changed at all
	if err := os.Mkdir(apppayload.DatabasePath(e.dir)+".undo", 0o700); err != nil {
		t.Fatal(err)
	}
	writes = 0
	if _, err := e.r.restore(context.Background(), firstBackup, "admin", true, false); err == nil || writes != 0 {
		t.Fatalf("%v after %d writes", err, writes)
	}
}

// Stopped while the database is set aside, before the restore began: nothing is restored, and
// nothing says it was.
func TestStoppedBeforeTheRestoreBegan(t *testing.T) {
	e := newExitsRig(t)
	e.write(e.asked())
	prev := dumpAsideBundled
	t.Cleanup(func() { dumpAsideBundled = prev })
	started := make(chan struct{})
	dumpAsideBundled = func(ctx context.Context, b pgbundle.Bundle, path string) error {
		close(started)
		<-ctx.Done()
		return prev(context.WithoutCancel(ctx), b, path) // it ended all the same
	}
	done := make(chan error, 1)
	go func() { done <- e.start("v0.2.0") }()
	<-started
	e.inst.running = "0.2.0"
	if err := e.r.abandon(context.Background(), "admin"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !supervisor.Halted(err) || !strings.Contains(err.Error(), "stopped by admin") {
		t.Fatalf("the step: %v", err)
	}
	if _, _, r := e.f.counts(); r != 0 || e.rec().State != apppayload.DatabaseHeld {
		t.Fatalf("%d restores, record %+v", r, e.rec())
	}
}

// Once a restore was asked or given up the process leaves: a step of it that comes to its try
// then does nothing.
func TestNoTryOnceTheProcessLeaves(t *testing.T) {
	e := newExitsRig(t)
	e.write(e.asked())
	if _, err := e.r.restore(context.Background(), otherBackup, "admin", true, false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	g := &migrationGate{stateDir: e.dir, version: "v0.2.0", pg: &pgbundle.Bundle{Backups: e.backups}, api: exec.Command("maktaba-api"),
		keep: 3, log: e.log}
	if err := g.run(ctx, noReport); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if _, _, r := e.f.counts(); r != 0 {
		t.Fatalf("%d restores", r)
	}
}

// What settles the data: a schema that cannot be read now says nothing, what is known needs no
// list of what may be there, and asking for another backup looks first.
func TestSettlingTheData(t *testing.T) {
	probe := func(id string, schema int64) dataProbe {
		return dataProbe{identity: func(context.Context) (string, int64, error) { return id, schema, nil },
			backupSchema: func(context.Context, apppayload.Database) int64 { return 140 }}
	}
	ctx := context.Background()
	known := apppayload.Database{Data: apppayload.DataUntouched, DataID: "1/2/3"}
	if rec, err := settleData(ctx, known, probe("9/9/9", -1)); err == nil || rec.Data != apppayload.DataUntouched {
		t.Errorf("a schema that cannot be read: %+v %v", rec, err)
	}
	if rec, err := settleData(ctx, known, probe("1/2/3", -1)); err != nil || rec.Data != apppayload.DataUntouched {
		t.Errorf("the same database, its schema not read: %+v %v", rec, err)
	}
	rec, err := settleData(ctx, apppayload.Database{MaybeSchemas: []int64{150}}, probe("1/2/3", 149))
	if err != nil || rec.Data != apppayload.DataUntouched || rec.MaybeSchemas != nil || rec.DataID != "1/2/3" {
		t.Errorf("known: %+v %v", rec, err)
	}
	e := newExitsRig(t)
	e.write(e.asked()) // untouched, it says
	e.f.id, e.f.schema = "9/9/9", 140
	if _, err := e.r.restore(ctx, otherBackup, "admin", true, false); err != nil {
		t.Fatal(err)
	}
	if rec := e.rec(); rec.Data != apppayload.DataReplaced {
		t.Errorf("asked over another database: %+v", rec)
	}
}

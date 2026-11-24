package main

// The ways out of a restore a person asked for that cannot finish (its backup lost or
// unreadable, a full disk, a try that hangs). One rule: the record (state/database.json)
// says what state the DATA is in, written before each step that changes it, and each way
// out is allowed or refused by that alone:
//
//	data       try again  another backup  give it up  a newer release installed since
//	untouched  yes        yes             yes         yes
//	replaced   yes        yes             no          no
//	gone       yes        yes             no          no
//	unknown    yes        yes             no          no
//
// "Untouched" is never taken on trust. The record holds the identity of the database the
// restore found (dbsafe.Target.Identity), and whatever puts a dump back makes another
// database, or another public schema: settleData compares it before each try, after each
// failed one, and before a way out that needs it, so a release that keeps no track of the
// data (one from before 2026-11-25 that tried in between), a crash, or a Postgres that made
// the database again empty cannot be taken for it. A restore asked on such a release has no
// identity: its database is the one from before when its schema is another than the
// backup's, else that is not known.
//
// One lock (restoreGuard) covers asking for a restore, giving one up, and the start's own
// try, in the one process that does all three (the supervisor; offline, `maktaba-server
// restore` holds the launcher's lock). Once one of the first two is done the process is
// about to restart, and nothing else of it changes the record or goes on from it.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/recovery"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/updater"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// restoreGuard is the lock over a home's restore in this process.
type restoreGuard struct {
	mu sync.Mutex
	// a try is under way: stop ends it (its error is the cause), ended closes once its
	// outcome is recorded
	stop  context.CancelCauseFunc
	ended chan struct{}
	// leaving: a restore was asked or given up, and the process restarts for it
	leaving bool
}

var restoreGuards sync.Map // state dir -> *restoreGuard

func restoreGuardOf(stateDir string) *restoreGuard {
	g, _ := restoreGuards.LoadOrStore(filepath.Clean(stateDir), &restoreGuard{})
	return g.(*restoreGuard)
}

// begin starts a try of the restore the record asks for: its context (exclusive may end
// it) and what to call once its outcome is recorded. ok false: the process is leaving.
func (g *restoreGuard) begin(ctx context.Context) (tctx context.Context, end func(), ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.leaving {
		return ctx, func() {}, false
	}
	tctx, stop := context.WithCancelCause(ctx)
	ended := make(chan struct{})
	g.stop, g.ended = stop, ended
	var once sync.Once
	return tctx, func() {
		once.Do(func() {
			g.mu.Lock()
			if g.ended == ended {
				g.stop, g.ended = nil, nil
			}
			g.mu.Unlock()
			stop(nil)
			close(ended)
		})
	}, true
}

// restoreStopWait is how long a try asked to end is waited for (its programs are killed).
const restoreStopWait = 2 * time.Minute

// exclusive runs fn alone: no try under way (stop, when not nil, says whether one is ended
// for it: "" no), nothing asked before that the process restarts for. fn's leave says the
// process restarts for what it did.
func (g *restoreGuard) exclusive(stop func() string, fn func() (leave bool, err error)) error {
	g.mu.Lock()
	for g.stop != nil {
		why := ""
		if stop != nil {
			why = stop()
		}
		if why == "" {
			g.mu.Unlock()
			return refuse("restore_under_way", "the library database is being put back from a backup right now: wait "+
				"until that ends")
		}
		g.stop(errors.New(why))
		ended := g.ended
		g.mu.Unlock()
		t := time.NewTimer(restoreStopWait)
		select {
		case <-ended:
			t.Stop()
		case <-t.C:
			return refuse("restore_under_way", "the restore under way did not stop within %s: restart Maktaba, then try "+
				"again", restoreStopWait)
		}
		g.mu.Lock()
	}
	defer g.mu.Unlock()
	if g.leaving {
		return refuse("restore_under_way", "Maktaba is about to restart for what was asked just before")
	}
	leave, err := fn()
	if err == nil && leave {
		g.leaving = true
	}
	return err
}

// gone reports whether the process is leaving (never in the middle of what it leaves for).
func (g *restoreGuard) gone() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.leaving
}

// tryRuns reports whether a try is under way.
func (g *restoreGuard) tryRuns() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stop != nil
}

// dataProbe looks at the library database.
type dataProbe struct {
	// identity is dbsafe.Target.Identity
	identity func(ctx context.Context) (id string, schema int64, err error)
	// backupSchema is the schema of the record's backup (0: cannot be told)
	backupSchema func(ctx context.Context, rec apppayload.Database) int64
}

// settleData is rec with what its data is, as the database itself says (it is not answered:
// an error, and rec as it was).
func settleData(ctx context.Context, rec apppayload.Database, p dataProbe) (apppayload.Database, error) {
	if p.identity == nil {
		return rec, errors.New("the library database cannot be asked")
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	id, schema, err := p.identity(cctx)
	if err != nil {
		return rec, err
	}
	switch {
	case id != "" && rec.DataID == id:
		rec.Data = apppayload.DataUntouched
	case schema < 0:
		return rec, errors.New("its schema cannot be read")
	case id == "" || schema == 0:
		rec.Data = apppayload.DataGone
	case rec.DataID != "":
		rec.Data = apppayload.DataReplaced
	case rec.Data == apppayload.DataUntouched: // asked while the database did not run
		rec.DataID = id
	case rec.Data == apppayload.DataUnknown && p.backupSchema != nil:
		// asked on a release that kept no track of it: every release's restore makes the
		// database (or its schema) afresh, so one with another schema than the backup's is
		// the one from before it
		if want := p.backupSchema(cctx, rec); want > 0 && want != schema {
			rec.Data, rec.DataID = apppayload.DataUntouched, id
		}
	}
	return rec, nil
}

// probe is the gate's dataProbe.
func (g *migrationGate) probe() dataProbe {
	return dataProbe{
		identity: func(ctx context.Context) (string, int64, error) { return dataIdentity(ctx, g) },
		backupSchema: func(ctx context.Context, rec apppayload.Database) int64 {
			if t, err := g.target(); err == nil {
				if v, err := t.DumpSchema(ctx, rec.Dump); err == nil && v > 0 {
					return v
				}
			}
			if g.prev == nil || rec.From == "" {
				return 0
			}
			return updater.NewestMigration(g.prev(rec.From)) // the release it is put back for, whose schema is the backup's
		},
	}
}

// probe is the restorer's dataProbe.
func (r *restorer) probe() dataProbe {
	return dataProbe{identity: r.identity, backupSchema: func(ctx context.Context, rec apppayload.Database) int64 {
		if r.schema != nil {
			if v, err := r.schema(ctx, rec.Dump); err == nil && v > 0 {
				return v
			}
		}
		if k, ok := r.inst.(updater.Keeper); ok {
			for _, kr := range k.Kept() {
				if sameRelease(kr.Version, rec.From) && kr.Schema > 0 {
					return kr.Schema
				}
			}
		}
		return 0
	}}
}

func sameRelease(a, b string) bool {
	return layout.NormalizeVersion(a) == layout.NormalizeVersion(b)
}

// newerThan reports whether release a is newer than release b (versions as any record or
// payload writes them).
func newerThan(a, b string) bool {
	return b != "" && apppayload.Older(layout.NormalizeVersion(b), layout.NormalizeVersion(a))
}

// asked reports whether rec is a restore a person asked for that is not done.
func asked(rec apppayload.Database) bool {
	return rec.State == apppayload.DatabaseRestoring && rec.By != ""
}

// waitsOn is the release a record's database waits on or runs on: a held update's failed
// release, else the one that ran on it last.
func waitsOn(rec apppayload.Database) string {
	if rec.State == apppayload.DatabaseHeld {
		return rec.Failed
	}
	return rec.Ran()
}

// leftBehind is the record the restore replaced, when release cur goes on from it: cur is a
// newer release a person installed since the restore failed. It is not the release whose
// try failed (a plain restart tries again), nor the one the restore is for, and the data is
// what it was (rec is settled).
func leftBehind(rec apppayload.Database, cur string) (apppayload.Database, bool) {
	if !asked(rec) || rec.Data != apppayload.DataUntouched || rec.Before == nil || rec.TriedBy == "" ||
		sameRelease(rec.TriedBy, cur) || sameRelease(rec.From, cur) || !newerThan(cur, waitsOn(*rec.Before)) {
		return rec, false
	}
	return *rec.Before, true
}

// giveUpTo is what giving the restore rec up leaves while release running runs: the record,
// and the release that has to start on it ("": the one running). ok false: what was there
// before it is not recorded (asked on a release from before 2026-11-25), and running is not
// a newer release that can go on from it.
func giveUpTo(rec apppayload.Database, running string) (base apppayload.Database, release string, ok bool) {
	target := sameRelease(rec.From, running)
	if rec.Before == nil {
		// the release that ran on it last is all that is known: a newer one updates it, from
		// a backup of its own
		if target || !newerThan(running, rec.Version) {
			return rec, "", false
		}
		return apppayload.Database{Version: rec.Version, State: apppayload.DatabaseMigrated}, "", true
	}
	base = *rec.Before
	ran := base.Ran()
	if base.State == apppayload.DatabaseHeld || ran == "" || sameRelease(ran, running) || (!target && newerThan(running, ran)) {
		// a held update waits for a person whichever release starts
		return base, "", true
	}
	return base, ran, true
}

// canGiveUp is giveUpTo's ok for the record as it says it is.
func canGiveUp(rec apppayload.Database, running string) bool {
	_, _, ok := giveUpTo(rec, running)
	return ok && rec.Data == apppayload.DataUntouched
}

// keptRelease is the kept release version, the running copy of it first.
func (r *restorer) keptRelease(version string) (out updater.KeptRelease, ok bool) {
	k, is := r.inst.(updater.Keeper)
	if !is {
		return out, false
	}
	for _, kr := range k.Kept() {
		if sameRelease(kr.Version, version) && (!ok || kr.Running) {
			out, ok = kr, true
		}
	}
	return out, ok
}

// abandon gives up a restore that is not done (a try under way is stopped first), when the
// data is what it was: the record is again what the restore replaced, and the home restarts
// on the release that runs on it. Refused otherwise: only a backup brings a database back
// then.
func (r *restorer) abandon(ctx context.Context, by string) error {
	read := func() (apppayload.Database, error) {
		rec, had, err := apppayload.ReadDatabase(r.stateDir)
		if err != nil {
			return rec, err
		}
		if !had || !asked(rec) {
			return rec, refuse("restore_none", "no restore waits here")
		}
		return rec, nil
	}
	// the record of a try under way says what the try did so far
	stop := func() string {
		if rec, err := read(); err != nil || !canGiveUp(rec, r.running()) {
			return ""
		}
		return "stopped by " + by
	}
	err := restoreGuardOf(r.stateDir).exclusive(stop, func() (bool, error) {
		rec, err := read()
		if err != nil {
			return false, err
		}
		settled, err := settleData(ctx, rec, r.probe())
		if err != nil {
			return false, refuse("restore_database_unanswered", "the library database does not answer, so it cannot be told "+
				"that it is as it was before the restore: %v", err)
		}
		if settled.Data != rec.Data || settled.DataID != rec.DataID {
			rec = settled
			if err := apppayload.WriteDatabase(r.stateDir, rec); err != nil {
				return false, err
			}
		}
		if rec.Data != apppayload.DataUntouched {
			return false, refuse("restore_data_changed", "the library database is not the one from before the restore any "+
				"more (%s): put a backup back", orUnknown(rec.Data))
		}
		base, release, ok := giveUpTo(rec, r.running())
		if !ok {
			return false, refuse("restore_before_unknown", "the restore was asked on a release that did not record what was "+
				"there before it: put a backup back, or install a release newer than %s and give it up there", rec.Version)
		}
		if err := r.leaveFor(ctx, base, &rec, release); err != nil {
			return false, err
		}
		fmt.Fprintf(orDiscardW(r.log), "[serve] the restore of %s is given up by %s (%s): the library database stays as it "+
			"was before it\n", filepath.Base(rec.Dump), by, orUnknown(rec.RestoreError))
		return r.exit != nil, nil
	})
	if err != nil {
		return err
	}
	r.restart()
	return nil
}

// leaveFor writes rec, then makes release ("": the one running) the one that starts. A
// switch that fails leaves the record as it was (old; nil: there was none) and nothing is
// restarted.
func (r *restorer) leaveFor(ctx context.Context, rec apppayload.Database, old *apppayload.Database, release string) error {
	var target updater.KeptRelease
	if release != "" {
		var ok bool
		if target, ok = r.keptRelease(release); !ok {
			return refuse("restore_no_release", "%s is no longer kept: install it, or put a backup back that a release "+
				"this home keeps runs", release)
		}
	}
	if err := r.holdFor(rec); err != nil {
		return err
	}
	err := apppayload.WriteDatabase(r.stateDir, rec)
	if err == nil && release != "" {
		// a client that goes away does not cut the switch
		if err = r.inst.(updater.Keeper).RunKept(context.WithoutCancel(ctx), target); err != nil {
			err = refuse("restore_switch_failed", "%s cannot be made the release that starts: %v", release, err)
		}
	}
	if err == nil {
		return nil
	}
	// nothing is restarted: the record goes back to how it was, so no start acts on it
	back := apppayload.Database{}
	if old != nil {
		back = *old
		_ = apppayload.WriteDatabase(r.stateDir, back)
	} else {
		_ = os.Remove(apppayload.DatabasePath(r.stateDir))
	}
	_ = r.holdFor(back)
	return err
}

// holdFor holds the backups while rec is a restore that is not done, and releases them
// otherwise (pgbundle.HoldBackups).
func (r *restorer) holdFor(rec apppayload.Database) error {
	if r.db == nil {
		return nil
	}
	return holdBackupsFor(r.db.Backups, rec)
}

func holdBackupsFor(dir string, rec apppayload.Database) error {
	if dir == "" {
		return nil
	}
	if !asked(rec) {
		return pgbundle.ReleaseBackups(dir)
	}
	if err := pgbundle.HoldBackups(dir); err != nil {
		return fmt.Errorf("keep the backups in %s until the restore is done: %w", dir, err)
	}
	return nil
}

// restart restarts the home, once the answer went out.
func (r *restorer) restart() {
	if r.exit == nil {
		return
	}
	delay := r.exitDelay
	go func() {
		time.Sleep(delay)
		r.exit(75)
	}()
}

// stuckOf is the restore a person asked for that is not done, nil when none: the recovery
// page's ways out of it (running: the release that runs).
func stuckOf(stateDir, running string) *recovery.Stuck {
	rec, ok, err := apppayload.ReadDatabase(stateDir)
	if err != nil || !ok || !asked(rec) {
		return nil
	}
	s := &recovery.Stuck{Backup: filepath.Base(rec.Dump), Why: rec.RestoreError, Data: rec.Data,
		Running: restoreGuardOf(stateDir).tryRuns(), CanGiveUp: canGiveUp(rec, running)}
	if s.Data == apppayload.DataUnknown {
		s.Data = apppayload.DataReplaced // what can be done is the same
	}
	if !s.Running && s.Why == "" {
		return nil // not tried yet: the restart that was asked does
	}
	if s.CanGiveUp {
		base, release, _ := giveUpTo(rec, running)
		if release == "" && base.State != apppayload.DatabaseHeld {
			release = running
		}
		s.Release = layout.NormalizeVersion(release)
	}
	return s
}

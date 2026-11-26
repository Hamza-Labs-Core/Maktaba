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
// identity: its database is the one from before when its schema is another than that of every
// backup that may have been put back since (the one asked, and those asked before it:
// MaybeSchemas), else that is not known.
//
// One lock (restoreGuard) covers asking for a restore, giving one up, and the start's own
// try, in the one process that does all three (the supervisor; offline, `maktaba-server
// restore` holds the launcher's lock). Once one of the first two is done the process is
// about to restart, and nothing else of it changes the record or goes on from it.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
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
	id, table, _ := strings.Cut(id, "#")
	was, inside := strings.CutSuffix(rec.DataID, insideMark)
	switch {
	case id != "" && was == id && (rec.DataTable == table || rec.DataTable == "" && !inside):
		rec.Data, rec.DataID, rec.DataTable = apppayload.DataUntouched, id, table
	case schema < 0:
		return rec, errors.New("its schema cannot be read")
	case id == "" || schema == 0:
		rec.Data = apppayload.DataGone
	case rec.DataID != "":
		rec.Data = apppayload.DataReplaced
	case rec.Data == apppayload.DataUntouched: // asked while the database did not run
		rec.DataID, rec.DataTable = id, table
	case rec.Data == apppayload.DataUnknown && p.backupSchema != nil:
		// asked on a release that kept no track of it: every release's restore makes the
		// database (or its schema) afresh, so one with another schema than every backup's that
		// was asked since is the one from before them all
		other := true
		for _, want := range append([]int64{p.backupSchema(cctx, rec)}, rec.MaybeSchemas...) {
			other = other && want > 0 && want != schema
		}
		if other {
			rec.Data, rec.DataID, rec.DataTable = apppayload.DataUntouched, id, table
		}
	}
	if rec.Data != apppayload.DataUnknown {
		rec.MaybeSchemas = nil
	}
	return rec, nil
}

// insideMark at the end of a record's DataID says that a restore inside public may have been
// committed since. The schema stays then, and with it what DataID names; a release that goes by
// that alone (from before DataTable, which it also drops from the record) reads the mark as
// another database, never as the one from before.
const insideMark = "/inside"

// replacing is rec as it is written before step of its restore.
func replacing(rec apppayload.Database, step string) apppayload.Database {
	if rec.Data = step; step == pgbundle.StepInside {
		rec.Data, rec.DataID = apppayload.DataReplaced, strings.TrimSuffix(rec.DataID, insideMark)+insideMark
	}
	return rec
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

// before is the record the restore rec replaced: what it says, else (asked on a release from
// before 2026-11-25) the release that ran on its database last, which is all that is known: a
// newer one updates it, from a backup of its own.
func before(rec apppayload.Database) apppayload.Database {
	if rec.Before != nil {
		return *rec.Before
	}
	return apppayload.Database{Version: rec.Version, State: apppayload.DatabaseMigrated}
}

// leftBehind is the record the restore replaced, when release cur goes on from it: cur is a
// newer release a person installed since the restore failed. It is not the release whose
// try failed (a plain restart tries again), nor the one the restore is for, and the data is
// what it was (rec is settled).
func leftBehind(rec apppayload.Database, cur string) (apppayload.Database, bool) {
	if !asked(rec) || rec.Data != apppayload.DataUntouched || rec.TriedBy == "" ||
		sameRelease(rec.TriedBy, cur) || sameRelease(rec.From, cur) || !newerThan(cur, waitsOn(before(rec))) {
		return rec, false
	}
	return before(rec), true
}

// giveUpTo is what giving the restore rec up leaves while release running runs: the record,
// and the release that has to start on it ("": the one running). ok false: what was there
// before it is not recorded (asked on a release from before 2026-11-25), and running is not
// a newer release that can go on from it.
func giveUpTo(rec apppayload.Database, running string) (base apppayload.Database, release string, ok bool) {
	target := sameRelease(rec.From, running)
	base = before(rec)
	if rec.Before == nil {
		if target || !newerThan(running, rec.Version) {
			return rec, "", false
		}
		return base, "", true
	}
	ran := base.Ran()
	if base.State == apppayload.DatabaseHeld || ran == "" || sameRelease(ran, running) || (!target && newerThan(running, ran)) {
		// a held update waits for a person whichever release starts
		return base, "", true
	}
	return base, ran, true
}

// canGiveUp says whether the restore rec can be given up, as the record says it is: the data
// is what it was, and the release that would have to start on it is kept.
func (r *restorer) canGiveUp(rec apppayload.Database) bool {
	_, release, ok := giveUpTo(rec, r.running())
	if ok && release != "" {
		_, ok = r.keptRelease(release)
	}
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
		if rec, err := read(); err != nil || !r.canGiveUp(rec) {
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
		if settled.Data != rec.Data || settled.DataID != rec.DataID || settled.DataTable != rec.DataTable {
			rec = settled
			if err := writeRecord(r.stateDir, rec); err != nil {
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
	// what puts the record back, made now: a disk with no room for another copy of it then is
	// what a switch may fail on
	undo, err := stashRecord(r.stateDir, old != nil)
	if err != nil {
		return fmt.Errorf("the database's record cannot be copied before it is changed (nothing was): %w", err)
	}
	if err := r.holdFor(rec); err != nil {
		undo.forget()
		return err
	}
	err = writeRecord(r.stateDir, rec)
	if err == nil && release != "" {
		// a client that goes away does not cut the switch
		if err = r.inst.(updater.Keeper).RunKept(context.WithoutCancel(ctx), target); err != nil {
			err = refuse("restore_switch_failed", "%s cannot be made the release that starts: %v", release, err)
		}
	}
	if err == nil {
		undo.forget()
		return nil
	}
	// nothing is restarted: the record goes back to how it was, so no start acts on it
	if uerr := undo.back(); uerr != nil {
		return fmt.Errorf("%w (and the database's record could not be put back, so the next start acts on what was asked: %v)", err, uerr)
	}
	back := apppayload.Database{}
	if old != nil {
		back = *old
	}
	_ = r.holdFor(back)
	return err
}

// writeRecord is apppayload.WriteDatabase (tests replace it).
var writeRecord = apppayload.WriteDatabase

// recordUndo puts the database's record back as it was when it was stashed.
type recordUndo struct {
	path, copy string // copy "": there was none
}

// stashRecord keeps a copy of the record beside it (had false: there is none to keep).
func stashRecord(stateDir string, had bool) (recordUndo, error) {
	u := recordUndo{path: apppayload.DatabasePath(stateDir)}
	if !had {
		return u, nil
	}
	raw, err := os.ReadFile(u.path)
	if err != nil {
		return u, err
	}
	u.copy = u.path + ".undo"
	return u, layout.WriteFileAtomic(u.copy, raw, 0o644)
}

// back takes no room: a rename, or a removal.
func (u recordUndo) back() error {
	if u.copy == "" {
		if err := os.Remove(u.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	return os.Rename(u.copy, u.path)
}

func (u recordUndo) forget() {
	if u.copy != "" {
		_ = os.Remove(u.copy)
	}
}

// copyError is a copy of the database a restore replaces that could not be kept.
type copyError struct{ err error }

func (e *copyError) Error() string { return e.err.Error() }
func (e *copyError) Unwrap() error { return e.err }

// asideKeeps are the backups that stay when room is made for the copy of the database the
// restore rec replaces: the one it puts back, the copy itself, and the one the record before
// it names.
func asideKeeps(rec apppayload.Database) []string {
	var names []string
	for _, p := range []string{rec.Dump, rec.Aside, before(rec).Dump} {
		if p != "" {
			names = append(names, filepath.Base(p))
		}
	}
	return names
}

// asideThere reports whether the copy the record names is there, as it was taken.
func asideThere(rec apppayload.Database) bool {
	if rec.Aside == "" || rec.AsideBytes <= 0 {
		return false
	}
	info, err := os.Lstat(rec.Aside)
	return err == nil && pgbundle.IsDump(info) && pgbundle.DumpBytes(rec.Aside) == rec.AsideBytes
}

// validAsideCandidate reports whether path is a non-empty, verified PostgreSQL dump that can
// safely protect the live database before overwriting it. Empty files, empty directories,
// and corrupt or unverified archives are rejected.
func validAsideCandidate(ctx context.Context, path string, verify func(context.Context, string) error) bool {
	if !pgbundle.HasDumpHeader(path) || pgbundle.DumpBytes(path) <= 0 {
		return false
	}
	if err := pgbundle.CheckCounts(path); err != nil {
		return false
	}
	if verify == nil {
		return false
	}
	return verify(ctx, path) == nil
}

// replacedDumpThere is a copy of the database a restore replaces that is there already (""
// when there is none): the record's own path — verified whole via validAsideCandidate
// (checking both recorded AsideBytes when > 0 and full archive content, so same-size
// corruption is never trusted) — or, the data not known (asked on a release that kept no
// record of it), a verified replaced-* dump of that version taken since the ask.
func replacedDumpThere(ctx context.Context, dir string, rec apppayload.Database, verify func(context.Context, string) error) string {
	if rec.Aside != "" {
		if (rec.AsideBytes <= 0 || asideThere(rec)) && validAsideCandidate(ctx, rec.Aside, verify) {
			return rec.Aside
		}
	}
	if rec.Data != apppayload.DataUnknown {
		return ""
	}
	prefix, since := pgbundle.ReplacedDumpPrefix+rec.Version+"-", rec.At
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if info, err := e.Info(); err == nil && strings.HasPrefix(e.Name(), prefix) &&
			strings.HasSuffix(e.Name(), ".dump") && !info.ModTime().Before(since) && pgbundle.IsDump(info) {
			p := filepath.Join(dir, e.Name())
			if validAsideCandidate(ctx, p, verify) {
				return p
			}
		}
	}
	return ""
}

// goOnWithoutCopy reports whether the person who asked for rec confirmed that it goes on with
// no copy, err being why there is none.
func goOnWithoutCopy(rec apppayload.Database, err error) bool {
	var room *pgbundle.NoRoomError
	return rec.NoCopy == apppayload.NoCopyAny || (rec.NoCopy == apppayload.NoCopyRoom && errors.As(err, &room))
}

// A restore on a release older than the one running is done by its code: it may know nothing
// of the hold, of a copy that is there already, of a database that needs none, or of a person
// who agreed to go on without one. So it is asked of it only when there is room for the copy it
// takes as it is, with nothing removed for room (olderRoom), and what it needs is out of its reach
// (protect). What that release removes by its other rules (the dumps' share of the disk, their
// age, their number) it still may.

// olderRoom refuses a restore on the older release when its own rules leave no room for the
// copy it takes, whatever this home's record says: it would remove backups, and go on with
// no copy all the same. The copy's size is at least max(liveDBSize, newestBackup * 1.25).
func (r *restorer) olderRoom(ctx context.Context, release string) error {
	var live uint64
	if r.dbSize != nil {
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		sz, err := r.dbSize(sctx)
		cancel()
		if err == nil && sz > 0 {
			live = uint64(sz)
		}
	}
	var err error
	if live > 0 {
		err = roomNowAtLeast(r.db.Backups, r.room, live)
	} else {
		err = roomNow(r.db.Backups, r.room)
	}
	if err == nil {
		return nil
	}
	free := "Free some space there"
	var room *pgbundle.NoRoomError
	if errors.As(err, &room) && room.Need > room.Free {
		free = fmt.Sprintf("Free %d MB more there", (room.Need-room.Free+1<<20-1)>>20)
	}
	return refuse("restore_older_no_room", "%s, the release that puts this backup back, first takes a copy of the library "+
		"database by its own rules (which may also remove other backups over their share of the disk, their number or their age), "+
		"and there is no room for it (%v). Nothing was changed. %s (delete backups you no longer "+
		"need: `maktaba-server backups delete <name>`), or put back a backup that %s, the release running now, puts back itself", release, err, free, r.running())
}

// unprotectedAdvice describes the actionable fix for a KeepForRestore failure without
// blaming hard-link support when the underlying error is disk space, read-only storage, or
// permissions.
func unprotectedAdvice(err error) string {
	var linkErr *os.LinkError
	isLink := errors.As(err, &linkErr)
	switch {
	case errors.Is(err, syscall.ENOSPC):
		return "The backups' disk is full: free some space there, or put back the newest pre-<from>-to-<to> backup"
	case errors.Is(err, syscall.EROFS):
		return "The backups' disk is read-only: make it writable, or put back the newest pre-<from>-to-<to> backup"
	case errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.ENOSYS) ||
		errors.Is(err, syscall.EXDEV) || errors.Is(err, syscall.EMLINK) || (isLink && errors.Is(err, syscall.EPERM)):
		return "Put back the newest pre-<from>-to-<to> backup, or move the backups to a disk that has hard links and try again"
	case errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EACCES):
		return "Maktaba does not have permission to write in the backups folder: fix its permissions, or put back the newest pre-<from>-to-<to> backup"
	default:
		return "Fix the backups folder error above, or put back the newest pre-<from>-to-<to> backup"
	}
}

// protect keeps the backups a restore on the older release pl.Release needs out of its reach
// (pgbundle.KeepForRestore): those asideKeeps names, and no others, whose room stays theirs to
// give. It returns the chosen one's path there. When protection fails: the chosen one's own path,
// when that release never removes it (the newest an update took), else a refusal.
func (r *restorer) protect(pl restorePlan, rec apppayload.Database) (string, error) {
	dir := r.db.Backups
	names := []string{pl.Backup}
	otherRequired := false
	for _, b := range r.list().Backups {
		if slices.Contains(asideKeeps(rec), b.Name) && b.Name != pl.Backup {
			names = append(names, b.Name)
			otherRequired = true
		}
	}
	err := pgbundle.KeepForRestore(dir, names...)
	switch {
	case err == nil:
		return pgbundle.KeptPath(dir, pl.Backup), nil
	case !otherRequired && pgbundle.IsUpdateDump(pl.Backup) && pgbundle.NewestOfKind(dir, pl.Backup):
		return rec.Dump, nil
	}
	return "", refuse("restore_unprotected", "%s, the release that puts this backup back, may remove it first (under its own "+
		"rules, including over the backups' share of the disk, their number or their age), and it cannot be kept from that in %s "+
		"(%v): nothing was changed. %s", pl.Release, dir, err, unprotectedAdvice(err))
}

// copyRoom says why no copy of the database a restore of the backup name replaces could be
// kept now (nil: it can, or none is needed: one is there already, or the database is another
// since).
func (r *restorer) copyRoom(ctx context.Context, name string) error {
	if r.db == nil || !r.copyNeeded(ctx) {
		return nil
	}
	rec, _, _ := apppayload.ReadDatabase(r.stateDir)
	return roomKeeping(r.db.Backups, r.room, append(asideKeeps(rec), name)...)
}

// copyNeeded reports whether a restore takes a copy of the database it replaces: not when there
// is one already, or the database is another since.
func (r *restorer) copyNeeded(ctx context.Context) bool {
	old, had, err := apppayload.ReadDatabase(r.stateDir)
	if err != nil || !had || !asked(old) {
		return true
	}
	old.Aside = pgbundle.MovedDump(r.db.Backups, old.Aside)
	// the copy the record names, or one a release that kept no record of it made, is found
	// and validated before reusing it (replacedDumpThere): the room for one is not asked for
	return replacedDumpThere(ctx, r.db.Backups, old, r.verifyAside) == "" && old.Data != apppayload.DataReplaced && old.Data != apppayload.DataGone
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
	err := pgbundle.HoldBackups(dir)
	if err == nil {
		err = pgbundle.ReturnKept(dir) // what an older release that tried removed
	}
	if err != nil {
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
// page's ways out of it.
func stuckOf(r *restorer) *recovery.Stuck {
	rec, ok, err := apppayload.ReadDatabase(r.stateDir)
	if err != nil || !ok || !asked(rec) {
		return nil
	}
	s := &recovery.Stuck{Backup: filepath.Base(rec.Dump), Why: rec.RestoreError, Data: rec.Data,
		Running: restoreGuardOf(r.stateDir).tryRuns(), CanGiveUp: r.canGiveUp(rec)}
	if s.Data == apppayload.DataUnknown {
		s.Data = apppayload.DataReplaced // what can be done is the same
	}
	s.NoCopy = rec.CopyError != ""
	s.WithoutCopy = s.NoCopy && !newerThan(r.running(), rec.From) // refused there (olderRoom)
	if !s.Running && s.Why == "" && (rec.Before == nil || sameRelease(r.running(), rec.From)) {
		return nil // not tried yet: the restart that was asked does
	}
	if !s.Running && s.Why == "" {
		s.WaitingFor = layout.NormalizeVersion(rec.From)
	}
	if s.CanGiveUp {
		base, release, _ := giveUpTo(rec, r.running())
		if release == "" && base.State != apppayload.DatabaseHeld {
			release = r.running()
		}
		s.Release = layout.NormalizeVersion(release)
	}
	return s
}

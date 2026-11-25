package main

// A restore of one of the home's backups by hand (Epic 35 story 35.5 AC8): the library
// database put back as a dump in the home's backups folder holds it, on the kept release
// whose schema is the dump's (every release refuses a database a newer one migrated).
// One code path, in the home's supervisor: the Updates page asks it through the API
// (admin, audited), `maktaba-server restore --backup <file> --confirm` through its
// loopback admin, or does the same itself when no server runs. It marks the database's
// record restoring with the dump (state/database.json), points the install at the
// matching release and restarts (exit 75): that release's database step puts the dump
// back before any role runs — the put-back a failed update uses (one transaction, the
// machines' copies made anew), with the database it replaces set aside first
// (backups/replaced-<version>-<utc>.dump).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/dbsafe"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/updater"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
)

// restorer restores the home's backups.
type restorer struct {
	stateDir string
	// db is the home's database: its programs read a dump's schema, Backups is its folder
	// (nil: this home has no Postgres of its own to put a dump into, a SQLite one)
	db   *dbsafe.Target
	inst updater.Installer
	// run is the machine's update run (nil or ended: none under way)
	run  func() *updater.Journal
	exit func(code int)
	log  io.Writer
	// schema reads the schema a dump holds (dbsafe's DumpSchema; tests replace it)
	schema func(ctx context.Context, path string) (int64, error)
	// exitDelay is how long a restore lets its answer go out before the home restarts
	// (restoreExitDelay; tests set it to 0)
	exitDelay time.Duration
	// room is what a dump leaves on its disk
	room pgbundle.RoomPolicy
	// identity looks at the library database (dbsafe's Identity; tests replace it)
	identity func(ctx context.Context) (string, int64, error)
	// tell records a restore the Updates page did not ask for (the command line's) where
	// the page's is recorded: this machine's API audits it and tells the machines the home
	// goes away (best effort; nil: nothing to tell)
	tell func(ctx context.Context, pl restorePlan, by string) error
}

// backupInfo is one dump in the home's backups folder.
type backupInfo struct {
	Name  string    `json:"name"`
	Bytes int64     `json:"bytes"`
	At    time.Time `json:"at"`
	// Kind: update (taken before an update's migrations: From is the release whose
	// database it holds, To the update's), failed (the database a failed update left),
	// replaced (the database a restore replaced), daily (the bundled Postgres's own).
	Kind string `json:"kind"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// backupList is what the Updates page shows: the backups, newest first, and the releases
// this home keeps (a backup can be put back on one whose schema is the backup's).
type backupList struct {
	Backups []backupInfo          `json:"backups"`
	Kept    []updater.KeptRelease `json:"kept"`
	Running string                `json:"running"`
	// Restoring is a restore asked and not finished (the next start puts Restoring back).
	Restoring string `json:"restoring,omitempty"`
	// RestoreError is why its last try failed, and Data what the database is since
	// (apppayload's Data*): it is tried again at the next start, replaced by another backup,
	// or given up (CanGiveUp).
	RestoreError string `json:"restore_error,omitempty"`
	Data         string `json:"data,omitempty"`
	CanGiveUp    bool   `json:"can_give_up,omitempty"`
}

// restorePlan is what a restore of a backup does.
type restorePlan struct {
	Backup string `json:"backup"`
	// Schema is the newest migration the dump holds; Release the kept release that runs
	// it (the home restarts on it), Running the one running now.
	Schema  int64  `json:"schema"`
	Release string `json:"release"`
	Running string `json:"running"`
	// At is when the dump was taken: every change since then is lost.
	At time.Time `json:"at"`
	// NoCopy is why no copy of the database it replaces can be kept now ("": it can):
	// CopyFree bytes are free for one, CopyNeed needed. It is then restored only once the
	// person confirms that none is (restoreAsk.withoutCopy). Never on an older release, which
	// is refused instead (olderRoom).
	NoCopy   string `json:"no_copy,omitempty"`
	CopyFree uint64 `json:"copy_free,omitempty"`
	CopyNeed uint64 `json:"copy_need,omitempty"`
}

// restoreError is a refusal with its code (the page says it in its language).
type restoreError struct {
	Code   string
	Detail string
}

func (e *restoreError) Error() string { return e.Code + ": " + e.Detail }

// RestoreCode is its code (the recovery page's).
func (e *restoreError) RestoreCode() string { return e.Code }

func refuse(code, format string, a ...any) error {
	return &restoreError{Code: code, Detail: fmt.Sprintf(format, a...)}
}

func (r *restorer) running() string {
	if r.inst == nil {
		return ""
	}
	return r.inst.Running()
}

// list is the backups folder's dumps and the kept releases.
func (r *restorer) list() backupList {
	out := backupList{Backups: []backupInfo{}, Kept: []updater.KeptRelease{}, Running: r.running()}
	if k, ok := r.inst.(updater.Keeper); ok {
		out.Kept = append(out.Kept, k.Kept()...)
	}
	if rec, ok, _ := apppayload.ReadDatabase(r.stateDir); ok && rec.State == apppayload.DatabaseRestoring {
		out.Restoring, out.RestoreError, out.Data = filepath.Base(rec.Dump), rec.RestoreError, rec.Data
		out.CanGiveUp = asked(rec) && r.canGiveUp(rec)
	}
	if r.db == nil {
		return out
	}
	ents, _ := os.ReadDir(r.db.Backups)
	for _, e := range ents {
		info, err := e.Info()
		if err != nil || !pgbundle.IsDump(info) {
			continue
		}
		if b, ok := backupOf(e.Name()); ok {
			b.Bytes, b.At = pgbundle.DumpBytes(filepath.Join(r.db.Backups, e.Name())), info.ModTime().UTC()
			out.Backups = append(out.Backups, b)
		}
	}
	sort.SliceStable(out.Backups, func(i, j int) bool { return out.Backups[i].At.After(out.Backups[j].At) })
	return out
}

// backupOf reads a dump's name (pre-<from>-to-<to>.dump, failed-<v>-<utc>.dump,
// replaced-<v>-<utc>.dump, maktaba-<local time>.dump); ok false for anything else.
func backupOf(name string) (backupInfo, bool) {
	b := backupInfo{Name: name}
	base, ok := strings.CutSuffix(name, ".dump")
	if !ok || strings.HasPrefix(name, ".") {
		return b, false
	}
	switch {
	case strings.HasPrefix(base, "pre-"):
		b.Kind = "update"
		from, to, ok := strings.Cut(strings.TrimPrefix(base, "pre-"), "-to-")
		if ok {
			if from != "unknown" {
				b.From = layout.NormalizeVersion(from)
			}
			b.To = layout.NormalizeVersion(to)
		}
	case strings.HasPrefix(base, "failed-"):
		b.Kind = "failed"
	case strings.HasPrefix(base, pgbundle.ReplacedDumpPrefix):
		b.Kind = "replaced"
	case strings.HasPrefix(base, "maktaba-"):
		b.Kind = "daily"
	default:
		return b, false
	}
	return b, true
}

// path is a backup's file: a plain name of a dump in the backups folder, never a path
// (the request comes from a page).
func (r *restorer) path(name string) (string, os.FileInfo, error) {
	if r.db == nil {
		return "", nil, refuse("restore_no_database", "this home's library database is not a PostgreSQL this "+
			"Maktaba can put a dump back into")
	}
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\:`) || strings.Contains(name, "..") ||
		name == "." {
		return "", nil, refuse("restore_bad_backup", "%q is not the name of a backup in %s", name, r.db.Backups)
	}
	if _, ok := backupOf(name); !ok {
		return "", nil, refuse("restore_bad_backup", "%q is not a backup (a .dump in %s)", name, r.db.Backups)
	}
	// the backups folder's own file, never one outside it
	dir, err := filepath.Abs(r.db.Backups)
	if err != nil {
		return "", nil, refuse("restore_no_backup", "the backups folder %s: %v", r.db.Backups, err)
	}
	p, err := filepath.Abs(filepath.Join(dir, name))
	if err != nil || !strings.HasPrefix(p, dir+string(filepath.Separator)) {
		return "", nil, refuse("restore_bad_backup", "%q is not the name of a backup in %s", name, r.db.Backups)
	}
	st, err := os.Lstat(p)
	if err != nil || !pgbundle.IsDump(st) {
		return "", nil, refuse("restore_no_backup", "there is no backup %s in %s", name, r.db.Backups)
	}
	return p, st, nil
}

// plan says what a restore of the backup name does, or why it cannot be done.
func (r *restorer) plan(ctx context.Context, name string) (restorePlan, error) {
	p, st, err := r.path(name)
	if err != nil {
		return restorePlan{}, err
	}
	if j := r.journal(); j != nil {
		return restorePlan{}, refuse("restore_update_under_way", "an update to %s is under way (%s): cancel it on "+
			"the Updates page, or wait until it ends", j.Job.Version, j.State)
	}
	schema, err := r.schema(ctx, p)
	if err != nil {
		return restorePlan{}, refuse("restore_unreadable", "the backup %s cannot be read: %v", name, err)
	}
	b, _ := backupOf(name)
	var kept []updater.KeptRelease
	if k, ok := r.inst.(updater.Keeper); ok {
		kept = k.Kept()
	}
	rel, err := chooseRelease(kept, schema, b.From)
	if err != nil {
		return restorePlan{}, err
	}
	pl := restorePlan{Backup: name, Schema: schema, Release: rel.Version, Running: r.running(), At: st.ModTime().UTC()}
	if newerThan(r.running(), pl.Release) {
		if err := r.olderRoom(pl.Release); err != nil {
			return pl, err
		}
	} else if err := r.copyRoom(name); err != nil {
		pl.NoCopy = err.Error()
		var room *pgbundle.NoRoomError
		if errors.As(err, &room) {
			pl.CopyFree, pl.CopyNeed = room.Free, room.Need
		}
	}
	return pl, nil
}

// journal is the update run under way, nil when none is.
func (r *restorer) journal() *updater.Journal {
	if r.run == nil {
		return nil
	}
	if j := r.run(); j != nil && !updater.Ended(j.State) {
		return j
	}
	return nil
}

// chooseRelease picks the kept release a dump at schema runs on: one whose migrations
// make exactly that schema, the release the dump was taken from (hint) first, else the
// newest. None: the release to install by hand is named (story 35.5 EC4).
func chooseRelease(kept []updater.KeptRelease, schema int64, hint string) (updater.KeptRelease, error) {
	var match []updater.KeptRelease
	for _, k := range kept {
		if k.Schema > 0 && k.Schema == schema {
			if hint != "" && layout.NormalizeVersion(k.Version) == layout.NormalizeVersion(hint) {
				return k, nil
			}
			match = append(match, k)
		}
	}
	if len(match) > 0 {
		sort.SliceStable(match, func(i, j int) bool { return layout.CompareVersions(match[i].Version, match[j].Version) > 0 })
		return match[0], nil
	}
	install := fmt.Sprintf("a release whose newest migration is %04d", schema)
	if hint != "" {
		install = hint + " (the release the backup was taken from)"
	}
	var have []string
	for _, k := range kept {
		if k.Schema > 0 {
			have = append(have, fmt.Sprintf("%s (%04d)", k.Version, k.Schema))
		}
	}
	held := "none"
	if len(have) > 0 {
		held = strings.Join(have, ", ")
	}
	return updater.KeptRelease{}, refuse("restore_no_release", "no release this home keeps runs a database at %04d "+
		"(it keeps %s): install %s by hand, then restore the backup again", schema, held, install)
}

// restore puts the backup name back: confirmed (the page and the command make the
// person say so), checked as plan checks it, then the record marked, the install pointed
// at the matching release and the home restarted on it. by is who asked; tell: record it
// through the API before the restart (the page's own request is recorded by the API).
func (r *restorer) restore(ctx context.Context, name, by string, confirm, tell bool) (restorePlan, error) {
	return r.restoreAs(ctx, name, by, restoreAsk{confirm: confirm, tell: tell})
}

// restoreAsk is how a restore is asked: confirm (every change since the backup is lost),
// withoutCopy (and no copy of the database it replaces is kept, when there is no room for
// one), tell (recorded through the API here).
type restoreAsk struct{ confirm, withoutCopy, tell bool }

// restoreAs is restore.
func (r *restorer) restoreAs(ctx context.Context, name, by string, ask restoreAsk) (restorePlan, error) {
	if !ask.confirm {
		return restorePlan{}, refuse("restore_not_confirmed", "a restore replaces the library database with the "+
			"backup and loses every change made since it was taken: confirm it")
	}
	pl, err := r.plan(ctx, name)
	if err != nil {
		return pl, err
	}
	if pl.NoCopy != "" && !ask.withoutCopy {
		return pl, refuse("restore_no_copy", "no copy of the library database as it is now can be kept (%s). Nothing was "+
			"changed: free some space, or delete backups you no longer need, and try again; or restore it without keeping a "+
			"copy (maktaba-server restore --without-copy), and the library as it is now is lost for good", pl.NoCopy)
	}
	if err := restoreGuardOf(r.stateDir).exclusive(nil, func() (bool, error) {
		return r.exit != nil, r.mark(ctx, pl, by, ask.withoutCopy)
	}); err != nil {
		return pl, err
	}
	fmt.Fprintf(orDiscardW(r.log), "[serve] restore of %s asked by %s: the home restarts on %s, which puts it back "+
		"before any role runs\n", pl.Backup, by, pl.Release)
	if ask.tell && r.tell != nil {
		if err := r.tell(ctx, pl, by); err != nil {
			fmt.Fprintf(orDiscardW(r.log), "[serve] the restore could not be recorded by the API: %v\n", err)
		}
	}
	r.restart()
	return pl, nil
}

// restoreExitDelay is how long a restore lets its answer go out before the home restarts.
const restoreExitDelay = 2 * time.Second

// mark records the restore and points the install at the plan's release: the next start
// is that release's, and its database step puts the dump back. A restore that is not done
// is replaced by it (the same backup again, or another), whatever the data is; what was
// there before that one stays the way back.
func (r *restorer) mark(ctx context.Context, pl restorePlan, by string, withoutCopy bool) error {
	p := filepath.Join(r.db.Backups, pl.Backup)
	held := r.running()
	old, had, err := apppayload.ReadDatabase(r.stateDir)
	if err != nil {
		return err
	}
	var orig *apppayload.Database
	was := &apppayload.Database{Version: held, State: apppayload.DatabaseMigrated}
	data := apppayload.Database{Data: apppayload.DataUntouched}
	if had {
		o := old
		orig = &o
		switch {
		case asked(old):
			held, was, data = old.Version, old.Before, old
		default:
			b := old
			if b.State == apppayload.DatabaseRestoring { // no person asked: held, as its next start makes it
				b.State = apppayload.DatabaseHeld
				if b.Held == "" {
					b.Held = "db_held_unasked"
				}
			}
			was = &b
			if old.Ran() != "" {
				held = old.Ran()
			}
		}
	}
	target, ok := r.keptRelease(pl.Release)
	if !ok {
		return refuse("restore_no_release", "%s is no longer kept", pl.Release)
	}
	if err := sameMajorRelease(r.stateDir, target.Version, held); err != nil {
		return err
	}
	if settled, serr := settleData(ctx, data, r.probe()); serr == nil {
		data = settled
	}
	var maybe []int64
	if data.Data == apppayload.DataUnknown && asked(old) {
		// what was asked before may be what is there, in part or whole
		maybe = append(append(maybe, data.MaybeSchemas...), r.probe().backupSchema(ctx, old))
	}
	older := newerThan(r.running(), pl.Release)
	if data.Data == apppayload.DataUntouched && data.DataID == "" && older {
		// the database does not run now (offline), and the release that tries is an older one,
		// which may keep no track of the data
		data.Data = apppayload.DataUnknown
	}
	rec := apppayload.Database{Version: held, State: apppayload.DatabaseRestoring, From: pl.Release, Dump: p, By: by,
		Error: "restore asked by " + by, Before: was, Data: data.Data, DataID: data.DataID}
	if asideThere(data) {
		rec.Aside, rec.AsideBytes = data.Aside, data.AsideBytes
	}
	if withoutCopy && !older { // an older release keeps one whatever it is told
		// what was said when it was confirmed, and no more
		switch {
		case pl.NoCopy != "":
			rec.NoCopy = apppayload.NoCopyRoom
		case data.CopyError != "":
			rec.NoCopy = apppayload.NoCopyAny
		}
	}
	slices.Sort(maybe)
	rec.MaybeSchemas = slices.Compact(maybe)
	for _, o := range []*apppayload.Database{orig, was} {
		if o != nil && pgbundle.OriginalPath(o.Dump) == p && o.DumpKeptFor != "" && sameRelease(o.DumpKeptFor, pl.Release) {
			// the dump of a schema kept for the release it goes back for (a held update from a
			// kept schema): put back, it is that schema again, kept for it
			rec.DumpKeptFor = o.DumpKeptFor
		}
	}
	if older {
		if rec.Dump, err = r.protect(pl, rec); err != nil {
			_ = r.holdFor(old) // as it was
			return err
		}
	}
	return r.leaveFor(ctx, rec, orig, pl.Release)
}

func orDiscardW(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

// restoreCode is the code of a restore's refusal ("restore_failed" for anything else).
func restoreCode(err error) string {
	var re *restoreError
	if errors.As(err, &re) {
		return re.Code
	}
	return "restore_failed"
}

// newRestorer is the home's restorer over its database (nil db: none to put a dump into).
func newRestorer(cfg config.Config, db *dbsafe.Target, inst updater.Installer, exit func(int)) *restorer {
	r := &restorer{stateDir: cfg.StateDir(), db: db, inst: inst, exit: exit, log: os.Stderr,
		exitDelay: restoreExitDelay, tell: apiRestoreNotice(cfg), room: roomOf(cfg)}
	if db != nil {
		t := *db
		r.schema, r.identity = t.DumpSchema, t.Identity
	}
	return r
}

// restoreRequest is POST /update/restore's body.
type restoreRequest struct {
	Backup  string `json:"backup"`
	By      string `json:"by"`
	Confirm bool   `json:"confirm"`
	// WithoutCopy: confirmed too that no copy is kept of the database it replaces, when there
	// is no room for one
	WithoutCopy bool `json:"without_copy,omitempty"`
	// Page: the Updates page asks (through the API, which audits it and tells the
	// machines itself); otherwise the command line, recorded through the API here
	Page bool `json:"page,omitempty"`
}

// serveRestore serves /update/backups (GET: the backups and the kept releases) and
// /update/restore (GET ?backup=: what restoring it does; POST restoreRequest: do it) on
// the supervisor's loopback admin (the local token checked by adminHandler).
func (u *updates) serveRestore(w http.ResponseWriter, r *http.Request, name string) {
	w.Header().Set("Content-Type", "application/json")
	answer := func(code int, doc map[string]any) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(doc)
	}
	if u.r == nil {
		answer(http.StatusNotFound, map[string]any{"code": "restore_not_home",
			"error": "a machine of a home keeps no backups of its own: the home's are restored on the home"})
		return
	}
	refused := func(pl restorePlan, err error) {
		answer(http.StatusConflict, map[string]any{"code": restoreCode(err), "error": err.Error(), "plan": pl})
	}
	switch {
	case name == "backups" && r.Method == http.MethodGet:
		answer(http.StatusOK, map[string]any{"backups": u.r.list()})
	case name == "restore" && r.Method == http.MethodGet:
		pl, err := u.r.plan(r.Context(), r.URL.Query().Get("backup"))
		if err != nil {
			refused(pl, err)
			return
		}
		answer(http.StatusOK, map[string]any{"plan": pl})
	case name == "restore-abandon" && r.Method == http.MethodPost:
		var req restoreRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || !req.Confirm {
			answer(http.StatusBadRequest, map[string]any{"code": "bad_request", "error": "confirm it"})
			return
		}
		by := strings.TrimSpace(req.By)
		if by == "" {
			by = "an admin"
		}
		if err := u.r.abandon(r.Context(), by); err != nil {
			refused(restorePlan{}, err)
			return
		}
		answer(http.StatusAccepted, map[string]any{"abandoned": true})
	case name == "restore" && r.Method == http.MethodPost:
		var req restoreRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			answer(http.StatusBadRequest, map[string]any{"code": "bad_request", "error": err.Error()})
			return
		}
		by := strings.TrimSpace(req.By)
		if by == "" {
			by = "an admin"
		}
		pl, err := u.r.restoreAs(r.Context(), req.Backup, by, restoreAsk{confirm: req.Confirm, withoutCopy: req.WithoutCopy, tell: !req.Page})
		if err != nil {
			refused(pl, err)
			return
		}
		answer(http.StatusAccepted, map[string]any{"plan": pl})
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

// apiRestoreNotice tells this machine's API of a restore the command line asked for
// (POST /internal/updates/restored, with this supervisor's per-boot local token): it is
// audited as the page's is and the machines are told the home goes away.
func apiRestoreNotice(cfg config.Config) func(ctx context.Context, pl restorePlan, by string) error {
	return func(ctx context.Context, pl restorePlan, by string) error {
		tok, err := os.ReadFile(supervisor.LocalTokenPath(cfg))
		if err != nil {
			return err
		}
		body, _ := json.Marshal(map[string]any{"backup": pl.Backup, "release": pl.Release, "schema": pl.Schema,
			"running": pl.Running, "backup_at": pl.At, "by": by})
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		url := "http://" + supervisor.LocalAddr(cfg.Server.Listen, ports.APIPublic.PortString()) + "/internal/updates/restored"
		req, err := http.NewRequestWithContext(cctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Maktaba-Local-Token", strings.TrimSpace(string(tok)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("the API answered %d", resp.StatusCode)
		}
		return nil
	}
}

// sameMajorRelease refuses a restore onto a release that runs on another cluster than the
// one in use (Epic 49 story 49.2): on a home upgraded to PostgreSQL 18, a release that ran
// on 16 (the last one the 16 cluster's own record names, database.json, or one before it)
// would start on the 16 cluster, which the upgrade left as it was, and never put the dump
// back (its record is not the one marked): the home would run on its data from before the
// upgrade, saying it restored.
func sameMajorRelease(stateDir, release, running string) error {
	if apppayload.ClusterMajorIn(stateDir) != "18" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(stateDir, "state", apppayload.ClusterRecordName("")))
	if err != nil {
		if _, err := os.Stat(filepath.Join(stateDir, "postgres", "PG_VERSION")); err == nil {
			// a 16 cluster with no record of its own (an install from before the record): which
			// releases ran on it cannot be told; the one running now runs on 18, so a release
			// older than it may be one that ran on 16, and is refused
			if running != "" && apppayload.Older(release, running) {
				return refuse("restore_other_major", "the library database runs on PostgreSQL 18 since its upgrade, "+
					"and its PostgreSQL 16 cluster keeps no record of the releases that ran on it: %s, older than %s, "+
					"may be one of them; restore a backup made since the upgrade", release, running)
			}
		}
		return nil // no 16 cluster: no release ran on 16 here
	}
	var rec16 apppayload.Database
	if json.Unmarshal(raw, &rec16) != nil || rec16.Ran() == "" {
		return nil
	}
	if last := rec16.Ran(); !apppayload.Older(last, release) {
		return refuse("restore_other_major", "the library database runs on PostgreSQL 18 since its upgrade, and %s "+
			"runs on PostgreSQL 16 (%s is the last release that ran on it): restore a backup made since the upgrade",
			release, last)
	}
	return nil
}

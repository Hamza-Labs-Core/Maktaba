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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/dbsafe"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/updater"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
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
	now    func() time.Time
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
}

// restoreError is a refusal with its code (the page says it in its language).
type restoreError struct {
	Code   string
	Detail string
}

func (e *restoreError) Error() string { return e.Code + ": " + e.Detail }

func refuse(code, format string, a ...any) error {
	return &restoreError{Code: code, Detail: fmt.Sprintf(format, a...)}
}

func (r *restorer) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
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
		out.Restoring = filepath.Base(rec.Dump)
	}
	if r.db == nil {
		return out
	}
	ents, _ := os.ReadDir(r.db.Backups)
	for _, e := range ents {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if b, ok := backupOf(e.Name()); ok {
			b.Bytes, b.At = info.Size(), info.ModTime().UTC()
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
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", nil, refuse("restore_bad_backup", "%q is not the name of a backup in %s", name, r.db.Backups)
	}
	if _, ok := backupOf(name); !ok {
		return "", nil, refuse("restore_bad_backup", "%q is not a backup (a .dump in %s)", name, r.db.Backups)
	}
	p := filepath.Join(r.db.Backups, name)
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() {
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
	return restorePlan{Backup: name, Schema: schema, Release: rel.Version, Running: r.running(), At: st.ModTime().UTC()}, nil
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
// at the matching release and the home restarted on it. by is who asked.
func (r *restorer) restore(ctx context.Context, name, by string, confirm bool) (restorePlan, error) {
	if !confirm {
		return restorePlan{}, refuse("restore_not_confirmed", "a restore replaces the library database with the "+
			"backup and loses every change made since it was taken: confirm it")
	}
	pl, err := r.plan(ctx, name)
	if err != nil {
		return pl, err
	}
	if err := r.mark(pl, by); err != nil {
		return pl, err
	}
	fmt.Fprintf(orDiscardW(r.log), "[serve] restore of %s asked by %s: the home restarts on %s, which puts it back "+
		"before any role runs\n", pl.Backup, by, pl.Release)
	if r.exit != nil {
		go func() {
			time.Sleep(restoreExitDelay) // the answer goes out first
			r.exit(75)
		}()
	}
	return pl, nil
}

// restoreExitDelay is how long a restore lets its answer go out before the home restarts.
var restoreExitDelay = 2 * time.Second

// mark records the restore and points the install at the plan's release: the next start
// is that release's, and its database step puts the dump back.
func (r *restorer) mark(pl restorePlan, by string) error {
	p := filepath.Join(r.db.Backups, pl.Backup)
	held := r.running()
	old, had, err := apppayload.ReadDatabase(r.stateDir)
	if err != nil {
		return err
	}
	if had {
		if old.State == apppayload.DatabaseRestoring {
			return refuse("restore_under_way", "the library database is being put back from %s already: its next "+
				"start finishes that", old.Dump)
		}
		if old.Version != "" {
			held = old.Version
		}
	}
	var target updater.KeptRelease
	if k, ok := r.inst.(updater.Keeper); ok {
		for _, kr := range k.Kept() {
			if kr.Version == pl.Release && (target.Version == "" || kr.Running) {
				target = kr
			}
		}
	}
	if target.Version == "" {
		return refuse("restore_no_release", "%s is no longer kept", pl.Release)
	}
	rec := apppayload.Database{Version: held, State: apppayload.DatabaseRestoring, From: pl.Release, Dump: p, By: by,
		Error: "restore asked by " + by}
	if err := apppayload.WriteDatabase(r.stateDir, rec); err != nil {
		return err
	}
	if err := r.inst.(updater.Keeper).RunKept(context.Background(), target); err != nil {
		// nothing is restarted: the record goes back to how it was, so no start puts the
		// dump back on a release that cannot run it
		if had {
			_ = apppayload.WriteDatabase(r.stateDir, old)
		} else {
			_ = os.Remove(apppayload.DatabasePath(r.stateDir))
		}
		return refuse("restore_switch_failed", "%s cannot be made the release that starts: %v", pl.Release, err)
	}
	return nil
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
	r := &restorer{stateDir: cfg.StateDir(), db: db, inst: inst, exit: exit, log: os.Stderr}
	if db != nil {
		t := *db
		r.schema = t.DumpSchema
	}
	return r
}

// restoreRequest is POST /update/restore's body.
type restoreRequest struct {
	Backup  string `json:"backup"`
	By      string `json:"by"`
	Confirm bool   `json:"confirm"`
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
		pl, err := u.r.restore(r.Context(), req.Backup, by, req.Confirm)
		if err != nil {
			refused(pl, err)
			return
		}
		answer(http.StatusAccepted, map[string]any{"plan": pl})
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

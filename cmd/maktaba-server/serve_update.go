package main

// Automatic updates in the supervisor (Epic 35 stories 35.4–35.7, 35.12;
// specs/epics/35-cluster-full-nodes-and-updates/install-kinds.md): this machine's part
// of a run the home's API drives. The home's API orders the home's own supervisor on its
// loopback admin (/update/prepare, /update/apply, /update/status, /update/abort,
// /update/rollback, with the per-boot local token); a node's machine agent takes the same
// as update.* commands on the cluster channel. internal/updater runs them over the
// install kind this server runs as.

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appvenv"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/dbsafe"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/machineagent"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/updater"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// envLauncherRoot is the launcher's state root, set by a launcher that keeps the
// update guard (app bundle, runtime copy): where pending.json and payload-override go.
const envLauncherRoot = "MAKTABA_LAUNCHER_ROOT"

// envRuntimeOwner is who owns a Linux runtime copy, set by its launcher: "app" (the
// desktop app's: it asks first) or "headless".
const envRuntimeOwner = "MAKTABA_RUNTIME_OWNER"

// runtimeOwner is the runtime's owner ("app" unless the launcher says headless).
func runtimeOwner() string {
	if strings.TrimSpace(os.Getenv(envRuntimeOwner)) == "headless" {
		return "headless"
	}
	return "app"
}

// envAppBundled is the app's own payload, set by a launcher that runs a kept copy in
// its place (a rolled-back app update).
const envAppBundled = "MAKTABA_APP_BUNDLED"

// updates is serve's update machinery.
type updates struct {
	m *updater.Machine
	// health's view of this machine, set once the supervisor exists
	mu   sync.Mutex
	snap func() supervisor.Snapshot
}

// installKind picks this server's installer (install-kinds.md): the desktop app's
// bundle, the Linux runtime copy, the side-by-side layout, or a flat install that
// cannot update itself.
func installKind(cfg config.Config, releaseDir string, home bool, src updater.Source) (updater.Installer, string) {
	record := ""
	if home {
		record = apppayload.DatabasePath(cfg.StateDir())
	}
	lroot := strings.TrimSpace(os.Getenv(envLauncherRoot))
	if bundle := config.AppBundle(); bundle != "" {
		if rt, ok := appruntime.Of(bundle); ok || filepath.Base(bundle) == appruntime.CurrentLink {
			if !ok {
				rt = appruntime.New(filepath.Dir(bundle))
			}
			return &updater.RuntimeInstaller{Runtime: rt, Source: src, Version: version.Version, LauncherRoot: lroot,
				Owner: runtimeOwner(), Record: record, Open: updater.OpenURL}, filepath.Join(orDir(lroot, cfg.StateDir()), "updates")
		}
		bundled := strings.TrimSpace(os.Getenv(envAppBundled))
		if bundled == "" {
			bundled = bundle
		}
		return &updater.AppInstaller{Payload: bundle, Bundled: bundled, App: appOf(bundled), LauncherRoot: lroot,
				Source: src, Version: version.Version, Record: record, Rehearse: home, Open: updater.OpenURL},
			filepath.Join(orDir(lroot, cfg.StateDir()), "updates")
	}
	if releaseDir != "" {
		l := layout.New(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Clean(releaseDir)))))
		return &updater.LayoutInstaller{Layout: l, Source: src, Version: filepath.Base(filepath.Clean(releaseDir)),
			Record: record, PipelinePython: strings.TrimSpace(cfg.Pipeline.Python)}, l.UpdatesDir()
	}
	return flatInstaller{}, filepath.Join(cfg.StateDir(), "updates")
}

// updatesDirOf is this install's updates folder (installKind's: the journal, the app's
// order, the update attempts): the launcher root's (or the state root's) for the desktop
// app and the runtime copy, the layout's for a plain server, the state root's for a flat
// install.
func updatesDirOf(cfg config.Config, releaseDir string) string {
	if config.AppBundle() != "" {
		return filepath.Join(orDir(strings.TrimSpace(os.Getenv(envLauncherRoot)), cfg.StateDir()), "updates")
	}
	if releaseDir != "" {
		return layout.New(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Clean(releaseDir))))).UpdatesDir()
	}
	return filepath.Join(cfg.StateDir(), "updates")
}

func orDir(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// appOf is the app a payload is the bundle's of: …/Maktaba.app (from
// Contents/Resources/server), or <install>\Maktaba.exe.
func appOf(payload string) string {
	up := filepath.Dir(filepath.Clean(payload)) // Resources, or <install>
	if filepath.Base(up) == "Resources" {
		return filepath.Dir(filepath.Dir(up))
	}
	return filepath.Join(up, "Maktaba.exe")
}

// flatInstaller is an install the supervisor cannot update by itself: binaries copied
// by hand with no launcher (one command converts it: `maktaba-server setup --service`).
type flatInstaller struct{}

var errNotSelfManaged = updater.Fail("not_self_managed", "this install has no launcher to fall back with: "+
	"run `maktaba-server setup --service` once, and it updates itself from then on")

func (flatInstaller) Kind() string    { return "flat" }
func (flatInstaller) Running() string { return layout.NormalizeVersion(version.Version) }
func (flatInstaller) Preflight(context.Context, *updater.Journal) error {
	return errNotSelfManaged
}
func (flatInstaller) Stage(context.Context, *updater.Journal, io.Writer) error {
	return errNotSelfManaged
}
func (flatInstaller) Smoke(context.Context, *updater.Journal) error  { return errNotSelfManaged }
func (flatInstaller) Tools(*updater.Journal) (string, string)        { return "", "" }
func (flatInstaller) Switch(context.Context, *updater.Journal) error { return errNotSelfManaged }
func (flatInstaller) Restart(context.Context, *updater.Journal, func(int)) error {
	return errNotSelfManaged
}
func (flatInstaller) Revert(context.Context, *updater.Journal) error  { return nil }
func (flatInstaller) Commit(context.Context, *updater.Journal) error  { return nil }
func (flatInstaller) Discard(context.Context, *updater.Journal) error { return nil }

// updateSource is the pinned repository and the trusted root (refreshed through TUF
// into <data_dir>/sigstore, as the API's).
func updateSource(cfg config.Config) updater.Source {
	id, err := releaseverify.IdentityFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[serve] update: %v: the default repository is used\n", err)
		id, _ = releaseverify.NewIdentity("")
	}
	store := &releaseverify.RootStore{Dir: filepath.Join(cfg.Server.DataDir, "sigstore")}
	return updater.Source{Identity: id, HTTP: &http.Client{Timeout: 30 * time.Minute},
		Trusted: func(ctx context.Context) (root.TrustedMaterial, error) {
			if err := store.Refresh(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "[serve] update: the Sigstore trusted root was not refreshed (%v): the last good one\n", err)
			}
			tm, _, err := store.TrustedMaterial()
			return tm, err
		}}
}

// homeDatabase is the home's database for the rehearsal and the failed state's dump:
// the bundled Postgres (its superuser and programs), or the operator's (its URL, the
// pg programs on PATH or in [postgres] bin_dir); nil on a node or a SQLite home.
func homeDatabase(cfg config.Config, pg *pgbundle.Bundle) *dbsafe.Target {
	if pg != nil {
		u, err := pg.SuperURL()
		if err != nil {
			return nil
		}
		return &dbsafe.Target{URL: u, Bin: pg.Bin, Backups: pg.Backups, Data: pg.Data}
	}
	u := strings.TrimSpace(cfg.Database.URL)
	if u == "" || (!strings.HasPrefix(u, "postgres://") && !strings.HasPrefix(u, "postgresql://")) {
		return nil
	}
	return &dbsafe.Target{URL: u, Bin: pgProgram, Backups: filepath.Join(cfg.Server.DataDir, "backups")}
}

// pgProgram finds a PostgreSQL client program: MAKTABA_PG_BIN (a directory), else PATH.
func pgProgram(name string) string {
	if dir := strings.TrimSpace(os.Getenv("MAKTABA_PG_BIN")); dir != "" {
		return filepath.Join(dir, layout.Exe(name))
	}
	return ""
}

// rehearser is updater.Rehearser over a dbsafe.Target.
type rehearser struct{ t dbsafe.Target }

func (r rehearser) Check(ctx context.Context, rehearse bool, maxGB int) error {
	if err := r.t.Check(ctx); err != nil {
		return codedFailure(err)
	}
	return codedFailure(r.t.Space(ctx, rehearse, maxGB))
}

func (r rehearser) Rehearse(ctx context.Context, api, migrations string, maxGB int, log io.Writer) (updater.Rehearsal, error) {
	res, err := r.t.Rehearse(ctx, api, migrations, maxGB, log)
	return updater.Rehearsal{Skipped: res.Skipped, DBBytes: res.DBBytes, MigrateSec: res.MigrateSec, Pending: res.Pending},
		codedFailure(err)
}

func (r rehearser) DumpAside(ctx context.Context, label string) (string, error) {
	p := filepath.Join(r.t.Backups, label+"-"+time.Now().UTC().Format("20060102T150405")+".dump")
	return p, r.t.Dump(ctx, p)
}

// PruneBackups keeps rules.updates.backups_keep dumps of each kind once an update
// committed, and records it for the dump pruning at the next start (updater.BackupPruner).
func (r rehearser) PruneBackups(keep int) error {
	if err := pgbundle.SetBackupsKeep(r.t.Backups, keep); err != nil {
		return err
	}
	return pgbundle.PruneBackups(r.t.Backups, "", keep)
}

// codedFailure turns dbsafe's "code: detail" errors into the run's failures.
func codedFailure(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if code, detail, ok := strings.Cut(msg, ": "); ok && code != "" && strings.Trim(code, "abcdefghijklmnopqrstuvwxyz_") == "" {
		return &updater.Failure{Code: code, Detail: detail}
	}
	return &updater.Failure{Code: "database", Detail: msg}
}

// recordFile is updater.Records over the database step's record.
type recordFile string

func (r recordFile) MarkRestore(version, _, reason string) (bool, error) {
	return layout.MarkRecordRestoring(string(r), version, reason)
}
func (r recordFile) Migrated(version string) bool { return layout.RecordMigrated(string(r), version) }
func (r recordFile) Unmark(version string) error {
	_, err := layout.UnmarkRecordRestoring(string(r), version)
	return err
}

// startUpdates makes the machine and takes up the run the last start left. exit stops
// serve with a code (75).
func startUpdates(ctx context.Context, cfg config.Config, releaseDir string, home bool, pg *pgbundle.Bundle,
	node *nodeServices, exit func(int)) *updates {
	src := updateSource(cfg)
	inst, dir := installKind(cfg, releaseDir, home, src)
	u := &updates{}
	m := &updater.Machine{Dir: dir, Installer: inst, Exit: exit, Log: os.Stderr}
	if home {
		m.Records = recordFile(apppayload.DatabasePath(cfg.StateDir()))
		if t := homeDatabase(cfg, pg); t != nil {
			m.Rehearser = rehearser{*t}
			go func() { // a crash during a rehearsal left its scratch database
				dctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				defer cancel()
				_ = t.DropScratch(dctx)
			}()
		}
	}
	m.Health = &machineHealth{cfg: cfg, home: home, node: node, releaseDir: releaseDir, snap: u.snapshot}
	m.TVOnAir = func() bool {
		snap, ok := u.snapshot()
		if !ok {
			return true // not known: not now
		}
		for _, p := range snap.Processes {
			if p.Role == supervisor.RoleTVSlot && p.State == supervisor.StateRunning {
				return true
			}
		}
		return false
	}
	u.m = m
	m.Start(ctx)
	m.Resume()
	fmt.Fprintf(os.Stderr, "[serve] updates: %s install (%s)\n", inst.Kind(), dir)
	return u
}

func (u *updates) setSupervisor(sup *supervisor.Supervisor) {
	u.mu.Lock()
	u.snap = sup.Snapshot
	u.mu.Unlock()
}

func (u *updates) snapshot() (supervisor.Snapshot, bool) {
	u.mu.Lock()
	f := u.snap
	u.mu.Unlock()
	if f == nil {
		return supervisor.Snapshot{}, false
	}
	return f(), true
}

// agentState adds this machine's run to the machine agent's update.state.
func (u *updates) agentState(st *machineagent.UpdateState) {
	s := u.m.Status()
	st.Layout, st.Attempts = s.Kind, s.Attempts
	if s.Journal == nil {
		return
	}
	j := s.Journal
	if j.State == updater.StatePrepared {
		st.PreparedVersion = j.Job.Version
	}
	rs := &machineagent.RunState{RunID: j.Job.RunID, Version: j.Job.Version, State: j.State, Step: j.Step}
	if j.Error != nil {
		rs.Error = j.Error.Error()
	}
	st.Run = rs
}

// ---------- requests ----------

// updateRequest is a request of the home's (loopback or update.* command).
type updateRequest struct {
	RunID string `json:"run_id"`
	updater.Job
}

// handle runs a request of the home's; the answer is the machine's status.
func (u *updates) handle(name string, body json.RawMessage) (any, int, error) {
	var req updateRequest
	if len(body) > 0 && string(body) != "null" {
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, http.StatusBadRequest, fmt.Errorf("bad request: %w", err)
		}
	}
	if req.Job.RunID == "" {
		req.Job.RunID = req.RunID
	}
	var err error
	switch name {
	case "prepare":
		err = u.m.Prepare(req.Job)
	case "apply":
		err = u.m.Apply(req.Job.RunID)
	case "abort":
		err = u.m.Abort(req.Job.RunID)
	case "rollback":
		err = u.m.RollbackCommitted(req.Job.RunID)
	case "status":
	default:
		return nil, http.StatusNotFound, fmt.Errorf("no such request %q", name)
	}
	if err != nil {
		code := http.StatusConflict
		var f *updater.Failure
		if errors.As(err, &f) && f.Code == "bad_job" {
			code = http.StatusBadRequest
		}
		return u.m.Status(), code, err
	}
	return u.m.Status(), http.StatusOK, nil
}

// adminHandler adds /update/* to the supervisor's loopback admin (the per-boot local
// token as a bearer; GET for status).
func (u *updates) adminHandler(base http.Handler, token string) http.Handler {
	if u == nil {
		return base
	}
	mux := http.NewServeMux()
	mux.Handle("/", base)
	mux.HandleFunc("/update/", func(w http.ResponseWriter, r *http.Request) {
		if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/update/")
		if name == "status" && r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		if name == "log" {
			u.serveLog(w, r)
			return
		}
		if name != "status" && r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		out, code, err := u.handle(name, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		doc := map[string]any{"status": out}
		if err != nil {
			doc["error"] = err.Error()
		}
		_ = json.NewEncoder(w).Encode(doc)
	})
	return mux
}

// serveLog serves a run's log (logs/<run>.log; ?run=).
// runIDRe is a run id as the log endpoint takes it (a UUID's characters: no path).
var runIDRe = regexp.MustCompile(`^[0-9A-Za-z-]{1,64}$`)

func (u *updates) serveLog(w http.ResponseWriter, r *http.Request) {
	run := r.URL.Query().Get("run")
	if !runIDRe.MatchString(run) {
		http.Error(w, "bad run", http.StatusBadRequest)
		return
	}
	f, err := os.Open(filepath.Join(u.m.Dir, "logs", run+".log"))
	if err != nil {
		http.Error(w, "no log", http.StatusNotFound)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.Copy(w, io.LimitReader(f, 8<<20))
}

// command answers the home's update.* commands on the cluster channel (a node's).
func (u *updates) command(_ context.Context, name string, args json.RawMessage) (handled, ok bool, detail any) {
	op, isUpdate := strings.CutPrefix(name, "update.")
	if !isUpdate || u == nil {
		return false, false, nil
	}
	out, _, err := u.handle(op, args)
	if err != nil {
		return true, false, map[string]any{"error": err.Error(), "status": out}
	}
	return true, true, map[string]any{"status": out}
}

// ---------- health ----------

// machineHealth judges the release that came up (design §5.4): every supervised role
// running and none crash-looping or halted, the pipeline and the api answering with the
// new version, the api ready, and on a node its schema guard content and its machine
// agent on the channel.
type machineHealth struct {
	cfg        config.Config
	home       bool
	node       *nodeServices
	releaseDir string // the running release's (a plain server's bin/releases/<v>)
	snap       func() (supervisor.Snapshot, bool)
	http       *http.Client
	// offSince is when each machine first said it cannot log in to this home's database
	// in this watch (kept when it is back meanwhile: a machine that starts again, or
	// flaps, does not wait anew); offTold: its wait ended and was logged
	offSince map[string]time.Time
	offTold  map[string]bool
	now      func() time.Time
}

// machinesWait is how long a home's watch waits for its machines to follow its database
// (the home re-makes their access as soon as they say they cannot log in): an update that
// moved the database carries them along before it commits, and a machine that still
// cannot after this is shown as needing a person rather than holding the home's update.
const machinesWait = 10 * time.Minute

func (h *machineHealth) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// machinesOff is the home's watch waiting for its machines that cannot log in to its
// database (pending, while their access is re-made and they follow it): each machine
// holds it for machinesWait at most from the first time it said so in this watch, however
// often it starts again or comes and goes meanwhile; after that it is shown as needing a
// person and no longer holds the home's update.
func (h *machineHealth) machinesOff(off []selfMachineDB) (updater.Verdict, bool) {
	if h.offSince == nil {
		h.offSince, h.offTold = map[string]time.Time{}, map[string]bool{}
	}
	now := h.clock()
	var waiting, gone []string
	for _, m := range off {
		name := m.Name
		if name == "" {
			name = m.ID
		}
		state := m.Code
		if m.Healing {
			state += ", following its new access"
		}
		first, seen := h.offSince[m.ID]
		if !seen {
			first = now
			h.offSince[m.ID] = now
		}
		if now.Sub(first) >= machinesWait {
			if !h.offTold[m.ID] {
				h.offTold[m.ID] = true
				gone = append(gone, name+" ("+state+")")
			}
			continue
		}
		waiting = append(waiting, name+" ("+state+")")
	}
	if len(gone) > 0 {
		fmt.Fprintf(os.Stderr, "[update] machines still cannot log in to this home's database after %s: %s; "+
			"the update goes on, the Updates page shows them\n", machinesWait, strings.Join(gone, ", "))
	}
	if len(waiting) == 0 {
		return updater.Verdict{}, false
	}
	why := "the machines follow this home's database: " + strings.Join(waiting, ", ")
	return updater.Verdict{Pending: true, Why: why, Progress: why}, true
}

// selfMachineDB is a machine the home's /api/cluster/self says cannot log in to its
// database.
type selfMachineDB struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Code    string `json:"code"`
	Healing bool   `json:"healing"`
}

// haltCodes are database-step details no retry mends: the release cannot run here.
var haltCodes = map[string]bool{"db_restored": true, "db_restore_failed": true, "db_ahead": true,
	"db_refused_older": true, "db_no_cluster": true, "pg_major": true}

// startingCodes are the start steps at work (detail_codes.json): the database step's
// dump and migrations, the Python environment being built. While they make progress the
// release is starting, not unhealthy (the watch bounds them: updater.pendingCap).
var startingCodes = map[string]bool{"db_waiting": true, "db_dumping": true, "db_migrating": true,
	"db_checking": true, "db_restoring": true, "venv_waiting": true, "venv_building": true}

// unmeasuredCodes are the start steps whose progress cannot be seen while they work: a
// migration's statement (an index built for an hour on a large home), a dump put back.
// Only the watch's cap bounds them (updater.Verdict.Unmeasured); a migration's lock wait is
// bounded by its lock timeout.
var unmeasuredCodes = map[string]bool{"db_migrating": true, "db_checking": true, "db_restoring": true}

func (h *machineHealth) client() *http.Client {
	if h.http != nil {
		return h.http
	}
	return &http.Client{Timeout: 3 * time.Second}
}

// Check implements updater.Health.
func (h *machineHealth) Check(ctx context.Context, want string) updater.Verdict {
	snap, ok := h.snap()
	if !ok {
		return updater.Verdict{Why: "the supervisor is starting"}
	}
	hasAPI := false
	var procs []supervisor.ChildStatus
	for _, p := range snap.Processes {
		if p.Role == supervisor.RoleAPI {
			hasAPI = true
		}
		if p.Instance != "" && !p.Wanted {
			continue // an on-demand process nobody asked for
		}
		if haltCodes[p.DetailCode] {
			return updater.Verdict{Fatal: true, Why: fmt.Sprintf("%s: %s", childName(p), p.Detail)}
		}
		if p.CrashLoop {
			return updater.Verdict{Fatal: true, Why: childName(p) + " crash-loops"}
		}
		procs = append(procs, p)
	}
	var pending []supervisor.ChildStatus
	for _, p := range procs {
		if p.State != supervisor.StateRunning && startingCodes[p.DetailCode] {
			pending = append(pending, p)
		}
	}
	if len(pending) > 0 {
		// every waiting process's detail: it moves while the step works (the dump's size,
		// the migrations done, the environment's install), in whichever process runs it
		v := updater.Verdict{Pending: true, Why: fmt.Sprintf("%s: %s", childName(pending[0]), pending[0].Detail)}
		var progress strings.Builder
		for _, p := range pending {
			progress.WriteString(childName(p) + ": " + p.Detail + "\n")
			v.Unmeasured = v.Unmeasured || unmeasuredCodes[p.DetailCode]
		}
		v.Progress = progress.String()
		return v
	}
	for _, p := range procs {
		if p.State != supervisor.StateRunning {
			return updater.Verdict{Why: fmt.Sprintf("%s is %s %s", childName(p), p.State, p.Detail)}
		}
	}
	if v := h.pipelineVersion(ctx, procs); v.Why != "" {
		return v
	}
	if !hasAPI {
		return updater.Verdict{Healthy: true}
	}
	admin := "http://" + supervisor.LocalAddr(h.cfg.Server.AdminAddr, "9100")
	if code := h.get(ctx, admin+"/readyz", nil); code != http.StatusOK {
		return updater.Verdict{Why: fmt.Sprintf("api /readyz answers %d", code)}
	}
	var self struct {
		Version    string          `json:"version"`
		Paused     string          `json:"paused"`
		MachinesDB []selfMachineDB `json:"machines_db"`
	}
	api := "http://" + supervisor.LocalAddr(h.cfg.Server.Listen, "8080")
	if code := h.getSelf(ctx, api+"/api/cluster/self", &self); code != http.StatusOK {
		return updater.Verdict{Why: fmt.Sprintf("api /api/cluster/self answers %d", code)}
	}
	if self.Version != "" && layout.CompareVersions(self.Version, want) != 0 {
		return updater.Verdict{Fatal: true, Why: fmt.Sprintf("the api answers as %s, not %s", self.Version, want)}
	}
	if h.home {
		if v, wait := h.machinesOff(self.MachinesDB); wait {
			return v
		}
	}
	if h.node != nil {
		if self.Paused != "" {
			return updater.Verdict{Why: "the node is paused: " + self.Paused}
		}
		if h.node.agent != nil && !h.node.agent.Status().Connected {
			return updater.Verdict{Why: "the machine agent is not on the channel yet"}
		}
	}
	return updater.Verdict{Healthy: true}
}

// pipelineVersion checks that every pipeline process runs this release's pipeline (story
// 35.4 AC7): its /healthz says the version of the release's wheel (PEP 440's spelling,
// as its `--version`). Only where the pipeline runs the release's own environment
// (supervisor.PipelineOwnEnv); a pipeline from before it said its version says none.
func (h *machineHealth) pipelineVersion(ctx context.Context, procs []supervisor.ChildStatus) updater.Verdict {
	want := h.pipelineWant()
	if want == "" {
		return updater.Verdict{}
	}
	for _, p := range procs {
		if p.Role != supervisor.RolePipeline || p.ProbeURL == "" {
			continue
		}
		var hz struct {
			Version string `json:"version"`
		}
		if code := h.get(ctx, p.ProbeURL, &hz); code != http.StatusOK {
			return updater.Verdict{Why: fmt.Sprintf("%s /healthz answers %d", childName(p), code)}
		}
		if hz.Version != "" && hz.Version != want {
			return updater.Verdict{Fatal: true, Why: fmt.Sprintf("%s runs the pipeline %s, not %s", childName(p), hz.Version, want)}
		}
	}
	return updater.Verdict{}
}

// pipelineWant is the version this release's pipeline says: its wheel's (the payload's
// for a desktop app, bin/releases/<v>/pipeline's for a plain server); "" when the
// pipeline runs another environment or no wheel says.
func (h *machineHealth) pipelineWant() string {
	if !supervisor.PipelineOwnEnv(supervisor.Options{Config: h.cfg, ReleaseDir: h.releaseDir}) {
		return ""
	}
	dir := h.releaseDir
	if b := config.AppBundle(); b != "" {
		dir = b
	}
	if dir == "" {
		return ""
	}
	wheel, err := appvenv.Wheel(dir)
	if err != nil {
		return ""
	}
	return appvenv.WheelVersion(wheel)
}

func (h *machineHealth) get(ctx context.Context, url string, out any) int {
	return h.getAs(ctx, url, out, "")
}

// getSelf is get of the api's /api/cluster/self as this machine's supervisor (its local
// token): the home's machines that cannot log in to its database are told to it alone.
func (h *machineHealth) getSelf(ctx context.Context, url string, out any) int {
	tok, _ := os.ReadFile(supervisor.LocalTokenPath(h.cfg))
	return h.getAs(ctx, url, out, strings.TrimSpace(string(tok)))
}

func (h *machineHealth) getAs(ctx context.Context, url string, out any, localToken string) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0
	}
	if localToken != "" {
		req.Header.Set("X-Maktaba-Local-Token", localToken)
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode == http.StatusOK {
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
	}
	return resp.StatusCode
}

func childName(p supervisor.ChildStatus) string {
	if p.Instance != "" {
		return string(p.Role) + "/" + p.Instance
	}
	return string(p.Role)
}

package plugins

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginsig"
)

// Part is the component_status part a plugin's files are reported under (story 54.38 §4).
const Part = "plugins"

// PolicyKey is the app_settings key of the home's plugin policy (story 54.10:
// allow_unreviewed). A plugin's own settings are plugins.<id>, and an id has at least
// three labels, so the two never meet.
const PolicyKey = "plugins.policy"

// Channels the Runner listens on besides the reconciler's desired state.
var Channels = []string{"plugin_installs_changed", "plugin_grants_changed"}

// Install is one plugin_installs row as the runner reads it.
type Install struct {
	ID           string
	Version      string
	Digest       string
	Path         string
	PreviousPath string
	Manifest     json.RawMessage
	ReviewLevel  string
	InstalledAt  time.Time
}

// state is what one pass reads.
type state struct {
	installs        map[string]Install
	grants          map[string][]string
	keepOff         map[string]bool
	allowUnreviewed bool
	// entitledUntil is, per plugin, the latest valid_until of its entitlements (story 54.19
	// §6: checked offline when the API fetched them); at is when the state was read
	entitledUntil map[string]time.Time
	at            time.Time
}

// EntitlementGrace is pluginsig's: an entitlement holds this long past its valid_until.
const EntitlementGrace = pluginsig.EntitlementGrace

// paid is whether a manifest's price names a product (a paid plugin, story 54.19 §4).
func paid(price string) bool { return price != "" && price != "free" }

// CatalogSink takes the catalog with the plugins' entries (*supervisor.Reconciler).
type CatalogSink interface {
	SetPluginCatalog(c *components.Catalog)
}

// StatusReporter writes a part's rows (*store.Store is one).
type StatusReporter interface {
	ReportStatus(ctx context.Context, machine, part string, rows []store.Status) error
}

// Runner keeps the plugin files following plugin_installs and the desired rows of the
// plugin.* entries, gives the reconciler the merged catalog and each plugin's gate, and
// reports the files part. Level based: each pass reads the tables and acts on the
// difference; a pass runs on plugin_installs_changed or plugin_grants_changed, on each
// desired state the reconciler reads (Desired), and every Poll.
type Runner struct {
	Files   *Files
	DB      *sql.DB
	DSN     string // the listener's ("" : poll only)
	Machine string
	// Version is this server's (the plugins' maktaba range is checked against it).
	Version string
	Catalog CatalogSink
	Status  StatusReporter
	// RestartPipeline restarts the main pipeline worker, which loads plugin stages at its
	// start (story 54.06), when the installed set changes (nil: nothing).
	RestartPipeline func()
	// PipelineStarted is when the main pipeline worker last started (zero: not known, or
	// not running): at the first pass, a plugin installed after it restarts it.
	PipelineStarted func() time.Time
	// Host is the plugin host's last health (plugins.host's sandbox level); RunDir its run
	// folder (the failed sandbox step's file).
	Host   *supervisor.PluginsHealth
	RunDir string
	// Off is [components] reconcile = "off": it reports and gates, and changes no file.
	Off  bool
	Log  io.Writer
	Poll time.Duration
	// Now is the clock (nil: time.Now).
	Now func() time.Time
	// Home is set on a joined machine (story 54.42): its packages are fetched from the
	// home and verified, it runs only the plugins its placement fits (GPU: it serves the
	// gpu pool), and it writes no plugin table (an uninstall is the home's; it removes
	// its own copies when the home's row is gone).
	Home HomeFiles
	GPU  bool

	mu       sync.Mutex
	st       *state
	desired  map[string]store.Desired
	wake     chan struct{}
	loaded   string // the installed set the pipeline loaded (id@version@digest …)
	primed   bool
	recov    bool
	absentAt map[string]time.Time // when a want absent was first seen
	failed   map[string]*Refusal  // a removal that failed, by id
	written  []store.Status
}

func (r *Runner) logf(format string, args ...any) {
	if r.Log != nil {
		fmt.Fprintf(r.Log, "[plugins] "+format+"\n", args...)
	}
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) poke() {
	r.mu.Lock()
	w := r.wake
	r.mu.Unlock()
	if w == nil {
		return
	}
	select {
	case w <- struct{}{}:
	default:
	}
}

// Desired takes each desired state the reconciler reads (its OnDesired).
func (r *Runner) Desired(d map[string]store.Desired) {
	r.mu.Lock()
	r.desired = d
	r.mu.Unlock()
	r.poke()
}

// Gate is a plugin entry's gate (supervisor.Reconciler.PluginGate): "" when it passes,
// else the reason (story 54.37 §3). Before the first read every plugin waits: the
// reconciler starts no host for what it has not read.
func (r *Runner) Gate(id string) string {
	r.mu.Lock()
	st := r.st
	r.mu.Unlock()
	if st == nil {
		return components.ReasonIncompatible
	}
	pid := strings.TrimPrefix(id, components.PluginPrefix)
	if r.Home != nil {
		if why := r.machineGate(st, pid); why != "" {
			return why
		}
	}
	reason, _ := st.gate(pid, r.Version)
	return reason
}

// machineGate is why a joined machine does not run a plugin: its placement, or a copy
// that did not verify.
func (r *Runner) machineGate(st *state, id string) string {
	in, ok := st.installs[id]
	if !ok {
		return ""
	}
	var m struct {
		Placement string `json:"placement"`
	}
	_ = json.Unmarshal(in.Manifest, &m)
	if why := components.RunsOnMachine(m.Placement, r.GPU); why != "" {
		return why
	}
	r.mu.Lock()
	ref := r.failed[id]
	r.mu.Unlock()
	if ref != nil && ref.Code == components.ReasonPluginUntrusted {
		return components.ReasonPluginUntrusted
	}
	return ""
}

func (st *state) gate(id, version string) (string, map[string]any) {
	in, ok := st.installs[id]
	if !ok {
		return components.ReasonIncompatible, nil
	}
	var m struct {
		Maktaba      string `json:"maktaba"`
		PluginAPI    string `json:"plugin_api"`
		Price        string `json:"price"`
		Capabilities struct {
			Required []string `json:"required"`
		} `json:"capabilities"`
	}
	_ = json.Unmarshal(in.Manifest, &m)
	// a paid plugin waits while no entitlement holds now (54.19 §6): queue and data kept
	missing := false
	if paid(m.Price) {
		until, ok := st.entitledUntil[id]
		missing = !ok || !st.at.Before(until.Add(EntitlementGrace))
	}
	return components.PluginGate(components.PluginGateInput{ServerVersion: version, Range: m.Maktaba,
		PluginAPI: m.PluginAPI, Required: m.Capabilities.Required, Granted: st.grants[id],
		ReviewLevel: in.ReviewLevel, AllowUnreviewed: st.allowUnreviewed, EntitlementMissing: missing})
}

// Overlay adds the host's sandbox to plugins.host's row (supervisor.Reconciler.Overlay):
// running with part of its sandbox is degraded, sandbox_partial, with what is missing; a
// failed sandbox names its step.
func (r *Runner) Overlay(row *store.Status) {
	if row.ID != components.PluginsHost {
		return
	}
	if row.Phase == components.PhaseFailed {
		for i, c := range row.Conditions {
			if c.Reason == supervisor.ReasonSandboxUnavailable && r.RunDir != "" {
				if b, err := os.ReadFile(supervisor.SandboxStepFile(r.RunDir)); err == nil {
					row.Conditions[i].Params = map[string]any{"step": strings.TrimSpace(string(b))}
				}
			}
		}
		return
	}
	if r.Host == nil || row.Phase != components.PhaseRunning || row.Detail["idle"] == true {
		return
	}
	h, _, ok := r.Host.Last()
	if !ok || h.Sandbox != "partial" {
		return
	}
	row.Phase = components.PhaseDegraded
	for i, c := range row.Conditions {
		if c.Type == components.CondReady {
			row.Conditions[i].Reason = components.ReasonSandboxPartial
			row.Conditions[i].Params = map[string]any{"missing": strings.Join(h.SandboxMissing, ",")}
		}
	}
}

// Run passes until ctx ends.
func (r *Runner) Run(ctx context.Context) {
	r.mu.Lock()
	r.wake = make(chan struct{}, 1)
	r.mu.Unlock()
	poll := r.Poll
	if poll <= 0 {
		poll = 30 * time.Second
	}
	var notify <-chan *pq.Notification
	if r.DSN != "" {
		l := pq.NewListener(r.DSN, time.Second, 30*time.Second, nil)
		for _, ch := range Channels {
			_ = l.Listen(ch)
		}
		notify = l.Notify
		defer func() { _ = l.Close() }()
	}
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		r.Pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-notify:
		case <-r.wake:
		case <-t.C:
		}
	}
}

// Pass reads the tables and acts once (exported for the tests).
func (r *Runner) Pass(ctx context.Context) {
	st, err := r.load(ctx)
	if err != nil {
		if ctx.Err() == nil {
			r.logf("level=warn the plugin tables cannot be read (tried again): %v", err)
		}
		return
	}
	r.mu.Lock()
	desired := r.desired
	r.mu.Unlock()
	if !r.recov {
		r.recov = true
		for _, did := range r.Files.Layout.RecoverWith(func(id string) string { return st.installs[id].Version }) {
			r.logf("%s", did)
		}
		r.sweepStaging()
	}
	if !r.Off {
		r.follow(ctx, st)
		if desired != nil {
			r.removeAbsent(ctx, st, desired)
		}
	}
	r.mu.Lock()
	r.st = st
	r.mu.Unlock()
	r.swapCatalog(st)
	r.restartIfChanged(st)
	r.report(ctx, st, desired)
}

func (r *Runner) load(ctx context.Context) (*state, error) {
	st := &state{installs: map[string]Install{}, grants: map[string][]string{}, keepOff: map[string]bool{},
		entitledUntil: map[string]time.Time{}, at: r.now()}
	rows, err := r.DB.QueryContext(ctx, `SELECT plugin_id, version, digest, path, COALESCE(previous_path, ''),
		manifest, review_level, installed_at FROM plugin_installs`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var in Install
		var raw []byte
		if err := rows.Scan(&in.ID, &in.Version, &in.Digest, &in.Path, &in.PreviousPath, &raw, &in.ReviewLevel, &in.InstalledAt); err != nil {
			rows.Close()
			return nil, err
		}
		in.Manifest = raw
		st.installs[in.ID] = in
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	grows, err := r.DB.QueryContext(ctx, `SELECT plugin_id, capabilities FROM plugin_grants`)
	if err != nil {
		return nil, err
	}
	for grows.Next() {
		var id string
		var caps []string
		if err := grows.Scan(&id, pq.Array(&caps)); err != nil {
			grows.Close()
			return nil, err
		}
		st.grants[id] = caps
	}
	grows.Close()
	krows, err := r.DB.QueryContext(ctx, `SELECT plugin_id FROM plugin_keep_off`)
	if err == nil {
		for krows.Next() {
			var id string
			if krows.Scan(&id) == nil {
				st.keepOff[id] = true
			}
		}
		krows.Close()
	}
	// an older schema has no entitlements table: no paid plugin runs
	if erows, err := r.DB.QueryContext(ctx, `SELECT plugin_id, max(valid_until) FROM plugin_entitlements GROUP BY plugin_id`); err == nil {
		for erows.Next() {
			var id string
			var until time.Time
			if erows.Scan(&id, &until) == nil {
				st.entitledUntil[id] = until
			}
		}
		erows.Close()
	}
	var pol []byte
	if err := r.DB.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, PolicyKey).Scan(&pol); err == nil {
		var p struct {
			AllowUnreviewed bool `json:"allow_unreviewed"`
		}
		if json.Unmarshal(pol, &p) == nil {
			st.allowUnreviewed = p.AllowUnreviewed
		}
	}
	return st, nil
}

func (r *Runner) swapCatalog(st *state) {
	if r.Catalog == nil {
		return
	}
	ids := make([]string, 0, len(st.installs))
	for id := range st.installs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	specs := make([]components.PluginSpec, 0, len(ids))
	for _, id := range ids {
		s, err := components.PluginSpecOf(st.installs[id].Manifest, "")
		if err != nil {
			r.logf("level=warn %s: %v", id, err)
			continue
		}
		specs = append(specs, s)
	}
	cat, skipped := components.WithPlugins(components.Default(), specs)
	for _, s := range skipped {
		r.logf("level=warn a plugin is left out of the catalog: %s", s)
	}
	r.Catalog.SetPluginCatalog(cat)
}

// sweepStaging deletes the root's staging folders a crash left (an unpack whose id was
// not read yet).
func (r *Runner) sweepStaging() {
	entries, err := os.ReadDir(r.Files.Layout.Root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), ".staging-") {
			_ = os.RemoveAll(filepath.Join(r.Files.Layout.Root, e.Name()))
		}
	}
}

// follow points each plugin's current at the folder plugin_installs names (an update's
// switch, a rollback) and deletes its folders but that one and the previous version's
// (story 54.07 §5 step 5).
func (r *Runner) follow(ctx context.Context, st *state) {
	if r.Home != nil {
		r.followHome(ctx, st)
		return
	}
	for id, in := range st.installs {
		lay := r.Files.Layout
		want := filepath.Base(in.Path)
		if filepath.Dir(in.Path) != lay.Dir(id) {
			continue // a folder this layout does not hold (a development folder)
		}
		if info, err := os.Stat(in.Path); err != nil || !info.IsDir() {
			continue // reported missing
		}
		keep := []string{want}
		if in.PreviousPath != "" {
			keep = append(keep, filepath.Base(in.PreviousPath))
		}
		if lay.Current(id) != want {
			unlock, err := lay.Lock(ctx, id)
			if err != nil {
				continue
			}
			if err := lay.SetCurrent(id, want); err == nil {
				r.logf("%s: current now names %s", id, want)
			}
			unlock()
		}
		for _, f := range lay.Folders(id) {
			if !slices.Contains(keep, f) {
				if _, err := r.Files.Discard(ctx, id, f, keep...); err == nil {
					r.logf("%s: deleted the folder %s (neither current nor the kept previous version)", id, f)
				}
			}
		}
	}
}

// removeAbsent uninstalls each plugin whose desired want is absent (story 54.07 §7): once
// its jobs drained (none running, or the drain grace passed), its files go, then its
// plugin_installs row, its grants and its queued jobs (the only path that drops jobs: no
// code will run them). Its data, writes and settings stay (a purge is the API's).
func (r *Runner) removeAbsent(ctx context.Context, st *state, desired map[string]store.Desired) {
	if r.Home != nil {
		return // the home uninstalls; followHome removes this machine's copies
	}
	r.mu.Lock()
	if r.absentAt == nil {
		r.absentAt = map[string]time.Time{}
	}
	r.mu.Unlock()
	for cid, d := range desired {
		id, ok := strings.CutPrefix(cid, components.PluginPrefix)
		if !ok {
			continue
		}
		in, installed := st.installs[id]
		onDisk := len(r.Files.Layout.Folders(id)) > 0
		if d.Want != components.WantAbsent || (!installed && !onDisk) {
			r.mu.Lock()
			delete(r.absentAt, id)
			r.mu.Unlock()
			continue
		}
		r.mu.Lock()
		first, seen := r.absentAt[id]
		if !seen {
			first = r.now()
			r.absentAt[id] = first
		}
		r.mu.Unlock()
		grace := 120 * time.Second
		if installed {
			if s, err := components.PluginSpecOf(in.Manifest, ""); err == nil {
				grace = time.Duration(components.PluginEntry(s).Drain.GraceS) * time.Second
			}
		}
		if r.running(ctx, id) > 0 && r.now().Sub(first) < grace {
			continue // still draining
		}
		if err := r.Files.Remove(ctx, id); err != nil {
			r.mu.Lock()
			if r.failed == nil {
				r.failed = map[string]*Refusal{}
			}
			r.failed[id] = refuse(ReasonBusy, ReasonBusy, "%v", err)
			r.mu.Unlock()
			r.logf("level=warn %s: its files cannot be removed (tried again): %v", id, err)
			continue
		}
		dropped, err := r.forget(ctx, id)
		if err != nil {
			r.logf("level=warn %s: removed from disk, but its rows are not: %v", id, err)
			continue
		}
		delete(st.installs, id)
		delete(st.grants, id)
		r.mu.Lock()
		delete(r.failed, id)
		delete(r.absentAt, id)
		r.mu.Unlock()
		r.logf("%s: uninstalled (%d queued jobs dropped; its data, writes and settings are kept)", id, dropped)
	}
}

// running counts the plugin's claimed and running jobs.
func (r *Runner) running(ctx context.Context, id string) int {
	var n int
	_ = r.DB.QueryRowContext(ctx, `SELECT count(*) FROM processing_jobs j JOIN job_stages s ON s.stage = j.stage
		WHERE s.extension = $1 AND j.state IN ('claimed', 'running')`, "plugin:"+id).Scan(&n)
	return n
}

// forget deletes an uninstalled plugin's install, grants and queued jobs in one
// transaction; it answers how many jobs it dropped.
func (r *Runner) forget(ctx context.Context, id string) (int64, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM processing_jobs WHERE state = 'pending' AND stage IN
		(SELECT stage FROM job_stages WHERE extension = $1)`, "plugin:"+id)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	for _, q := range []string{`DELETE FROM plugin_installs WHERE plugin_id = $1`, `DELETE FROM plugin_grants WHERE plugin_id = $1`} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return 0, err
		}
	}
	return n, tx.Commit()
}

// restartIfChanged restarts the main pipeline worker when the installed set differs from
// the one it loaded: it reads plugin_installs at its start. The first pass only records
// the set (the worker starts with it).
func (r *Runner) restartIfChanged(st *state) {
	keys := make([]string, 0, len(st.installs))
	for id, in := range st.installs {
		keys = append(keys, id+"@"+in.Version+"@"+in.Digest)
	}
	sort.Strings(keys)
	set := strings.Join(keys, " ")
	r.mu.Lock()
	first := !r.primed
	changed := r.loaded != set
	r.loaded, r.primed = set, true
	r.mu.Unlock()
	if first && r.PipelineStarted != nil {
		// the worker loaded the set of its own start: one installed since (before this
		// runner's first read) is not in it
		started := r.PipelineStarted()
		for _, in := range st.installs {
			if !started.IsZero() && in.InstalledAt.After(started) {
				first, changed = false, true
			}
		}
	}
	if first || !changed || r.RestartPipeline == nil {
		return
	}
	r.logf("the installed plugins changed: the pipeline worker restarts to load their stages")
	r.RestartPipeline()
}

// report writes the files part of every plugin installed or being removed: off when its
// files are in place (the stage group's phase is the pipeline's), failed with
// pack_missing when its folder is gone, removing while a removal waits for the drain.
func (r *Runner) report(ctx context.Context, st *state, desired map[string]store.Desired) {
	if r.Status == nil {
		return
	}
	var rows []store.Status
	ids := map[string]bool{}
	for id := range st.installs {
		ids[id] = true
	}
	r.mu.Lock()
	for id := range r.absentAt {
		ids[id] = true
	}
	failed := map[string]*Refusal{}
	for k, v := range r.failed {
		failed[k] = v
	}
	r.mu.Unlock()
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	for _, id := range sorted {
		cid := components.PluginPrefix + id
		d := desired[cid]
		row := store.Status{ID: cid, ObservedGeneration: d.Generation}
		in, installed := st.installs[id]
		inst := store.Condition{Type: components.CondInstalled, Status: "true"}
		switch {
		case failed[id] != nil:
			row.Phase = components.PhaseFailed
			inst.Status, inst.Reason = "false", failed[id].Reason
		case d.Want == components.WantAbsent:
			row.Phase = components.PhaseRemoving
		case installed && !dirExists(in.Path):
			row.Phase = components.PhaseFailed
			inst.Status, inst.Reason = "false", "pack_missing"
			inst.Params = map[string]any{"component": cid}
		default:
			row.Phase = components.PhaseOff
			row.Version = in.Version
			row.Detail = map[string]any{"version": in.Version, "review_level": in.ReviewLevel}
			if st.keepOff[id] {
				row.Detail["kept_off"] = true
			}
		}
		row.Conditions = []store.Condition{inst}
		rows = append(rows, row)
	}
	if rows == nil {
		rows = []store.Status{}
	}
	r.mu.Lock()
	same := r.written != nil && reflect.DeepEqual(r.written, rows)
	r.mu.Unlock()
	if same {
		return
	}
	if err := r.Status.ReportStatus(ctx, r.Machine, Part, rows); err != nil {
		if ctx.Err() == nil {
			r.logf("level=warn the plugins' status cannot be written: %v", err)
		}
		return
	}
	r.mu.Lock()
	r.written = rows
	r.mu.Unlock()
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// Kept are the folders of id plugin_installs names (its current and previous versions):
// a discard never removes them.
func (r *Runner) Kept(id string) []string {
	r.mu.Lock()
	st := r.st
	r.mu.Unlock()
	if st == nil {
		return nil
	}
	in, ok := st.installs[id]
	if !ok {
		return nil
	}
	out := []string{filepath.Base(in.Path)}
	if in.PreviousPath != "" {
		out = append(out, filepath.Base(in.PreviousPath))
	}
	return out
}

// followHome is follow on a joined machine: each plugin it may run gets its verified copy
// of the home's current folder (FetchFromHome), current points at it, and copies of
// plugins the home no longer has, or of versions it no longer keeps, are deleted.
func (r *Runner) followHome(ctx context.Context, st *state) {
	lay := r.Files.Layout
	for id, in := range st.installs {
		var m struct {
			Placement string `json:"placement"`
		}
		_ = json.Unmarshal(in.Manifest, &m)
		if components.RunsOnMachine(m.Placement, r.GPU) != "" {
			continue
		}
		want := filepath.Base(in.Path)
		if ref := r.Files.FetchFromHome(ctx, r.Home, in); ref != nil {
			r.mu.Lock()
			if r.failed == nil {
				r.failed = map[string]*Refusal{}
			}
			r.failed[id] = ref
			r.mu.Unlock()
			r.logf("level=warn %s: the home's package is not here: %s", id, ref.Error())
			continue
		}
		r.mu.Lock()
		delete(r.failed, id)
		r.mu.Unlock()
		if lay.Current(id) != want {
			if unlock, err := lay.Lock(ctx, id); err == nil {
				if err := lay.SetCurrent(id, want); err == nil {
					r.logf("%s: current now names %s", id, want)
				}
				unlock()
			}
		}
		keep := []string{want}
		if in.PreviousPath != "" {
			keep = append(keep, filepath.Base(in.PreviousPath))
		}
		for _, f := range lay.Folders(id) {
			if !slices.Contains(keep, f) {
				_, _ = r.Files.Discard(ctx, id, f, keep...)
			}
		}
	}
	entries, err := os.ReadDir(lay.Root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, ok := st.installs[e.Name()]; !ok {
			if err := r.Files.Remove(ctx, e.Name()); err == nil {
				r.logf("%s: removed this machine's copy (the home uninstalled it)", e.Name())
			}
		}
	}
}

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
	"sync/atomic"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appvenv"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/dbsafe"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/homemirror"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/machineagent"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/updater"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
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

// envAgentOrders is set ("1") by a server agent that answers the home's update orders
// itself (the Windows app's `Maktaba.exe --server-agent`, agent_orders.rs): an update
// never waits for a window app.
const envAgentOrders = "MAKTABA_AGENT_ORDERS"

// appOpener is how the server starts the desktop app for an order it has not answered
// (maktaba://install-update): none when the agent that started this server answers the
// orders, so no window is opened, and no link Windows may not know is sent.
func appOpener() func(context.Context, string) error {
	if strings.TrimSpace(os.Getenv(envAgentOrders)) == "1" {
		return nil
	}
	return updater.OpenURL
}

// updates is serve's update machinery.
type updates struct {
	m *updater.Machine
	// r restores the home's backups by hand (serve_restore.go; nil on a node)
	r *restorer
	// health's view of this machine, and its processes' stop, set once the supervisor
	// exists
	mu    sync.Mutex
	snap  func() supervisor.Snapshot
	want  func(role supervisor.Role, instance string, on bool) error
	inUse func() []string
	// the TV handover (serve_handover.go): the supervisor, the layout of a plain server's
	// install, and an exit 75 armed to hand the TV processes over
	sup   *supervisor.Supervisor
	lay   *layout.Layout
	armed atomic.Bool
}

// pythonStopWait is how long stopPython waits for the pipeline's processes to be gone.
var pythonStopWait = time.Minute

// stopPython stops every pipeline process (the Python ones) and waits for them to be gone
// (pythonStopWait at most): a runtime of another Python minor takes their place
// (updater.RuntimeInstaller.StopPython). Nothing to stop before the supervisor exists.
func (u *updates) stopPython(ctx context.Context) error {
	u.mu.Lock()
	snap, want := u.snap, u.want
	u.mu.Unlock()
	if snap == nil || want == nil {
		return nil
	}
	for _, p := range snap().Processes {
		if p.Role == supervisor.RolePipeline {
			if err := want(p.Role, p.Instance, false); err != nil {
				return err
			}
		}
	}
	deadline := time.Now().Add(pythonStopWait)
	for {
		running := false
		for _, p := range snap().Processes {
			running = running || (p.Role == supervisor.RolePipeline && p.PID != 0)
		}
		if !running {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the pipeline still runs after %s", pythonStopWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// startPython turns every pipeline process back on (a runtime switch that went no further
// after stopPython: the release before runs on, its pipeline with it).
func (u *updates) startPython(_ context.Context) error {
	u.mu.Lock()
	snap, want := u.snap, u.want
	u.mu.Unlock()
	if snap == nil || want == nil {
		return nil
	}
	var first error
	for _, p := range snap().Processes {
		if p.Role == supervisor.RolePipeline {
			if err := want(p.Role, p.Instance, true); err != nil && first == nil {
				first = err
			}
		}
	}
	return first
}

// installKind picks this server's installer (install-kinds.md): the desktop app's
// bundle, the Linux runtime copy (a headless install's and the Docker install's too),
// the side-by-side layout, or a flat install that cannot update itself.
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
			updates := filepath.Join(orDir(lroot, cfg.StateDir()), "updates")
			return &updater.RuntimeInstaller{Runtime: rt, Source: src, Version: version.Version, LauncherRoot: lroot,
				Owner: runtimeOwner(), Record: record, Container: config.Container(), Open: appOpener(),
				WheelRoot: filepath.Join(updates, wheelhouseFolder)}, updates
		}
		bundled := strings.TrimSpace(os.Getenv(envAppBundled))
		if bundled == "" {
			bundled = bundle
		}
		updates := filepath.Join(orDir(lroot, cfg.StateDir()), "updates")
		return &updater.AppInstaller{Payload: bundle, Bundled: bundled, App: appOf(bundled), LauncherRoot: lroot,
			Source: src, Version: version.Version, Record: record, Rehearse: home, Open: appOpener(),
			WheelRoot: filepath.Join(updates, wheelhouseFolder)}, updates
	}
	if releaseDir != "" {
		l := layout.New(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Clean(releaseDir)))))
		return &updater.LayoutInstaller{Layout: l, Source: src, Version: filepath.Base(filepath.Clean(releaseDir)),
			Record: record, PipelinePython: strings.TrimSpace(cfg.Pipeline.Python)}, l.UpdatesDir()
	}
	return flatInstaller{}, filepath.Join(cfg.StateDir(), "updates")
}

// keptStreaming finds a release's streaming binary where this install keeps it besides
// bin/releases (nil: nowhere else): a desktop app's payloads kept before its updates, a
// runtime copy's other directories. A TV slot bound to the release before (a TV held there
// when its change's check failed, story 35.6) starts again on it from there.
func keptStreaming(cfg config.Config) func(version string) []string {
	bundle := config.AppBundle()
	if bundle == "" {
		return nil
	}
	if rt, ok := appruntime.Of(bundle); ok || filepath.Base(bundle) == appruntime.CurrentLink {
		if !ok {
			rt = appruntime.New(filepath.Dir(bundle))
		}
		return (&updater.RuntimeInstaller{Runtime: rt}).StreamingOf
	}
	root := orDir(strings.TrimSpace(os.Getenv(envLauncherRoot)), cfg.StateDir())
	return func(version string) []string { return updater.KeptStreaming(root, version) }
}

// wheelhouseFolder is where, in the updates folder, a desktop app's or a runtime copy's
// staged release keeps the home's wheelhouse (updater.WheelhouseDir).
const wheelhouseFolder = "wheelhouse"

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

// homeMirrorClient is a node's way to its home's mirror: its requests carry the delegation
// key the machine agent holds.
func homeMirrorClient(agent *machineagent.Agent) *homemirror.Client {
	return &homemirror.Client{Home: agent.ID.HomeURL, NodeID: agent.ID.NodeID, Log: os.Stderr,
		Key: func() (homemirror.Key, bool) {
			k := agent.CurrentKey()
			if k == nil {
				return homemirror.Key{}, false
			}
			return homemirror.Key{ID: k.ID, Secret: k.Key}, true
		}}
}

// withHomeMirror makes a node download releases from its home's mirror first (GitHub
// after three failures, at once for a file the home does not hold) and its pipeline's
// packages from the home's wheelhouse (story 35.12 AC3, design §3.4); every file is
// verified here as before.
func withHomeMirror(src updater.Source, c *homemirror.Client) updater.Source {
	hc := *src.HTTP
	hc.Transport = c.Transport(hc.Transport)
	src.HTTP = &hc
	src.Wheels = c.Wheelhouse
	return src
}

// updateSource is the pinned repository and the trusted root (refreshed through TUF
// into <data_dir>/sigstore, as the API's; a node reads the TUF repository through its home
// when it cannot reach Sigstore's: tufMirror).
func updateSource(cfg config.Config, tufMirror *releaseverify.TUFMirror) updater.Source {
	id, err := releaseverify.IdentityFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[serve] update: %v: the default repository is used\n", err)
		id, _ = releaseverify.NewIdentity("")
	}
	store := &releaseverify.RootStore{Dir: filepath.Join(cfg.Server.DataDir, "sigstore"), Mirror: tufMirror}
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
			// the error names the password file, never the password
			fmt.Fprintf(os.Stderr, "[serve] level=warn update: the database's superuser URL: %v: no rehearsal or dump\n", err)
			return nil
		}
		return &dbsafe.Target{URL: u, Bin: pg.Bin, Backups: pg.Backups, Data: pg.Data}
	}
	u := strings.TrimSpace(cfg.Database.URL)
	if u == "" || (!strings.HasPrefix(u, "postgres://") && !strings.HasPrefix(u, "postgresql://")) {
		return nil
	}
	return &dbsafe.Target{URL: u, Bin: pgProgram, Backups: cfg.BackupsDir(filepath.Join(cfg.Server.DataDir, "backups"))}
}

// pgProgram finds a PostgreSQL client program: MAKTABA_PG_BIN (a directory), else PATH.
func pgProgram(name string) string {
	if dir := strings.TrimSpace(os.Getenv("MAKTABA_PG_BIN")); dir != "" {
		return filepath.Join(dir, layout.Exe(name))
	}
	return ""
}

// rehearser is updater.Rehearser over a dbsafe.Target, and the home's pre-update dump
// taken while it serves (updater.SideBackup): keep and room are the dumps' rules
// ([backups] update_keep, its room), off [backups] pre_update = "off".
type rehearser struct {
	t    dbsafe.Target
	keep int
	room pgbundle.RoomPolicy
	off  bool
}

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

// Pending implements updater.SideBackup: the staged release's migrations (dir) the
// database lacks.
func (r rehearser) Pending(ctx context.Context, dir string) (int, bool) {
	if dir == "" {
		return 0, false
	}
	return r.t.Pending(ctx, dir)
}

// backupProgressEvery is how often the backup step says how much it wrote.
var backupProgressEvery = 10 * time.Second

// BackupBefore implements updater.SideBackup: the pre-update dump (dumpBeforeUpdateExternal:
// room made first, the rows Maktaba makes again left out, the dumps of its kind pruned),
// while this release serves.
func (r rehearser) BackupBefore(ctx context.Context, from, to string, progress func(int64)) (*updater.Backup, error) {
	if r.off {
		return nil, nil
	}
	dump := filepath.Join(r.t.Backups, pgbundle.UpdateDumpName(from, to))
	pctx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(backupProgressEvery)
		defer t.Stop()
		for {
			select {
			case <-pctx.Done():
				return
			case <-t.C:
				if n := pgbundle.PartialBytes(dump); n > 0 && progress != nil {
					progress(n)
				}
			}
		}
	}()
	p, err := dumpBeforeUpdateExternal(ctx, r.t, from, to, r.keep, r.room)
	stop()
	<-done
	if err != nil {
		return nil, err
	}
	return &updater.Backup{Path: p, Bytes: pgbundle.DumpBytes(p)}, nil
}

// UpdateDump is the dump a release's start took before its migrations
// (backups/pre-<from>-to-<to>.dump, the versions as the start named them: with or without
// their v), the newest when several match (updater.BackupFinder).
func (r rehearser) UpdateDump(from, to string) *updater.Backup {
	var best *updater.Backup
	var at time.Time
	for _, f := range []string{from, "v" + strings.TrimPrefix(from, "v"), strings.TrimPrefix(from, "v")} {
		for _, t := range []string{to, "v" + strings.TrimPrefix(to, "v"), strings.TrimPrefix(to, "v")} {
			p := filepath.Join(r.t.Backups, pgbundle.UpdateDumpName(f, t))
			if st, err := os.Lstat(p); err == nil && pgbundle.IsDump(st) && (best == nil || st.ModTime().After(at)) {
				best, at = &updater.Backup{Path: p, Bytes: pgbundle.DumpBytes(p)}, st.ModTime()
			}
		}
	}
	return best
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
func (r recordFile) NoteNext(running, next, dump string) error {
	return layout.NoteRecordNext(string(r), running, next, dump, time.Now())
}
func (r recordFile) MarkKept(version, from, reason string) (bool, error) {
	return layout.MarkRecordKept(string(r), version, from, reason)
}
func (r recordFile) Unmark(version string) error {
	if _, err := layout.UnmarkRecordKept(string(r), version); err != nil {
		return err
	}
	_, err := layout.UnmarkRecordRestoring(string(r), version)
	return err
}

// startUpdates makes the machine and takes up the run the last start left. exit stops
// serve with a code (75).
func startUpdates(ctx context.Context, cfg config.Config, releaseDir string, home bool, pg *pgbundle.Bundle,
	node *nodeServices, exit func(int)) *updates {
	var mirror *homemirror.Client
	var tufMirror *releaseverify.TUFMirror
	if node != nil && node.agent != nil {
		mirror = homeMirrorClient(node.agent)
		// story 35.16: over the machine's cluster link first (the node API's loopback fetch);
		// the signed path while the link is not up and the home does not require it
		mirror.Link = &homemirror.LocalLink{API: "http://" + supervisor.LocalAddr(cfg.Server.Listen, ports.APIPublic.PortString()),
			TokenFile: supervisor.LocalTokenPath(cfg)}
		tufMirror = &releaseverify.TUFMirror{BaseURL: strings.TrimRight(mirror.Home, "/") + "/api/cluster/updates/tuf",
			Transport: mirror.SignedTransport(nil)}
	}
	src := updateSource(cfg, tufMirror)
	if mirror != nil {
		src = withHomeMirror(src, mirror)
	}
	inst, dir := installKind(cfg, releaseDir, home, src)
	u := &updates{}
	if ri, ok := inst.(*updater.RuntimeInstaller); ok {
		ri.StopPython = u.stopPython   // a runtime of another Python minor: the pipeline stops first
		ri.StartPython = u.startPython // and starts again when the switch goes no further
	}
	if li, ok := inst.(*updater.LayoutInstaller); ok && handoverDir(releaseDir) != "" {
		// story 35.7: the TV processes run on across its restarts (their releases stay: InUse)
		u.lay = &li.Layout
	}
	m := &updater.Machine{Dir: dir, Installer: inst, Exit: exit, Log: os.Stderr}
	m.TVHandover = u.tvHandover
	if home {
		m.Records = recordFile(apppayload.DatabasePath(cfg.StateDir()))
		m.RestoreBack = u.restoreBack(cfg, releaseDir, pg)
		u.r = newRestorer(cfg, homeDatabase(cfg, pg), inst, exit)
		u.r.run = func() *updater.Journal { return m.Status().Journal }
		if t := homeDatabase(cfg, pg); t != nil {
			m.Rehearser = rehearser{t: *t, keep: updateKeep(cfg), room: roomOf(cfg), off: !cfg.Backups.PreUpdateOn()}
			go func() { // a crash during a rehearsal left its scratch database
				dctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				defer cancel()
				_ = t.DropScratch(dctx)
			}()
		}
	}
	m.Health = &machineHealth{cfg: cfg, home: home, node: node, releaseDir: releaseDir, snap: u.snapshot,
		tvChanging: func() bool {
			j := m.Status().Journal
			return j != nil && j.TV != nil && j.TV.Outcome == "" && j.TV.ChangeID != ""
		}}
	m.TVOnAir = func() bool {
		snap, ok := u.snapshot()
		if !ok {
			return true // not known: not now
		}
		for _, p := range snap.Processes {
			// the slots, or the streaming role where there are none (it is the TV then)
			if (p.Role == supervisor.RoleTVSlot || p.Role == supervisor.RoleStreaming) && p.State == supervisor.StateRunning {
				return true
			}
		}
		return false
	}
	if home && supervisor.SlotsEnabled(cfg) {
		// the TV moves to the new release's streaming binary before the release commits
		// (story 35.6): its TV change goes through the API's change controller
		m.TV = &apiTV{cfg: cfg, releaseDir: releaseDir}
	}
	// a front door or a TV slot keeps the release it runs (a TV held on the release before
	// when its change's check failed: story 35.6, the owner's decision of 2026-10-28)
	switch in := inst.(type) {
	case *updater.LayoutInstaller:
		in.InUse = u.releasesInUse
	case *updater.AppInstaller:
		in.InUse = u.releasesInUse
	case *updater.RuntimeInstaller:
		in.InUse = u.releasesInUse
	}
	u.m = m
	m.Start(ctx)
	m.Resume()
	fmt.Fprintf(os.Stderr, "[serve] updates: %s install (%s)\n", inst.Kind(), dir)
	return u
}

// restoreBack is updater.Machine.RestoreBack for this home: its database step's restore
// (the bundled Postgres, or the plain server's own), run by this release before its
// rollback switches back, once the roles that use the database are stopped.
func (u *updates) restoreBack(cfg config.Config, releaseDir string, pg *pgbundle.Bundle) func(context.Context,
	*updater.Journal, string) error {
	return func(ctx context.Context, j *updater.Journal, _ string) error {
		g := &migrationGate{stateDir: cfg.StateDir(), version: j.From, pg: pg, log: os.Stderr,
			updates: updatesDirOf(cfg, releaseDir), keep: updateKeep(cfg), room: roomOf(cfg)}
		if pg == nil {
			g.ext = homeDatabase(cfg, nil)
		}
		rec, err := g.record()
		if err != nil {
			return err
		}
		if rec.State != apppayload.DatabaseRestoring || rec.By != "" ||
			layout.NormalizeVersion(rec.Failed) != layout.NormalizeVersion(j.Job.Version) {
			return fmt.Errorf("the database record is %s, not marked for %s's dump to be put back", rec.State, j.Job.Version)
		}
		if err := u.stopDatabaseRoles(ctx); err != nil {
			return err
		}
		_, err = restoreBackWith(ctx, g, rec)
		return err
	}
}

// restoreBackWith is the database step's restore (tests replace it).
var restoreBackWith = func(ctx context.Context, g *migrationGate, rec apppayload.Database) (apppayload.Database, error) {
	return g.restore(ctx, rec, func(supervisor.Detail) {})
}

// databaseStopWait is how long the roles that use the database get to stop before a
// rollback puts the dump back.
var databaseStopWait = 2 * time.Minute

// stopDatabaseRoles stops every process that uses the database but the TV's (the api, the
// pipeline, the streaming role: their writes would be lost with the database they write
// to): the TV slots run on, on the database being replaced (the restore never touches it
// until it renames the one it put back into place). Postgres and the model server stay.
func (u *updates) stopDatabaseRoles(ctx context.Context) error {
	u.mu.Lock()
	snap, want := u.snap, u.want
	u.mu.Unlock()
	if snap == nil || want == nil {
		return nil
	}
	uses := func(p supervisor.ChildStatus) bool {
		return p.Role == supervisor.RoleAPI || p.Role == supervisor.RolePipeline || p.Role == supervisor.RoleStreaming
	}
	for _, p := range snap().Processes {
		if uses(p) && p.Wanted {
			if err := want(p.Role, p.Instance, false); err != nil {
				return err
			}
		}
	}
	deadline := time.Now().Add(databaseStopWait)
	for {
		running := ""
		for _, p := range snap().Processes {
			if uses(p) && p.PID != 0 {
				running = childName(p)
			}
		}
		if running == "" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s still runs after %s", running, databaseStopWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (u *updates) setSupervisor(sup *supervisor.Supervisor) {
	u.mu.Lock()
	u.snap, u.want, u.inUse, u.sup = sup.Snapshot, sup.Want, sup.ReleasesInUse, sup
	u.mu.Unlock()
}

// releasesInUse are the releases the supervisor's running processes run from (none before
// it exists).
func (u *updates) releasesInUse() []string {
	u.mu.Lock()
	f := u.inUse
	u.mu.Unlock()
	if f == nil {
		return nil
	}
	return f()
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
	// TVBusy (apply): the home orders it while the TV is watched (story 35.7: it counted on
	// this machine handing its TV over)
	TVBusy bool `json:"tv_busy,omitempty"`
	updater.Job
	// TVDeadline (apply): how long the release's TV change may wait behind another change
	// (the window's end and overrun, story 35.6 AC4)
	TVDeadline *time.Time `json:"tv_deadline,omitempty"`
	// InstallBy (apply, an order to a desktop app): the latest an automatic run gives the
	// go (its window's end and overrun); the app's notice names it
	InstallBy *time.Time `json:"install_by,omitempty"`
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
		if req.TVDeadline != nil {
			u.m.SetTVDeadline(req.Job.RunID, *req.TVDeadline)
		}
		if req.InstallBy != nil {
			u.m.SetInstallBy(req.Job.RunID, *req.InstallBy)
		}
		err = u.m.ApplyGated(req.Job.RunID, req.TVBusy)
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
		if name == "backups" || name == "restore" {
			u.serveRestore(w, r, name)
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
	// tvChanging reports whether the release's TV change is under way (its TV step began
	// and has no outcome yet): its slots are the TV step's to judge then (nil: never)
	tvChanging func() bool
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
	"db_checking": true, "db_restoring": true, "venv_waiting": true, "venv_building": true,
	"storage_waiting": true, "storage_moving": true}

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
		if p.Role == supervisor.RoleTVSlot {
			// a slot starting, stopped or between two starts is the TV's doing (its change's
			// staging slot starts, its swap stops a slot: 2026-10-28, "tv-slot/amber is
			// starting" counted against the watch), never the home unhealthy. A slot that
			// crash-loops or cannot start at all is the live TV off the air: fatal, but while
			// the release's TV change is under way, whose staging slot runs the new binary
			// (its check failing holds the TV, its swap failing rolls back: the TV step's)
			if (p.CrashLoop || haltCodes[p.DetailCode]) && (h.tvChanging == nil || !h.tvChanging()) {
				return updater.Verdict{Fatal: true, Why: childName(p) + " cannot run (it crash-loops or cannot start): the TV is off the air"}
			}
			continue
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
	admin := "http://" + supervisor.LocalAddr(h.cfg.Server.AdminAddr, ports.APIAdmin.PortString())
	if code, err := h.get(ctx, admin+"/readyz", nil); code != http.StatusOK {
		return updater.Verdict{Why: answers("api /readyz", code, err)}
	}
	var self struct {
		Version    string          `json:"version"`
		Paused     string          `json:"paused"`
		MachinesDB []selfMachineDB `json:"machines_db"`
	}
	api := "http://" + supervisor.LocalAddr(h.cfg.Server.Listen, ports.APIPublic.PortString())
	if code, err := h.getSelf(ctx, api+"/api/cluster/self", &self); code != http.StatusOK {
		return updater.Verdict{Why: answers("api /api/cluster/self", code, err)}
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
		if code, err := h.get(ctx, p.ProbeURL, &hz); code != http.StatusOK {
			return updater.Verdict{Why: answers(childName(p)+" /healthz", code, err)}
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

func (h *machineHealth) get(ctx context.Context, url string, out any) (int, error) {
	return h.getAs(ctx, url, out, "")
}

// getSelf is get of the api's /api/cluster/self as this machine's supervisor (its local
// token): the home's machines that cannot log in to its database are told to it alone.
func (h *machineHealth) getSelf(ctx context.Context, url string, out any) (int, error) {
	tok, _ := os.ReadFile(supervisor.LocalTokenPath(h.cfg))
	return h.getAs(ctx, url, out, strings.TrimSpace(string(tok)))
}

// getAs is the status url answers (with the local token when given), or 0 and the
// error when it did not answer at all (refused, timed out): a rollback's reason says which.
func (h *machineHealth) getAs(ctx context.Context, url string, out any, localToken string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	if localToken != "" {
		req.Header.Set("X-Maktaba-Local-Token", localToken)
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode == http.StatusOK {
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
	}
	return resp.StatusCode, nil
}

// answers is a verdict's words for what did not answer 200: its error when it did not
// answer at all ("api /readyz: … connection refused"), else its status.
func answers(what string, code int, err error) string {
	if err != nil {
		return what + ": " + err.Error()
	}
	return fmt.Sprintf("%s answers %d", what, code)
}

func childName(p supervisor.ChildStatus) string {
	if p.Instance != "" {
		return string(p.Role) + "/" + p.Instance
	}
	return string(p.Role)
}

package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appvenv"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/attempts"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/dbsafe"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/updater"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// The server's start inside the desktop app (Epic 38 story 38.12, for the Mac,
// Windows and Linux; Epic 36's contract-update.md). The app's updater replaces
// the whole payload; at its next start the server brings the database and the
// pipeline's venv to the new payload before any role runs:
//
//   - every process but postgres (and the model server, which keeps nothing of
//     the library) waits for the database step, run once — except on a joined
//     full node, whose api reads the home's database and never migrates (the
//     home's own start does this): there it does not run and writes nothing. It
//     reads the record
//     of the release whose migrations the database holds
//     (<state>/state/database.json) and refuses an older release than that; with
//     another release than the one that ran last (or no record: an install from
//     before it) it is an update. One that adds no migration (none of its own the
//     database lacks) takes no dump: the switch is a restart, and the release before
//     it can run on the database again as it is. One that does takes the dump the
//     release before took for it while it still served (the update's backup step,
//     internal/updater), else it dumps the database now (backups/pre-<old>-to-<new>.dump,
//     the way back), and records the update before migrating; then it runs the api's
//     migrations (`maktaba-api migrate up`) and records them done. When they fail, it
//     puts the dump back (nothing is dumped first) and keeps the server stopped,
//     saying so;
//   - the pipeline's processes then wait for the venv bootstrap (one run for all
//     of them), unless MAKTABA_PYTHON or [pipeline] python names their Python;
//   - postgres refuses a cluster another major made;
//   - once every process ran healthy for confirmAfter, the payload is saved as
//     the last one that ran (state/payload.json).
//
// What each process waits on shows as its detail in /processes: the English,
// and a code from detailCodes with its values for a screen to say it in its own
// language.

// detailCodes are the codes of every detail the app's start steps show
// (/processes' detail_code), each with the names of its values (detail_params):
// a fixed list the app's screens translate. It is detail_codes.json, which the
// screens' test reads too, so the server and the screens have one list. A new
// detail takes a new code there (and in contract-update.md's list). Releases
// are versions ("" when not known), paths are whole, error values are the
// programs' own English.
var detailCodes = parseDetailCodes(detailCodesJSON)

//go:embed detail_codes.json
var detailCodesJSON []byte

func parseDetailCodes(b []byte) map[string][]string {
	var codes map[string][]string
	if err := json.Unmarshal(b, &codes); err != nil {
		panic("detail_codes.json: " + err.Error()) // embedded: a test run meets it first
	}
	return codes
}

// detail is code's Detail: values are its params in detailCodes' order, text
// the English.
func detail(code, text string, values ...any) supervisor.Detail {
	var params map[string]any
	if names := detailCodes[code]; len(names) > 0 {
		params = make(map[string]any, len(names))
		for i, n := range names {
			if i < len(values) {
				params[n] = values[i]
			}
		}
	}
	return supervisor.Detail{Code: code, Params: params, Text: text}
}

// detailErr is an error carrying detail(code, text, values...).
func detailErr(code, text string, values ...any) error {
	return supervisor.Coded(detail(code, text, values...))
}

// appBundleGates gives procs their Prepare steps for the payload in dir.
func appBundleGates(cfg config.Config, dir string, pg *pgbundle.Bundle, procs []supervisor.Process, log io.Writer) []supervisor.Process {
	cur, err := apppayload.Read(filepath.Join(dir, "payload.json"))
	if err != nil {
		fmt.Fprintf(log, "[serve] the app's payload.json cannot be read (%v): starting without the update checks\n", err)
		return procs
	}
	// a joined full node (the same test the api's environment is made by,
	// supervisor/env.go): its api runs in node mode against the home's database,
	// which only the home migrates and dumps; no step, no record here (one would
	// later judge an app on a machine that has no database of its own)
	var dbStep *sharedStep
	if n, err := supervisor.NodeOf(cfg); err != nil {
		// it may be a node's: no update of a database of its own here (the roles do not
		// start on it either, supervisor.ChildEnv)
		fmt.Fprintf(log, "[serve] no library database update: %v\n", err)
	} else if supervisor.NodeMode(cfg, n) {
		fmt.Fprintf(log, "[serve] this machine is a full node of %s: the home updates the library database\n", n.HomeURL())
	} else {
		db := &migrationGate{stateDir: cfg.StateDir(), version: cur.Version, pg: pg,
			keep: updateKeep(cfg), log: log, updates: updatesDirOf(cfg, ""), noDump: !cfg.Backups.PreUpdateOn(),
			room: roomOf(cfg)}
		for _, p := range procs {
			if p.Role == supervisor.RoleAPI && p.Cmd != nil {
				db.api = p.Cmd
			}
		}
		dbStep = &sharedStep{wait: detail("db_waiting", "Waiting for the library database"), run: db.run}
	}
	var venvStep *sharedStep
	if py := supervisor.PythonOverride(cfg); py == "" {
		// a node's update staged the home's wheelhouse beside (updater.WheelhouseDir): the
		// pipeline's packages come from it first
		wheels := updater.WheelhouseDir(filepath.Join(updatesDirOf(cfg, ""), wheelhouseFolder), cur.Version)
		venv := &venvGate{spec: appvenv.Spec{Payload: dir, Venv: filepath.Join(cfg.StateDir(), "venv"), Wheelhouse: wheels},
			updates: updatesDirOf(cfg, ""), version: cur.Version}
		venvStep = &sharedStep{wait: detail("venv_waiting", "Waiting for the transcription tools"), run: venv.run}
	} else {
		fmt.Fprintf(log, "[serve] the pipeline runs %s (MAKTABA_PYTHON or [pipeline] python): the app's venv is not built\n", py)
	}
	for i := range procs {
		p := &procs[i]
		switch {
		case p.Role == supervisor.RolePostgres:
			if pg != nil {
				p.Prepare = postgresMajorGate(*pg, cur.PostgresMajor())
			}
		case p.Role == supervisor.RoleOllama:
			// the model server keeps nothing of the library: it waits on nothing
		case p.Role == supervisor.RolePipeline && venvStep != nil:
			if dbStep != nil {
				p.Prepare = steps(dbStep.prepare, venvStep.prepare)
			} else {
				p.Prepare = venvStep.prepare
			}
		case dbStep != nil:
			p.Prepare = dbStep.prepare
		}
	}
	return procs
}

// layoutHomeGates gives a plain server home (the side-by-side layout: Epic 35 story
// 35.2) the app's database step (Epic 38 story 38.12): at the start of a release other
// than the one whose migrations the database holds, every role waits while the database
// is dumped (the bundled Postgres, or an operator's with the pg programs), migrated, and
// put back when the migrations fail; a rollback of an automatic update marks the record
// for the next start to put the dump back (internal/updater). version is the release
// running; a SQLite home has no step (its migrations run as before).
func layoutHomeGates(cfg config.Config, version string, pg *pgbundle.Bundle, ext *dbsafe.Target, procs []supervisor.Process,
	updates string, log io.Writer) []supervisor.Process {
	db := &migrationGate{stateDir: cfg.StateDir(), version: version, pg: pg, ext: ext,
		keep: updateKeep(cfg), log: log, updates: updates, noDump: !cfg.Backups.PreUpdateOn(), room: roomOf(cfg)}
	for _, p := range procs {
		if p.Role == supervisor.RoleAPI && p.Cmd != nil {
			db.api = p.Cmd
		}
	}
	if db.api == nil {
		return procs
	}
	step := &sharedStep{wait: detail("db_waiting", "Waiting for the library database"), run: db.run}
	for i := range procs {
		switch procs[i].Role {
		case supervisor.RolePostgres, supervisor.RoleOllama:
		default:
			procs[i].Prepare = step.prepare
		}
	}
	return procs
}

// postgresMajorGate refuses to start Postgres on a cluster another major made:
// the server would refuse the data files; an upgrade between majors is a dump
// and a restore no update does yet.
func postgresMajorGate(b pgbundle.Bundle, payloadMajor string) step {
	return func(context.Context, func(supervisor.Detail)) error {
		have, err := b.ClusterMajor()
		if err != nil {
			return detailErr("pg_unreadable", fmt.Sprintf("the library database (%s) cannot be read: %v", b.Data, err),
				b.Data, err.Error())
		}
		if have != "" && payloadMajor != "" && have != payloadMajor {
			return detailErr("pg_major", fmt.Sprintf("the library database was made by PostgreSQL %s and this Maktaba "+
				"carries PostgreSQL %s: the database needs an upgrade between the two, which no update does yet",
				have, payloadMajor), have, payloadMajor)
		}
		return nil
	}
}

// step is a Prepare.
type step = func(ctx context.Context, report func(supervisor.Detail)) error

// steps runs each in turn.
func steps(all ...step) step {
	return func(ctx context.Context, report func(supervisor.Detail)) error {
		for _, s := range all {
			if err := s(ctx, report); err != nil {
				return err
			}
		}
		return nil
	}
}

// sharedStep is a start step a set of processes waits on, run once for all of
// them: the first to come runs it, the others wait, and every one of them shows
// what it reports as its detail. Once it succeeded it is done for this run of
// the server, and a Halt holds for it; another failure is every waiter's (each
// is tried again as the supervisor tries any Prepare again).
type sharedStep struct {
	run  step
	wait supervisor.Detail // what a waiting process shows before the step reports

	mu       sync.Mutex
	done     bool
	halt     error
	running  chan struct{}     // closed when the run in progress ends
	result   error             // the last run's
	last     supervisor.Detail // what the run in progress last reported
	watchers map[int]func(supervisor.Detail)
	next     int
}

func (s *sharedStep) prepare(ctx context.Context, report func(supervisor.Detail)) error {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return nil
	}
	if s.halt != nil && supervisor.HaltCleared(s.halt) {
		s.halt = nil // its way out came (the database record put right): the step runs again
	}
	if s.halt != nil {
		s.mu.Unlock()
		return s.halt
	}
	if s.watchers == nil {
		s.watchers = map[int]func(supervisor.Detail){}
	}
	id := s.next
	s.next++
	s.watchers[id] = report
	running := s.running
	shown := s.last
	if running == nil {
		s.running, s.last, shown = make(chan struct{}), supervisor.Detail{}, supervisor.Detail{}
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.watchers, id)
		s.mu.Unlock()
	}()
	if shown.Code == "" && shown.Text == "" {
		shown = s.wait
	}
	report(shown)

	if running != nil { // another process runs it: wait for its end
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-running:
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case s.done:
			return nil
		case s.halt != nil:
			return s.halt
		}
		return s.result
	}
	err := s.run(ctx, s.broadcast)
	s.mu.Lock()
	switch {
	case err == nil:
		s.done = true
	case supervisor.Halted(err):
		s.halt = err
	}
	s.result = err
	close(s.running)
	s.running = nil
	s.mu.Unlock()
	return err
}

// broadcast shows detail on every process waiting on the step.
func (s *sharedStep) broadcast(d supervisor.Detail) {
	s.mu.Lock()
	s.last = d
	ws := make([]func(supervisor.Detail), 0, len(s.watchers))
	for _, w := range s.watchers {
		ws = append(ws, w)
	}
	s.mu.Unlock()
	for _, w := range ws {
		w(d)
	}
}

// migrationGate is the database step of an app start (the package comment).
type migrationGate struct {
	stateDir string
	version  string           // the payload's
	pg       *pgbundle.Bundle // the bundled Postgres; nil: a database of the user's own
	// ext is a database of the user's own that a plain server install dumps and puts back
	// with the pg programs (Epic 35: every home's update has its dump); nil with pg nil:
	// no dump, no restore
	ext  *dbsafe.Target
	api  *exec.Cmd // the api's command: its binary and environment run the migrations; nil: no api here
	keep int       // pre-update dumps kept ([backups] update_keep, else [postgres] update_dumps)
	// noDump: [backups] pre_update = "off", no dump before an update's migrations (and
	// so no way back when they fail); room is what a dump leaves on its disk
	noDump bool
	room   pgbundle.RoomPolicy
	log    io.Writer
	// updates is the machine's updates folder: a release's start is recorded there
	// (server-attempts.json), so the Updates page says why it failed ("": not recorded)
	updates string
}

// attempt records what this start does with the update to g.version (update-attempts.md),
// on the attempt the start goes on with (settleAttempt's new try). Best effort: a failure
// to record is logged, nothing else.
func (g *migrationGate) attempt(from string, fn func(a *attempts.Attempt)) {
	settleAttempt(g.updates, g.version, from, true, g.log, fn)
}

// retryWithin is how long after a failure a start of the same release goes on with that
// attempt (a step tried again) instead of beginning another.
const retryWithin = 30 * time.Minute

// settleAttempt changes the newest attempt at release to in the updates folder dir, or a
// new one from from: when there is none, or for a new try when the newest ended (put back,
// installed, or failed longer than retryWithin ago) or began longer than retryWithin ago
// and never ended (that start was cut short: it ends failed, start_interrupted). Not a new
// try (a restore, which settles the start that failed): the newest, whatever its end. A
// first install (no release before) that installs is no update: nothing is recorded.
func settleAttempt(dir, to, from string, newTry bool, log io.Writer, fn func(a *attempts.Attempt)) {
	if dir == "" {
		return
	}
	a, ok := attempts.Find(dir, to)
	ended := ok && a.EndedAt != nil &&
		(a.Outcome != attempts.Failed || attempts.Now().Sub(*a.EndedAt) > retryWithin)
	if newTry && ok && a.EndedAt == nil && attempts.Now().Sub(a.At) > retryWithin {
		a.Code = attempts.Interrupted
		a.Detail = fmt.Sprintf("the start of %s that began %s did not finish (the server or the machine stopped)",
			a.To, a.At.Format(time.RFC3339))
		a.Ended(attempts.Failed)
		if err := attempts.Record(dir, a); err != nil {
			fmt.Fprintf(log, "[serve] the update's attempt cannot be recorded in %s: %v\n", dir, err)
		}
		ended = true
	}
	fresh := false
	if !ok || (newTry && ended) {
		a = attempts.Attempt{ID: attempts.NewStartID(to), At: attempts.Now(), Source: attempts.SourceServer,
			Trigger: "start", From: layout.NormalizeVersion(from), To: layout.NormalizeVersion(to)}
		fresh = a.From == ""
	}
	if newTry {
		a.Outcome, a.EndedAt = attempts.Running, nil // going on with it
	}
	fn(&a)
	if fresh && a.Outcome != attempts.Failed && a.Outcome != attempts.PutBack {
		return // a first install's start: no update to record (its failure is still one)
	}
	if err := attempts.Record(dir, a); err != nil {
		fmt.Fprintf(log, "[serve] the update's attempt cannot be recorded in %s: %v\n", dir, err)
	}
}

// failAttempt ends the start's attempt failed at step, with the reason.
func (g *migrationGate) failAttempt(from, step, code string, err error) {
	g.attempt(from, func(a *attempts.Attempt) {
		a.Step, a.Code, a.Detail = step, code, err.Error()
		a.Ended(attempts.Failed)
	})
}

// The database step's programs (tests replace them).
var (
	dumpBeforeUpdate = func(ctx context.Context, b pgbundle.Bundle, from, to string, keep int) (string, error) {
		return b.DumpBeforeUpdate(ctx, from, to, keep)
	}
	restoreDump = func(ctx context.Context, b pgbundle.Bundle, dump string, log io.Writer) error {
		return b.RestoreRunning(ctx, dump, log)
	}
	migrateUp = runMigrateUp
	// a database of the user's own (a plain server install's): its dump beside the
	// data, and putting it back in place
	dumpExternal = func(ctx context.Context, t dbsafe.Target, from, to string, keep int, room pgbundle.RoomPolicy) (string, error) {
		return dumpBeforeUpdateExternal(ctx, t, from, to, keep, room)
	}
	restoreExternal = func(ctx context.Context, t dbsafe.Target, dump string, log io.Writer) error {
		return t.Restore(ctx, dump, log)
	}
	// the database a restore by hand replaces, set aside before the backup is put back
	dumpAsideBundled  = func(ctx context.Context, b pgbundle.Bundle, path string) error { return b.DumpTo(ctx, path) }
	dumpAsideExternal = func(ctx context.Context, t dbsafe.Target, path string) error { return t.Dump(ctx, path) }
	// the other sessions on the database, ended before an update's migrations
	endSessionsBundled  = func(ctx context.Context, b pgbundle.Bundle) (int, error) { return b.EndSessions(ctx) }
	endSessionsExternal = func(ctx context.Context, t dbsafe.Target) (int, error) { return t.EndSessions(ctx) }
	// how many of the payload's migrations the database lacks (known false: that cannot be
	// told, and the update dumps as before)
	pendingMigrations = func(ctx context.Context, g *migrationGate) (int, bool) { return g.pendingNow(ctx) }
	// the workers' group's CONNECT on the database, given when it lacks it
	grantWorkersConnect = func(ctx context.Context, g *migrationGate) error {
		t, err := g.target()
		if err != nil {
			return err
		}
		return t.GrantWorkersConnect(ctx)
	}
	// how often the dump's size is said while it is taken
	dumpReportEvery = time.Minute
)

// dumpBeforeUpdateExternal is the pre-update dump of a database of the user's own (a plain
// server install's, or the bundled one through its superuser URL): the whole database,
// beside the others in t.Backups (room made first), the dumps of its kind pruned to keep
// after it.
func dumpBeforeUpdateExternal(ctx context.Context, t dbsafe.Target, from, to string, keep int, room pgbundle.RoomPolicy) (string, error) {
	if err := pgbundle.MakeRoomWith(t.Backups, room); err != nil {
		return "", err
	}
	path := filepath.Join(t.Backups, pgbundle.UpdateDumpName(from, to))
	if err := t.Dump(ctx, path); err != nil {
		return "", err
	}
	return path, pruneDumps(t.Backups, path, keep)
}

// nextDumpFresh is how old the dump the release before took for this one (the update's
// backup step) may be for this start to take it as its pre-update dump: the switch follows
// it at once; one from an update that did not switch then is not (an older one would put
// back an older database).
const nextDumpFresh = time.Hour

// target is the gate's database as a dbsafe.Target: the user's own, or the bundled one
// through its superuser.
func (g *migrationGate) target() (dbsafe.Target, error) {
	if g.ext != nil {
		return *g.ext, nil
	}
	if g.pg == nil {
		return dbsafe.Target{}, errors.New("no database of this Maktaba's own")
	}
	u, err := g.pg.SuperURL()
	if err != nil {
		return dbsafe.Target{}, err
	}
	return dbsafe.Target{URL: u, Bin: g.pg.Bin, Backups: g.pg.Backups, Data: g.pg.Data}, nil
}

// pendingNow counts this release's migrations (the folder its api migrates from) the
// database does not hold yet.
func (g *migrationGate) pendingNow(ctx context.Context) (int, bool) {
	dir := migrationsDirOf(g.api)
	if dir == "" {
		return 0, false
	}
	t, err := g.target()
	if err != nil {
		return 0, false
	}
	return t.Pending(ctx, dir)
}

// migrationsDirOf is the folder the api's `migrate` reads (api/migrate.go,
// migrationsDirFor): MAKTABA_MIGRATIONS_DIR in its environment, else the migrations beside
// its binary (a release of the side-by-side layout) or beside its bin/ (an app payload, a
// runtime copy); "" when there is none.
func migrationsDirOf(api *exec.Cmd) string {
	if api == nil {
		return ""
	}
	for i := len(api.Env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(api.Env[i], "MAKTABA_MIGRATIONS_DIR="); ok && v != "" {
			return v
		}
	}
	exe := api.Path
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	for _, d := range []string{filepath.Join(filepath.Dir(exe), "migrations"), filepath.Join(filepath.Dir(filepath.Dir(exe)), "migrations")} {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
	}
	return ""
}

// nextDump is the dump the release before took for this one while it served (the
// update's backup step, noted in the record), "" when there is none it can take: one for
// another release, gone, or older than nextDumpFresh.
func (g *migrationGate) nextDump(rec apppayload.Database) string {
	if rec.NextDump == "" || layout.NormalizeVersion(rec.Next) != layout.NormalizeVersion(g.version) ||
		rec.NextAt == nil || time.Since(*rec.NextAt) > nextDumpFresh {
		return ""
	}
	if info, err := os.Lstat(rec.NextDump); err != nil || !pgbundle.IsDump(info) {
		return ""
	}
	return rec.NextDump
}

// backups is where the gate's dumps go.
func (g *migrationGate) backups() string {
	if g.pg != nil {
		return g.pg.Backups
	}
	return g.ext.Backups
}

// setAsideReplaced dumps the database a restore by hand replaces
// (replaced-<version>-<utc>.dump beside the backups), once since it was asked: it holds
// every change made since the backup, which the person chose to put back. Best effort:
// the putting back goes on either way (D2). An update's rollback sets nothing aside: the
// release before starts at once, from the dump taken before the failed release's
// migrations when they ran.
func (g *migrationGate) setAsideReplaced(ctx context.Context, rec apppayload.Database) {
	dir := g.backups()
	prefix, since := pgbundle.ReplacedDumpPrefix+rec.Version+"-", rec.At
	if ents, err := os.ReadDir(dir); err == nil {
		for _, e := range ents {
			if info, err := e.Info(); err == nil && strings.HasPrefix(e.Name(), prefix) &&
				strings.HasSuffix(e.Name(), ".dump") && !info.ModTime().Before(since) {
				return // set aside already
			}
		}
	}
	path := filepath.Join(dir, prefix+time.Now().UTC().Format("20060102T150405")+".dump")
	err := pgbundle.MakeRoomWith(dir, g.room)
	if err == nil {
		if g.pg != nil {
			err = dumpAsideBundled(ctx, *g.pg, path)
		} else {
			err = dumpAsideExternal(ctx, *g.ext, path)
		}
	}
	if err != nil {
		fmt.Fprintf(g.log, "[serve] could not set the library database aside before the restore: %v\n", err)
		return
	}
	fmt.Fprintf(g.log, "[serve] the library database the restore replaces is in %s\n", path)
}

// keeps reports whether the gate dumps and puts back (a bundled or an external one).
func (g *migrationGate) keeps() bool { return g.pg != nil || g.ext != nil }

func (g *migrationGate) dump(ctx context.Context, from, to string) (string, error) {
	if g.pg != nil {
		return dumpBeforeUpdate(ctx, *g.pg, from, to, g.keep)
	}
	return dumpExternal(ctx, *g.ext, from, to, g.keep, g.room)
}

// reportDumpSize says how much of the dump is written, every dumpReportEvery while it
// is taken: the update's watch sees the step make progress (a dump that stops growing is
// stuck). The func it returns stops it.
func (g *migrationGate) reportDumpSize(ctx context.Context, from, to string, expect int64, report func(supervisor.Detail)) func() {
	dump := filepath.Join(g.backups(), pgbundle.UpdateDumpName(from, to))
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(dumpReportEvery)
		defer t.Stop()
		said := int64(-1)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			n := pgbundle.PartialBytes(dump)
			if n == 0 || n == said {
				continue
			}
			said = n
			report(detail("db_dumping", fmt.Sprintf("Saving the library database before the update to %s (%d MB so far)",
				to, said>>20), from, to, said>>20, expect))
		}
	}()
	return func() { cancel(); <-done }
}

// expectedDumpMB is how big this dump will likely be: the newest finished pre-update dump
// in the backups (they leave the same rows out), else the newest of any kind (a nightly
// one), in MB; 0 when there is none. The starting screen shows the dump's progress
// against it.
func expectedDumpMB(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var newest, newestPre time.Time
	var path, pre string
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !pgbundle.IsDump(info) {
			continue
		}
		if info.ModTime().After(newest) {
			newest, path = info.ModTime(), filepath.Join(dir, e.Name())
		}
		if strings.HasPrefix(e.Name(), "pre-") && info.ModTime().After(newestPre) {
			newestPre, pre = info.ModTime(), filepath.Join(dir, e.Name())
		}
	}
	if pre != "" {
		path = pre
	}
	return pgbundle.DumpBytes(path) >> 20
}

// endSessions ends the other client sessions on the database before an update's
// migrations (the machines' api sessions, anything left holding a lock): a migration
// never waits on them. Best effort, and the migrations' lock timeout bounds the rest.
func (g *migrationGate) endSessions(ctx context.Context) {
	var n int
	var err error
	switch {
	case g.pg != nil:
		n, err = endSessionsBundled(ctx, *g.pg)
	case g.ext != nil:
		n, err = endSessionsExternal(ctx, *g.ext)
	default:
		return
	}
	switch {
	case err != nil:
		fmt.Fprintf(g.log, "[serve] the other sessions on the library database could not be ended before the migrations: %v\n", err)
	case n > 0:
		fmt.Fprintf(g.log, "[serve] ended %d other sessions on the library database before the migrations\n", n)
	}
}

// migrateTries is how often an update's migrations are tried when one waited longer
// than the lock timeout for a lock another session holds; the other sessions are ended
// before each try.
const migrateTries = 3

// migrate runs the migrations (an update's, when update: the other sessions ended first,
// each migration done said for the watch to see the step move).
func (g *migrationGate) migrate(ctx context.Context, cur string, update bool, report func(supervisor.Detail)) error {
	log := g.log
	if update {
		log = &migrationsDone{w: g.log, done: func(n int) {
			report(detail("db_migrating", fmt.Sprintf("Updating the library database for %s (%d migrations done)", cur, n), cur))
		}}
	}
	for try := 1; ; try++ {
		if update {
			g.endSessions(ctx)
		}
		err := migrateUp(ctx, g.api, log)
		if err == nil || ctx.Err() != nil || !lockTimedOut(err) || try == migrateTries {
			return err
		}
		fmt.Fprintf(g.log, "[serve] a migration for %s waited too long for a lock another session holds (try %d of %d): "+
			"trying again\n", cur, try, migrateTries)
	}
}

// lockTimedOut says a migration gave up waiting for a lock (lock_timeout, 55P03).
func lockTimedOut(err error) bool {
	return strings.Contains(err.Error(), "lock timeout") || strings.Contains(err.Error(), "55P03")
}

// migrationsDone passes the migrations' output on and counts the migrations goose says
// it applied: its "OK   <file> (<time>)", which the api's logger writes with no line
// end of its own (so they are counted in the stream, not by line).
type migrationsDone struct {
	w    io.Writer
	done func(n int)
	tail []byte // the last bytes of the output (a mark cut between two writes)
	n    int
}

// gooseOK is what goose writes for each migration it applied.
const gooseOK = "OK   "

func (m *migrationsDone) Write(p []byte) (int, error) {
	buf := append(append([]byte(nil), m.tail...), p...)
	// tail is shorter than the mark: every mark found ends in p, none is counted twice
	if found := strings.Count(string(buf), gooseOK); found > 0 {
		m.n += found
		m.done(m.n)
	}
	if keep := len(gooseOK) - 1; len(buf) > keep {
		buf = buf[len(buf)-keep:]
	}
	m.tail = append([]byte(nil), buf...)
	return m.w.Write(p)
}

func (g *migrationGate) putBack(ctx context.Context, dump string) error {
	if g.pg != nil {
		return restoreDump(ctx, *g.pg, dump, g.log)
	}
	return restoreExternal(ctx, *g.ext, dump, g.log)
}

// pruneDumps keeps the newest keep pre-update dumps in dir (keepPath always) and as many
// dumps of failed updates, and removes rehearsals a crash left (pgbundle.PruneBackups);
// keep is rules.updates.backups_keep when the last committed update recorded it.
func pruneDumps(dir, keepPath string, keep int) error {
	return pgbundle.PruneBackups(dir, keepPath, pgbundle.BackupsKeep(dir, keep))
}

// apiExitSchemaAhead is maktaba-api migrate's exit when the database holds
// migrations newer than the ones it has (api/main.go): a newer Maktaba ran here.
const apiExitSchemaAhead = 3

// errSchemaAhead: the database is newer than this payload's migrations.
var errSchemaAhead = errors.New("the database holds migrations newer than this Maktaba's")

// migrateLockTimeout is how long one migration waits for a lock another session holds
// before it fails (and is tried again, migrationGate.migrate).
const migrateLockTimeout = "2m"

// runMigrateUp runs the api's migrations with its binary and environment, with
// no time limit of their own (a stop of the server ends them, and the update's watch
// bounds an update's: updater.pendingCap), but none waits on another session's lock
// longer than migrateLockTimeout. Their output goes to the server's log; an error
// carries their last lines.
func runMigrateUp(ctx context.Context, api *exec.Cmd, log io.Writer) error {
	cmd := exec.CommandContext(ctx, api.Path, "migrate", "--timeout", "0", "--lock-timeout", migrateLockTimeout, "up")
	cmd.Env, cmd.Dir = api.Env, api.Dir
	tail := &tailWriter{max: 4 << 10}
	cmd.Stdout = io.MultiWriter(log, tail)
	cmd.Stderr = cmd.Stdout
	err := cmd.Run()
	if err == nil {
		return nil
	}
	msg := tail.lastLines(3)
	if msg == "" {
		msg = err.Error()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == apiExitSchemaAhead {
		return fmt.Errorf("%w: %s", errSchemaAhead, msg)
	}
	return errors.New(msg)
}

// record is the database's record, else one made from the payload that last
// ran healthy (an install that ran this release's server before the record was
// written), else the zero record (Version "": not known).
func (g *migrationGate) record() (apppayload.Database, error) {
	rec, ok, err := apppayload.ReadDatabase(g.stateDir)
	if err != nil || ok {
		return rec, err
	}
	if ran, ok, _ := apppayload.Confirmed(g.stateDir); ok {
		return apppayload.Database{Version: ran.Version, State: apppayload.DatabaseMigrated}, nil
	}
	return apppayload.Database{}, nil
}

// keptFor reports whether the record keeps its newer schema for release cur (a rollback
// with nothing put back, layout.MarkRecordKept): cur is the release it was kept for, or
// one between it and the release whose migrations it holds. Those migrations are all
// additive, and a release between knows a part of them (its own come before), so it runs
// on that schema as the release kept for does. Without this, a rollback from 0.3.46 to
// 0.3.43 refused 0.3.45, a good release, as older than the database (update rehearsal 10a).
func keptFor(rec apppayload.Database, cur string) bool {
	if rec.KeptFor == "" || (rec.State != apppayload.DatabaseMigrated && rec.State != apppayload.DatabaseRestored) {
		return false
	}
	c, k := layout.NormalizeVersion(cur), layout.NormalizeVersion(rec.KeptFor)
	return c == k || (apppayload.Older(k, c) && apppayload.Older(c, rec.Version))
}

// envSchemaAheadOK tells the api (its `migrate up` and its serve) that the database may
// hold migrations newer than its own: the record keeps them for this release.
const envSchemaAheadOK = "MAKTABA_SCHEMA_AHEAD_OK"

// allowSchemaAhead sets envSchemaAheadOK in the api's environment (its command is the
// template every start of the api clones, and the database step's migrations run it).
func (g *migrationGate) allowSchemaAhead() {
	if g.api == nil || slices.Contains(g.api.Env, envSchemaAheadOK+"=1") {
		return
	}
	if g.api.Env == nil {
		g.api.Env = os.Environ()
	}
	g.api.Env = append(g.api.Env, envSchemaAheadOK+"=1")
}

// recordChanges reports, from now on, whether the database's record differs from what
// it says now (a halt's way out: it is read every few seconds while the roles wait).
func (g *migrationGate) recordChanges() func() bool {
	path := apppayload.DatabasePath(g.stateDir)
	then, err := os.ReadFile(path)
	if err != nil {
		then = nil
	}
	return func() bool {
		now, err := os.ReadFile(path)
		if err != nil || bytes.Equal(now, then) {
			return false
		}
		// put right: the database is one a release runs on. Not a restore a person asked
		// for (restoring, by): `maktaba-server restore` restarts the server for it, and a
		// step run again meanwhile would race that restart
		var rec apppayload.Database
		if json.Unmarshal(now, &rec) != nil {
			return false
		}
		return rec.State == apppayload.DatabaseMigrated || rec.State == apppayload.DatabaseRestored
	}
}

func (g *migrationGate) save(rec apppayload.Database) error {
	if err := apppayload.WriteDatabase(g.stateDir, rec); err != nil {
		return detailErr("db_record_failed", fmt.Sprintf("the library database's record (%s) cannot be saved: %v",
			apppayload.DatabasePath(g.stateDir), err), apppayload.DatabasePath(g.stateDir), err.Error())
	}
	return nil
}

// run is the database step (the package comment); a failure that no retry in
// this run can mend is a supervisor.Halt, which lasts until the database's record
// changes (put right by hand, or by `maktaba-server restore`): then the step runs again
// and the roles start, with no restart of the server (the outage of 2026-10-28 stayed
// down after the record said restored, until a full restart).
func (g *migrationGate) run(ctx context.Context, report func(supervisor.Detail)) (err error) {
	defer func() {
		if supervisor.Halted(err) {
			err = supervisor.HaltUntil(err, g.recordChanges())
		}
	}()
	// a restart of Postgres (the machines' reach changed) would cut the dump, the
	// migrations or a restore short: it waits until this step is done
	defer pgbundle.Hold()()
	cur := g.version
	rec, err := g.record()
	if err != nil {
		return detailErr("db_record_failed", fmt.Sprintf("the library database's record (%s) cannot be read: %v",
			apppayload.DatabasePath(g.stateDir), err), apppayload.DatabasePath(g.stateDir), err.Error())
	}
	held := rec.Version
	if g.api == nil {
		// a run without the api (serve --role ...): nothing migrates here, so
		// nothing is dumped or recorded; only what must not run is refused
		switch {
		case rec.State == apppayload.DatabaseRestoring:
			return supervisor.Halt(detailErr("db_restore_unfinished", fmt.Sprintf("the library database is being put "+
				"back as it was before the update to %s (from %s): start the whole server (`maktaba-server serve`) to "+
				"finish it", rec.Failed, rec.Dump), rec.Failed, rec.Dump))
		case held != "" && held != cur && apppayload.Older(cur, held) && !keptFor(rec, cur):
			return supervisor.Halt(downgrade(cur, rec))
		}
		return nil
	}
	// a start of another release that never finished (stopped during its dump, or the
	// launcher went back from it) ends failed: this start is cur's
	if n, err := attempts.SettleStale(g.updates, cur); err != nil {
		fmt.Fprintf(g.log, "[serve] the update's attempts cannot be settled in %s: %v\n", g.updates, err)
	} else if n > 0 {
		fmt.Fprintf(g.log, "[serve] %d update attempt(s) that never finished are recorded failed\n", n)
	}
	// a putting back cut short (the server stopped meanwhile): finish it first
	if rec.State == apppayload.DatabaseRestoring {
		if rec, err = g.restore(ctx, rec, report); err != nil {
			return err
		}
		if cur == rec.Failed {
			return supervisor.Halt(failedUpdate(rec))
		}
		held = rec.Version
	}
	// a rollback that kept the newer, rollback-safe schema for this release (nothing put
	// back: updater/schemakeep.go): it starts on it, its api told it may
	kept := keptFor(rec, cur)
	if kept {
		g.allowSchemaAhead()
		fmt.Fprintf(g.log, "[serve] the library database holds %s's migrations, kept for %s by its rollback (%s): "+
			"they are additive, nothing was put back\n", rec.Version, cur, orUnknown(rec.KeptReason))
	}
	if held != "" && held != cur && apppayload.Older(cur, held) && !kept {
		err := downgrade(cur, rec)
		g.failAttempt(held, "start", "db_refused_older", err)
		return supervisor.Halt(err)
	}

	ran := rec.Ran() // the release that started on the database last
	update := apppayload.Database{Version: cur, State: apppayload.DatabaseMigrating}
	// back: a release older than the one that ran last, which the database allows (the
	// one that ran last added no migration): its rollback, no update of its own
	back := false
	switch {
	case rec.State == apppayload.DatabaseMigrating:
		// a start that did not finish migrating (stopped, cut off, its putting
		// back failed): go on; the dump is still the one from before it all
		update.From, update.Dump, update.DumpKeptFor = rec.From, rec.Dump, rec.DumpKeptFor
	case ran != "" && ran == cur: // the same release: nothing to update
		update = apppayload.Database{}
	default:
		// the dump is of this database: of a kept schema, when it is one
		update.From, update.DumpKeptFor = ran, rec.KeptFor
		back = ran != "" && apppayload.Older(cur, ran)
		if n, known := pendingMigrations(ctx, g); kept || (known && n == 0) {
			// no migration to run: no dump, and Version stays (the release before knows
			// the database as well); the start is a restart. A kept schema is newer than
			// this release's migrations, so none can be missing from it.
			update.Version, update.State, update.Release = held, apppayload.DatabaseMigrated, cur
			if held == "" || held == cur {
				update.Version, update.Release = cur, ""
			}
			// the schema does not change: whom it is kept for neither
			update.KeptFor, update.KeptReason, update.DumpKeptFor = rec.KeptFor, rec.KeptReason, ""
			if !back {
				fmt.Fprintf(g.log, "[serve] update %s -> %s: no migration to run, so no dump: the switch is a restart\n",
					orUnknown(ran), cur)
			}
			break
		}
		if !back {
			g.attempt(ran, func(a *attempts.Attempt) { a.Step = "dump" })
		}
		if next := g.nextDump(rec); next != "" && g.keeps() {
			// taken while the release before still served: only its switch waited for it
			fmt.Fprintf(g.log, "[serve] update %s -> %s: the library database was saved in %s while %s served\n",
				orUnknown(ran), cur, next, orUnknown(ran))
			update.Dump = next
			break
		}
		if g.keeps() && g.noDump {
			fmt.Fprintf(g.log, "[serve] level=warn update %s -> %s: no dump before the migrations ([backups] "+
				"pre_update = \"off\"): if they fail, the database cannot be put back as it was\n", orUnknown(ran), cur)
		}
		if g.keeps() && !g.noDump {
			expect := expectedDumpMB(g.backups())
			report(detail("db_dumping", fmt.Sprintf("Saving the library database before the update to %s", cur),
				ran, cur, 0, expect))
			stop := g.reportDumpSize(ctx, ran, cur, expect, report)
			dump, err := g.dump(ctx, ran, cur)
			stop()
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				g.failAttempt(ran, "dump", "db_dump_failed", err)
				return detailErr("db_dump_failed", fmt.Sprintf("the library database could not be saved before the "+
					"update to %s, so the update waits (nothing was changed): %v", cur, err), cur, err.Error())
			}
			fmt.Fprintf(g.log, "[serve] update %s -> %s: the library database is saved in %s\n", orUnknown(ran), cur, dump)
			update.Dump = dump
		}
	}
	migrating := update.State == apppayload.DatabaseMigrating
	if migrating {
		if err := g.save(update); err != nil {
			return err
		}
		report(detail("db_migrating", fmt.Sprintf("Updating the library database for %s", cur), cur))
		if !back {
			g.attempt(update.From, func(a *attempts.Attempt) { a.Step = "migrate" })
		}
	} else {
		report(detail("db_checking", "Checking the library database", cur))
	}
	migStart := time.Now()
	err = g.migrate(ctx, cur, migrating, report)
	if err == nil && migrating {
		// the update's migrations, for /metrics (story 35.15)
		if rerr := updater.RecordMigration(g.updates, cur, time.Since(migStart), time.Now()); rerr != nil {
			fmt.Fprintf(g.log, "[serve] the migrations' time is not recorded: %v\n", rerr)
		}
	}
	if ctx.Err() != nil {
		return ctx.Err() // stopped: the next start goes on (the record says migrating)
	}
	if now, rerr := g.record(); rerr == nil && now.State == apppayload.DatabaseRestoring && now.Failed == cur {
		// the update's watch gave up on this start meanwhile (start_timeout) and marked the
		// dump to be put back: the start it restarts does that; nothing is recorded over it
		return detailErr("db_restoring", fmt.Sprintf("The update to %s gave up: the library database is put back "+
			"as it was at the next start", cur), cur, now.Dump)
	}
	switch {
	case err == nil:
		// the database is one this release runs on (migrated, or restored): the machines'
		// way in is there (a restore by a release before 2026-10-28 dropped it)
		if g.keeps() {
			if gerr := grantWorkersConnect(ctx, g); gerr != nil {
				fmt.Fprintf(g.log, "[serve] level=warn the machines' CONNECT on the library database could not be "+
					"checked: %v\n", gerr)
			}
		}
		switch {
		case update.State == "":
			if rec.State == apppayload.DatabaseMigrated {
				return nil
			}
			// restored, and this is the release from before (on the kept schema it was put back to)
			update = apppayload.Database{Version: cur, KeptFor: rec.KeptFor, KeptReason: rec.KeptReason}
		case !back:
			g.attempt(update.From, func(a *attempts.Attempt) { a.Step, a.Code, a.Detail = "migrate", "", ""; a.Ended(attempts.Installed) })
		}
		update.State = apppayload.DatabaseMigrated
		return g.save(update)
	case errors.Is(err, errSchemaAhead):
		// nothing was changed (the dump of a development build, whose order the
		// versions could not tell, is not needed): the record goes back as it was
		if migrating {
			if err := g.save(rec); err != nil {
				return err
			}
		}
		g.failAttempt(ran, "migrate", "db_ahead", err)
		return supervisor.Halt(detailErr("db_ahead", fmt.Sprintf("the library database was changed by a newer "+
			"Maktaba than this one (%s), whose code would not know those changes: install that newer version again (%v)",
			cur, err), cur, err.Error()))
	case update.Dump == "":
		// nothing to put back (the same release, an update that adds no migration, or
		// a database of the user's own): tried again like any start step
		if update.State != "" && !back {
			g.failAttempt(update.From, "migrate", "db_migrate_failed", err)
		}
		return detailErr("db_migrate_failed", fmt.Sprintf("the library database could not be brought up to date "+
			"for %s: %v", cur, err), cur, err.Error())
	}
	// a failed update: the database goes back to how it was before it
	update.State, update.Failed, update.Error = apppayload.DatabaseRestoring, cur, err.Error()
	if err := g.save(update); err != nil {
		return err
	}
	rec, err = g.restore(ctx, update, report)
	if err != nil {
		return err
	}
	return supervisor.Halt(failedUpdate(rec))
}

// restore puts the dump of a failed update back (rec is restoring) and records
// it; the database is then as it was before that update. Nothing is dumped first: the
// release before starts as soon as its dump is back (the failed state is not kept).
func (g *migrationGate) restore(ctx context.Context, rec apppayload.Database, report func(supervisor.Detail)) (apppayload.Database, error) {
	if !g.keeps() {
		return rec, supervisor.Halt(detailErr("db_no_cluster", fmt.Sprintf("the update to %s could not change the "+
			"library database (%s), and this Maktaba runs no database of its own to put %s back into",
			rec.Failed, rec.Error, rec.Dump), rec.Failed, rec.Error, rec.Dump))
	}
	if rec.By != "" {
		return g.restoreAsked(ctx, rec, report)
	}
	report(detail("db_restoring", fmt.Sprintf("The update to %s could not change the library database: putting it "+
		"back as it was", rec.Failed), rec.Failed, rec.Dump))
	fmt.Fprintf(g.log, "[serve] the update to %s failed (%s): restoring %s\n", rec.Failed, rec.Error, rec.Dump)
	err := g.putBack(ctx, rec.Dump)
	if ctx.Err() != nil {
		return rec, ctx.Err() // the record still says restoring: the next start finishes it
	}
	// the attempt at the failed release: put back (its database and the release before
	// run again), or failed when even the dump could not be put back
	code := "db_migrate_failed" // its own migrations failed (this start is the failed release's)
	if layout.NormalizeVersion(rec.Failed) != layout.NormalizeVersion(g.version) {
		code = reasonCode(rec.Error) // the release before puts it back (a rollback marked it)
	}
	settleAttempt(g.updates, rec.Failed, rec.From, false, g.log, func(a *attempts.Attempt) {
		if a.From == "" {
			a.From = layout.NormalizeVersion(rec.From)
		}
		if a.Step == "" || a.Step == "dump" {
			a.Step = "start"
		}
		a.Code, a.Detail = code, rec.Error
		if err != nil {
			a.Code, a.Detail = "db_restore_failed", fmt.Sprintf("%s; putting %s back failed: %v", rec.Error, rec.Dump, err)
			a.Ended(attempts.Failed)
			return
		}
		a.Ended(attempts.PutBack)
	})
	if err != nil {
		return rec, supervisor.Halt(detailErr("db_restore_failed", fmt.Sprintf("the update to %s could not change "+
			"the library database (%s), and putting the database back from %s failed too: %v. Maktaba stays stopped; "+
			"its next start tries to put it back again", rec.Failed, rec.Error, rec.Dump, err),
			rec.Failed, rec.Error, rec.Dump, err.Error()))
	}
	// the dump of a schema kept for a release is that schema again, kept for it
	done := apppayload.Database{Version: rec.From, State: apppayload.DatabaseRestored, From: rec.From, Dump: rec.Dump,
		Failed: rec.Failed, Error: rec.Error, KeptFor: rec.DumpKeptFor}
	if err := g.save(done); err != nil {
		return rec, err
	}
	fmt.Fprintf(g.log, "[serve] the library database is back as it was before the update to %s\n", rec.Failed)
	return done, nil
}

// restoreAsked puts back a backup a person asked for (rec.By: story 35.5 AC8), before
// any role runs: the database it replaces set aside first, the dump put back as a failed
// update's is (one transaction, the machines' copies made anew), and recorded restored
// with the release it was put back for. No update attempt: nothing failed.
func (g *migrationGate) restoreAsked(ctx context.Context, rec apppayload.Database, report func(supervisor.Detail)) (apppayload.Database, error) {
	report(detail("db_restoring_backup", fmt.Sprintf("Putting the library database back from %s, as %s asked",
		rec.Dump, rec.By), rec.Dump, rec.By))
	g.setAsideReplaced(ctx, rec)
	fmt.Fprintf(g.log, "[serve] restore asked by %s: putting %s back for %s\n", rec.By, rec.Dump, orUnknown(rec.From))
	err := g.putBack(ctx, rec.Dump)
	if ctx.Err() != nil {
		return rec, ctx.Err() // the record still says restoring: the next start finishes it
	}
	if err != nil {
		return rec, supervisor.Halt(detailErr("db_restore_backup_failed", fmt.Sprintf("putting the library database "+
			"back from %s failed: %v. Nothing was changed; Maktaba stays stopped and its next start tries again",
			rec.Dump, err), rec.Dump, err.Error()))
	}
	done := apppayload.Database{Version: rec.From, State: apppayload.DatabaseRestored, From: rec.From, Dump: rec.Dump,
		By: rec.By}
	if err := g.save(done); err != nil {
		return rec, err
	}
	fmt.Fprintf(g.log, "[serve] the library database is back as it was in %s\n", rec.Dump)
	return done, nil
}

// failedUpdate is the detail of an update whose migrations failed and whose
// database was put back.
func failedUpdate(rec apppayload.Database) error {
	before := "the version you had before"
	if rec.From != "" {
		before = rec.From
	}
	return detailErr("db_restored", fmt.Sprintf("the update to %s could not change the library database (%s). The "+
		"database is back as it was before the update (restored from %s), and Maktaba stays stopped: install %s again "+
		"to go on as before, or an update that fixes this; starting Maktaba again tries this update again",
		rec.Failed, rec.Error, rec.Dump, before), rec.Failed, rec.Error, rec.Dump, rec.From)
}

// downgrade is the detail of a release older than the database's.
func downgrade(cur string, rec apppayload.Database) error {
	msg := fmt.Sprintf("this Maktaba (%s) is older than the one whose changes the library database holds (%s): it "+
		"would not know them. Install %s or later", cur, rec.Version, rec.Version)
	if rec.Dump != "" {
		msg += fmt.Sprintf(" (the database as it was before the update to %s is in %s)", rec.Version, rec.Dump)
	}
	return detailErr("db_refused_older", msg, cur, rec.Version, rec.Dump)
}

func orUnknown(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

// tailWriter keeps the last max bytes written to it.
type tailWriter struct {
	max int
	buf []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.max {
		w.buf = append([]byte(nil), w.buf[len(w.buf)-w.max:]...)
	}
	return len(p), nil
}

// lastLines is the last n lines that say something.
func (w *tailWriter) lastLines(n int) string {
	var out []string
	for _, l := range strings.Split(string(w.buf), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return strings.Join(out, "; ")
}

// reasonCode is a rollback's reason as an attempt's code: the reason itself when it is
// one (start_timeout, boot_loop), else start_failed (the detail says the rest).
func reasonCode(reason string) string {
	if reason != "" && strings.Trim(reason, "abcdefghijklmnopqrstuvwxyz0123456789_") == "" {
		return reason
	}
	return "start_failed"
}

// venvGate builds the app's venv for every pipeline process (the main worker
// and the transcription workers) through a sharedStep.
type venvGate struct {
	// updates and version: the start's attempt (update-attempts.md) a failure is recorded on
	updates, version string
	spec             appvenv.Spec
}

// venvRun is appvenv.Run (tests replace it).
var venvRun = appvenv.Run

func (g *venvGate) run(ctx context.Context, report func(supervisor.Detail)) error {
	spec := g.spec
	if f, err := config.OpenLog("venv.log"); err == nil { // the user's only, as every log
		defer f.Close()
		spec.Log = f
	}
	logPath := filepath.Join(config.LogsDir(), "venv.log")
	res, err := venvRun(ctx, spec, func(p appvenv.Progress) {
		pct := int(p.Fraction * 100)
		report(detail("venv_building", fmt.Sprintf("Installing the transcription tools: %s (%d%%)", p.Message, pct),
			p.Step, p.Done, p.Total, pct, p.Message))
	})
	switch {
	case err == nil:
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, appvenv.ErrInterrupted):
		return detailErr("venv_interrupted", fmt.Sprintf("the transcription tools' install was stopped; it goes on "+
			"from where it was next time (log: %s)", logPath), logPath)
	default:
		g.failAttempt("venv_failed", err)
		return detailErr("venv_failed", fmt.Sprintf("the transcription tools could not be installed: %v (log: %s)",
			err, logPath), err.Error(), logPath)
	}
	if !res.Reused {
		fmt.Fprintf(os.Stderr, "[serve] the pipeline's venv is ready (pipeline %s, Python %s)\n", res.Pipeline, res.Python)
	}
	// the home's wheelhouse the update staged for it (GBs with torch) has served: its
	// packages are in the venv now (or the venv needed none of them)
	if g.spec.Wheelhouse != "" {
		_ = os.RemoveAll(g.spec.Wheelhouse)
	}
	return nil
}

// failAttempt ends this release's start attempt failed at the pipeline's environment,
// when the start is an update's (an attempt at this release that runs, or ended within
// the last two hours): the release came up, its pipeline did not.
func (g *venvGate) failAttempt(code string, err error) {
	if g.updates == "" || g.version == "" {
		return
	}
	a, ok := attempts.Find(g.updates, g.version)
	if !ok || (a.EndedAt != nil && attempts.Now().Sub(*a.EndedAt) > 2*time.Hour) {
		return
	}
	a.Step, a.Code, a.Detail = "venv", code, err.Error()
	a.Ended(attempts.Failed)
	_ = attempts.Record(g.updates, a)
}

// confirmAppPayload saves the payload as the last one that ran once every
// process has run healthy for confirmAfter (state/payload.json: what ran, for
// the owner; the database's record is what an update compares with).
func confirmAppPayload(ctx context.Context, sup *supervisor.Supervisor, stateDir, payloadDir string) {
	after := confirmAfter()
	if after <= 0 {
		return
	}
	cur, err := apppayload.Read(filepath.Join(payloadDir, "payload.json"))
	if err != nil {
		return
	}
	if confirmed, ok, _ := apppayload.Confirmed(stateDir); ok && confirmed.Version == cur.Version &&
		confirmed.Pipeline.ConstraintsSHA256 == cur.Pipeline.ConstraintsSHA256 && confirmed.Pipeline.SHA256 == cur.Pipeline.SHA256 {
		return // this payload ran healthy already
	}
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		if healthyFor(sup.Snapshot(), after) {
			if err := apppayload.Confirm(stateDir, payloadDir); err != nil {
				fmt.Fprintf(os.Stderr, "[serve] saving the app's payload as the one that ran failed: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "[serve] the app's payload %s ran healthy for %s\n", cur.Version, after)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

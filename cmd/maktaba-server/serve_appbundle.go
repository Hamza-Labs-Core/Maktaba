package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appvenv"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
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
//     another release than that one (or no record: an install from before it)
//     it dumps the database (backups/pre-<old>-to-<new>.dump, the way back) and
//     records the update before migrating; then it runs the api's migrations
//     (`maktaba-api migrate up`) and records them done. When they fail, it puts
//     the dump back and keeps the server stopped, saying so;
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
// a fixed list the app's screens translate. A new detail takes a new code here
// (and in contract-update.md's list). Releases are versions ("" when not
// known), paths are whole, error values are the programs' own English.
var detailCodes = map[string][]string{
	// the database step
	"db_waiting":            nil,
	"db_checking":           {"release"},
	"db_dumping":            {"from", "to"},
	"db_migrating":          {"to"},
	"db_restoring":          {"failed", "dump"},
	"db_restored":           {"failed", "error", "dump", "release"},
	"db_refused_older":      {"release", "needed", "dump"},
	"db_ahead":              {"release", "error"},
	"db_dump_failed":        {"to", "error"},
	"db_migrate_failed":     {"release", "error"},
	"db_restore_failed":     {"failed", "error", "dump", "restore_error"},
	"db_restore_unfinished": {"failed", "dump"},
	"db_no_cluster":         {"failed", "error", "dump"},
	"db_record_failed":      {"path", "error"},
	// postgres
	"pg_unreadable": {"path", "error"},
	"pg_major":      {"cluster", "payload"},
	// the pipeline's venv
	"venv_waiting":     nil,
	"venv_building":    {"step", "done", "total", "percent", "message"},
	"venv_interrupted": {"log"},
	"venv_failed":      {"error", "log"},
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
	if n := supervisor.NodeConfig(); supervisor.NodeMode(cfg, n) {
		fmt.Fprintf(log, "[serve] this machine is a full node of %s: the home updates the library database\n", n.HomeURL())
	} else {
		db := &migrationGate{stateDir: cfg.StateDir(), version: cur.Version, pg: pg,
			keep: cfg.Postgres.KeepUpdateDumps(), log: log}
		for _, p := range procs {
			if p.Role == supervisor.RoleAPI && p.Cmd != nil {
				db.api = p.Cmd
			}
		}
		dbStep = &sharedStep{wait: detail("db_waiting", "Waiting for the library database"), run: db.run}
	}
	var venvStep *sharedStep
	if py := supervisor.PythonOverride(cfg); py == "" {
		venv := &venvGate{spec: appvenv.Spec{Payload: dir, Venv: filepath.Join(cfg.StateDir(), "venv")}}
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
	pg       *pgbundle.Bundle // the bundled Postgres; nil: a database of the user's own (no dump, no restore)
	api      *exec.Cmd        // the api's command: its binary and environment run the migrations; nil: no api here
	keep     int              // pre-update dumps kept ([postgres] update_dumps)
	log      io.Writer
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
)

// apiExitSchemaAhead is maktaba-api migrate's exit when the database holds
// migrations newer than the ones it has (api/main.go): a newer Maktaba ran here.
const apiExitSchemaAhead = 3

// errSchemaAhead: the database is newer than this payload's migrations.
var errSchemaAhead = errors.New("the database holds migrations newer than this Maktaba's")

// runMigrateUp runs the api's migrations with its binary and environment, with
// no time limit of their own (nothing else uses the database meanwhile, and a
// stop of the server ends them). Their output goes to the server's log; an
// error carries their last lines.
func runMigrateUp(ctx context.Context, api *exec.Cmd, log io.Writer) error {
	cmd := exec.CommandContext(ctx, api.Path, "migrate", "--timeout", "0", "up")
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

func (g *migrationGate) save(rec apppayload.Database) error {
	if err := apppayload.WriteDatabase(g.stateDir, rec); err != nil {
		return detailErr("db_record_failed", fmt.Sprintf("the library database's record (%s) cannot be saved: %v",
			apppayload.DatabasePath(g.stateDir), err), apppayload.DatabasePath(g.stateDir), err.Error())
	}
	return nil
}

// run is the database step (the package comment); a failure that no retry in
// this run can mend is a supervisor.Halt.
func (g *migrationGate) run(ctx context.Context, report func(supervisor.Detail)) error {
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
		case held != "" && held != cur && apppayload.Older(cur, held):
			return supervisor.Halt(downgrade(cur, rec))
		}
		return nil
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
	if held != "" && held != cur && apppayload.Older(cur, held) {
		return supervisor.Halt(downgrade(cur, rec))
	}

	update := apppayload.Database{Version: cur, State: apppayload.DatabaseMigrating}
	switch {
	case rec.State == apppayload.DatabaseMigrating:
		// a start that did not finish migrating (stopped, cut off, its putting
		// back failed): go on; the dump is still the one from before it all
		update.From, update.Dump = rec.From, rec.Dump
	case held == "" || held != cur:
		update.From = held
		if g.pg != nil {
			report(detail("db_dumping", fmt.Sprintf("Saving the library database before the update to %s", cur), held, cur))
			dump, err := dumpBeforeUpdate(ctx, *g.pg, held, cur, g.keep)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return detailErr("db_dump_failed", fmt.Sprintf("the library database could not be saved before the "+
					"update to %s, so the update waits (nothing was changed): %v", cur, err), cur, err.Error())
			}
			fmt.Fprintf(g.log, "[serve] update %s -> %s: the library database is saved in %s\n", orUnknown(held), cur, dump)
			update.Dump = dump
		}
	default: // the same release: nothing to update
		update = apppayload.Database{}
	}
	if update.State != "" {
		if err := g.save(update); err != nil {
			return err
		}
	}
	if update.State != "" {
		report(detail("db_migrating", fmt.Sprintf("Updating the library database for %s", cur), cur))
	} else {
		report(detail("db_checking", "Checking the library database", cur))
	}
	err = migrateUp(ctx, g.api, g.log)
	if ctx.Err() != nil {
		return ctx.Err() // stopped: the next start goes on (the record says migrating)
	}
	switch {
	case err == nil:
		if update.State == "" {
			if rec.State == apppayload.DatabaseMigrated {
				return nil
			}
			update = apppayload.Database{Version: cur} // restored, and this is the release from before
		}
		update.State = apppayload.DatabaseMigrated
		return g.save(update)
	case errors.Is(err, errSchemaAhead):
		// nothing was changed (the dump of a development build, whose order the
		// versions could not tell, is not needed): the record goes back as it was
		if update.State != "" {
			if err := g.save(rec); err != nil {
				return err
			}
		}
		return supervisor.Halt(detailErr("db_ahead", fmt.Sprintf("the library database was changed by a newer "+
			"Maktaba than this one (%s), whose code would not know those changes: install that newer version again (%v)",
			cur, err), cur, err.Error()))
	case update.Dump == "":
		// nothing to put back (the same release, or a database of the user's
		// own): tried again like any start step
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
// it; the database is then as it was before that update.
func (g *migrationGate) restore(ctx context.Context, rec apppayload.Database, report func(supervisor.Detail)) (apppayload.Database, error) {
	if g.pg == nil {
		return rec, supervisor.Halt(detailErr("db_no_cluster", fmt.Sprintf("the update to %s could not change the "+
			"library database (%s), and this Maktaba runs no database of its own to put %s back into",
			rec.Failed, rec.Error, rec.Dump), rec.Failed, rec.Error, rec.Dump))
	}
	report(detail("db_restoring", fmt.Sprintf("The update to %s could not change the library database: putting it "+
		"back as it was", rec.Failed), rec.Failed, rec.Dump))
	fmt.Fprintf(g.log, "[serve] the update to %s failed (%s): restoring %s\n", rec.Failed, rec.Error, rec.Dump)
	err := restoreDump(ctx, *g.pg, rec.Dump, g.log)
	if ctx.Err() != nil {
		return rec, ctx.Err() // the record still says restoring: the next start finishes it
	}
	if err != nil {
		return rec, supervisor.Halt(detailErr("db_restore_failed", fmt.Sprintf("the update to %s could not change "+
			"the library database (%s), and putting the database back from %s failed too: %v. Maktaba stays stopped; "+
			"its next start tries to put it back again", rec.Failed, rec.Error, rec.Dump, err),
			rec.Failed, rec.Error, rec.Dump, err.Error()))
	}
	done := apppayload.Database{Version: rec.From, State: apppayload.DatabaseRestored, From: rec.From, Dump: rec.Dump,
		Failed: rec.Failed, Error: rec.Error}
	if err := g.save(done); err != nil {
		return rec, err
	}
	fmt.Fprintf(g.log, "[serve] the library database is back as it was before the update to %s\n", rec.Failed)
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

// venvGate builds the app's venv for every pipeline process (the main worker
// and the transcription workers) through a sharedStep.
type venvGate struct {
	spec appvenv.Spec
}

// venvRun is appvenv.Run (tests replace it).
var venvRun = appvenv.Run

func (g *venvGate) run(ctx context.Context, report func(supervisor.Detail)) error {
	spec := g.spec
	logDir := config.LogsDir()
	if err := os.MkdirAll(logDir, 0o755); err == nil {
		if f, err := os.OpenFile(filepath.Join(logDir, "venv.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			defer f.Close()
			spec.Log = f
		}
	}
	logPath := filepath.Join(logDir, "venv.log")
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
		return detailErr("venv_failed", fmt.Sprintf("the transcription tools could not be installed: %v (log: %s)",
			err, logPath), err.Error(), logPath)
	}
	if !res.Reused {
		fmt.Fprintf(os.Stderr, "[serve] the pipeline's venv is ready (pipeline %s, Python %s)\n", res.Pipeline, res.Python)
	}
	return nil
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

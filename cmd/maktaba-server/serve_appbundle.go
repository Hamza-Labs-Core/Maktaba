package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appvenv"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// The server's start inside the desktop app (Epic 38 story 38.12, for the Mac,
// Windows and Linux; specs/epics/38-windows-app/contract-update.md and Epic 36's
// contract-update.md). The app's updater replaces the whole payload; what the
// update changed is told by comparing the payload's payload.json with the copy
// saved after the last start that ran healthy (apppayload):
//
//   - the pipeline's processes wait for the venv: the bootstrap builds it, or
//     brings it up to date when the constraints, the wheel or the Python
//     changed (it does nothing, in a moment, when nothing did);
//   - with a new version the api waits for a dump of the database, taken
//     before its migrations run (backups/pre-<old>-to-<new>.dump, the way
//     back);
//   - an older release than the one that ran is refused (its code does not
//     know the newer one's migrations), and so is a Postgres of another major
//     than the cluster's;
//   - once every process ran healthy for confirmAfter, the payload is saved as
//     the one that ran.
//
// What each process waits on shows as its detail in /processes.

// appBundleGates gives procs their Prepare steps for the payload in dir.
func appBundleGates(cfg config.Config, dir string, pg *pgbundle.Bundle, procs []supervisor.Process, log io.Writer) []supervisor.Process {
	cur, err := apppayload.Read(filepath.Join(dir, "payload.json"))
	if err != nil {
		fmt.Fprintf(log, "[serve] the app's payload.json cannot be read (%v): starting without the update checks\n", err)
		return procs
	}
	confirmed, ok, err := apppayload.Confirmed(cfg.StateDir())
	if err != nil {
		fmt.Fprintf(log, "[serve] the last payload that ran cannot be read (%v): treated as a first start\n", err)
	}
	change := apppayload.Compare(confirmed, ok, cur)
	venv := &venvGate{spec: appvenv.Spec{Payload: dir, Venv: filepath.Join(cfg.StateDir(), "venv")}}
	for i := range procs {
		p := &procs[i]
		switch {
		case p.Role == supervisor.RolePostgres && pg != nil:
			p.Prepare = postgresMajorGate(*pg, cur.PostgresMajor())
		case p.Role == supervisor.RoleAPI:
			p.Prepare = updateGate(change, pg)
		case p.Role == supervisor.RolePipeline:
			p.Prepare = venv.prepare
		}
	}
	return procs
}

// postgresMajorGate refuses to start Postgres on a cluster another major made:
// the server would refuse the data files; an upgrade between majors is a dump
// and a restore no update does yet.
func postgresMajorGate(b pgbundle.Bundle, payloadMajor string) func(context.Context, func(string)) error {
	return func(context.Context, func(string)) error {
		have, err := b.ClusterMajor()
		if err != nil {
			return fmt.Errorf("the library database cannot be read: %w", err)
		}
		if have != "" && payloadMajor != "" && have != payloadMajor {
			return fmt.Errorf("the library database was made by PostgreSQL %s and this Maktaba carries PostgreSQL %s: "+
				"the database needs an upgrade between the two, which no update does yet", have, payloadMajor)
		}
		return nil
	}
}

// backupAs is Bundle.BackupAs (tests replace it).
var backupAs = func(ctx context.Context, b pgbundle.Bundle, name string) (string, error) {
	return b.BackupAs(ctx, name)
}

// dumpName makes a version safe in a file name.
var dumpName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// updateGate: the api starts once a new version's dump is taken, and never over
// a database a newer release ran.
func updateGate(c apppayload.Change, pg *pgbundle.Bundle) func(context.Context, func(string)) error {
	var once sync.Mutex
	dumped := false
	return func(ctx context.Context, report func(string)) error {
		if c.Downgrade {
			return fmt.Errorf("this Maktaba (%s) is older than the one that last ran here (%s): its database may hold "+
				"changes this version does not know. Install %s or later (the dump taken before that update is in the backups folder)",
				c.To, c.From, c.From)
		}
		if !c.Upgrade || pg == nil {
			return nil
		}
		once.Lock()
		defer once.Unlock()
		if dumped {
			return nil
		}
		name := "pre-" + dumpName.ReplaceAllString(c.From, "_") + "-to-" + dumpName.ReplaceAllString(c.To, "_")
		if _, err := os.Stat(filepath.Join(pg.Backups, name+".dump")); err == nil {
			dumped = true // taken by a start that did not get to run healthy
			return nil
		}
		report(fmt.Sprintf("Saving the library database before the update to %s", c.To))
		path, err := backupAs(ctx, *pg, name)
		if err != nil {
			return fmt.Errorf("the database could not be saved before the update's changes: %w", err)
		}
		fmt.Fprintf(os.Stderr, "[serve] update %s -> %s: the database is saved in %s\n", c.From, c.To, path)
		dumped = true
		return nil
	}
}

// venvGate builds the app's venv once for every pipeline process (the main
// worker and the transcription workers): the first runs the bootstrap, the
// others wait for it.
type venvGate struct {
	spec appvenv.Spec
	mu   sync.Mutex
	done bool
}

// venvRun is appvenv.Run (tests replace it).
var venvRun = appvenv.Run

func (g *venvGate) prepare(ctx context.Context, report func(string)) error {
	report("Waiting for the transcription tools")
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.done {
		return nil
	}
	spec := g.spec
	logDir := config.LogsDir()
	if err := os.MkdirAll(logDir, 0o755); err == nil {
		if f, err := os.OpenFile(filepath.Join(logDir, "venv.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			defer f.Close()
			spec.Log = f
		}
	}
	res, err := venvRun(ctx, spec, func(p appvenv.Progress) {
		report(fmt.Sprintf("Installing the transcription tools: %s (%d%%)", p.Message, int(p.Fraction*100)))
	})
	if err != nil {
		return fmt.Errorf("the transcription tools could not be installed: %w (log: %s)", err, filepath.Join(logDir, "venv.log"))
	}
	if !res.Reused {
		fmt.Fprintf(os.Stderr, "[serve] the pipeline's venv is ready (pipeline %s, Python %s)\n", res.Pipeline, res.Python)
	}
	g.done = true
	return nil
}

// confirmAppPayload saves the payload as the one that ran once every process
// has run healthy for confirmAfter (a crash before that repeats the update's
// steps at the next start, each of which is safe to repeat).
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

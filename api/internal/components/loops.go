package components

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/pgdsn"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store/pqlisten"
)

// Host is the API's name for its loops in the catalog (`api:<name>`).
const Host = "api"

// NewRunner is the API's component runner (story 51.05). Every background loop of the
// API's start files is declared on it by its catalog name; until Start reads the
// home's desired state every component is on, so the API starts what it always
// started. MAKTABA_COMPONENTS_RECONCILE=off keeps every loop running.
func NewRunner(logger *slog.Logger) *runner.Runner { return NewRunnerFor(logger, "") }

// NewRunnerFor is NewRunner on machine: a joined machine's cluster_nodes id follows that
// machine's desired state (the home's with the machine's overrides, component_desired_for)
// and reports its status rows as that machine; "" is the home.
func NewRunnerFor(logger *slog.Logger, machine string) *runner.Runner {
	return runner.New(runner.Options{Host: Host, Machine: machine, Logger: logger, NoReconcile: runner.ReconcileOff()})
}

// Start reconciles r against the home's database for the rest of the process: the
// desired state from the components tables (a LISTEN on dsn, a revision poll every
// 30 s), the status in component_status rows with part "api". It never blocks: dsn is
// settled and the listener dialled off the caller's path (a joined machine starts while
// the home's database is gone), and a database that does not answer leaves every loop
// as it is until it does. Before the first pass it repairs a mirror that disagrees with
// the desired state (RepairMirrors) on the home (a joined machine has none to repair:
// its login cannot write them).
func Start(r *runner.Runner, db *sql.DB, dsn func() string, logger *slog.Logger) {
	if r == nil || db == nil {
		return
	}
	st := New(db)
	src := runner.Source{Store: st}
	go func() {
		if !store.IsSQLite(db) && dsn != nil {
			if d := dsn(); d != "" {
				if l, err := listenWithin(d, pgdsn.ConnectTimeout); err != nil {
					logger.Warn("components: no change listener; polling the desired state every 30 s",
						"err", err.Error(), "event", "components_listen_failed")
				} else {
					src.Listener = l
				}
			}
		}
		// a switch a release before 51.07 changed alone is put back in step first
		if r.Machine() == store.Home {
			rctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if _, err := RepairMirrors(rctx, st, logger); err != nil {
				logger.Warn("components: the mirrors were not checked", "err", err.Error(),
					"event", "component_mirror_repair_failed")
			}
			cancel()
		}
		r.Run(context.Background(), src)
	}()
}

// listenWithin opens the change listener on dsn, waiting at most d: lib/pq bounds only
// the dial, so a database that takes the connection and never answers would hold the
// runner for ever. A listener that comes up after d is closed (the runner polls).
func listenWithin(dsn string, d time.Duration) (*pqlisten.Listener, error) {
	type res struct {
		l   *pqlisten.Listener
		err error
	}
	ch := make(chan res, 1)
	go func() {
		l, err := pqlisten.New(dsn, nil)
		ch <- res{l, err}
	}()
	select {
	case r := <-ch:
		return r.l, r.err
	case <-time.After(d):
		go func() {
			if r := <-ch; r.l != nil {
				_ = r.l.Close()
			}
		}()
		return nil, fmt.Errorf("the database did not answer within %s", d)
	}
}

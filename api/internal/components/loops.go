package components

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

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
// 30 s), the status in component_status rows with part "api". It never blocks: a
// database that does not answer leaves every loop as it is until it does. Before the
// first pass it repairs a mirror that disagrees with the desired state (RepairMirrors)
// on the home (a joined machine has none to repair: its login cannot write them).
func Start(r *runner.Runner, db *sql.DB, dsn string, logger *slog.Logger) {
	if r == nil || db == nil {
		return
	}
	st := New(db)
	src := runner.Source{Store: st}
	if !store.IsSQLite(db) && dsn != "" {
		l, err := pqlisten.New(dsn, nil)
		if err != nil {
			logger.Warn("components: no change listener; polling the desired state every 30 s",
				"err", err.Error(), "event", "components_listen_failed")
		} else {
			src.Listener = l
		}
	}
	go func() {
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

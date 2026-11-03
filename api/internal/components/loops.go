package components

import (
	"context"
	"database/sql"
	"log/slog"

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
func NewRunner(logger *slog.Logger) *runner.Runner {
	return runner.New(runner.Options{Host: Host, Logger: logger, NoReconcile: runner.ReconcileOff()})
}

// Start reconciles r against the home's database for the rest of the process: the
// desired state from the components tables (a LISTEN on dsn, a revision poll every
// 30 s), the status in component_status rows with part "api". It never blocks: a
// database that does not answer leaves every loop as it is until it does.
func Start(r *runner.Runner, db *sql.DB, dsn string, logger *slog.Logger) {
	if r == nil || db == nil {
		return
	}
	src := runner.Source{Store: New(db)}
	if !store.IsSQLite(db) && dsn != "" {
		l, err := pqlisten.New(dsn, nil)
		if err != nil {
			logger.Warn("components: no change listener; polling the desired state every 30 s",
				"err", err.Error(), "event", "components_listen_failed")
		} else {
			src.Listener = l
		}
	}
	go r.Run(context.Background(), src)
}

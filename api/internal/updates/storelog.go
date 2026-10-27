package updates

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
)

// noteStore logs a store read or write the driver goes on without: the run's
// record, a target's state or a machine's drain flag may then be stale, and
// the line says which (update_store_failed). A cancelled context (the API
// stopping) and a row that is not there are not failures.
func noteStore(logger *slog.Logger, op string, err error, attrs ...any) {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, sql.ErrNoRows) {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	args := append([]any{"op", op}, attrs...)
	args = append(args, "err", err.Error(), "event", "update_store_failed")
	logger.Warn("update: a store write failed; the run goes on", args...)
}

// storeExec is noteStore for a statement run with DB.ExecContext:
// storeExec(log, "drain")(db.ExecContext(...)).
func storeExec(logger *slog.Logger, op string, attrs ...any) func(sql.Result, error) {
	return func(_ sql.Result, err error) { noteStore(logger, op, err, attrs...) }
}

package pgbundle

import (
	"io"
	"os"
)

// ChildOutput is serve's stderr before it stamps its own lines with their time (serve's
// stderr_stamp.go): a program this package runs (initdb, a server of its own, psql,
// pg_restore) that is given serve's stderr writes there instead, its lines carrying their
// own times and Postgres's levels. nil: as given.
var ChildOutput *os.File

// childLog is log for a program this package runs: serve's original stderr when log is
// serve's (stamped) stderr, else log.
func childLog(log io.Writer) io.Writer {
	if f, ok := log.(*os.File); ok && ChildOutput != nil && f == os.Stderr {
		return ChildOutput
	}
	return log
}

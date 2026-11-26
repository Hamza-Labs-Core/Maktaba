package pgbundle

import (
	"errors"
	"fmt"
	"testing"
)

// Which failed try of a restore is made again with one worker: a failure of putting the
// dump's data in or of checking it, not one of the restore's own last steps (the analysis,
// the save before the swap, the swap) — the data was whole before those, so the whole
// restore again buys nothing for them.
func TestRetryOneWorker(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("pg_restore the rows (the rows): exit status 1"), true},
		{&Missing{}, true}, // the check of what was put back
		{&afterRestoreError{errors.New("the restore stopped before its next step: no")}, false},
		{fmt.Errorf("put the restored database in place of maktaba: %w", &afterRestoreError{errors.New("psql: no")}), false},
	} {
		if got := retryOneWorker(c.err); got != c.want {
			t.Errorf("%v: %v, want %v", c.err, got, c.want)
		}
	}
}

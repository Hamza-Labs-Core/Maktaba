package statements

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/lib/pq"
)

// A connection the database closed is a 503 the plugin's side retries, never the
// statement's own failure (409): a job is not failed for good by a database restart.
func TestDBError_AConnectionLostIsRetryable(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		status  int
		code    string
		badConn bool
	}{
		{"the driver's bad connection", driver.ErrBadConn, 503, CodeDatabase, true},
		{"wrapped", fmt.Errorf("exec: %w", driver.ErrBadConn), 503, CodeDatabase, true},
		{"the database shut down (57P01)", &pq.Error{Code: "57P01", Message: "terminating connection due to administrator command"}, 503, CodeDatabase, true},
		{"the database crashed (57P02)", &pq.Error{Code: "57P02", Message: "crash shutdown"}, 503, CodeDatabase, true},
		{"the database is starting (57P03)", &pq.Error{Code: "57P03", Message: "the database system is starting up"}, 503, CodeDatabase, true},
		{"connection failure (08006)", &pq.Error{Code: "08006", Message: "connection failure"}, 503, CodeDatabase, true},
		{"another error of the connection", io.ErrUnexpectedEOF, 503, CodeDatabase, false},
		{"a unique violation is the statement's own", &pq.Error{Code: "23505", Message: "duplicate key"}, 409, CodeDatabase, false},
		{"a cancelled statement is the statement's own", &pq.Error{Code: "57014", Message: "canceling statement"}, 409, CodeDatabase, false},
		{"a table outside the role", &pq.Error{Code: "42501", Message: "permission denied"}, 403, CodeCapability, false},
	}
	for _, tc := range cases {
		ref := dbError(tc.err)
		if ref.Status != tc.status || ref.Code != tc.code || ref.badConn != tc.badConn {
			t.Errorf("%s: %d %s badConn=%v, want %d %s badConn=%v", tc.name, ref.Status, ref.Code, ref.badConn,
				tc.status, tc.code, tc.badConn)
		}
	}
	if ref := dbError(errors.New("x")); ref.Detail != "x" {
		t.Errorf("detail %q", ref.Detail)
	}
}

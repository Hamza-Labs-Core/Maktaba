package queues

import (
	"database/sql/driver"
	"net/http"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

func runningRule() *queuestest.Rule {
	row := jobRefRow(11, "transcribe", "running")
	row[len(row)-1] = "node-n1-1"
	return &queuestest.Rule{Match: "order by j.started_at nulls last, j.id", Cols: jobRefCols(),
		Rows: [][]driver.Value{row}}
}

func TestRunning_FiltersByHost(t *testing.T) {
	cases := []struct {
		name, target, frag string
		args               int
	}{
		{"all machines", "/api/queues/running", "", 0},
		{"the home", "/api/queues/running?host=home", "where node_id is null", 0},
		{"a joined machine", "/api/queues/running?host=" + uidA, "where node_id = $1::uuid", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(runningRule())
			rec := f.do(http.MethodGet, tc.target, "", viewer())
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body)
			}
			var out struct {
				Jobs []JobRef `json:"jobs"`
			}
			decode(t, rec, &out)
			if len(out.Jobs) != 1 || out.Jobs[0].ClaimedBy == nil || *out.Jobs[0].ClaimedBy != "node-n1-1" {
				t.Fatalf("jobs = %+v", out.Jobs)
			}
			calls := f.db.Find("order by j.started_at nulls last")
			if len(calls) != 1 {
				t.Fatalf("calls = %+v", f.db.Calls())
			}
			sql := calls[0].SQL
			if !strings.Contains(sql, "j.state in ('claimed','running','resuming')") {
				t.Errorf("not limited to in-flight jobs: %s", sql)
			}
			if tc.frag == "" {
				if strings.Contains(sql, "from workers") {
					t.Errorf("unfiltered query filters by workers: %s", sql)
				}
			} else if !strings.Contains(sql, tc.frag) {
				t.Errorf("missing %q in %s", tc.frag, sql)
			}
			if len(calls[0].Args) != tc.args {
				t.Errorf("args = %v", calls[0].Args)
			}
			if tc.args == 1 && calls[0].Args[0] != uidA {
				t.Errorf("node arg = %v", calls[0].Args[0])
			}
		})
	}
}

func TestRunning_RejectsBadHostAndAnonymous(t *testing.T) {
	f := newFixture(runningRule())
	if rec := f.do(http.MethodGet, "/api/queues/running?host=not-a-node", "", viewer()); rec.Code != http.StatusBadRequest {
		t.Errorf("bad host status = %d", rec.Code)
	}
	if rec := f.do(http.MethodGet, "/api/queues/running", "", nil); rec.Code != http.StatusForbidden {
		t.Errorf("anonymous status = %d", rec.Code)
	}
	if n := len(f.db.Calls()); n != 0 {
		t.Errorf("ran %d statements", n)
	}
}

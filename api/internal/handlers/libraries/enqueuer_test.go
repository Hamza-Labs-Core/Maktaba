package libraries

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

// The scan enqueue must go through the SQL function (plan-06-11 D8) so
// Go and Python share coalescing rules.
func TestEnqueueScan_UsesSQLFunction(t *testing.T) {
	const lib = "11111111-1111-1111-1111-111111111111"
	tests := []struct {
		name    string
		outcome string
		jobID   driver.Value
		wantID  string
		wantErr bool
	}{
		{"inserted", "inserted", int64(7), "7", false},
		{"coalesced returns the live job", "coalesced", int64(5), "5", false},
		{"rerun requested", "rerun_requested", int64(9), "9", false},
		{"disabled", "disabled", nil, "", true},
		{"invalid subject", "invalid_subject", nil, "", true},
		{"unknown stage", "unknown_stage", nil, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := queuestest.New(&queuestest.Rule{
				Match: "processing_jobs_enqueue",
				Cols:  []string{"job_id", "outcome"},
				Rows:  [][]driver.Value{{tc.jobID, tc.outcome}},
			})
			defer db.Close()
			got, err := (&PostgresJobEnqueuer{DB: db.DB}).EnqueueScan(context.Background(), lib, 50)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if got != tc.wantID {
				t.Errorf("job id %q want %q", got, tc.wantID)
			}
			calls := db.Find("processing_jobs_enqueue")
			if len(calls) != 1 {
				t.Fatalf("want 1 enqueue call, got %d", len(calls))
			}
			a := calls[0].Args
			if a[0] != "library" || a[1] != lib || a[2] != "scan" || a[3] != int64(50) {
				t.Errorf("args = %v", a[:4])
			}
			if !strings.Contains(a[6].(string), `"why":"library scan"`) {
				t.Errorf("reason = %v", a[6])
			}
		})
	}
}

func TestEnqueueScan_NoLegacyInsert(t *testing.T) {
	db := queuestest.New(&queuestest.Rule{
		Match: "processing_jobs_enqueue", Cols: []string{"job_id", "outcome"},
		Rows: [][]driver.Value{{int64(1), "inserted"}},
	})
	defer db.Close()
	if _, err := (&PostgresJobEnqueuer{DB: db.DB}).EnqueueScan(context.Background(), "11111111-1111-1111-1111-111111111111", 50); err != nil {
		t.Fatal(err)
	}
	for _, c := range db.Calls() {
		if strings.Contains(c.SQL, "insert into processing_jobs") {
			t.Errorf("handler still INSERTs directly: %s", c.SQL)
		}
	}
}

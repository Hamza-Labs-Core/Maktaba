// Story 9.6 AC-1 - concrete library-scoped scan-job enqueuer.
//
// `POST /api/libraries/{id}/scan` creates (or re-triggers) the library's
// SCAN job. Since plan-06-11 the INSERT is no longer written here: both
// the API and the Python engine call the SQL function
// processing_jobs_enqueue(...), so the two languages cannot disagree
// about coalescing. A scan on a library that already has a live scan
// coalesces into it (outcome coalesced / rerun_requested) and returns
// that job's id, which keeps the endpoint idempotent. There is
// deliberately no "skip when a done scan exists" branch - a library is
// always re-scannable because its on-disk tree mutates out of band.
package libraries

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
)

// PostgresJobEnqueuer is the production JobEnqueuer. Wired in router/p6.go.
type PostgresJobEnqueuer struct {
	DB *sql.DB
}

// EnqueueScan enqueues the library-scoped SCAN job and returns its
// BIGSERIAL id rendered as a decimal string (the HTTP contract is a
// string job_id).
func (e *PostgresJobEnqueuer) EnqueueScan(ctx context.Context, libraryID string, priority int) (string, error) {
	res, err := queues.Enqueue(ctx, e.DB, queues.EnqueueParams{
		SubjectKind: queues.KindLibrary,
		SubjectID:   libraryID,
		Stage:       "scan",
		Priority:    &priority,
		Reason:      queues.ReasonFor(ctx, "library scan", nil),
	})
	if err != nil {
		return "", err
	}
	if !res.Accepted() || res.JobID == nil {
		return "", fmt.Errorf("enqueue_scan: processing_jobs_enqueue returned %q", res.Outcome)
	}
	return strconv.FormatInt(*res.JobID, 10), nil
}

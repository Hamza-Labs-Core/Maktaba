// Story 9.6 AC-1 — concrete library-scoped scan-job enqueuer.
//
// `POST /api/libraries/{id}/scan` used to soft-fail (202 with an empty
// job_id) because production left `Handler.JobEnqueuer` nil. The
// pipeline already owns the *processing* side of a SCAN job
// (`pipeline/.../db/jobs.py:enqueue_scan`) and the slot-0058 schema
// already supports library-scoped scan rows
// (`processing_jobs.library_id`, `video_id` NULL, the partial unique
// index `processing_jobs_one_live_scan_per_library`). The only missing
// piece was the API-side INSERT that creates the row a worker then
// claims.
//
// This file is that INSERT. It mirrors the pipeline's `enqueue_scan`
// byte-for-byte on the SQL contract so a scan job created via the API
// is indistinguishable from one the pipeline would create:
//
//   - same columns: (library_id, video_id NULL, stage 'scan',
//     state 'pending', priority, payload, max_attempts)
//   - same idempotency: ON CONFLICT (library_id, stage) WHERE
//     stage='scan' AND state IN (<5 live states>) DO NOTHING, then a
//     fallback SELECT of the existing live row's id (the partial
//     unique index `processing_jobs_one_live_scan_per_library`
//     guarantees at most one live scan per library, exactly like the
//     pipeline relies on).
//
// There is deliberately no "skip when a done scan exists" branch — a
// library is always re-scannable because its on-disk tree mutates out
// of band (the same reasoning documented in jobs.py:enqueue_scan).
package libraries

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

// liveScanStates is the exact set the slot-0058 partial unique index
// `processing_jobs_one_live_scan_per_library` predicates on (and the
// same five states the per-video index from slot 0002 uses). Kept as a
// named constant so the fallback SELECT and the documented invariant
// can never drift from the index.
const liveScanStatesSQL = `('pending','claimed','running','resuming','paused')`

// insertScanSQL is the API mirror of pipeline jobs.py `_INSERT_SCAN_SQL`.
// The ON CONFLICT target + predicate match the slot-0058 partial
// unique index exactly so concurrent API+pipeline enqueues collide on
// the index (loser's INSERT is a no-op) instead of creating a second
// live scan.
const insertScanSQL = `
INSERT INTO processing_jobs
       (library_id, video_id, stage, state, priority, payload, max_attempts)
VALUES ($1, NULL, 'scan', 'pending', $2, $3, $4)
ON CONFLICT (library_id, stage)
   WHERE stage = 'scan'
     AND state IN ` + liveScanStatesSQL + `
DO NOTHING
RETURNING id
`

// fallbackLiveScanSQL is the API mirror of pipeline jobs.py
// `_FALLBACK_LIVE_SCAN_SQL`: when the INSERT was swallowed by the
// partial unique index, the live row already exists — return its id
// with no second job created (idempotent re-trigger).
const fallbackLiveScanSQL = `
SELECT id FROM processing_jobs
 WHERE library_id = $1
   AND stage      = 'scan'
   AND state IN ` + liveScanStatesSQL + `
 LIMIT 1
`

// PostgresJobEnqueuer is the production JobEnqueuer. It is the API-side
// twin of pipeline `enqueue_scan`: same SQL, same idempotency, same
// slot-0058 contract. Wired in router/p6.go.
type PostgresJobEnqueuer struct {
	DB *sql.DB
}

// EnqueueScan inserts a library-scoped pending SCAN job, or returns the
// id of the already-live one. Default max_attempts mirrors the
// processing_jobs schema default (3) and the pipeline `enqueue_scan`
// default. The job_id is the BIGSERIAL `processing_jobs.id` rendered as
// a decimal string (the HTTP contract is a string job_id; the pipeline
// surfaces the same integer).
func (e *PostgresJobEnqueuer) EnqueueScan(ctx context.Context, libraryID string, priority int) (string, error) {
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	var id int64
	err = tx.QueryRowContext(ctx, insertScanSQL, libraryID, priority, nil, 3).Scan(&id)
	switch {
	case err == nil:
		if cErr := tx.Commit(); cErr != nil {
			return "", cErr
		}
		return strconv.FormatInt(id, 10), nil
	case errors.Is(err, sql.ErrNoRows):
		// INSERT swallowed by the partial unique index → a live scan
		// already exists. Return its id (idempotent re-trigger), mirror
		// of jobs.py:enqueue_scan's `_FALLBACK_LIVE_SCAN_SQL` branch.
		var liveID int64
		if sErr := tx.QueryRowContext(ctx, fallbackLiveScanSQL, libraryID).Scan(&liveID); sErr != nil {
			if errors.Is(sErr, sql.ErrNoRows) {
				// Truly impossible by construction: the INSERT only
				// fails-to-RETURNING when a live row exists, so the
				// SELECT must find one. A miss means the slot-0058
				// schema invariant was violated.
				return "", errors.New("enqueue_scan: INSERT swallowed but no live scan row found — schema invariant violated")
			}
			return "", sErr
		}
		if cErr := tx.Commit(); cErr != nil {
			return "", cErr
		}
		return strconv.FormatInt(liveID, 10), nil
	default:
		return "", err
	}
}

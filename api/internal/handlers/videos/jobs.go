package videos

// The release JobControl (Story 7.5): POST /api/videos/{id}/process and
// /reprocess queue real pipeline jobs through processing_jobs_enqueue, the
// one SQL function every Go and Python enqueue goes through (the library
// scan, the queue retrigger, the display-title refresh). Before this, the
// router built the handler without a JobControl, so both routes answered
// 200 with no job in every build.

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
)

// NotQueuedError is an enqueue the pipeline refused: the outcome
// processing_jobs_enqueue answered (unknown_stage, invalid_subject,
// disabled, skipped_component_off). No job exists.
type NotQueuedError struct {
	Stage   string
	Outcome string
}

func (e *NotQueuedError) Error() string {
	return fmt.Sprintf("stage %q not queued: %s", e.Stage, e.Outcome)
}

// PostgresJobs is the production JobControl.
type PostgresJobs struct{ DB *sql.DB }

// BumpOrEnqueue queues stage for the video as a forced run (someone asked for
// it: it runs now and runs even when its output is up to date), or bumps the
// live job of that stage to priority.
func (j PostgresJobs) BumpOrEnqueue(ctx context.Context, videoID, stage string, priority int) (int64, string, error) {
	return j.enqueue(ctx, videoID, stage, priority, false, "video_process")
}

// RollbackToStage queues fromStage as a forced cascade: it re-runs, and every
// stage after it re-runs too when it is done.
func (j PostgresJobs) RollbackToStage(ctx context.Context, videoID, fromStage string, priority int) ([]int64, error) {
	id, _, err := j.enqueue(ctx, videoID, fromStage, priority, true, "video_reprocess")
	if err != nil {
		return nil, err
	}
	return []int64{id}, nil
}

func (j PostgresJobs) enqueue(ctx context.Context, videoID, stage string, priority int, cascade bool, why string) (int64, string, error) {
	res, err := queues.Enqueue(ctx, j.DB, queues.EnqueueParams{
		SubjectKind: queues.KindVideo, SubjectID: videoID, Stage: stage,
		Priority: &priority, Force: true, ForceCascade: cascade,
		Reason: queues.ReasonFor(ctx, why, nil),
	})
	if err != nil {
		return 0, "", err
	}
	if !res.Accepted() || res.JobID == nil {
		return 0, "", &NotQueuedError{Stage: stage, Outcome: res.Outcome}
	}
	var state string
	if err := j.DB.QueryRowContext(ctx, `SELECT state FROM processing_jobs WHERE id = $1`, *res.JobID).Scan(&state); err != nil {
		return 0, "", err
	}
	return *res.JobID, state, nil
}

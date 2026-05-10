package states

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
)

// IllegalStateTransition is returned when (from, trigger, outcome) has
// no matching edge in the runtime transition table. It carries enough
// context for observability without leaking the entire table.
type IllegalStateTransition struct {
	VideoID string
	From    State
	Trigger Trigger
	Outcome Outcome
}

// Error implements error. Format is stable; tests grep for it.
func (e *IllegalStateTransition) Error() string {
	return fmt.Sprintf(
		"illegal state transition for video %s: from=%s trigger=%s outcome=%s",
		e.VideoID, e.From, e.Trigger, e.Outcome,
	)
}

// ErrIllegalTransition is the sentinel callers compare against with
// errors.Is. The concrete *IllegalStateTransition value still carries
// the from/trigger/outcome detail.
var ErrIllegalTransition = errors.New("illegal state transition")

// Is satisfies errors.Is so callers can write
// `errors.Is(err, states.ErrIllegalTransition)` without unwrapping.
func (e *IllegalStateTransition) Is(target error) bool {
	return target == ErrIllegalTransition
}

// txQuerier is the minimal subset of *sql.Tx that AdvanceAfterStage
// needs. Defined as an interface so callers can swap a fake in tests
// without spinning up a database.
type txQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// txBeginner is anything that can open a *sql.Tx. *sql.DB satisfies it.
type txBeginner interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// AdvanceAfterStage is the SOLE function that mutates videos.state.
// All callers — the orchestrator, the watcher (filesystem trigger),
// Epic 9 (library trigger), Epic 24 (integrity trigger) — funnel
// through here.
//
// Behavior:
//
//  1. Open a transaction and SELECT … FOR UPDATE to serialize
//     concurrent advances on the same row.
//  2. Re-read the current state inside the lock.
//  3. If the current state is a terminal-drop state (SUPERSEDED /
//     CORRUPTED / FAILED) the function logs `late_stage_finish` and
//     commits a no-op. The stage's work is wasted but the state is
//     correct (story edge case 1).
//  4. Look up (from, trigger, outcome). A miss returns
//     *IllegalStateTransition without changing state.
//  5. UPDATE videos.state and updated_at, COMMIT, return the new
//     state. The slot 0004 NOTIFY trigger fires
//     `videos.state_changed` automatically.
//
// The function is dialect-agnostic — the SQL is plain ANSI and works
// against either Postgres or SQLite (via the goose-managed migration
// suite). The pgx-flavored variant in plan-01-06 §7.2 is equivalent;
// we use database/sql here so the package has no third-party deps.
func AdvanceAfterStage(
	ctx context.Context,
	db txBeginner,
	log *slog.Logger,
	videoID string,
	trigger Trigger,
	outcome Outcome,
) (State, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	state, err := AdvanceInTx(ctx, tx, log, videoID, trigger, outcome)
	if err != nil {
		return state, err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return state, nil
}

// AdvanceInTx is the same logic as AdvanceAfterStage but reuses an
// existing transaction. Library merge / integrity sweep callers that
// need to run additional side-effects (e.g. cancel pending probe jobs
// when MISSING → DISCOVERED) take this path so the state flip and the
// side-effect commit atomically.
func AdvanceInTx(
	ctx context.Context,
	tx txQuerier,
	log *slog.Logger,
	videoID string,
	trigger Trigger,
	outcome Outcome,
) (State, error) {
	var current State
	err := tx.QueryRowContext(ctx,
		`SELECT state FROM videos WHERE id = $1 FOR UPDATE`,
		videoID,
	).Scan(&current)
	if err != nil {
		return "", fmt.Errorf("lock video %s: %w", videoID, err)
	}

	// Late-stage finish on a terminal row: log and no-op. Returning
	// the current state (not an error) lets the caller close out
	// cleanly without re-doing the work it already did.
	if current.IsTerminalDrop() {
		if log != nil {
			log.Info("late_stage_finish",
				"video_id", videoID,
				"current", string(current),
				"trigger", string(trigger),
				"outcome", string(outcome),
			)
		}
		return current, nil
	}

	target, ok := Lookup(current, trigger, outcome)
	if !ok {
		return current, &IllegalStateTransition{
			VideoID: videoID,
			From:    current,
			Trigger: trigger,
			Outcome: outcome,
		}
	}

	// The (TRANSCRIBED, *, partial) self-loop is a real edge: we run
	// the UPDATE so updated_at advances and the NOTIFY trigger fires
	// even though state is unchanged. Observers see the partial-gate
	// progress reflected on the row.
	_, err = tx.ExecContext(ctx,
		`UPDATE videos SET state = $1, updated_at = now() WHERE id = $2`,
		string(target), videoID,
	)
	if err != nil {
		return "", fmt.Errorf("update video %s: %w", videoID, err)
	}
	return target, nil
}

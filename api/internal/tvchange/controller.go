package tvchange

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
)

// CatalogID is the engine's catalog subject (pipeline engine/graph.py CATALOG_ID).
const CatalogID = "00000000-0000-0000-0000-00000000ca7a"

// Pipeline stages the controller enqueues (pipeline engine/stages/tv_*.py).
const (
	StageTVStage = "tv_stage"
	StageTVSwap  = "tv_swap"
	StageTVGC    = "tv_gc"
	// StageTVRollback (swap.go) is the reverse commit of a rollback.
)

// controllerLock is the advisory lock that keeps the controller to one API process
// (story 33.10 AC9).
const controllerLock = "tv_change_controller"

// EnqueueFunc queues a pipeline stage for the catalog subject with a payload.
type EnqueueFunc func(ctx context.Context, stage string, payload map[string]any, why string) error

// Controller drives changes through the states no admin presses (design §11.2):
// staged -> verifying -> ready (or back to draft on a fail), an approval given ahead, an approved change's
// swap time, an auto-promote rule switched on, a queued draft whose turn came, and
// the daily GC. All state is in the database: a restarted API resumes where the last
// one stopped (AC9).
type Controller struct {
	DB      *sql.DB
	Store   *Store
	Now     func() time.Time
	Logger  *slog.Logger
	Enqueue EnqueueFunc
	// Viewers counts the people watching (channel_runtime) for the quiet window; nil
	// reads channel_runtime.viewer_count.
	Viewers  func(ctx context.Context) int
	Interval time.Duration
	// Location is the quiet window's zone (nil: the server's local zone).
	Location *time.Location
	// Notifier receives the change notices (story 33.13); nil sends none (the audit
	// rows of automatic transitions are written either way).
	Notifier notify.Notifier
	// Staging is the front door's staging slot runtime (story 33.5); nil, or a
	// front door without slots, verifies on the database only.
	Staging StagingRuntime

	lastGC     time.Time
	interimWhy string
}

// NewController wires a controller over db.
func NewController(db *sql.DB, logger *slog.Logger) *Controller {
	if logger == nil {
		logger = slog.Default()
	}
	c := &Controller{DB: db, Store: &Store{DB: db}, Logger: logger, Interval: 5 * time.Second}
	c.Enqueue = c.enqueueQueue
	return c
}

func (c *Controller) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// enqueueQueue calls processing_jobs_enqueue; a pending job that coalesced is pointed
// at this change (one change in flight at a time, so the newest request wins).
func (c *Controller) enqueueQueue(ctx context.Context, stage string, payload map[string]any, why string) error {
	res, err := queues.Enqueue(ctx, c.DB, queues.EnqueueParams{
		SubjectKind: "catalog", SubjectID: CatalogID, Stage: stage, Force: true,
		Reason: map[string]any{"why": why, "by": "tv_change_controller"}, Payload: payload,
	})
	if err != nil {
		return err
	}
	if !res.Accepted() {
		return fmt.Errorf("enqueue %s: %s", stage, res.Outcome)
	}
	if res.JobID != nil && payload != nil && res.Outcome != queues.OutcomeInserted {
		if id, ok := payload["change_id"].(string); ok {
			_, _ = c.DB.ExecContext(ctx, `UPDATE processing_jobs
				SET payload = COALESCE(payload, '{}'::jsonb) || jsonb_build_object('change_id', $2::text)
				WHERE id = $1 AND state = 'pending'`, *res.JobID, id)
		}
	}
	return nil
}

// Run ticks until ctx ends. Only the process holding the advisory lock acts.
func (c *Controller) Run(ctx context.Context) {
	iv := c.Interval
	if iv <= 0 {
		iv = 5 * time.Second
	}
	var conn *sql.Conn
	defer func() {
		if conn != nil {
			_ = conn.Close()
		}
	}()
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		if !IsSQLite(c.DB) && conn == nil {
			cn, err := c.DB.Conn(ctx)
			if err == nil {
				var got bool
				if err := cn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, controllerLock).Scan(&got); err == nil && got {
					conn = cn
				} else {
					_ = cn.Close()
				}
			}
		}
		if IsSQLite(c.DB) || conn != nil {
			if err := c.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
				c.Logger.Warn("tv change controller tick failed", "event", "tv_change_controller_failed", "error", err.Error())
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick runs one pass over the open changes.
func (c *Controller) Tick(ctx context.Context) error {
	cfg, err := LoadConfig(ctx, c.DB)
	if err != nil {
		return err
	}
	// story 33.12 AC3: one open change; a second (opened before the rule, or while
	// the first was in flight) joins the older one
	if err := c.collapseOpen(ctx); err != nil {
		c.Logger.Warn("tv change collapse failed", "event", "tv_change_collapse_failed", "error", err.Error())
	}
	open, err := c.Store.List(ctx, []string{StateDraft, StateStaged, StateVerifying, StateReady, StateApproved}, 200)
	if err != nil {
		return err
	}
	inFlight := false
	for _, ch := range open {
		if ch.State != StateDraft {
			inFlight = true
		}
	}
	for i := len(open) - 1; i >= 0; i-- { // oldest first
		ch := open[i]
		switch ch.State {
		case StateStaged, StateVerifying:
			if c.Staging != nil {
				// story 33.5: the staging slot runs the release; the front door samples it
				var err error
				if ch.State == StateStaged {
					err = c.stageSlot(ctx, cfg, ch)
					if err == nil && ch.State == StateVerifying {
						err = c.verifyStream(ctx, cfg, ch)
					}
				} else {
					err = c.verifyStream(ctx, cfg, ch)
				}
				if err == nil {
					continue
				}
				if !errors.Is(err, ErrNoStagingRuntime) {
					c.Logger.Warn("tv change verification tick failed", "change_id", ch.ID, "error", err.Error(),
						"event", "tv_change_verify_tick_failed")
					continue
				}
				c.interimWhy = err.Error()
			}
			if ch.State == StateStaged {
				if err := c.Store.Move(ctx, c.DB, ch, StateVerifying, ActionStartVerify, nil,
					map[string]any{"interim": "verified on the database: no staging slot runtime"}, nil); err != nil {
					if moved(err) {
						continue
					}
					return err
				}
			}
			if err := c.verifyAndMove(ctx, ch); err != nil && !moved(err) {
				return err
			}
		case StateReady:
			if c.Staging != nil {
				if err := c.recheck(ctx, cfg, ch); err != nil && !errors.Is(err, ErrNoStagingRuntime) {
					c.Logger.Warn("tv change recheck failed", "change_id", ch.ID, "error", err.Error())
				}
				if ch.State != StateReady {
					continue
				}
			}
			if err := c.ready(ctx, cfg, ch); err != nil && !moved(err) {
				return err
			}
		case StateApproved:
			if ch.SwapAt == nil || !c.now().Before(*ch.SwapAt) {
				if err := c.startSwap(ctx, ch, ActionSwapStart, nil); err != nil {
					return err
				}
			}
		case StateDraft:
			if !inFlight && stageRequested(ch) {
				if err := c.requestStage(ctx, ch, false); err != nil {
					c.Logger.Warn("tv change stage enqueue failed", "change_id", ch.ID, "error", err.Error())
				} else {
					inFlight = true // one at a time: the next draft waits for this one
				}
			}
		}
	}
	// story 33.7/33.9: swaps the front door finished, rollbacks to commit
	if err := c.tickOps(ctx, cfg); err != nil {
		c.Logger.Warn("tv swap operations tick failed", "event", "tv_swap_ops_failed", "error", err.Error())
	}
	// story 35.6: an automatic update's change its run withdrew while it swapped goes
	// back once it is live
	if err := c.tickWithdrawn(ctx); err != nil {
		c.Logger.Warn("tv withdrawn changes tick failed", "event", "tv_change_withdrawn_failed", "error", err.Error())
	}
	// story 33.5: the staging slot ends with its change (cancel, fail, live)
	c.reconcileStaging(ctx)
	// story 33.13: notices and the audit rows of automatic transitions (best-effort:
	// a failure here never holds a change back)
	if err := c.notifyEvents(ctx, cfg); err != nil {
		c.Logger.Warn("tv change event feed failed", "event", "tv_change_feed_failed", "error", err.Error())
	}
	// story 33.13 AC1: the front door's failovers and staging crash loops
	if err := c.notifySlotEvents(ctx); err != nil {
		c.Logger.Warn("tv slot event feed failed", "event", "tv_slot_feed_failed", "error", err.Error())
	}
	if err := c.notifyTimers(ctx, open); err != nil {
		c.Logger.Warn("tv change timed notices failed", "event", "tv_change_timers_failed", "error", err.Error())
	}
	if c.now().Sub(c.lastGC) >= 24*time.Hour {
		if c.Enqueue != nil {
			if err := c.Enqueue(ctx, StageTVGC, map[string]any{}, "tv staging daily clean-up"); err == nil {
				c.lastGC = c.now()
			}
		}
	}
	return nil
}

// moved reports a change that left the state the tick read it in (an update reopened
// it, an admin cancelled it): the next tick sees where it is.
func moved(err error) bool {
	var ce *ConflictError
	return errors.As(err, &ce)
}

func stageRequested(ch *Change) bool {
	st, _ := ch.Diff["stage"].(map[string]any)
	if st == nil {
		return false
	}
	if _, ok := st["requested_at"]; !ok {
		return false
	}
	at, _ := st["enqueued_at"].(string)
	if at == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, at)
	// a stage job that did not move it within two minutes (queued behind another
	// change, or lost): ask again
	return err != nil || time.Since(t) > 2*time.Minute
}

// RequestStage marks a draft to be staged and enqueues tv_stage when nothing else is
// in flight (else the controller does when its turn comes).
func (c *Controller) RequestStage(ctx context.Context, ch *Change, actor *string) error {
	other, err := c.Store.InFlightChange(ctx, c.DB)
	if err != nil {
		return err
	}
	if other != nil && other.ID != ch.ID {
		st := map[string]any{"requested_at": c.now().Format(time.RFC3339)}
		ch.Diff["stage"] = st
		if err := c.Store.Set(ctx, c.DB, ch.ID, map[string]any{"diff": ch.Diff}); err != nil {
			return err
		}
		return &ConflictError{State: ch.State, Reason: "change_in_flight", InFlight: other.ID}
	}
	_ = actor
	return c.requestStage(ctx, ch, true)
}

func (c *Controller) requestStage(ctx context.Context, ch *Change, first bool) error {
	st, _ := ch.Diff["stage"].(map[string]any)
	if st == nil {
		st = map[string]any{}
	}
	if _, ok := st["requested_at"]; !ok || first {
		st["requested_at"] = c.now().Format(time.RFC3339)
	}
	if c.Enqueue == nil {
		return errors.New("no queue")
	}
	if err := c.Enqueue(ctx, StageTVStage, map[string]any{"change_id": ch.ID}, "stage TV change"); err != nil {
		return err
	}
	st["enqueued_at"] = c.now().Format(time.RFC3339)
	ch.Diff["stage"] = st
	return c.Store.Set(ctx, c.DB, ch.ID, map[string]any{"diff": ch.Diff})
}

func (c *Controller) ready(ctx context.Context, cfg Config, ch *Change) error {
	// a swap that failed brought the change back here: it waits for an admin (approve
	// again, Swap now), else the kept approval or the rule would retry it every tick
	if held, err := c.Store.HeldAfterSwapFailure(ctx, c.DB, ch.ID); err != nil || held {
		return err
	}
	if len(ch.ApprovedBy) > 0 {
		return c.Store.Move(ctx, c.DB, ch, StateApproved, ActionAutoApprove, nil,
			map[string]any{"approved_by": ch.ApprovedBy}, nil)
	}
	loc := c.Location
	if loc == nil {
		loc = time.Local
	}
	warn, _ := ch.VerifyReport["warnings"].(float64)
	if AutoBlocked(cfg, ch) != "" {
		return nil
	}
	rule := ChangeRule(cfg, ch)
	if AutoPromoteRule(cfg, rule, c.now().In(loc), c.viewers(ctx), warn > 0, ch.SwapAt) {
		return c.startSwap(ctx, ch, ActionAutoPromote, map[string]any{"rule": rule})
	}
	return nil
}

func (c *Controller) viewers(ctx context.Context) int {
	if c.Viewers != nil {
		return c.Viewers(ctx)
	}
	var n sql.NullInt64
	if err := c.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(viewer_count), 0) FROM channel_runtime`).Scan(&n); err != nil {
		return 1 << 20 // unknown: never "quiet"
	}
	return int(n.Int64)
}

// startSwap moves a change to swapping and enqueues the tv_swap job: the swap commit
// (lineup, settings, generation flip and — with TV slots — the front door's swap
// operation, one transaction). The front door then switches each channel at its
// programme boundary; tickOps makes the change live when it is done (swap.go).
func (c *Controller) startSwap(ctx context.Context, ch *Change, action string, detail map[string]any) error {
	if c.Enqueue == nil {
		return errors.New("no queue")
	}
	if err := c.Store.Move(ctx, c.DB, ch, StateSwapping, action, nil, detail, nil); err != nil {
		return err
	}
	if err := c.Enqueue(ctx, StageTVSwap, map[string]any{"change_id": ch.ID}, "swap TV change"); err != nil {
		// nothing switched: back to where it was, so the next tick tries again
		prev := StateApproved
		if action == ActionAutoPromote {
			prev = StateReady
		}
		c.revertSwapStart(ctx, ch.ID, prev, action, nil, err)
		return err
	}
	return nil
}

// ActionSwapNotStarted: the swap job could not be enqueued; the change went back
// to where it was (the event reverts the swapping one in the history and the
// notification feed: nothing went on the air).
const ActionSwapNotStarted = "swap_not_started"

// revertSwapStart puts a change whose swap job could not be enqueued back to
// prev, with an event that says so (the swapping event before it stays in the
// history, followed by this one: the feed sees swapping -> prev, never a swap).
func (c *Controller) revertSwapStart(ctx context.Context, id, prev, action string, actor *string, cause error) {
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE tv_changes SET state = $2 WHERE id = $1 AND state = 'swapping'`, id, prev)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return
	}
	from, to := StateSwapping, prev
	if err := c.Store.Event(ctx, tx, id, Event{ActorUserID: actor, ActorSource: actorSource(actor, "controller"),
		FromState: &from, ToState: &to, Action: ActionSwapNotStarted,
		Detail: map[string]any{"reverts": action, "error": cause.Error()}}); err != nil {
		return
	}
	_ = tx.Commit()
}

// Promote is Swap now: an approved change (or a ready one whose rule allows it).
func (c *Controller) Promote(ctx context.Context, ch *Change, actor *string) error {
	switch ch.State {
	case StateApproved:
		return c.startSwapBy(ctx, ch, ActionPromote, actor)
	case StateReady:
		cfg, err := LoadConfig(ctx, c.DB)
		if err != nil {
			return err
		}
		if ChangeRule(cfg, ch) == RuleApproval {
			return &ConflictError{State: ch.State, Reason: "approval_required"}
		}
		return c.startSwapBy(ctx, ch, ActionPromote, actor)
	}
	return &ConflictError{State: ch.State}
}

func (c *Controller) startSwapBy(ctx context.Context, ch *Change, action string, actor *string) error {
	if c.Enqueue == nil {
		return errors.New("no queue")
	}
	prev := ch.State
	if err := c.Store.Move(ctx, c.DB, ch, StateSwapping, action, actor, nil, nil); err != nil {
		return err
	}
	if err := c.Enqueue(ctx, StageTVSwap, map[string]any{"change_id": ch.ID}, "swap TV change"); err != nil {
		c.revertSwapStart(ctx, ch.ID, prev, action, actor, err)
		return err
	}
	return nil
}

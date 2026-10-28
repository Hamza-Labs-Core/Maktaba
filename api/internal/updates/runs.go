package updates

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Run states the driver moves through (README "State machines", reconciled with the
// install kinds: install-kinds.md).
const (
	RunPlanned       = "planned"
	RunHomePreparing = "home_preparing"
	RunHomePrepared  = "home_prepared" // waits for its gates (window, TV) and the drain
	RunDraining      = "draining"
	RunHomeApplying  = "home_applying" // ordered (an app: stages it), switched, restarting, watching
	RunHomeCommitted = "home_committed"
	RunRollingOut    = "rolling_out"
	RunDone          = "done"
	RunDoneFailures  = "done_with_failures"
	// RunDoneNeedsPerson: every machine took the release but those that need a person
	// (an install that cannot update itself, an app that cannot change itself…): no
	// failure of the release, the machines named with their reasons
	RunDoneNeedsPerson = "done_needs_person"
	RunHeld            = "held"
	RunFailed          = "failed"          // nothing changed (before the switch)
	RunRolledBack      = "rolled_back"     // the home went back to the release before
	RunFailedRollback  = "failed_rollback" // the home could not go back: needs a person
	RunCancelled       = "cancelled"
	RunSuperseded      = "superseded" // a newer release took the place of a run that waited for desktop apps
)

// StepWaitingApp is a run's step while all it waits for is desktop apps staging the
// release they were ordered (the home's, or the machines' still to go): nothing is
// drained, and a newer release may take its place.
const StepWaitingApp = "waiting_app"

// StepWaitingClick is that step as runs from before owner decision D1 was replaced
// (2026-10-27) name it (they waited for clicks): read as StepWaitingApp.
const StepWaitingClick = "waiting_click"

// waitingApp reports whether a run's step is StepWaitingApp (or its older name).
func waitingApp(step string) bool { return step == StepWaitingApp || step == StepWaitingClick }

// Target states (README "Machine target").
const (
	TargetPending       = "pending"
	TargetOffline       = "offline"
	TargetPreparing     = "preparing"
	TargetPrepared      = "prepared"
	TargetFailedPrepare = "failed_prepare"
	TargetApplying      = "applying"
	TargetHealthy       = "healthy"
	TargetRolledBack    = "rolled_back"
	TargetFailed        = "failed"
	TargetHeld          = "held"
	TargetSkipped       = "skipped"
	TargetWorkerOnly    = "worker_only"
	// TargetConverting: a worker-only machine becoming a full node on its turn (story 35.8
	// AC8, convert.go)
	TargetConverting = "converting"
	// A desktop app that runs the server: ordered, its app stages the release (the
	// rollout goes on with the others), then it waits for its turn (app_staged) and
	// installs at its go, with no click (owner decision D1, 2026-10-27).
	TargetOrdered   = "ordered"
	TargetAppStaged = "app_staged"
	// TargetWaitingClick and TargetClicked are those two states as runs from before
	// that decision name them: read as TargetOrdered and TargetAppStaged.
	TargetWaitingClick = "waiting_click"
	TargetClicked      = "clicked"
	// TargetNeedsPerson: something on that machine only a person mends (an install that
	// cannot update itself, a launcher too old, no room): reported, outside
	// node_failure_policy (it holds no other machine)
	TargetNeedsPerson = "needs_person"
)

// targetEnded reports whether a target's part is over in this run.
func targetEnded(s string) bool {
	switch s {
	case TargetHealthy, TargetRolledBack, TargetFailed, TargetFailedPrepare, TargetSkipped, TargetWorkerOnly, TargetHeld,
		TargetOffline, TargetNeedsPerson:
		return true
	}
	return false
}

// targetAppOrdered reports whether a target is ordered and its app has not staged the
// release yet (TargetOrdered, or its older name).
func targetAppOrdered(s string) bool { return s == TargetOrdered || s == TargetWaitingClick }

// targetAppStaged reports whether a target's app staged the release: it waits for its
// turn (TargetAppStaged, or its older name).
func targetAppStaged(s string) bool { return s == TargetAppStaged || s == TargetClicked }

// targetBeforeGo reports whether a target is an order to its desktop app before its go:
// nothing changed there, and the order is withdrawn when the run lets it go.
func targetBeforeGo(s string) bool { return targetAppOrdered(s) || targetAppStaged(s) }

// RunDetail is update_runs.detail: what the driver keeps of a run beside its state.
type RunDetail struct {
	// PriorDrain is each machine's drain flag before the run drained it (put back
	// after); HomeDrained: the home's own workers were sent drain.
	PriorDrain  map[string]bool `json:"prior_drain,omitempty"`
	HomeDrained bool            `json:"home_drained,omitempty"`
	DrainSince  *time.Time      `json:"drain_since,omitempty"`
	// Waiting is the gate the run waits at (outside_window, tv_busy).
	Waiting string `json:"waiting,omitempty"`
	// Home is the home supervisor's last report.
	Home *MachineStatus `json:"home,omitempty"`
	// Targets restricts a run to these machines (catch_up, retry); empty: every one.
	Targets []string `json:"targets,omitempty"`
	// NoHome: the home already runs the version (catch_up, retry): the run is a rollout.
	NoHome bool `json:"no_home,omitempty"`
	// HomeSince is when the home phase began moving.
	HomeSince *time.Time `json:"home_since,omitempty"`
	// ApplySince is when the home was told to switch (its apply, or its app's go): the
	// home phase's ceiling runs from it.
	ApplySince *time.Time `json:"apply_since,omitempty"`
	// NodeDrains are the machines drained for their own apply (story 35.12 AC6: drain,
	// switch, restart, check, undrain), each with its drain flag before.
	NodeDrains map[string]NodeDrain `json:"node_drains,omitempty"`
	// TV is the TV held at the home's commit: its change's check failed before any swap,
	// so the live TV stayed on its streaming software (tvheld.go; nil: not held).
	TV *RunTV `json:"tv,omitempty"`
}

// NodeDrain is a machine drained for its own apply.
type NodeDrain struct {
	Prior bool      `json:"prior"`
	Since time.Time `json:"since"`
}

// Target is one update_targets row.
type Target struct {
	RunID       string          `json:"run_id"`
	NodeID      string          `json:"node_id"`
	Name        string          `json:"name"`
	Ord         int             `json:"ord"`
	Platform    string          `json:"platform"`
	Kind        string          `json:"kind"`
	State       string          `json:"state"`
	FromVersion string          `json:"from_version"`
	ToVersion   string          `json:"to_version"`
	Attempts    int             `json:"attempts"`
	Error       json.RawMessage `json:"error,omitempty"`
	Detail      json.RawMessage `json:"detail,omitempty"`
	PreparedAt  *time.Time      `json:"prepared_at,omitempty"`
	AppliedAt   *time.Time      `json:"applied_at,omitempty"`
	HealthyAt   *time.Time      `json:"healthy_at,omitempty"`
	UpdatedAt   *time.Time      `json:"updated_at,omitempty"`
}

// RunStep is one update_run_steps row.
type RunStep struct {
	Seq        int             `json:"seq"`
	Host       string          `json:"host"`
	Step       string          `json:"step"`
	State      string          `json:"state"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	Detail     json.RawMessage `json:"detail,omitempty"`
}

// RunView is a run with its kind, detail, steps and targets (GET /runs/{id}).
type RunView struct {
	Run
	InstallKind string    `json:"install_kind"`
	Detail      RunDetail `json:"detail"`
	Steps       []RunStep `json:"steps"`
	Targets     []Target  `json:"targets"`
}

// RunDetailOf reads a run's kind and detail.
func (s *Store) RunDetailOf(ctx context.Context, id string) (string, RunDetail, error) {
	var kind string
	var raw nullJSON
	err := s.DB.QueryRowContext(ctx, `SELECT install_kind, detail FROM update_runs WHERE id = $1`, id).Scan(&kind, &raw)
	var d RunDetail
	if err != nil {
		return "", d, err
	}
	if len(raw.b) > 0 {
		_ = json.Unmarshal(raw.b, &d)
	}
	return kind, d, nil
}

// SetRunState sets a run's state and step (finished runs are not changed).
func (s *Store) SetRunState(ctx context.Context, id, state, step string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE update_runs SET state = $2, step = $3 WHERE id = $1 AND finished_at IS NULL`,
		id, state, step)
	return err
}

// SetRunDetail writes a run's kind ("" keeps it) and detail.
func (s *Store) SetRunDetail(ctx context.Context, id, kind string, d RunDetail) error {
	b, _ := json.Marshal(d)
	_, err := s.DB.ExecContext(ctx, `UPDATE update_runs SET detail = $2,
		install_kind = CASE WHEN $3 = '' THEN install_kind ELSE $3 END WHERE id = $1`, id, string(b), kind)
	return err
}

// SetRunBackup records the dump the home's update took (update_runs.backup).
func (s *Store) SetRunBackup(ctx context.Context, id string, backup map[string]any) error {
	b, _ := json.Marshal(backup)
	_, err := s.DB.ExecContext(ctx, `UPDATE update_runs SET backup = $2 WHERE id = $1`, id, string(b))
	return err
}

// GetRun is one run (sql.ErrNoRows when unknown).
func (s *Store) GetRun(ctx context.Context, id string) (Run, error) {
	return scanRun(s.DB.QueryRowContext(ctx, `SELECT `+runCols+` FROM update_runs WHERE id = $1`, id))
}

// LastFinishedOf is the newest finished run of version (nil: none).
func (s *Store) LastFinishedOf(ctx context.Context, version string) (*Run, error) {
	r, err := scanRun(s.DB.QueryRowContext(ctx, `SELECT `+runCols+` FROM update_runs WHERE version = $1
		AND finished_at IS NOT NULL ORDER BY started_at DESC LIMIT 1`, version))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// FailedAutoRuns are version's automatic runs that failed (nothing changed: state
// failed) and began at or after since, newest first.
func (s *Store) FailedAutoRuns(ctx context.Context, version string, since time.Time) ([]Run, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+runCols+` FROM update_runs WHERE version = $1 AND trigger = $2
		AND state = $3 AND finished_at IS NOT NULL ORDER BY started_at DESC LIMIT 50`, version, TriggerAuto, RunFailed)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		if !r.StartedAt.Before(since) {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

// InsertRunWith inserts a planned run of a given trigger with a detail.
// The run and its detail are one statement: a driver tick in between must not take a
// one-machine Retry for a whole run.
func (s *Store) InsertRunWith(ctx context.Context, version, from, trigger, actor string, d RunDetail) (Run, error) {
	return s.insertRun(ctx, version, from, trigger, actor, &d)
}

// ---------- steps ----------

// stepSeqBase spreads hosts' steps over seq: the home's 0..9999, a target's from
// (ord+1)*10000.
func stepSeqBase(ord int, home bool) int {
	if home {
		return 0
	}
	return (ord + 1) * 10000
}

// ReplaceSteps writes a host's steps of a run as its machine reports them.
func (s *Store) ReplaceSteps(ctx context.Context, runID, host string, base int, steps []MachineStep) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM update_run_steps WHERE run_id = $1 AND host_id = $2`, runID, host); err != nil {
		return err
	}
	for i, st := range steps {
		detail, _ := json.Marshal(map[string]any{"detail": st.Detail})
		started := st.StartedAt
		if started.IsZero() {
			started = s.now()
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO update_run_steps (run_id, seq, host_id, step, state, started_at,
			finished_at, detail) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, runID, base+i, host, st.Step, st.State,
			s.ts(started), s.tsp(st.FinishedAt), string(detail)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Steps are a run's steps, in order.
func (s *Store) Steps(ctx context.Context, runID string) ([]RunStep, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT seq, host_id, step, state, started_at, finished_at, detail
		FROM update_run_steps WHERE run_id = $1 ORDER BY seq`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunStep{}
	for rows.Next() {
		var st RunStep
		var started, fin nullTime
		var d nullJSON
		if err := rows.Scan(&st.Seq, &st.Host, &st.Step, &st.State, &started, &fin, &d); err != nil {
			return nil, err
		}
		if started.t != nil {
			st.StartedAt = *started.t
		}
		st.FinishedAt = fin.t
		if len(d.b) > 0 {
			st.Detail = json.RawMessage(d.b)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// ---------- targets ----------

const targetCols = `run_id, node_id, name, ord, platform, kind, state, from_version, to_version, attempts, error,
	detail, prepared_at, applied_at, healthy_at, updated_at`

func scanTarget(sc interface{ Scan(...any) error }) (Target, error) {
	var t Target
	var e, d nullJSON
	var prep, appl, heal, upd nullTime
	if err := sc.Scan(&t.RunID, &t.NodeID, &t.Name, &t.Ord, &t.Platform, &t.Kind, &t.State, &t.FromVersion, &t.ToVersion,
		&t.Attempts, &e, &d, &prep, &appl, &heal, &upd); err != nil {
		return t, err
	}
	if len(e.b) > 0 && string(e.b) != "null" {
		t.Error = json.RawMessage(e.b)
	}
	if len(d.b) > 0 {
		t.Detail = json.RawMessage(d.b)
	}
	t.PreparedAt, t.AppliedAt, t.HealthyAt, t.UpdatedAt = prep.t, appl.t, heal.t, upd.t
	return t, nil
}

// Targets are a run's machines, in apply order.
func (s *Store) Targets(ctx context.Context, runID string) ([]Target, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+targetCols+` FROM update_targets WHERE run_id = $1 ORDER BY ord, node_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Target{}
	for rows.Next() {
		t, err := scanTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// LastTargetState is a machine's target state in the latest run of version that had it
// ("" none), and why it was skipped when an admin skipped it ("skipped").
func (s *Store) LastTargetState(ctx context.Context, nodeID, version string) (state, why string, err error) {
	var detail nullJSON
	err = s.DB.QueryRowContext(ctx, `SELECT t.state, t.detail FROM update_targets t JOIN update_runs r ON r.id = t.run_id
		WHERE t.node_id = $1 AND t.to_version = $2 ORDER BY r.started_at DESC LIMIT 1`, nodeID, NormalizeVersion(version)).
		Scan(&state, &detail)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if len(detail.b) > 0 {
		var d struct {
			Why string `json:"why"`
		}
		_ = json.Unmarshal(detail.b, &d)
		why = d.Why
	}
	return state, why, err
}

// LastTargetOf is a machine's target in the latest run of version that had it (ok false:
// none).
func (s *Store) LastTargetOf(ctx context.Context, nodeID, version string) (Target, bool, error) {
	t, err := scanTarget(s.DB.QueryRowContext(ctx, `SELECT `+prefixed("t.", targetCols)+` FROM update_targets t
		JOIN update_runs r ON r.id = t.run_id WHERE t.node_id = $1 AND t.to_version = $2
		ORDER BY r.started_at DESC LIMIT 1`, nodeID, NormalizeVersion(version)))
	if errors.Is(err, sql.ErrNoRows) {
		return Target{}, false, nil
	}
	if err != nil {
		return Target{}, false, err
	}
	return t, true, nil
}

// prefixed qualifies a column list with a table alias.
func prefixed(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = alias + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// TargetState is one target's state now ("" none).
func (s *Store) TargetState(ctx context.Context, runID, nodeID string) string {
	var state string
	_ = s.DB.QueryRowContext(ctx, `SELECT state FROM update_targets WHERE run_id = $1 AND node_id = $2`, runID, nodeID).Scan(&state)
	return state
}

// InsertTarget adds a machine to a run (once).
func (s *Store) InsertTarget(ctx context.Context, t Target) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO update_targets (run_id, node_id, name, ord, platform, kind, state,
		from_version, to_version, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (run_id, node_id) DO NOTHING`, t.RunID, t.NodeID, t.Name, t.Ord, t.Platform, t.Kind, t.State,
		t.FromVersion, t.ToVersion, s.ts(s.now()))
	return err
}

// TargetChange is what moves on a target.
type TargetChange struct {
	State    string
	Error    any // nil: unchanged; map: set; "clear": cleared
	Detail   any // nil: unchanged
	Prepared bool
	Applied  bool
	Healthy  bool
	Attempt  bool
	From     string
	Kind     string
}

// UpdateTarget applies a change to a target.
func (s *Store) UpdateTarget(ctx context.Context, runID, nodeID string, c TargetChange) error {
	now := s.ts(s.now())
	q := `UPDATE update_targets SET updated_at = $3`
	args := []any{runID, nodeID, now}
	add := func(col string, v any) {
		args = append(args, v)
		q += fmt.Sprintf(", %s = $%d", col, len(args))
	}
	if c.State != "" {
		add("state", c.State)
	}
	switch e := c.Error.(type) {
	case nil:
	case string:
		if e == "clear" {
			q += ", error = NULL"
		}
	default:
		b, _ := json.Marshal(e)
		add("error", string(b))
	}
	if c.Detail != nil {
		b, _ := json.Marshal(c.Detail)
		add("detail", string(b))
	}
	if c.Prepared {
		q += ", prepared_at = $3"
	}
	if c.Applied {
		q += ", applied_at = $3"
	}
	if c.Healthy {
		q += ", healthy_at = $3"
	}
	if c.Attempt {
		q += ", attempts = attempts + 1"
	}
	if c.From != "" {
		add("from_version", c.From)
	}
	if c.Kind != "" {
		add("kind", c.Kind)
	}
	_, err := s.DB.ExecContext(ctx, q+` WHERE run_id = $1 AND node_id = $2`, args...)
	return err
}

// View is a run with everything the page shows of it.
func (s *Store) View(ctx context.Context, id string) (RunView, error) {
	r, err := s.GetRun(ctx, id)
	if err != nil {
		return RunView{}, err
	}
	kind, d, err := s.RunDetailOf(ctx, id)
	if err != nil {
		return RunView{}, err
	}
	steps, err := s.Steps(ctx, id)
	if err != nil {
		return RunView{}, err
	}
	targets, err := s.Targets(ctx, id)
	if err != nil {
		return RunView{}, err
	}
	return RunView{Run: r, InstallKind: kind, Detail: d, Steps: steps, Targets: targets}, nil
}

// SetAttempts adds one to a release's failed attempts and blocks it at three (story
// 35.4 AC9); it reports whether it is blocked now.
func (s *Store) CountAttempt(ctx context.Context, version string) (bool, error) {
	if _, err := s.DB.ExecContext(ctx, `UPDATE release_catalog SET attempts = attempts + 1 WHERE version = $1`, version); err != nil {
		return false, err
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT attempts FROM release_catalog WHERE version = $1`, version).Scan(&n); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if n >= MaxAttempts {
		return true, s.Block(ctx, version, "attempts")
	}
	return false, nil
}

// MaxAttempts is how many failed runs block a release (story 35.4 AC9).
const MaxAttempts = 3

// Block blocks a release from automatic runs (reason: attempts, rehearsal, verify,
// rolled_back, ...): only a newer release, or an admin's Update now naming it, installs.
func (s *Store) Block(ctx context.Context, version, reason string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE release_catalog SET blocked_reason = $2 WHERE version = $1 AND blocked_reason = ''`,
		version, reason)
	return err
}

// Unblock clears a release's block and attempts (an admin's retry).
func (s *Store) Unblock(ctx context.Context, version string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE release_catalog SET blocked_reason = '', attempts = 0
		WHERE version = $1 AND withdrawn_at IS NULL AND verify_state = 'verified'`, version)
	return err
}

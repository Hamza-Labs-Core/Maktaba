package updates

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"time"
)

// Attempt is one try at bringing a machine to a release, every failure with its reason
// (specs/epics/35-cluster-full-nodes-and-updates/update-attempts.md): the desktop app's
// own updater (source app) and a release's start (source server), as the machine
// reports them, and the cluster's runs (source run), from update_runs / update_targets.
type Attempt struct {
	ID      string     `json:"id"`
	At      time.Time  `json:"at"`
	EndedAt *time.Time `json:"ended_at,omitempty"`
	Source  string     `json:"source"`
	Trigger string     `json:"trigger,omitempty"`
	From    string     `json:"from,omitempty"`
	To      string     `json:"to"`
	Outcome string     `json:"outcome"`
	Step    string     `json:"step,omitempty"`
	Code    string     `json:"code,omitempty"`
	Detail  string     `json:"detail,omitempty"`
	Fix     string     `json:"fix,omitempty"`
	RunID   string     `json:"run_id,omitempty"`
}

// Attempt outcomes.
const (
	OutcomeRunning    = "running"
	OutcomeInstalled  = "installed"
	OutcomeFailed     = "failed"
	OutcomePutBack    = "put_back"
	OutcomeRolledBack = "rolled_back"
	OutcomeCancelled  = "cancelled"
)

// SourceRun is an attempt read from the cluster's runs.
const SourceRun = "run"

// ShowAttempts is how many attempts the overview gives each machine.
const ShowAttempts = 5

// errDoc is a run's or a target's error document.
type errDoc struct {
	Code   string `json:"code"`
	Step   string `json:"step"`
	Detail string `json:"detail"`
	Reason string `json:"reason"`
}

func readErr(raw json.RawMessage) errDoc {
	var d errDoc
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &d)
	}
	if d.Detail == "" && d.Reason != "" && d.Reason != d.Code {
		d.Detail = d.Reason
	}
	return d
}

// runAttempt is the home's attempt of a finished run.
func runAttempt(r Run) Attempt {
	e := readErr(r.Error)
	a := Attempt{ID: "run-" + r.ID, At: r.StartedAt, EndedAt: r.FinishedAt, Source: SourceRun, Trigger: r.Trigger,
		From: NormalizeVersion(r.FromVersion), To: NormalizeVersion(r.Version), Step: e.Step, Code: e.Code,
		Detail: e.Detail, RunID: r.ID}
	switch r.State {
	case RunDone, RunDoneFailures, RunDoneNeedsPerson, RunHeld:
		a.Outcome = OutcomeInstalled // the home took it (the machines' parts are theirs)
	case RunRolledBack:
		a.Outcome = OutcomeRolledBack
	case RunCancelled, RunSuperseded:
		a.Outcome = OutcomeCancelled
	case RunFailed, RunFailedRollback:
		a.Outcome = OutcomeFailed
	default:
		a.Outcome = OutcomeRunning
	}
	return a
}

// targetAttempt is a machine's attempt of its part in run.
func targetAttempt(r Run, t Target) Attempt {
	e := readErr(t.Error)
	at := r.StartedAt
	for _, x := range []*time.Time{t.PreparedAt, t.AppliedAt} {
		if x != nil {
			at = *x
			break
		}
	}
	a := Attempt{ID: "run-" + r.ID + "-" + t.NodeID, At: at, EndedAt: t.UpdatedAt, Source: SourceRun, Trigger: r.Trigger,
		From: NormalizeVersion(t.FromVersion), To: NormalizeVersion(t.ToVersion), Step: e.Step, Code: e.Code,
		Detail: e.Detail, RunID: r.ID}
	if a.To == "" {
		a.To = NormalizeVersion(r.Version)
	}
	switch t.State {
	case TargetHealthy:
		a.Outcome = OutcomeInstalled
	case TargetRolledBack:
		a.Outcome = OutcomeRolledBack
	case TargetFailed, TargetFailedPrepare, TargetNeedsPerson:
		a.Outcome = OutcomeFailed
		if a.Code == "" {
			a.Code = t.State
		}
	case TargetSkipped, TargetHeld, TargetOffline, TargetWorkerOnly:
		a.Outcome = OutcomeCancelled
	default:
		a.Outcome = OutcomeRunning
	}
	return a
}

// mergeAttempts is a machine's own attempts and its runs', newest first, at most
// ShowAttempts, each detail and fix clean (cleanDetail: a machine's text).
func mergeAttempts(own []Attempt, runs ...Attempt) []Attempt {
	all := append(append([]Attempt(nil), own...), runs...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.After(all[j].At) })
	if len(all) > ShowAttempts {
		all = all[:ShowAttempts]
	}
	for i := range all {
		all[i].Detail, all[i].Fix = cleanDetail(all[i].Detail), cleanDetail(all[i].Fix)
	}
	return all
}

// maxDetail is the longest detail or fix the page gets (the machine's own record keeps
// as much; its logs keep the rest).
const maxDetail = 2048

// urlQueryRe is a URL's query: a signed download's redirect carries its signature there.
// The query ends before the punctuation that follows the URL in a sentence (a "):" or a
// full stop), which stays.
var urlQueryRe = regexp.MustCompile(`(https?://[^\s?#"'<>]+)\?(?:[^\s#"'<>()]*[^\s#"'<>().,:;])?`)

// cleanDetail is a detail without URLs' queries, at most maxDetail bytes (cut on a
// character, with an ellipsis), as the machines record it (a machine of an older build
// may not).
func cleanDetail(s string) string {
	s = urlQueryRe.ReplaceAllString(s, "$1")
	if len(s) <= maxDetail {
		return s
	}
	cut := maxDetail - len("…")
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}

// LastTargetAttempt is a machine's attempt in the newest run that ended its part (ok
// false when none did).
func (s *Store) LastTargetAttempt(ctx context.Context, nodeID string) (Attempt, bool, error) {
	var runID string
	err := s.DB.QueryRowContext(ctx, `SELECT t.run_id FROM update_targets t JOIN update_runs r ON r.id = t.run_id
		WHERE t.node_id = $1 ORDER BY r.started_at DESC LIMIT 1`, nodeID).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, false, nil
	}
	if err != nil {
		return Attempt{}, false, err
	}
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return Attempt{}, false, err
	}
	targets, err := s.Targets(ctx, runID)
	if err != nil {
		return Attempt{}, false, err
	}
	for _, t := range targets {
		if t.NodeID == nodeID && targetEnded(t.State) {
			return targetAttempt(run, t), true, nil
		}
	}
	return Attempt{}, false, nil
}

// homeRunAttempt is the home's attempt in the newest finished run that was about the
// home (not a catch-up or a retry of machines).
func (s *Store) homeRunAttempt(ctx context.Context) (Attempt, bool) {
	runs, err := s.Runs(ctx, 10)
	if err != nil {
		return Attempt{}, false
	}
	for _, r := range runs {
		if r.FinishedAt == nil {
			continue
		}
		if _, det, err := s.RunDetailOf(ctx, r.ID); err == nil && det.NoHome {
			continue
		}
		return runAttempt(r), true
	}
	return Attempt{}, false
}

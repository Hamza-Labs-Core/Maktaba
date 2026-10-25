package updater

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// The TV step of the home's update (story 35.6, design §7.1). Once the new release runs,
// its streaming binary goes on the air through the TV change controller (Epic 33): a
// change of the binary alone (source auto_update), staged, verified and swapped at
// programme boundaries without an approval. The release commits only once that change is
// live (or there was nothing to swap); a change that fails rolls the release back, and a
// rollback for any reason takes the change back (cancelled, or rolled back on the air).
// The step runs after the restart because the new release's migrations run at its start
// (the database step): the new streaming binary never meets an older schema.

// TV outcomes (TVStatus.Outcome; the API's tvchange.AutoOutcome*).
const (
	TVNone       = "none"
	TVSkipped    = "skipped"
	TVQueued     = "queued"
	TVInProgress = "in_progress"
	TVLive       = "live"
	TVFailed     = "failed"
	TVCancelled  = "cancelled"
)

// Codes a run rolls back with at its TV step (the API's updates.Code*).
const (
	CodeFailedTV          = "failed_tv"
	CodeTVChangePending   = "tv_change_pending"
	CodeTVTimeout         = "tv_timeout"
	CodeTVChangeCancelled = "tv_change_cancelled"
)

// TV step limits.
const (
	// tvQueueDefault is how long a change may wait behind another when the run has no
	// window to keep to (an admin's Update now, a desktop app's click).
	tvQueueDefault = time.Hour
	// tvTimeoutDefault is boundary_max_wait_minutes (30) + 10 when the API does not say.
	tvTimeoutDefault = 40 * time.Minute
	// tvAskLimit is how long the change may not be had at all (the API answers nothing
	// useful) before the run gives up.
	tvAskLimit = 10 * time.Minute
)

// TVSwapper moves this machine's TV to the new release's streaming binary.
type TVSwapper interface {
	// Begin asks for the run's TV change (one per run: asking again returns it).
	Begin(ctx context.Context, j *Journal) (TVStatus, error)
	// Status is the run's TV change now.
	Status(ctx context.Context, j *Journal) (TVStatus, error)
	// Withdraw takes the run's change back (its run rolls back).
	Withdraw(ctx context.Context, j *Journal, reason string) error
}

// TVStatus is the run's TV change as the API says it.
type TVStatus struct {
	Outcome        string      `json:"outcome"`
	Skipped        string      `json:"skipped,omitempty"`
	ChangeID       string      `json:"change_id,omitempty"`
	Number         int64       `json:"number,omitempty"`
	State          string      `json:"state,omitempty"`
	Reason         string      `json:"reason,omitempty"`
	Failed         []string    `json:"failed,omitempty"`
	StagedAt       *time.Time  `json:"staged_at,omitempty"`
	TimeoutMinutes int         `json:"timeout_minutes,omitempty"`
	QueuedBehind   *TVQueueRef `json:"queued_behind,omitempty"`
}

// TVQueueRef is the change a queued one waits for.
type TVQueueRef struct {
	ID       string `json:"id"`
	Number   int64  `json:"number"`
	State    string `json:"state"`
	Approved bool   `json:"approved"`
}

// TVRun is the run's TV step in the journal.
type TVRun struct {
	StartedAt time.Time `json:"started_at"`
	ChangeID  string    `json:"change_id,omitempty"`
	Number    int64     `json:"number,omitempty"`
	Outcome   string    `json:"outcome,omitempty"`
	// LastOK is when the API last answered about the change.
	LastOK *time.Time `json:"last_ok,omitempty"`
	// Withdrawn is why the run took the change back.
	Withdrawn string `json:"withdrawn,omitempty"`
}

// SetTVDeadline records how long the run's TV change may wait behind another (the
// window's end and its overrun: the home's apply says it).
func (m *Machine) SetTVDeadline(runID string, at time.Time) {
	_ = m.mutate(func(j *Journal) {
		if j.Job.RunID == runID && !at.IsZero() {
			t := at.UTC()
			j.TVDeadline = &t
		}
	})
}

// tvStep moves the TV on (the watch calls it while the new release is healthy): done once
// the change is live or skipped; code is set when the run must roll back.
func (m *Machine) tvStep(ctx context.Context) (done bool, code, detail string) {
	j := m.Status().Journal
	if m.TV == nil || j == nil || !j.Job.Home {
		return true, "", ""
	}
	if j.TV != nil && (j.TV.Outcome == TVLive || j.TV.Outcome == TVSkipped) {
		return true, "", ""
	}
	now := m.now()
	if j.TV == nil {
		_ = m.beginStep(StepTV)
		_ = m.mutate(func(j *Journal) { j.TV = &TVRun{StartedAt: now} })
		m.logf("moving the TV to %s's streaming software", j.Job.Version)
		j = m.Status().Journal
	}
	var st TVStatus
	var err error
	if j.TV.ChangeID == "" {
		st, err = m.TV.Begin(ctx, j)
	} else {
		st, err = m.TV.Status(ctx, j)
		if err == nil && st.Outcome == TVNone {
			st, err = m.TV.Begin(ctx, j) // the change is gone (a database put back): asked again
		}
	}
	if err != nil {
		last := j.TV.StartedAt
		if j.TV.LastOK != nil {
			last = *j.TV.LastOK
		}
		if now.Sub(last) >= tvAskLimit {
			return false, CodeTVTimeout, fmt.Sprintf("the TV change could not be had for %s: %v", tvAskLimit, err)
		}
		return false, "", ""
	}
	_ = m.mutate(func(j *Journal) {
		if j.TV != nil {
			j.TV.LastOK, j.TV.ChangeID, j.TV.Number = &now, st.ChangeID, st.Number
		}
	})
	name := "the TV change"
	if st.Number > 0 {
		name = fmt.Sprintf("TV change #%d", st.Number)
	}
	switch st.Outcome {
	case TVLive:
		_ = m.mutate(func(j *Journal) { j.TV.Outcome = TVLive })
		_ = m.endStep(StepTV, "done", name+" is on the air")
		m.logf("%s is on the air: the TV runs %s's streaming software", name, j.Job.Version)
		return true, "", ""
	case TVSkipped:
		_ = m.mutate(func(j *Journal) { j.TV.Outcome = TVSkipped })
		_ = m.endStep(StepTV, "skipped", tvSkipWhy(st.Skipped))
		m.logf("no TV change: %s", tvSkipWhy(st.Skipped))
		return true, "", ""
	case TVFailed:
		why := st.Reason
		if len(st.Failed) > 0 {
			why += " (" + strings.Join(st.Failed, ", ") + ")"
		}
		return false, CodeFailedTV, name + ": " + why
	case TVCancelled:
		return false, CodeTVChangeCancelled, name + " was cancelled"
	case TVQueued:
		deadline := j.TV.StartedAt.Add(tvQueueDefault)
		if j.TVDeadline != nil {
			deadline = *j.TVDeadline
		}
		if now.After(deadline) {
			behind := "another change"
			if q := st.QueuedBehind; q != nil {
				behind = fmt.Sprintf("TV change #%d (%s)", q.Number, q.State)
				if q.State == "ready" && !q.Approved {
					behind = fmt.Sprintf("TV change #%d, which waits for an approval", q.Number)
				}
			}
			return false, CodeTVChangePending, fmt.Sprintf("%s waited behind %s until %s", name, behind,
				deadline.Format(time.RFC3339))
		}
	case TVInProgress:
		limit := tvTimeoutDefault
		if st.TimeoutMinutes > 0 {
			limit = time.Duration(st.TimeoutMinutes) * time.Minute
		}
		if st.StagedAt != nil && now.Sub(*st.StagedAt) >= limit {
			return false, CodeTVTimeout, fmt.Sprintf("%s was not on the air %s after its stage (%s)", name, limit, st.State)
		}
	}
	return false, "", ""
}

func tvSkipWhy(code string) string {
	switch code {
	case "staging_off":
		return "TV staging is off: the restart put the new streaming software on"
	case "already_live":
		return "the TV runs this streaming software already"
	case "no_tv":
		return "no TV release airs yet"
	}
	return code
}

// failTV rolls the run back for its TV step.
func (m *Machine) failTV(code, detail string) {
	f := &Failure{Step: StepTV, Code: code, Detail: detail}
	_ = m.endStep(StepTV, "failed", f.Error())
	_ = m.mutate(func(j *Journal) {
		j.Error = f
		if j.TV != nil {
			j.TV.Outcome = TVFailed
		}
	})
	m.logf("the TV step failed: %s", f.Error())
	m.rollback(code)
}

// withdrawTV takes the run's TV change back before its rollback switches the release (a
// few tries: the API may be the part that fails); best effort, logged.
func (m *Machine) withdrawTV(reason string) {
	j := m.Status().Journal
	if m.TV == nil || j == nil || j.TV == nil || j.TV.Withdrawn != "" || j.TV.Outcome == TVSkipped {
		return
	}
	var err error
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = m.TV.Withdraw(ctx, j, reason)
		cancel()
		if err == nil {
			break
		}
		if i < 2 {
			time.Sleep(m.tvRetryPause())
		}
	}
	if err != nil {
		m.logf("the TV change could not be taken back (%v): the TV may stay on %s's streaming software", err, j.Job.Version)
		return
	}
	m.mu.Lock()
	if m.j != nil {
		if m.j.TV != nil {
			m.j.TV.Withdrawn = reason
		}
		for i := len(m.j.Steps) - 1; i >= 0; i-- {
			if m.j.Steps[i].Step == StepTV && m.j.Steps[i].FinishedAt == nil {
				// the release goes back while its TV change was under way
				m.endStepLocked(StepTV, "failed", "taken back: "+reason)
				break
			}
		}
		_ = m.saveLocked()
	}
	m.mu.Unlock()
	m.logf("the TV change was taken back (%s)", reason)
}

func (m *Machine) tvRetryPause() time.Duration {
	if m.CheckEvery > 0 && m.CheckEvery < 2*time.Second {
		return m.CheckEvery
	}
	return 2 * time.Second
}

// FrontDoorRestarts reports whether going to a release restarts the front door (story
// 35.6 AC6, design §7.3): only when the release's edge_protocol (its release.json's,
// releaseEdge) differs from the running front door's (runningEdge: its release's, or
// `maktaba-streaming --version --json`'s; 0 when not known). Equal, the front door keeps
// running its previous release's binary across the restart (story 35.7: no TV viewer sees
// it), and pruning keeps that release while it runs. A release or a front door that does
// not say restarts it (the safe side: the edge may have changed).
func FrontDoorRestarts(releaseEdge, runningEdge int) bool {
	if releaseEdge == 0 || runningEdge == 0 {
		return true
	}
	return releaseEdge != runningEdge
}

// waitsForTV reports whether a watched release waits only for its TV step: healthy since
// healthySince for its whole watch.
func waitsForTV(v Verdict, healthySince, now time.Time, watch time.Duration) bool {
	return v.Healthy && !healthySince.IsZero() && now.Sub(healthySince) >= watch
}

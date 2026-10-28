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
// programme boundaries. The release commits once that change is live (or there was
// nothing to swap), or once its check failed before any swap: then the TV is held on the
// streaming software it runs and the release commits all the same (the owner's decision,
// 2026-10-28: the admin retries or drops the change from the Updates page). A change whose
// swap fails, or that fails on the air after it switched, rolls the release back; a
// rollback for any reason takes the change back (cancelled, or rolled back on the air).
//
// The TV never holds the release up (the outage of 2026-10-28: a change that waited for
// an approval kept the watch going until it ran out, watch_timeout, and the rollback's
// restore failed). The watch judges the home's own health only; once the home passed its
// watch, a TV change still waiting (for an approval, a quiet window, behind another
// change, a slow staging, an API that does not answer) leaves the release committed with
// the TV pending: the change goes on under the TV change controller by itself.
// The step runs after the restart because the new release's migrations run at its start
// (the database step): the new streaming binary never meets an older schema.

// TV outcomes (TVStatus.Outcome; the API's tvchange.AutoOutcome*).
const (
	TVNone       = "none"
	TVSkipped    = "skipped"
	TVQueued     = "queued"
	TVInProgress = "in_progress"
	TVLive       = "live"
	TVHeld       = "held" // its check failed before any swap: the TV stays on its software
	// TVPending: the release committed while its change still waited (an approval, a
	// quiet window, a slow staging): the change goes on under the TV change controller.
	TVPending   = "pending"
	TVFailed    = "failed"
	TVCancelled = "cancelled"
)

// Codes of the TV step (the API's updates.Code*). A run rolls back with CodeFailedTV (the
// change's swap failed, or it failed on the air) and CodeTVChangeCancelled.
// CodeTVChangePending and CodeTVTimeout name a change that waits (behind another, past its
// stage's limit, the API silent): never a rollback any more, they say why the TV is
// pending at the commit.
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
	// LiveStreaming is the streaming software the live TV runs (the version its live
	// slot reports, else its TV release's).
	LiveStreaming string `json:"live_streaming,omitempty"`
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
	// LiveStreaming is the streaming software the live TV ran when the API last answered:
	// the release that ships it is kept from pruning (a held TV stays on it).
	LiveStreaming string `json:"live_streaming,omitempty"`
	// Failed is the checks that failed (a held change's).
	Failed []string `json:"failed,omitempty"`
}

// keeps are the releases the TV step needs kept at the commit: the one whose streaming
// software the live TV ran at its last answer (a held TV stays on it until a later change
// swaps it; a change that went live may still go back to it, Epic 33.9).
func (t *TVRun) keeps() []string {
	if t == nil || strings.TrimSpace(t.LiveStreaming) == "" {
		return nil
	}
	return []string{strings.TrimSpace(t.LiveStreaming)}
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

// tvResult is where the TV step stands: done (live, skipped, held: the release commits),
// failed (code: the run rolls back), or waiting (why: the release commits once the home
// passed its watch, with the TV pending).
type tvResult struct {
	done         bool
	code, detail string
	waiting      string
}

// tvStep moves the TV on (the watch calls it while the new release is healthy).
func (m *Machine) tvStep(ctx context.Context) tvResult {
	j := m.Status().Journal
	if m.TV == nil || j == nil || !j.Job.Home {
		return tvResult{done: true}
	}
	if j.TV != nil && (j.TV.Outcome == TVLive || j.TV.Outcome == TVSkipped || j.TV.Outcome == TVHeld ||
		j.TV.Outcome == TVPending) {
		return tvResult{done: true}
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
		why := fmt.Sprintf("the TV change cannot be had yet: %v", err)
		if now.Sub(last) >= tvAskLimit {
			why = fmt.Sprintf("%s: the TV change could not be had for %s: %v", CodeTVTimeout, tvAskLimit, err)
		}
		return tvResult{waiting: why}
	}
	_ = m.mutate(func(j *Journal) {
		if j.TV != nil {
			j.TV.LastOK, j.TV.ChangeID, j.TV.Number = &now, st.ChangeID, st.Number
			if st.LiveStreaming != "" {
				j.TV.LiveStreaming = st.LiveStreaming
			}
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
		return tvResult{done: true}
	case TVSkipped:
		_ = m.mutate(func(j *Journal) { j.TV.Outcome = TVSkipped })
		_ = m.endStep(StepTV, "skipped", tvSkipWhy(st.Skipped))
		m.logf("no TV change: %s", tvSkipWhy(st.Skipped))
		return tvResult{done: true}
	case TVHeld:
		// its check failed before any swap: the TV stays on the streaming software it
		// runs, the release commits all the same, and the admin retries or drops the
		// change (the owner's decision, 2026-10-28)
		on := st.LiveStreaming
		if on == "" {
			on = "its streaming software"
		}
		why := "its check failed"
		if len(st.Failed) > 0 {
			why += " (" + strings.Join(st.Failed, ", ") + ")"
		}
		failed := append([]string(nil), st.Failed...)
		_ = m.mutate(func(j *Journal) { j.TV.Outcome, j.TV.Failed = TVHeld, failed })
		stays := "the TV stays on " + on
		if strings.TrimPrefix(on, "v") == strings.TrimPrefix(j.Job.Version, "v") {
			// the live slot runs this release's binary already (a slot the restart
			// started unbound runs the installed one): say so, not "stays"
			stays = "the live TV runs " + on + "'s streaming software already"
		}
		_ = m.endStep(StepTV, "held", fmt.Sprintf("%s: %s; %s", name, why, stays))
		m.warnf("%s: %s; %s and %s commits (retry or drop it on the Updates page)",
			name, why, stays, j.Job.Version)
		return tvResult{done: true}
	case TVFailed:
		why := st.Reason
		if len(st.Failed) > 0 {
			why += " (" + strings.Join(st.Failed, ", ") + ")"
		}
		return tvResult{code: CodeFailedTV, detail: name + ": " + why}
	case TVCancelled:
		return tvResult{code: CodeTVChangeCancelled, detail: name + " was cancelled"}
	case TVQueued:
		behind := "another change"
		if q := st.QueuedBehind; q != nil {
			behind = fmt.Sprintf("TV change #%d (%s)", q.Number, q.State)
			if q.State == "ready" && !q.Approved {
				behind = fmt.Sprintf("TV change #%d, which waits for an approval", q.Number)
			}
		}
		why := fmt.Sprintf("%s waits behind %s", name, behind)
		deadline := j.TV.StartedAt.Add(tvQueueDefault)
		if j.TVDeadline != nil {
			deadline = *j.TVDeadline
		}
		if now.After(deadline) {
			why = fmt.Sprintf("%s: %s waited behind %s until %s", CodeTVChangePending, name, behind,
				deadline.Format(time.RFC3339))
		}
		return tvResult{waiting: why}
	case TVInProgress:
		why := fmt.Sprintf("%s is %s", name, orNone(st.State))
		if st.State == "ready" {
			// staged and checked: it waits for its swap (an approval, a quiet window)
			why = name + " passed its checks and waits for its swap (an approval or a quiet window)"
		}
		limit := tvTimeoutDefault
		if st.TimeoutMinutes > 0 {
			limit = time.Duration(st.TimeoutMinutes) * time.Minute
		}
		if st.StagedAt != nil && now.Sub(*st.StagedAt) >= limit {
			why = fmt.Sprintf("%s: %s was not on the air %s after its stage (%s)", CodeTVTimeout, name, limit, st.State)
		}
		return tvResult{waiting: why}
	}
	return tvResult{waiting: name + " is not on the air yet"}
}

// tvPending ends the TV step pending at the commit: the home passed its watch while its
// TV change still waits (why). The change is not taken back: it goes on under the TV
// change controller (an admin approves it, its window comes), and the release that ships
// the streaming software the live TV runs is kept from pruning meanwhile (TVRun.keeps).
func (m *Machine) tvPending(why string) {
	j := m.Status().Journal
	if j == nil || j.TV == nil || j.TV.Outcome != "" {
		return
	}
	_ = m.mutate(func(j *Journal) { j.TV.Outcome = TVPending })
	_ = m.endStep(StepTV, TVPending, why)
	m.warnf("%s passed its watch while its TV change waits (%s): it commits, and the TV change goes on by itself",
		j.Job.Version, why)
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

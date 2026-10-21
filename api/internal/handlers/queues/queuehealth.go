package queues

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"
)

// Epic 40 (queue health): a tile is red only when many recent jobs fail
// the same new way. Jobs parked on a dependency (the model, a share,
// disk space) make it Waiting; items no retry can fix are a Needs-you
// count. The thresholds are rules.queue_health, the same section the
// pipeline reads (engine/failure_classes.py); the defaults below are
// kept equal to schema.json by a test.

// Tile states of StageHealth.State.
const (
	HealthOK      = "ok"
	HealthWaiting = "waiting"
	HealthFailing = "failing"
)

// queueHealthRules is the slice of rules.queue_health the API uses.
type queueHealthRules struct {
	WindowHours    float64 `json:"window_hours"`
	OKFailureShare float64 `json:"ok_failure_share"`
	FailingMinJobs int     `json:"failing_min_jobs"`
}

func defaultQueueHealthRules() queueHealthRules {
	return queueHealthRules{WindowHours: 6, OKFailureShare: 0.02, FailingMinJobs: 10}
}

func (q queueHealthRules) window() time.Duration {
	return time.Duration(q.WindowHours * float64(time.Hour))
}

// loadQueueHealthRules reads app_settings rules.queue_health over the
// defaults; a missing row, a bad value or a missing table keeps them.
func (h *Handler) loadQueueHealthRules(ctx context.Context) queueHealthRules {
	cfg := defaultQueueHealthRules()
	var raw sql.NullString
	err := h.DB.QueryRowContext(ctx,
		`SELECT value::text FROM app_settings WHERE key = 'rules.queue_health'`).Scan(&raw)
	if err != nil || !raw.Valid {
		return cfg
	}
	var stored map[string]json.RawMessage
	if json.Unmarshal([]byte(raw.String), &stored) != nil {
		return cfg
	}
	var f float64
	var n int
	if v, ok := stored["window_hours"]; ok && json.Unmarshal(v, &f) == nil && f > 0 {
		cfg.WindowHours = f
	}
	if v, ok := stored["ok_failure_share"]; ok && json.Unmarshal(v, &f) == nil && f >= 0 && f <= 1 {
		cfg.OKFailureShare = f
	}
	if v, ok := stored["failing_min_jobs"]; ok && json.Unmarshal(v, &n) == nil && n >= 1 {
		cfg.FailingMinJobs = n
	}
	return cfg
}

// StageHealth is QueueSummary.health.
type StageHealth struct {
	State          string            `json:"state"`
	WindowHours    float64           `json:"window_hours"`
	FailedWindow   int               `json:"failed_window"`
	FinishedWindow int               `json:"finished_window"`
	FailureShare   float64           `json:"failure_share"`
	Waiting        []WaitingOn       `json:"waiting"`
	NeedsYou       int               `json:"needs_you"`
	Ignored        int               `json:"ignored"`
	Failing        *FailingSignature `json:"failing"`
}

// WaitingOn is one dependency or resource jobs of a stage are parked on.
type WaitingOn struct {
	On      string     `json:"on"`
	Count   int        `json:"count"`
	Healthy *bool      `json:"healthy"`
	Message *string    `json:"message"`
	Since   *time.Time `json:"since"`
}

// FailingSignature is the error that makes a stage red.
type FailingSignature struct {
	Signature string  `json:"signature"`
	Class     string  `json:"class"`
	Count     int     `json:"count"`
	Example   string  `json:"example"`
	Subject   *string `json:"subject"`
}

// windowFailure is one (stage, signature, class) group of the window.
type windowFailure struct {
	Signature, Class, Example string
	Subject                   *string
	Count                     int
}

// healthInputs is everything stageHealth needs; it is pure so the
// precedence is table-testable without a database.
type healthInputs struct {
	Failures   []windowFailure
	DoneWindow int
	Waiting    []WaitingOn
	FailedOpen map[string]int // open failed items by signature
	Ignored    int
}

// failingClass: only an unexplained or a passing failure can make a
// stage red; a dependency, a resource or a broken item is explained.
func failingClass(cls string) bool {
	return cls == "" || cls == "unknown" || cls == "transient"
}

func stageHealth(in healthInputs, cfg queueHealthRules) StageHealth {
	h := StageHealth{State: HealthOK, WindowHours: cfg.WindowHours, Waiting: in.Waiting, Ignored: in.Ignored}
	if h.Waiting == nil {
		h.Waiting = []WaitingOn{}
	}
	var top *windowFailure
	for i := range in.Failures {
		f := &in.Failures[i]
		h.FailedWindow += f.Count
		if failingClass(f.Class) && (top == nil || f.Count > top.Count ||
			(f.Count == top.Count && f.Signature < top.Signature)) {
			top = f
		}
	}
	h.FinishedWindow = h.FailedWindow + in.DoneWindow
	if h.FinishedWindow > 0 {
		h.FailureShare = float64(h.FailedWindow) / float64(h.FinishedWindow)
	}
	failingSig := ""
	if top != nil && h.FailureShare >= cfg.OKFailureShare && h.FailureShare > 0 &&
		top.Count >= cfg.FailingMinJobs {
		h.State = HealthFailing
		failingSig = top.Signature
		h.Failing = &FailingSignature{Signature: top.Signature, Class: top.Class, Count: top.Count,
			Example: top.Example, Subject: top.Subject}
		if h.Failing.Class == "" {
			h.Failing.Class = "unknown"
		}
	} else {
		for _, w := range h.Waiting {
			if w.Count > 0 {
				h.State = HealthWaiting
				break
			}
		}
	}
	for sig, n := range in.FailedOpen {
		if sig != failingSig || failingSig == "" {
			h.NeedsYou += n
		}
	}
	return h
}

// ---------- loading ----------

const windowFailuresSQL = `
SELECT wf.stage, COALESCE(wf.data->>'cause', ''), COALESCE(wf.data->>'class', ''), count(*),
       (array_agg(wf.message ORDER BY wf.id DESC))[1],
       (array_agg(wf.subject_kind || ':' || wf.subject_id::text ORDER BY wf.id DESC))[1]
  FROM job_events wf
 WHERE wf.at > $1 AND wf.kind IN ('failed', 'retry_scheduled')
 GROUP BY 1, 2, 3`

const windowDoneSQL = `
SELECT d.stage, count(*) FROM processing_jobs d
 WHERE d.state = 'done' AND d.finished_at > $1 GROUP BY d.stage`

const parkedSQL = `
SELECT j.stage, j.parked_on, count(*), dh.healthy, dh.message, dh.since
  FROM processing_jobs j
  LEFT JOIN dependency_health dh ON dh.name = j.parked_on
 WHERE j.state = 'pending' AND j.parked_on IS NOT NULL
 GROUP BY j.stage, j.parked_on, dh.healthy, dh.message, dh.since`

const ignoredCountSQL = `
SELECT fi.stage, count(*) FROM failure_ignores fi GROUP BY fi.stage`

// stageHealthData is what BuildSnapshot loads once for every stage.
type stageHealthData struct {
	cfg      queueHealthRules
	failures map[string][]windowFailure
	done     map[string]int
	waiting  map[string][]WaitingOn
	ignored  map[string]int
}

func (h *Handler) loadStageHealth(ctx context.Context, now time.Time) (stageHealthData, error) {
	d := stageHealthData{cfg: h.loadQueueHealthRules(ctx), failures: map[string][]windowFailure{},
		done: map[string]int{}, waiting: map[string][]WaitingOn{}, ignored: map[string]int{}}
	since := now.Add(-d.cfg.window())

	rows, err := h.DB.QueryContext(ctx, windowFailuresSQL, since)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var stage string
		var f windowFailure
		var ex, subj sql.NullString
		if err := rows.Scan(&stage, &f.Signature, &f.Class, &f.Count, &ex, &subj); err != nil {
			rows.Close()
			return d, err
		}
		f.Example = ex.String
		if subj.Valid {
			s := subj.String
			f.Subject = &s
		}
		d.failures[stage] = append(d.failures[stage], f)
	}
	if err := closeRows(rows); err != nil {
		return d, err
	}

	if err := h.scanCounts(ctx, windowDoneSQL, d.done, since); err != nil {
		return d, err
	}

	prow, err := h.DB.QueryContext(ctx, parkedSQL)
	if err != nil {
		return d, err
	}
	for prow.Next() {
		var stage string
		var w WaitingOn
		var healthy sql.NullBool
		var msg sql.NullString
		var at sql.NullTime
		if err := prow.Scan(&stage, &w.On, &w.Count, &healthy, &msg, &at); err != nil {
			prow.Close()
			return d, err
		}
		if healthy.Valid {
			b := healthy.Bool
			w.Healthy = &b
		}
		if msg.Valid {
			m := msg.String
			w.Message = &m
		}
		w.Since = nullTimePtr(at)
		d.waiting[stage] = append(d.waiting[stage], w)
	}
	if err := closeRows(prow); err != nil {
		return d, err
	}
	for _, ws := range d.waiting {
		sort.Slice(ws, func(i, j int) bool { return ws[i].Count > ws[j].Count })
	}

	if err := h.scanCounts(ctx, ignoredCountSQL, d.ignored); err != nil {
		return d, err
	}
	return d, nil
}

func (h *Handler) scanCounts(ctx context.Context, q string, into map[string]int, args ...any) error {
	rows, err := h.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			rows.Close()
			return err
		}
		into[k] = n
	}
	return closeRows(rows)
}

func closeRows(rows *sql.Rows) error {
	err := rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	return err
}

// openBySignature folds the open failure groups into stage -> signature -> count.
func openBySignature(groups []FailureGroup) map[string]map[string]int {
	out := map[string]map[string]int{}
	for _, g := range groups {
		if g.Cause == CauseAwaitingReview || g.Cause == CauseStaleGraph {
			continue
		}
		if out[g.Stage] == nil {
			out[g.Stage] = map[string]int{}
		}
		out[g.Stage][g.Cause] += g.Count
	}
	return out
}

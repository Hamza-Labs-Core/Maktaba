package queues

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
)

// Issue groups are computed over each subject's LATEST row per stage:
// a retried subject (whose newest row is no longer failed) drops out of
// the open issues (plan-06-11 §4.2 "Retry", §6.3).
//
// The CTE first finds the subjects that have any failed row (partial
// index processing_jobs_failed_cause_idx) and only then pulls their
// newest row through processing_jobs_subject_history_idx, so the cost
// follows the number of failing subjects, not the table size.
const latestFailedCTE = `
WITH f AS (
    SELECT DISTINCT subject_kind, subject_id, stage
      FROM processing_jobs
     WHERE state = 'failed' AND ($1::text = '' OR stage = $1::text)
), latest AS (
    SELECT DISTINCT ON (j.subject_kind, j.subject_id, j.stage)
           j.id, j.stage, j.subject_kind, j.subject_id, j.state,
           j.error_cause, j.error, j.finished_at, j.created_at
      FROM processing_jobs j
      JOIN f ON f.subject_kind = j.subject_kind AND f.subject_id = j.subject_id AND f.stage = j.stage
     ORDER BY j.subject_kind, j.subject_id, j.stage, j.id DESC
)`

const failureGroupsSQL = latestFailedCTE + `
SELECT stage, error_cause, count(*),
       min(COALESCE(finished_at, created_at)), max(COALESCE(finished_at, created_at)),
       (array_agg(error ORDER BY id DESC))[1],
       (array_agg(subject_kind || ':' || subject_id::text ORDER BY id DESC))[1:5]
  FROM latest
 WHERE state = 'failed' AND error_cause IS NOT NULL
 GROUP BY stage, error_cause`

// Rows from before the error_cause column existed are grouped in Go by
// the JSON "kind" of their error text (capped, newest first).
const legacyFailuresSQL = latestFailedCTE + `
SELECT id, stage, subject_kind, subject_id::text, error, COALESCE(finished_at, created_at)
  FROM latest
 WHERE state = 'failed' AND error_cause IS NULL
 ORDER BY id DESC
 LIMIT 5000`

const awaitingReviewSQL = `
SELECT j.stage, count(*), min(j.finished_at), max(j.finished_at),
       (array_agg(j.subject_kind || ':' || j.subject_id::text ORDER BY j.id DESC))[1:5]
  FROM processing_jobs j
  JOIN job_stages s ON s.stage = j.stage AND s.external_write
 WHERE j.state = 'done' AND j.outcome = 'awaiting_review'
   AND ($1::text = '' OR j.stage = $1::text)
   AND NOT EXISTS (SELECT 1 FROM processing_jobs n
                    WHERE n.subject_kind = j.subject_kind AND n.subject_id = j.subject_id
                      AND n.stage = j.stage AND n.id > j.id)
 GROUP BY j.stage`

const staleReadySQL = `
SELECT stage, count(*)
  FROM processing_jobs
 WHERE state = 'pending' AND (not_before IS NULL OR not_before <= $1)
   AND stage = ANY($2::text[])
 GROUP BY stage`

// staleGraphAge is how old graph_synced_at may be before a stage that
// has ready work but no running job is reported as having no worker.
const staleGraphAge = 24 * time.Hour

type groupAcc struct {
	FailureGroup
	keys []subjectKey
}

func samples(keys []string) []subjectKey {
	out := make([]subjectKey, 0, len(keys))
	for _, s := range keys {
		if k, ok := parseSubjectKey(s); ok {
			out = append(out, k)
		}
	}
	return out
}

// failureGroups returns the open issue groups for one stage ("" = all
// stages): failed causes, awaiting_review and stale_graph. withSamples
// resolves the labels of the sample subjects (one extra query); the
// snapshot passes false because it only needs the counts.
func (h *Handler) failureGroups(ctx context.Context, stage string, withSamples bool, stages []StageInfo, now time.Time) ([]FailureGroup, error) {
	acc := []*groupAcc{}

	rows, err := h.DB.QueryContext(ctx, failureGroupsSQL, stage)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var g groupAcc
		var first, last sql.NullTime
		var msg sql.NullString
		var sub pq.StringArray
		if err := rows.Scan(&g.Stage, &g.Cause, &g.Count, &first, &last, &msg, &sub); err != nil {
			rows.Close()
			return nil, err
		}
		g.FirstAt, g.LastAt = zeroTime(first), zeroTime(last)
		g.SampleMessage = errorText(msg.String)
		g.keys = samples(sub)
		acc = append(acc, &g)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// Pre-error_cause rows.
	lrows, err := h.DB.QueryContext(ctx, legacyFailuresSQL, stage)
	if err != nil {
		return nil, err
	}
	legacy := map[[2]string]*groupAcc{}
	for lrows.Next() {
		var id int64
		var st, kind, sid string
		var errTxt sql.NullString
		var at time.Time
		if err := lrows.Scan(&id, &st, &kind, &sid, &errTxt, &at); err != nil {
			lrows.Close()
			return nil, err
		}
		key := [2]string{st, legacyCause(errTxt.String)}
		g := legacy[key]
		if g == nil {
			g = &groupAcc{}
			g.Stage, g.Cause = key[0], key[1]
			g.FirstAt, g.LastAt = at, at
			g.SampleMessage = errorText(errTxt.String)
			legacy[key] = g
			acc = append(acc, g)
		}
		g.Count++
		if at.Before(g.FirstAt) {
			g.FirstAt = at
		}
		if at.After(g.LastAt) {
			g.LastAt = at
		}
		if len(g.keys) < 5 {
			g.keys = append(g.keys, subjectKey{kind, sid})
		}
	}
	if err := lrows.Err(); err != nil {
		lrows.Close()
		return nil, err
	}
	lrows.Close()

	// Merge legacy groups whose cause matches a coded group.
	acc = mergeSameCause(acc)

	// Awaiting review (external-write stages in review mode).
	arows, err := h.DB.QueryContext(ctx, awaitingReviewSQL, stage)
	if err != nil {
		return nil, err
	}
	for arows.Next() {
		var g groupAcc
		var first, last sql.NullTime
		var sub pq.StringArray
		if err := arows.Scan(&g.Stage, &g.Count, &first, &last, &sub); err != nil {
			arows.Close()
			return nil, err
		}
		g.Cause = CauseAwaitingReview
		g.FirstAt, g.LastAt = zeroTime(first), zeroTime(last)
		g.SampleMessage = "Proposed changes are waiting for your review"
		g.keys = samples(sub)
		acc = append(acc, &g)
	}
	if err := arows.Err(); err != nil {
		arows.Close()
		return nil, err
	}
	arows.Close()

	// stale_graph: ready work, nothing running, and no worker has
	// synced the graph for a day (or ever).
	stale := []string{}
	for _, s := range stages {
		if stage != "" && s.Stage != stage {
			continue
		}
		if !s.Enabled || s.Paused {
			continue
		}
		if s.GraphSyncedAt == nil || now.Sub(*s.GraphSyncedAt) > staleGraphAge {
			stale = append(stale, s.Stage)
		}
	}
	if len(stale) > 0 {
		srows, err := h.DB.QueryContext(ctx, staleReadySQL, now, pq.Array(stale))
		if err != nil {
			return nil, err
		}
		for srows.Next() {
			var g groupAcc
			if err := srows.Scan(&g.Stage, &g.Count); err != nil {
				srows.Close()
				return nil, err
			}
			g.Cause = CauseStaleGraph
			g.FirstAt, g.LastAt = now, now
			g.SampleMessage = "No worker has synced this stage's graph in the last day"
			acc = append(acc, &g)
		}
		if err := srows.Err(); err != nil {
			srows.Close()
			return nil, err
		}
		srows.Close()
	}

	// Labels for the sample subjects.
	var labels map[subjectKey]string
	if withSamples {
		var all []subjectKey
		for _, g := range acc {
			all = append(all, g.keys...)
		}
		if labels, err = labelsFor(ctx, h.DB, all); err != nil {
			return nil, err
		}
	}

	out := make([]FailureGroup, 0, len(acc))
	for _, g := range acc {
		fg := g.FailureGroup
		ci := describeCause(fg.Cause)
		fg.Label, fg.Action = ci.Label, ci.Action
		fg.SampleSubjects = []SubjectSample{}
		for _, k := range g.keys {
			if len(fg.SampleSubjects) == 5 {
				break
			}
			label := labels[k]
			if label == "" {
				label = k.ID
			}
			fg.SampleSubjects = append(fg.SampleSubjects, SubjectSample{Kind: k.Kind, ID: k.ID, Label: label})
		}
		out = append(out, fg)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].Stage != out[j].Stage {
			return out[i].Stage < out[j].Stage
		}
		return out[i].Cause < out[j].Cause
	})
	return out, nil
}

func zeroTime(t sql.NullTime) time.Time {
	if t.Valid {
		return t.Time
	}
	return time.Time{}
}

// mergeSameCause folds groups with the same (stage, cause) together.
func mergeSameCause(in []*groupAcc) []*groupAcc {
	idx := map[[2]string]*groupAcc{}
	out := []*groupAcc{}
	for _, g := range in {
		key := [2]string{g.Stage, g.Cause}
		if prev, ok := idx[key]; ok {
			prev.Count += g.Count
			if g.FirstAt.Before(prev.FirstAt) {
				prev.FirstAt = g.FirstAt
			}
			if g.LastAt.After(prev.LastAt) {
				prev.LastAt = g.LastAt
				prev.SampleMessage = g.SampleMessage
			}
			prev.keys = append(prev.keys, g.keys...)
			continue
		}
		idx[key] = g
		out = append(out, g)
	}
	return out
}

// tallyGroups derives the per-stage failed_open / awaiting_review
// counters and the issues_count badge (number of groups) from groups.
func tallyGroups(groups []FailureGroup) (failed, awaiting map[string]int, issues int) {
	failed, awaiting = map[string]int{}, map[string]int{}
	for _, g := range groups {
		issues++
		switch g.Cause {
		case CauseAwaitingReview:
			awaiting[g.Stage] += g.Count
		case CauseStaleGraph:
			// informational only
		default:
			failed[g.Stage] += g.Count
		}
	}
	return failed, awaiting, issues
}

// StageFailures implements GET /api/queues/{stage}/failures.
func (h *Handler) StageFailures(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	stage := chi.URLParam(r, "stage")
	if _, ok := h.stageOr404(w, r, stage); !ok {
		return
	}
	stages, err := h.loadStages(r.Context())
	if err != nil {
		writeErr(w, r, "load stages", err)
		return
	}
	groups, err := h.failureGroups(r.Context(), stage, true, stages, h.now())
	if err != nil {
		writeErr(w, r, "failures", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"groups": scopeFailures(principal.FromContext(r.Context()), groups)})
}

// Issues implements GET /api/issues (all stages).
func (h *Handler) Issues(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	stages, err := h.loadStages(r.Context())
	if err != nil {
		writeErr(w, r, "load stages", err)
		return
	}
	groups, err := h.failureGroups(r.Context(), "", true, stages, h.now())
	if err != nil {
		writeErr(w, r, "issues", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"groups": scopeFailures(principal.FromContext(r.Context()), groups)})
}

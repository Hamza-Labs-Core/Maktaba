package queues

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

const (
	maxRetriggerItems       = 5000
	defaultRetriggerPrio    = 50
	maxRetriggerPrio        = 1000
	retriggerBodyLimitBytes = 4 << 20
)

// Retrigger modes (plan-06-11 §6.2).
const (
	ModeQueue        = "queue"
	ModeForce        = "force"
	ModeForceCascade = "force_cascade"
)

const seasonsOfSeriesSQL = `
SELECT id::text FROM series_seasons WHERE series_id = $1::uuid AND retired_at IS NULL ORDER BY season`

// Retrigger implements POST /api/queue/retrigger: "re-add this to the
// right queue". It never computes hashes - the worker's skip check
// decides whether work actually runs. Every item runs inside one
// transaction.
func (h *Handler) Retrigger(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var body RetriggerRequest
	if e := common.ReadJSON(r, &body, retriggerBodyLimitBytes); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if len(body.Items) == 0 {
		httperror.Write(w, r, httperror.BadRequest("items must not be empty"))
		return
	}
	if len(body.Items) > maxRetriggerItems {
		httperror.Write(w, r, httperror.BadRequest("at most 5000 items per request"))
		return
	}
	mode := body.Mode
	if mode == "" {
		mode = ModeQueue
	}
	if mode != ModeQueue && mode != ModeForce && mode != ModeForceCascade {
		httperror.Write(w, r, httperror.BadRequest(`mode must be "queue", "force" or "force_cascade"`))
		return
	}
	prio := defaultRetriggerPrio
	if body.Priority != nil {
		prio = *body.Priority
	}
	if prio < 0 || prio > maxRetriggerPrio {
		httperror.Write(w, r, httperror.BadRequest("priority must be between 0 and 1000"))
		return
	}
	for i, it := range body.Items {
		if !subjectKinds[it.SubjectKind] {
			httperror.Write(w, r, httperror.BadRequest("items["+itoa(i)+"].subject_kind is invalid"))
			return
		}
		if !isUUID(it.SubjectID) {
			httperror.Write(w, r, httperror.BadRequest("items["+itoa(i)+"].subject_id must be a uuid"))
			return
		}
	}

	ctx := r.Context()
	stages, err := h.loadStages(ctx)
	if err != nil {
		writeErr(w, r, "load stages", err)
		return
	}
	entry := entryStages(stages)

	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		writeErr(w, r, "retrigger", err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	results := make([]RetriggerResult, 0, len(body.Items))
	for _, it := range body.Items {
		subjects := []subjectKey{{it.SubjectKind, it.SubjectID}}
		// A series is a display subject: no stage runs on it, so an
		// un-staged retrigger expands to its seasons.
		if it.SubjectKind == KindSeries && it.Stage == "" {
			subjects, err = seasonsOfSeries(ctx, tx, it.SubjectID)
			if err != nil {
				writeErr(w, r, "expand series", err)
				return
			}
		}
		for _, sub := range subjects {
			names := []string{it.Stage}
			if it.Stage == "" {
				names = entry[sub.Kind]
			}
			if len(names) == 0 {
				results = append(results, RetriggerResult{
					SubjectKind: sub.Kind, SubjectID: sub.ID, Stage: "", Outcome: OutcomeUnknownStage,
				})
				continue
			}
			for _, st := range names {
				res, err := Enqueue(ctx, tx, EnqueueParams{
					SubjectKind: sub.Kind, SubjectID: sub.ID, Stage: st,
					Priority: &prio, Force: mode == ModeForce, ForceCascade: mode == ModeForceCascade,
					Reason: ReasonFor(ctx, "retrigger", map[string]any{"mode": mode}),
				})
				if err != nil {
					writeErr(w, r, "enqueue "+st, err)
					return
				}
				results = append(results, RetriggerResult{
					SubjectKind: sub.Kind, SubjectID: sub.ID, Stage: st, JobID: res.JobID, Outcome: res.Outcome,
				})
			}
		}
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, r, "retrigger", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, RetriggerResponse{Results: results})
}

// entryStages maps a subject kind to the stages a bare "re-queue this"
// starts at (job_stages.entry_for), in graph order.
func entryStages(stages []StageInfo) map[string][]string {
	out := map[string][]string{}
	for _, s := range stages {
		for _, k := range s.EntryFor {
			out[k] = append(out[k], s.Stage)
		}
	}
	return out
}

func seasonsOfSeries(ctx context.Context, tx *sql.Tx, seriesID string) ([]subjectKey, error) {
	rows, err := tx.QueryContext(ctx, seasonsOfSeriesSQL, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []subjectKey{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, subjectKey{KindSeason, id})
	}
	return out, rows.Err()
}

func itoa(i int) string { return strconv.Itoa(i) }

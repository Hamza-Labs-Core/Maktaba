package queues

// Where each item of the offline pipelines is (Epic 47):
//
//	GET /api/queues/offline-items?library_id=&step=&limit=&before=
//
// Per item the order is download -> convert -> dedup check. An item is a
// subject and stage of those pipelines whose latest job is live (queued,
// running, paused) or failed; each row is that job, so the page's existing
// job controls (POST /api/jobs/{id}/cancel, /retry) act on it. A download
// row (a library's drain) names the video it is fetching now.

import (
	"net/http"
	"strconv"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Steps of an offline item, in pipeline order.
const (
	StepDownload = "download"
	StepConvert  = "convert"
	StepDedup    = "dedup"
	StepRestore  = "restore"
	StepPurge    = "purge"
)

var stepStages = map[string][]string{
	StepDownload: {StageYouTubeSync, StageYouTubeFetch, StageYouTubeBackfill},
	StepConvert:  {StageConvertMP4},
	StepDedup:    {StageDedupScan, StageDedupApply},
	StepRestore:  {StageFileRestore},
	StepPurge:    {StageTrashPurge},
}

var stepOrder = []string{StepDownload, StepConvert, StepDedup, StepRestore, StepPurge}

func stageStep(stage string) string {
	for step, stages := range stepStages {
		for _, s := range stages {
			if s == stage {
				return step
			}
		}
	}
	return ""
}

// OfflineItem is one item's latest job in the offline pipelines.
type OfflineItem struct {
	JobRef
	Step string `json:"step"`
	// Waiting is true for a dedup check that ran before its video's conversion
	// ({skipped: not_converted}): the conversion's edge queues it again.
	Waiting bool `json:"waiting_for_conversion"`
	// Downloading is the YouTube id a download job is fetching now.
	Downloading *string `json:"downloading"`
	Error       *string `json:"error"`
}

// OfflineItems implements GET /api/queues/offline-items.
func (h *Handler) OfflineItems(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lib := q.Get("library_id")
	if lib != "" && !isUUID(lib) {
		httperror.Write(w, r, httperror.BadRequest("library_id must be a uuid"))
		return
	}
	stages := []string{}
	if s := q.Get("step"); s != "" {
		st, ok := stepStages[s]
		if !ok {
			httperror.Write(w, r, httperror.BadRequest("step must be download, convert, dedup, restore or purge"))
			return
		}
		stages = append(stages, st...)
	} else {
		for _, s := range stepOrder {
			stages = append(stages, stepStages[s]...)
		}
	}
	limit, ok := parseLimit(q.Get("limit"), 100, 500)
	if !ok {
		httperror.Write(w, r, httperror.BadRequest("limit must be 1..500"))
		return
	}
	before, ok := parseCursor(q.Get("before"))
	if !ok {
		httperror.Write(w, r, httperror.BadRequest("before must be a job id"))
		return
	}
	// the latest job of each subject and stage, when it is live or failed (the
	// partial live index and the failed rows keep this small)
	query := jobRefQuery(`, j.error::text,
	       CASE WHEN j.stage = 'dedup_scan' AND j.subject_kind = 'video'
	            THEN COALESCE(j.output_summary ->> 'skipped' = 'not_converted', false) ELSE false END,
	       CASE WHEN j.stage IN ('youtube_fetch','youtube_backfill') AND j.state IN `+inFlightSQL+`
	            THEN (SELECT d.youtube_id FROM youtube_downloads d WHERE d.library_id = j.library_id
	                   AND d.state = 'downloading' ORDER BY d.requested_at DESC LIMIT 1) END`) + `
	 WHERE j.stage = ANY($1::text[]) AND ($2::uuid IS NULL OR j.library_id = $2)
	   AND (j.state IN ` + liveStatesSQL + ` OR j.state = 'failed'
	        OR (j.stage = 'dedup_scan' AND j.subject_kind = 'video' AND j.state = 'done'
	            AND j.output_summary ->> 'skipped' = 'not_converted'))
	   AND NOT EXISTS (SELECT 1 FROM processing_jobs k WHERE k.subject_kind = j.subject_kind
	        AND k.subject_id = j.subject_id AND k.stage = j.stage AND k.id > j.id)
	   AND ($3::bigint IS NULL OR j.id < $3)
	 ORDER BY j.id DESC LIMIT $4`
	var beforeArg any
	if before > 0 {
		beforeArg = before
	}
	rows, err := h.DB.QueryContext(r.Context(), query, pq.Array(stages), nullIfEmpty(lib), beforeArg, limit+1)
	if err != nil {
		writeErr(w, r, "offline items", err)
		return
	}
	defer rows.Close()
	items := []OfflineItem{}
	for rows.Next() {
		var it OfflineItem
		var errTxt []byte
		var waiting bool
		var ytID *string
		ref, err := scanJobRef(rows, &errTxt, &waiting, &ytID)
		if err != nil {
			writeErr(w, r, "offline items", err)
			return
		}
		it.JobRef, it.Step, it.Waiting, it.Downloading = ref, stageStep(ref.Stage), waiting, ytID
		if len(errTxt) > 0 {
			msg := errorText(string(errTxt))
			it.Error = &msg
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, r, "offline items", err)
		return
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		c := strconv.FormatInt(items[len(items)-1].ID, 10)
		next = &c
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items, "next": next})
}

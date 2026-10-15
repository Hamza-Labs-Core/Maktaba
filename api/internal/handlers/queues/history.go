package queues

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// shortVideoSec: an unplaced video shorter than this is a "short".
const shortVideoSec = 180

const (
	videoSubjectSQL = `
SELECT v.library_id::text, l.name, COALESCE(v.duration_sec, 0),
       EXISTS (SELECT 1 FROM series_episodes se WHERE se.video_id = v.id)
  FROM videos v JOIN libraries l ON l.id = v.library_id
 WHERE v.id = $1::uuid`

	librarySubjectSQL = `SELECT id::text, name FROM libraries WHERE id = $1::uuid`

	seasonSubjectSQL = `
SELECT ss.library_id::text, l.name, ss.kind
  FROM series_seasons ss LEFT JOIN libraries l ON l.id = ss.library_id
 WHERE ss.id = $1::uuid`

	seriesSubjectSQL = `
SELECT s.library_id::text, l.name
  FROM series s LEFT JOIN libraries l ON l.id = s.library_id
 WHERE s.id = $1::uuid`

	seriesSeasonsSQL = `
SELECT id::text, name, season, kind, episode_count
  FROM series_seasons WHERE series_id = $1::uuid AND retired_at IS NULL ORDER BY season`

	librarySeasonsListSQL = `
SELECT id::text, name, season, kind, episode_count
  FROM series_seasons WHERE library_id = $1::uuid AND retired_at IS NULL ORDER BY name, season LIMIT 500`

	lastJobsSQLTail = `
 WHERE j.id IN (SELECT DISTINCT ON (stage) id FROM processing_jobs
                 WHERE subject_kind = $1 AND subject_id = $2::uuid
                 ORDER BY stage, id DESC)`

	externalLinksSQL = `
SELECT el.target, el.title, el.external_id, el.subject_kind
  FROM external_links el
 WHERE (el.subject_kind = $1 AND el.subject_id = $2::uuid)
    OR (el.subject_kind = 'season' AND el.subject_id IN (%s))
 ORDER BY el.target, el.title
 LIMIT 50`

	videoSeasonIDsSQL = `
SELECT ss.id FROM series_episodes se
  JOIN series_seasons ss ON ss.series_id = se.series_id AND ss.season = COALESCE(se.season_override, se.season)
 WHERE se.video_id = $2::uuid`

	seriesSeasonIDsSQL = `SELECT id FROM series_seasons WHERE series_id = $2::uuid`

	noSeasonIDsSQL = `SELECT NULL::uuid WHERE FALSE`

	channelSlotsSQL = `
SELECT c.id::text, c.name, count(*)
  FROM channel_programs p JOIN channels c ON c.id = p.channel_id
 WHERE p.video_id = $1::uuid AND p.end_at > $2
 GROUP BY c.id, c.name
 ORDER BY c.name
 LIMIT 20`

	triggeredSQL = `
SELECT job_id, data FROM job_events WHERE job_id = ANY($1::bigint[]) AND kind = 'triggered'`
)

type triggeredData struct {
	To []struct {
		Stage       string `json:"stage"`
		SubjectKind string `json:"subject_kind"`
		SubjectID   string `json:"subject_id"`
		Outcome     string `json:"outcome"`
	} `json:"to"`
}

// ItemHistory implements GET /api/items/{kind}/{id}/history.
func (h *Handler) ItemHistory(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	ctx := r.Context()
	kind, id := chi.URLParam(r, "kind"), chi.URLParam(r, "id")
	if !subjectKinds[kind] {
		httperror.Write(w, r, httperror.NotFound("unknown subject kind "+strconv.Quote(kind)))
		return
	}
	if !isUUID(id) {
		httperror.Write(w, r, httperror.BadRequest("id must be a uuid"))
		return
	}
	limit, e := common.QueryInt(r, "limit", 50)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	cursor, ok := parseCursor(r.URL.Query().Get("cursor"))
	if !ok {
		httperror.Write(w, r, httperror.InvalidQuery("cursor must be a job id"))
		return
	}

	out := ItemHistory{
		Subject:   ItemSubject{Kind: kind, ID: id},
		Kinds:     []string{},
		Queues:    []ItemQueue{},
		AppearsIn: []AppearsIn{},
		Timeline:  []TimelineEntry{},
	}

	found, err := h.loadSubject(ctx, &out)
	if err != nil {
		writeErr(w, r, "load subject", err)
		return
	}
	if !found {
		httperror.Write(w, r, httperror.NotFound(kind+" "+id))
		return
	}

	labels, err := labelsFor(ctx, h.DB, []subjectKey{{kind, id}})
	if err != nil {
		writeErr(w, r, "label", err)
		return
	}
	out.Subject.Label = labels[subjectKey{kind, id}]
	if out.Subject.Label == "" {
		out.Subject.Label = id
	}

	// "Re-queue per queue": every stage that runs on this kind of subject.
	stages, err := h.loadStages(ctx)
	if err != nil {
		writeErr(w, r, "load stages", err)
		return
	}
	last, err := queryJobRefs(ctx, h.DB, jobRefQuery("")+lastJobsSQLTail, kind, id)
	if err != nil {
		writeErr(w, r, "last jobs", err)
		return
	}
	lastBy := map[string]JobRef{}
	for _, j := range last {
		lastBy[j.Stage] = j
	}
	for _, st := range stages {
		if !containsStr(st.SubjectKinds, kind) {
			continue
		}
		q := ItemQueue{Stage: st.Stage, CanRequeue: st.Enabled}
		if j, ok := lastBy[st.Stage]; ok {
			jj := j
			q.LastJob = &jj
		}
		out.Queues = append(out.Queues, q)
	}

	if kind == KindSeries || kind == KindLibrary {
		sq := seriesSeasonsSQL
		if kind == KindLibrary {
			sq = librarySeasonsListSQL
		}
		if out.Seasons, err = h.loadSeasons(ctx, sq, id); err != nil {
			writeErr(w, r, "seasons", err)
			return
		}
		out.Kinds = distinctSeasonKinds(out.Seasons)
	}

	if out.AppearsIn, err = h.loadAppearsIn(ctx, kind, id); err != nil {
		writeErr(w, r, "appears in", err)
		return
	}

	if err := h.loadTimeline(ctx, &out, kind, id, cursor, limit); err != nil {
		writeErr(w, r, "timeline", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func distinctSeasonKinds(seasons []SeasonRef) []string {
	set := map[string]bool{}
	for _, s := range seasons {
		set[s.Kind] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// loadSubject fills library info and kind chips; false when the
// subject does not exist.
func (h *Handler) loadSubject(ctx context.Context, out *ItemHistory) (bool, error) {
	kind, id := out.Subject.Kind, out.Subject.ID
	var lib, libName sql.NullString
	var err error
	switch kind {
	case KindVideo:
		var dur float64
		var inSeries bool
		err = h.DB.QueryRowContext(ctx, videoSubjectSQL, id).Scan(&lib, &libName, &dur, &inSeries)
		if err == nil {
			switch {
			case inSeries:
				out.Kinds = []string{"episode"}
			case dur > 0 && dur < shortVideoSec:
				out.Kinds = []string{"short"}
			default:
				out.Kinds = []string{"unassigned"}
			}
		}
	case KindLibrary:
		err = h.DB.QueryRowContext(ctx, librarySubjectSQL, id).Scan(&lib, &libName)
	case KindSeason:
		var k string
		err = h.DB.QueryRowContext(ctx, seasonSubjectSQL, id).Scan(&lib, &libName, &k)
		if err == nil {
			out.Kinds = []string{k}
		}
	case KindSeries:
		err = h.DB.QueryRowContext(ctx, seriesSubjectSQL, id).Scan(&lib, &libName)
	case KindCatalog:
		return id == CatalogID, nil
	}
	if err != nil {
		if isNoRows(err) {
			return false, nil
		}
		return false, err
	}
	out.Subject.LibraryID, out.Subject.LibraryLabel = nullStrPtr(lib), nullStrPtr(libName)
	return true, nil
}

func (h *Handler) loadSeasons(ctx context.Context, q, id string) ([]SeasonRef, error) {
	rows, err := h.DB.QueryContext(ctx, q, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SeasonRef{}
	for rows.Next() {
		var s SeasonRef
		var name string
		if err := rows.Scan(&s.ID, &name, &s.Season, &s.Kind, &s.EpisodeCount); err != nil {
			return nil, err
		}
		s.Label = name + " · S" + strconv.Itoa(s.Season)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (h *Handler) loadAppearsIn(ctx context.Context, kind, id string) ([]AppearsIn, error) {
	seasonIDs := noSeasonIDsSQL
	switch kind {
	case KindVideo:
		seasonIDs = videoSeasonIDsSQL
	case KindSeries:
		seasonIDs = seriesSeasonIDsSQL
	}
	rows, err := h.DB.QueryContext(ctx, sprintfSQL(externalLinksSQL, seasonIDs), kind, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppearsIn{}
	for rows.Next() {
		var a AppearsIn
		var via string
		if err := rows.Scan(&a.Target, &a.Title, &a.ExternalID, &via); err != nil {
			return nil, err
		}
		if via != kind {
			a.Detail = "via " + via
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if kind != KindVideo {
		return out, nil
	}
	crows, err := h.DB.QueryContext(ctx, channelSlotsSQL, id, h.now())
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var a AppearsIn
		var n int
		if err := crows.Scan(&a.ExternalID, &a.Title, &n); err != nil {
			return nil, err
		}
		a.Target = "channel_slot"
		a.Detail = strconv.Itoa(n) + " upcoming slot(s)"
		out = append(out, a)
	}
	return out, crows.Err()
}

type timelineRow struct {
	job     JobRef
	reasons []byte
	durMs   sql.NullFloat64
	summary []byte
}

func (h *Handler) loadTimeline(ctx context.Context, out *ItemHistory, kind, id string, cursor int64, limit int) error {
	q := jobRefQuery(`, j.reasons, EXTRACT(EPOCH FROM (j.finished_at - j.started_at)) * 1000, j.output_summary`) +
		` WHERE j.subject_kind = $1 AND j.subject_id = $2::uuid`
	args := []any{kind, id}
	if cursor > 0 {
		args = append(args, cursor)
		q += ` AND j.id < $3`
	}
	args = append(args, limit+1)
	q += ` ORDER BY j.id DESC LIMIT $` + strconv.Itoa(len(args))

	rows, err := h.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	var tl []timelineRow
	for rows.Next() {
		var t timelineRow
		j, err := scanJobRef(rows, &t.reasons, &t.durMs, &t.summary)
		if err != nil {
			rows.Close()
			return err
		}
		t.job = j
		tl = append(tl, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	if len(tl) > limit {
		tl = tl[:limit]
		c := strconv.FormatInt(tl[len(tl)-1].job.ID, 10)
		out.Next = &c
	}
	if len(tl) == 0 {
		return nil
	}

	// What each job re-queued (job_events kind='triggered').
	ids := make([]int64, len(tl))
	for i, t := range tl {
		ids[i] = t.job.ID
	}
	trows, err := h.DB.QueryContext(ctx, triggeredSQL, pq.Array(ids))
	if err != nil {
		return err
	}
	trig := map[int64]triggeredData{}
	var keys []subjectKey
	for trows.Next() {
		var jid int64
		var data []byte
		if err := trows.Scan(&jid, &data); err != nil {
			trows.Close()
			return err
		}
		var td triggeredData
		if json.Unmarshal(data, &td) != nil {
			continue
		}
		cur := trig[jid]
		cur.To = append(cur.To, td.To...)
		trig[jid] = cur
		for _, t := range td.To {
			keys = append(keys, subjectKey{t.SubjectKind, t.SubjectID})
		}
	}
	if err := trows.Err(); err != nil {
		trows.Close()
		return err
	}
	trows.Close()
	labels, err := labelsFor(ctx, h.DB, keys)
	if err != nil {
		return err
	}

	for _, t := range tl {
		e := TimelineEntry{Job: t.job, Triggered: []TriggeredRef{}, Reasons: parseReasons(t.reasons), Summary: rawOrNull(t.summary)}
		if t.durMs.Valid {
			d := int64(t.durMs.Float64)
			e.DurationMs = &d
		}
		for _, to := range trig[t.job.ID].To {
			label := labels[subjectKey{to.SubjectKind, to.SubjectID}]
			if label == "" {
				label = to.SubjectID
			}
			e.Triggered = append(e.Triggered, TriggeredRef{Stage: to.Stage, SubjectLabel: label, Outcome: to.Outcome})
		}
		out.Timeline = append(out.Timeline, e)
	}
	return nil
}

func parseReasons(raw []byte) []ReasonRef {
	out := []ReasonRef{}
	if len(raw) == 0 {
		return out
	}
	var list []map[string]any
	if json.Unmarshal(raw, &list) != nil {
		return out
	}
	for _, m := range list {
		var r ReasonRef
		r.At, _ = m["at"].(string)
		r.Why, _ = m["why"].(string)
		r.By, _ = m["by"].(string)
		out = append(out, r)
	}
	return out
}

func rawOrNull(b []byte) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("null")
	}
	return json.RawMessage(b)
}

func sprintfSQL(format, arg string) string { return fmt.Sprintf(format, arg) }

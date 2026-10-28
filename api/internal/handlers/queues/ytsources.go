package queues

// A library's YouTube sources (Epic 47, youtube_sources):
//
//	GET    /api/libraries/{id}/youtube/sources          the list, with each source's downloads
//	POST   /api/libraries/{id}/youtube/sources          {url, mode?, folder?, options?}
//	PATCH  /api/libraries/{id}/youtube/sources/{sid}    {mode?, folder?, options?}
//	DELETE /api/libraries/{id}/youtube/sources/{sid}
//	GET    /api/queues/youtube-downloads                the downloads (filters, keyset paging)
//
// A new source is resolved with `yt-dlp --flat-playlist -J --playlist-end 1`
// (the one thing the API runs itself). A source that is turned on (added on,
// or off -> on) queues the library's refresh.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/search"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// SourceResolver resolves a pasted channel or playlist URL
// (search.YtDlpSearcher in production).
type SourceResolver interface {
	ResolveSource(ctx context.Context, url string) (search.ResolvedSource, error)
}

// Source modes.
const (
	ModeOff            = "off"
	ModeNewAndBackfill = "new_and_backfill"
	ModeNewOnly        = "new_only"
)

var sourceModes = map[string]bool{ModeOff: true, ModeNewAndBackfill: true, ModeNewOnly: true}

// SourceDownloads counts a source's youtube_downloads by state.
type SourceDownloads struct {
	Wanted      int `json:"wanted"`
	Backfill    int `json:"backfill"`
	Downloading int `json:"downloading"`
	Done        int `json:"done"`
	Failed      int `json:"failed"`
	Skipped     int `json:"skipped"`
	Gone        int `json:"gone"`
}

// YouTubeSource is one row of the sources dialog.
type YouTubeSource struct {
	ID            string          `json:"id"`
	LibraryID     string          `json:"library_id"`
	Kind          string          `json:"kind"`
	URL           string          `json:"url"`
	ExternalID    string          `json:"external_id"`
	Folder        *string         `json:"folder"`
	Mode          string          `json:"mode"`
	AddedAt       *time.Time      `json:"added_at"`
	Options       json.RawMessage `json:"options"`
	Status        *string         `json:"status"`
	Error         *string         `json:"error"`
	LastCheckedAt *time.Time      `json:"last_checked_at"`
	LastFetchedAt *time.Time      `json:"last_fetched_at"`
	Downloads     SourceDownloads `json:"downloads"`
}

const sourceCols = `s.id::text, s.library_id::text, s.kind, s.url, s.external_id, s.folder, s.mode, s.added_at,
       COALESCE(s.options, '{}'::jsonb), s.status, s.error, s.last_checked_at, s.last_fetched_at,
       count(d.youtube_id) FILTER (WHERE d.state = 'wanted' AND NOT d.backfill),
       count(d.youtube_id) FILTER (WHERE d.state = 'wanted' AND d.backfill),
       count(d.youtube_id) FILTER (WHERE d.state = 'downloading'),
       count(d.youtube_id) FILTER (WHERE d.state = 'done'),
       count(d.youtube_id) FILTER (WHERE d.state = 'failed'),
       count(d.youtube_id) FILTER (WHERE d.state = 'skipped'),
       count(d.youtube_id) FILTER (WHERE d.state = 'gone')
  FROM youtube_sources s
  LEFT JOIN youtube_downloads d ON d.source_id = s.id`

func scanSource(rs rowScanner) (YouTubeSource, error) {
	var s YouTubeSource
	var folder, status, serr sql.NullString
	var added, checked, fetched sql.NullTime
	var opts []byte
	d := &s.Downloads
	err := rs.Scan(&s.ID, &s.LibraryID, &s.Kind, &s.URL, &s.ExternalID, &folder, &s.Mode, &added, &opts,
		&status, &serr, &checked, &fetched, &d.Wanted, &d.Backfill, &d.Downloading, &d.Done, &d.Failed,
		&d.Skipped, &d.Gone)
	if err != nil {
		return s, err
	}
	s.Folder, s.Status, s.Error = nullStrPtr(folder), nullStrPtr(status), nullStrPtr(serr)
	s.AddedAt, s.LastCheckedAt, s.LastFetchedAt = nullTimePtr(added), nullTimePtr(checked), nullTimePtr(fetched)
	s.Options = json.RawMessage(opts)
	return s, nil
}

func (h *Handler) loadSource(ctx context.Context, lib, sid string) (YouTubeSource, error) {
	return scanSource(h.DB.QueryRowContext(ctx, `SELECT `+sourceCols+`
 WHERE s.library_id = $1 AND s.id = $2 GROUP BY s.id`, lib, sid))
}

// ListSources implements GET /api/libraries/{id}/youtube/sources.
func (h *Handler) ListSources(w http.ResponseWriter, r *http.Request) {
	id, ok := libraryParam(w, r, false)
	if !ok {
		return
	}
	ctx := r.Context()
	if err := h.libraryExists(ctx, id); errors.Is(err, errLibraryNotFound) {
		httperror.Write(w, r, httperror.NotFound("library not found"))
		return
	} else if err != nil {
		writeErr(w, r, "library", err)
		return
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT `+sourceCols+`
 WHERE s.library_id = $1 GROUP BY s.id ORDER BY s.mode = 'off', s.kind, s.added_at, s.id`, id)
	if err != nil {
		writeErr(w, r, "sources", err)
		return
	}
	defer rows.Close()
	out := []YouTubeSource{}
	for rows.Next() {
		s, err := scanSource(rows)
		if err != nil {
			writeErr(w, r, "sources", err)
			return
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, r, "sources", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": out})
}

var (
	subLangsRe = regexp.MustCompile(`^[\w.,*-]*$`)
	browserRe  = regexp.MustCompile(`^[\w:+./-]{1,100}$`)
	rateRe     = regexp.MustCompile(`^\d+(?:\.\d+)?[KMG]?$`)
)

// validateOptions checks a source's download options the way the pipeline reads
// them (integrations/youtube_download.py DownloadOptions.from_json), so a value
// the pipeline would silently ignore is refused here instead. sub_langs given as
// a list is stored as the comma-joined string.
func validateOptions(raw map[string]any) (map[string]any, []httperror.FieldError) {
	out := map[string]any{}
	var errs []httperror.FieldError
	bad := func(f, m string) { errs = append(errs, httperror.FieldError{Field: "options." + f, Message: m}) }
	for k, v := range raw {
		if v == nil {
			continue // null: back to the default
		}
		switch k {
		case "max_height":
			f, ok := v.(float64)
			if !ok || f != float64(int(f)) || f < 144 || f > 4320 {
				bad(k, "a whole number from 144 to 4320")
				continue
			}
			out[k] = int(f)
		case "sub_langs":
			s, ok := v.(string)
			if list, isList := v.([]any); isList {
				parts := make([]string, 0, len(list))
				for _, x := range list {
					xs, ok := x.(string)
					if !ok {
						parts = nil
						break
					}
					parts = append(parts, strings.TrimSpace(xs))
				}
				s, ok = strings.Join(parts, ","), parts != nil
			}
			if !ok || len(s) > 200 || !subLangsRe.MatchString(s) {
				bad(k, `languages like "ar,en"`)
				continue
			}
			out[k] = s
		case "auto_subs", "audio_only":
			b, ok := v.(bool)
			if !ok {
				bad(k, "true or false")
				continue
			}
			out[k] = b
		case "cookies_from_browser":
			s, ok := v.(string)
			if !ok || !browserRe.MatchString(s) {
				bad(k, "a browser name like firefox or chrome:Default")
				continue
			}
			out[k] = s
		case "limit_rate":
			s, ok := v.(string)
			if !ok || !rateRe.MatchString(s) {
				bad(k, "a rate like 2M or 500K")
				continue
			}
			out[k] = s
		default:
			bad(k, "unknown option")
		}
	}
	return out, errs
}

var badFolder = regexp.MustCompile(`(^|/)\.\.?(/|$)|^/|[\x00-\x1f\\]`)

func validFolder(f string) bool {
	return f != "" && len(f) <= 255 && !badFolder.MatchString(f) && !strings.HasPrefix(f, ".maktaba")
}

// SourceRequest is the body of POST (url required) and PATCH.
type SourceRequest struct {
	URL     string         `json:"url"`
	Mode    *string        `json:"mode"`
	Folder  *string        `json:"folder"`
	Options map[string]any `json:"options"`
}

func (req SourceRequest) validate(create bool) (opts map[string]any, e *httperror.Error) {
	var errs []httperror.FieldError
	if req.Mode != nil && !sourceModes[*req.Mode] {
		errs = append(errs, httperror.FieldError{Field: "mode", Message: "off, new_and_backfill or new_only"})
	}
	if req.Folder != nil && *req.Folder != "" && !validFolder(*req.Folder) {
		errs = append(errs, httperror.FieldError{Field: "folder", Message: "a folder inside the library's first root"})
	}
	if req.Options != nil {
		var oe []httperror.FieldError
		opts, oe = validateOptions(req.Options)
		errs = append(errs, oe...)
	}
	if !create && req.URL != "" {
		errs = append(errs, httperror.FieldError{Field: "url", Message: "a source's URL cannot change; add a new one"})
	}
	if create && strings.TrimSpace(req.URL) == "" {
		errs = append(errs, httperror.FieldError{Field: "url", Message: "a channel or playlist URL"})
	}
	if errs != nil {
		return nil, httperror.Unprocessable(errs)
	}
	return opts, nil
}

// enqueueRefresh queues a forced youtube_sync for lib in tx (a source was turned on).
func enqueueRefresh(ctx context.Context, tx *sql.Tx, lib, why string) (EnqueueResult, error) {
	p := SourceRefreshPriority
	return Enqueue(ctx, tx, EnqueueParams{SubjectKind: KindLibrary, SubjectID: lib, Stage: StageYouTubeSync,
		Priority: &p, Force: true, Reason: ReasonFor(ctx, why, nil)})
}

// SourceWriteResponse is a written source and the refresh it queued, if any.
type SourceWriteResponse struct {
	Source  YouTubeSource        `json:"source"`
	Refresh *LibraryActionResult `json:"refresh"`
}

func nullable(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

// AddSource implements POST /api/libraries/{id}/youtube/sources.
func (h *Handler) AddSource(w http.ResponseWriter, r *http.Request) {
	id, ok := libraryParam(w, r, false)
	if !ok {
		return
	}
	var req SourceRequest
	if e := common.ReadJSON(r, &req, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	opts, e := req.validate(true)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	ctx := r.Context()
	if err := h.libraryExists(ctx, id); errors.Is(err, errLibraryNotFound) {
		httperror.Write(w, r, httperror.NotFound("library not found"))
		return
	} else if err != nil {
		writeErr(w, r, "library", err)
		return
	}
	if h.Sources == nil {
		httperror.Write(w, r, noYtDlp())
		return
	}
	res, err := h.Sources.ResolveSource(ctx, req.URL)
	switch {
	case errors.Is(err, search.ErrNotASource):
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "url",
			Message: "not a YouTube channel or playlist URL"}}))
		return
	case errors.Is(err, search.ErrYouTubeDisabled):
		httperror.Write(w, r, noYtDlp())
		return
	case errors.Is(err, search.ErrYouTubeRateLimited):
		httperror.Write(w, r, httperror.Unavailable(5))
		return
	case err != nil:
		httperror.Write(w, r, httperror.BadGateway("could not read the URL from YouTube").Wrap(err))
		return
	}
	mode := ModeNewAndBackfill
	if req.Mode != nil {
		mode = *req.Mode
	}
	optsJSON, err := json.Marshal(nonNil(opts))
	if err != nil {
		writeErr(w, r, "options", err)
		return
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		writeErr(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	var sid string
	err = tx.QueryRowContext(ctx, `INSERT INTO youtube_sources (library_id, kind, url, external_id, folder, mode, added_at, options)
		VALUES ($1, $2, $3, $4, $5, $6, now(), $7::jsonb)
		ON CONFLICT (library_id, kind, external_id) DO NOTHING RETURNING id::text`,
		id, res.Kind, res.URL, res.ExternalID, nullable(req.Folder), mode, string(optsJSON)).Scan(&sid)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.Conflict("", "this library already follows "+res.Kind+" "+res.ExternalID).
			With("external_id", res.ExternalID))
		return
	}
	if err != nil {
		writeErr(w, r, "add source", err)
		return
	}
	out := SourceWriteResponse{}
	if mode != ModeOff {
		er, err := enqueueRefresh(ctx, tx, id, "youtube source added")
		if err != nil {
			writeErr(w, r, "enqueue", err)
			return
		}
		out.Refresh = &LibraryActionResult{LibraryID: id, JobID: er.JobID, Outcome: er.Outcome}
	}
	if err := auditAdmin(ctx, tx, "queue.youtube_source.add", "youtube_source", sid, map[string]any{
		"library_id": id, "kind": res.Kind, "external_id": res.ExternalID, "url": res.URL, "mode": mode,
		"options": opts, "title": res.Title,
	}); err != nil {
		writeErr(w, r, "audit", err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, r, "commit", err)
		return
	}
	if out.Source, err = h.loadSource(ctx, id, sid); err != nil {
		writeErr(w, r, "source", err)
		return
	}
	common.WriteJSON(w, r, http.StatusCreated, out)
}

func sourceParams(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	id, ok := libraryParam(w, r, false)
	if !ok {
		return "", "", false
	}
	sid := chi.URLParam(r, "sid")
	if !isUUID(sid) {
		httperror.Write(w, r, httperror.BadRequest("source id must be a uuid"))
		return "", "", false
	}
	return id, sid, true
}

// PatchSource implements PATCH /api/libraries/{id}/youtube/sources/{sid}:
// mode, folder and options (null clears an option to its default; options
// given replace the stored ones). Turning a source on queues a refresh.
func (h *Handler) PatchSource(w http.ResponseWriter, r *http.Request) {
	id, sid, ok := sourceParams(w, r)
	if !ok {
		return
	}
	var req SourceRequest
	if e := common.ReadJSON(r, &req, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	opts, e := req.validate(false)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		writeErr(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	var oldMode string
	err = tx.QueryRowContext(ctx, `SELECT mode FROM youtube_sources WHERE id = $1 AND library_id = $2 FOR UPDATE`,
		sid, id).Scan(&oldMode)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("source not found"))
		return
	}
	if err != nil {
		writeErr(w, r, "source", err)
		return
	}
	sets, args := []string{"updated_at = now()"}, []any{sid}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, col+" = $"+strconv.Itoa(len(args)))
	}
	if req.Mode != nil {
		add("mode", *req.Mode)
	}
	if req.Folder != nil {
		add("folder", nullable(req.Folder))
	}
	if req.Options != nil {
		b, err := json.Marshal(opts)
		if err != nil {
			writeErr(w, r, "options", err)
			return
		}
		args = append(args, string(b))
		sets = append(sets, "options = $"+strconv.Itoa(len(args))+"::jsonb")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE youtube_sources SET `+strings.Join(sets, ", ")+` WHERE id = $1`, args...); err != nil {
		writeErr(w, r, "update source", err)
		return
	}
	out := SourceWriteResponse{}
	if oldMode == ModeOff && req.Mode != nil && *req.Mode != ModeOff {
		er, err := enqueueRefresh(ctx, tx, id, "youtube source turned on")
		if err != nil {
			writeErr(w, r, "enqueue", err)
			return
		}
		out.Refresh = &LibraryActionResult{LibraryID: id, JobID: er.JobID, Outcome: er.Outcome}
	}
	change := map[string]any{"library_id": id, "old_mode": oldMode}
	if req.Mode != nil {
		change["mode"] = *req.Mode
	}
	if req.Folder != nil {
		change["folder"] = *req.Folder
	}
	if req.Options != nil {
		change["options"] = opts
	}
	if err := auditAdmin(ctx, tx, "queue.youtube_source.patch", "youtube_source", sid, change); err != nil {
		writeErr(w, r, "audit", err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, r, "commit", err)
		return
	}
	if out.Source, err = h.loadSource(ctx, id, sid); err != nil {
		writeErr(w, r, "source", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// DeleteSource implements DELETE /api/libraries/{id}/youtube/sources/{sid}.
// Its downloads keep their rows (source_id SET NULL) and are never fetched.
func (h *Handler) DeleteSource(w http.ResponseWriter, r *http.Request) {
	id, sid, ok := sourceParams(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		writeErr(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	var kind, ext string
	err = tx.QueryRowContext(ctx, `DELETE FROM youtube_sources WHERE id = $1 AND library_id = $2 RETURNING kind, external_id`,
		sid, id).Scan(&kind, &ext)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("source not found"))
		return
	}
	if err != nil {
		writeErr(w, r, "delete source", err)
		return
	}
	if err := auditAdmin(ctx, tx, "queue.youtube_source.delete", "youtube_source", sid, map[string]any{
		"library_id": id, "kind": kind, "external_id": ext}); err != nil {
		writeErr(w, r, "audit", err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, r, "commit", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// YouTubeDownload is one youtube_downloads row.
type YouTubeDownload struct {
	LibraryID    string     `json:"library_id"`
	LibraryName  string     `json:"library_name"`
	YouTubeID    string     `json:"youtube_id"`
	SourceID     *string    `json:"source_id"`
	State        string     `json:"state"`
	Backfill     bool       `json:"backfill"`
	Reason       *string    `json:"reason"`
	FailureClass *string    `json:"failure_class"`
	Error        *string    `json:"error"`
	Attempts     int        `json:"attempts"`
	VideoID      *string    `json:"video_id"`
	UploadDate   *string    `json:"upload_date"`
	RequestedAt  *time.Time `json:"requested_at"`
	DoneAt       *time.Time `json:"done_at"`
	NotBefore    *time.Time `json:"not_before"`
}

var downloadStates = map[string]bool{"wanted": true, "downloading": true, "done": true, "skipped": true,
	"gone": true, "failed": true}

// YouTubeDownloads implements GET /api/queues/youtube-downloads: filters
// library_id, source_id, state (comma list; default failed,skipped,gone),
// keyset paging by (requested_at, youtube_id) newest first: cursor, limit.
func (h *Handler) YouTubeDownloads(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lib, src := q.Get("library_id"), q.Get("source_id")
	if (lib != "" && !isUUID(lib)) || (src != "" && !isUUID(src)) {
		httperror.Write(w, r, httperror.BadRequest("library_id and source_id must be uuids"))
		return
	}
	states := []string{"failed", "skipped", "gone"}
	if s := q.Get("state"); s != "" {
		states = strings.Split(s, ",")
		for _, st := range states {
			if !downloadStates[st] {
				httperror.Write(w, r, httperror.BadRequest("unknown state "+st))
				return
			}
		}
	}
	limit, ok := parseLimit(q.Get("limit"), 50, 200)
	if !ok {
		httperror.Write(w, r, httperror.BadRequest("limit must be 1..200"))
		return
	}
	curAt, curID, ok := parseKeyset(q.Get("cursor"))
	if !ok {
		httperror.Write(w, r, httperror.BadRequest("bad cursor"))
		return
	}
	rows, err := h.DB.QueryContext(r.Context(), `
SELECT d.library_id::text, l.name, d.youtube_id, d.source_id::text, d.state, d.backfill, d.reason, d.failure_class,
       d.error, d.attempts, d.video_id::text, to_char(d.upload_date, 'YYYY-MM-DD'), d.requested_at, d.done_at, d.not_before
  FROM youtube_downloads d JOIN libraries l ON l.id = d.library_id
 WHERE d.state = ANY($1::text[]) AND ($2::uuid IS NULL OR d.library_id = $2) AND ($3::uuid IS NULL OR d.source_id = $3)
   AND ($4::timestamptz IS NULL OR (d.requested_at, d.library_id::text || '/' || d.youtube_id) < ($4, $5))
 ORDER BY d.requested_at DESC, d.library_id::text || '/' || d.youtube_id DESC LIMIT $6`,
		pq.Array(states), nullIfEmpty(lib), nullIfEmpty(src), curAt, curID, limit+1)
	if err != nil {
		writeErr(w, r, "downloads", err)
		return
	}
	defer rows.Close()
	out := []YouTubeDownload{}
	for rows.Next() {
		var d YouTubeDownload
		var source, reason, cls, derr, vid, up sql.NullString
		var req, done, nb sql.NullTime
		if err := rows.Scan(&d.LibraryID, &d.LibraryName, &d.YouTubeID, &source, &d.State, &d.Backfill, &reason,
			&cls, &derr, &d.Attempts, &vid, &up, &req, &done, &nb); err != nil {
			writeErr(w, r, "downloads", err)
			return
		}
		d.SourceID, d.Reason, d.FailureClass, d.Error = nullStrPtr(source), nullStrPtr(reason), nullStrPtr(cls), nullStrPtr(derr)
		d.VideoID, d.UploadDate = nullStrPtr(vid), nullStrPtr(up)
		d.RequestedAt, d.DoneAt, d.NotBefore = nullTimePtr(req), nullTimePtr(done), nullTimePtr(nb)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, r, "downloads", err)
		return
	}
	var next *string
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		if last.RequestedAt != nil {
			c := makeKeyset(*last.RequestedAt, last.LibraryID+"/"+last.YouTubeID)
			next = &c
		}
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": out, "next": next})
}

func noYtDlp() *httperror.Error {
	e := httperror.Unavailable(60)
	e.Detail = "yt-dlp is not available on this server"
	return e
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

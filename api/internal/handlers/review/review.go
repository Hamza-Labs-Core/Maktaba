package review

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/transcript"
)

// LockFor is how long opening a flag holds it for the reviewer.
const LockFor = 5 * time.Minute

// ClipTTL is how long a clip URL works.
const ClipTTL = 5 * time.Minute

// ClipPad is the audio kept either side of the word; ClipMax the longest clip.
const (
	ClipPad = 1.5
	ClipMax = 12.0
)

// ClipSigner signs a streaming URL for the video (signedurl.Minter in production).
type ClipSigner interface {
	SignStreamURL(rawURL, sessionID, userID, libraryID string, ttl time.Duration) (string, time.Time, error)
}

// ComponentLister reads Epic 51's components (the language packs' page).
type ComponentLister func(ctx context.Context) ([]map[string]any, error)

// Handler serves word review.
type Handler struct {
	DB         *sql.DB
	Signer     ClipSigner
	Components ComponentLister
	NowFunc    func() time.Time
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc().UTC()
	}
	return time.Now().UTC()
}

// Mount wires every word review route.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/review/flags", h.List)
	r.Get("/api/review/flags/{id}", h.Get)
	r.Get("/api/review/flags/{id}/clip", h.Clip)
	r.Post("/api/review/flags/{id}/resolve", h.Resolve)
	r.Post("/api/review/edits/{id}/undo", h.UndoEdit)
	r.Post("/api/review/batches/{id}/undo", h.UndoBatch)
	r.Get("/api/review/summary", h.Summary)
	r.Get("/api/review/corrections/export", h.ExportCorrections)
	r.Get("/api/videos/{id}/transcript/edits", h.History)
	r.Post("/api/videos/{id}/transcript/words", h.FixWord)
	r.Get("/api/libraries/{id}/word-review", h.GetSettings)
	r.Put("/api/libraries/{id}/word-review", h.PutSettings)
	r.Get("/api/language-packs", h.LanguagePacks)
	r.Get("/api/vocabulary", h.VocabList)
	r.Post("/api/vocabulary", h.VocabAdd)
	r.Put("/api/vocabulary/{id}", h.VocabEdit)
	r.Delete("/api/vocabulary/{id}", h.VocabDelete)
	r.Post("/api/vocabulary/import", h.VocabImport)
	r.Get("/api/vocabulary/export", h.VocabExport)
}

// Routes lists every route (the role tests loop over them, placement lists them as
// home-only).
var Routes = []struct{ Method, Path string }{
	{"GET", "/api/review/flags"}, {"GET", "/api/review/flags/{id}"},
	{"GET", "/api/review/flags/{id}/clip"}, {"POST", "/api/review/flags/{id}/resolve"},
	{"POST", "/api/review/edits/{id}/undo"}, {"POST", "/api/review/batches/{id}/undo"},
	{"GET", "/api/review/summary"}, {"GET", "/api/review/corrections/export"},
	{"GET", "/api/videos/{id}/transcript/edits"}, {"POST", "/api/videos/{id}/transcript/words"},
	{"GET", "/api/libraries/{id}/word-review"}, {"PUT", "/api/libraries/{id}/word-review"},
	{"GET", "/api/language-packs"},
	{"GET", "/api/vocabulary"}, {"POST", "/api/vocabulary"}, {"PUT", "/api/vocabulary/{id}"},
	{"DELETE", "/api/vocabulary/{id}"}, {"POST", "/api/vocabulary/import"},
	{"GET", "/api/vocabulary/export"},
}

// rtl are the languages written right to left (the sentence's direction in the apps).
var rtl = map[string]bool{"ar": true, "fa": true, "ur": true, "he": true, "ps": true, "yi": true, "ckb": true, "sd": true, "ug": true}

// Direction is a language's writing direction.
func Direction(lang string) string {
	base := strings.ToLower(strings.SplitN(strings.SplitN(lang, "-", 2)[0], "_", 2)[0])
	if rtl[base] {
		return "rtl"
	}
	return "ltr"
}

// Flag is a word_flags row as the apps read it.
type Flag struct {
	ID           string          `json:"id"`
	Kind         string          `json:"kind"`
	TranscriptID string          `json:"transcript_id"`
	VideoID      string          `json:"video_id"`
	VideoTitle   string          `json:"video_title"`
	LibraryID    string          `json:"library_id"`
	SegmentSeq   int             `json:"segment_seq"`
	WordIndex    int             `json:"word_index"`
	WordCount    int             `json:"word_count"`
	CharStart    int             `json:"char_start"`
	CharLen      int             `json:"char_len"`
	StartSec     *float64        `json:"start_sec"`
	EndSec       *float64        `json:"end_sec"`
	Original     string          `json:"original"`
	Context      string          `json:"context"`
	Language     string          `json:"language"`
	Direction    string          `json:"direction"`
	Reasons      []string        `json:"reasons"`
	Scores       json.RawMessage `json:"scores"`
	Priority     float64         `json:"priority"`
	Candidates   json.RawMessage `json:"candidates"`
	Key          string          `json:"key"`
	Status       string          `json:"status"`
	Resolution   *string         `json:"resolution"`
	LockedBy     *string         `json:"locked_by"`
	LockedByName *string         `json:"locked_by_name"`
	LockedUntil  *time.Time      `json:"locked_until"`
	CreatedAt    time.Time       `json:"created_at"`
}

const flagCols = `f.id::text, f.kind, f.transcript_id::text, f.video_id::text, COALESCE(v.title, v.filename, ''),
	f.library_id::text, f.segment_seq, f.word_index, f.word_count, f.char_start, f.char_len,
	f.start_sec, f.end_sec, f.original, f.context, f.language, f.reasons, f.scores, f.priority,
	f.candidates, f.key, f.status, f.resolution, f.locked_by::text, u.username, f.locked_until, f.created_at`

const flagFrom = ` FROM word_flags f LEFT JOIN videos v ON v.id = f.video_id LEFT JOIN users u ON u.id = f.locked_by`

type scanner interface{ Scan(dest ...any) error }

func scanFlag(s scanner) (Flag, error) {
	var f Flag
	var start, end sql.NullFloat64
	var reasons pq.StringArray
	var scores, cands []byte
	err := s.Scan(&f.ID, &f.Kind, &f.TranscriptID, &f.VideoID, &f.VideoTitle, &f.LibraryID,
		&f.SegmentSeq, &f.WordIndex, &f.WordCount, &f.CharStart, &f.CharLen, &start, &end,
		&f.Original, &f.Context, &f.Language, &reasons, &scores, &f.Priority, &cands, &f.Key,
		&f.Status, &f.Resolution, &f.LockedBy, &f.LockedByName, &f.LockedUntil, &f.CreatedAt)
	if err != nil {
		return f, err
	}
	if start.Valid {
		f.StartSec = &start.Float64
	}
	if end.Valid {
		f.EndSec = &end.Float64
	}
	f.Reasons = []string(reasons)
	if f.Reasons == nil {
		f.Reasons = []string{}
	}
	f.Scores, f.Candidates = json.RawMessage(nonEmpty(scores, "{}")), json.RawMessage(nonEmpty(cands, "[]"))
	f.Direction = Direction(f.Language)
	return f, nil
}

func nonEmpty(b []byte, def string) []byte {
	if len(b) == 0 {
		return []byte(def)
	}
	return b
}

type cursor struct {
	P  float64 `json:"p"`
	ID string  `json:"id"`
}

// List implements GET /api/review/flags: by priority, cursor paged.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p, ok := gate(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	status := q.Get("status")
	if status == "" {
		status = "open"
	}
	if !in(Statuses, status) {
		httperror.Write(w, r, httperror.InvalidQuery("unknown status").With("code", "unknown_status"))
		return
	}
	limit, e := common.QueryInt(r, "limit", 50)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	limit = max(1, min(limit, 100))
	where := []string{"f.status = $1"}
	args := []any{status}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	for _, f := range []struct{ name, cond string }{
		{"library", "f.library_id = $%d"}, {"video", "f.video_id = $%d"}, {"language", "f.language = $%d"},
	} {
		if v := q.Get(f.name); v != "" {
			if f.name != "language" {
				if _, err := uuid.Parse(v); err != nil {
					httperror.Write(w, r, httperror.InvalidQuery(f.name+" is not an id"))
					return
				}
			}
			add(f.cond, v)
		}
	}
	if v := q.Get("reason"); v != "" {
		if !in(Reasons, v) {
			httperror.Write(w, r, httperror.InvalidQuery("unknown reason").With("code", "unknown_reason"))
			return
		}
		add("$%d = ANY(f.reasons)", v)
	}
	if q.Get("kind") != "" {
		add("f.kind = $%d", q.Get("kind"))
	}
	if q.Get("show_locked") != "true" {
		args = append(args, h.now(), p.UserID)
		where = append(where, fmt.Sprintf("(f.locked_until IS NULL OR f.locked_until <= $%d OR f.locked_by::text = $%d)", len(args)-1, len(args)))
	}
	if c := q.Get("cursor"); c != "" {
		var cur cursor
		raw, err := base64.RawURLEncoding.DecodeString(c)
		if err != nil || json.Unmarshal(raw, &cur) != nil {
			httperror.Write(w, r, httperror.InvalidQuery("bad cursor"))
			return
		}
		args = append(args, cur.P, cur.ID)
		where = append(where, fmt.Sprintf("(f.priority < $%d OR (f.priority = $%d AND f.id::text > $%d))", len(args)-1, len(args)-1, len(args)))
	}
	args = append(args, limit+1)
	rows, err := h.DB.QueryContext(r.Context(), `SELECT `+flagCols+flagFrom+` WHERE `+strings.Join(where, " AND ")+
		fmt.Sprintf(` ORDER BY f.priority DESC, f.id ASC LIMIT $%d`, len(args)), args...)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list flags").Wrap(err))
		return
	}
	defer func() { _ = rows.Close() }()
	items := []Flag{}
	for rows.Next() {
		f, err := scanFlag(rows)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("scan flag").Wrap(err))
			return
		}
		items = append(items, f)
	}
	if err := rows.Err(); err != nil {
		httperror.Write(w, r, httperror.Internal("list flags").Wrap(err))
		return
	}
	out := map[string]any{"items": items, "next_cursor": nil}
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		b, _ := json.Marshal(cursor{P: last.Priority, ID: last.ID})
		out["items"], out["next_cursor"] = items, base64.RawURLEncoding.EncodeToString(b)
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

func (h *Handler) loadFlag(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string, forUpdate bool) (Flag, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Flag{}, sql.ErrNoRows
	}
	sqlText := `SELECT ` + flagCols + flagFrom + ` WHERE f.id = $1`
	if forUpdate {
		sqlText += ` FOR UPDATE OF f`
	}
	return scanFlag(q.QueryRowContext(ctx, sqlText, id))
}

// Detail is a flag with its context.
type Detail struct {
	Flag
	Before         string        `json:"before"`
	After          string        `json:"after"`
	SameKeyOpen    int           `json:"same_key_open"`
	SameKeyPreview []SamePreview `json:"same_key_preview"`
	Verse          any           `json:"verse"`
	ReviewersNote  string        `json:"reviewers_note"`
}

// SamePreview is one of the other places a word is flagged.
type SamePreview struct {
	FlagID     string   `json:"flag_id"`
	VideoID    string   `json:"video_id"`
	VideoTitle string   `json:"video_title"`
	Context    string   `json:"context"`
	StartSec   *float64 `json:"start_sec"`
}

// Get implements GET /api/review/flags/{id}: the flag with the sentence before and after,
// the other places of its word, and a 5-minute soft lock for the caller (unless someone
// else holds it).
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	p, ok := gate(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	ctx := r.Context()
	now := h.now()
	_, _ = h.DB.ExecContext(ctx, `UPDATE word_flags SET locked_by = $2, locked_until = $3
		WHERE id = $1 AND (locked_until IS NULL OR locked_until <= $4 OR locked_by = $2)`,
		id, p.UserID, now.Add(LockFor), now)
	f, err := h.loadFlag(ctx, h.DB, id, false)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("flag "+id))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load flag").Wrap(err))
		return
	}
	d := Detail{Flag: f, SameKeyPreview: []SamePreview{}}
	_ = h.DB.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT text FROM transcript_segments WHERE transcript_id = $1 AND seq = $2 - 1), ''),
		COALESCE((SELECT text FROM transcript_segments WHERE transcript_id = $1 AND seq = $2 + 1), '')`,
		f.TranscriptID, f.SegmentSeq).Scan(&d.Before, &d.After)
	if f.Key != "" {
		_ = h.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM word_flags
			WHERE key = $1 AND language = $2 AND status = 'open' AND id <> $3`, f.Key, f.Language, f.ID).Scan(&d.SameKeyOpen)
		rows, err := h.DB.QueryContext(ctx, `SELECT f.id::text, f.video_id::text, COALESCE(v.title, v.filename, ''),
			f.context, f.start_sec FROM word_flags f LEFT JOIN videos v ON v.id = f.video_id
			WHERE f.key = $1 AND f.language = $2 AND f.status = 'open' AND f.id <> $3
			ORDER BY f.priority DESC, f.id LIMIT 5`, f.Key, f.Language, f.ID)
		if err == nil {
			for rows.Next() {
				var s SamePreview
				var start sql.NullFloat64
				if rows.Scan(&s.FlagID, &s.VideoID, &s.VideoTitle, &s.Context, &start) == nil {
					if start.Valid {
						s.StartSec = &start.Float64
					}
					d.SameKeyPreview = append(d.SameKeyPreview, s)
				}
			}
			_ = rows.Close()
		}
	}
	var scores map[string]any
	if json.Unmarshal(f.Scores, &scores) == nil {
		d.Verse = scores["quran"]
	}
	d.ReviewersNote = h.reviewersNote(ctx, f.LibraryID)
	common.WriteJSON(w, r, http.StatusOK, d)
}

// Clip implements GET /api/review/flags/{id}/clip: a signed URL to the streaming
// service for the audio around the word, on the transcript's own track.
func (h *Handler) Clip(w http.ResponseWriter, r *http.Request) {
	p, ok := gate(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.NotFound("flag "+id))
		return
	}
	var videoID, libID string
	var start, end, segStart, segEnd sql.NullFloat64
	var track int
	var scores []byte
	err := h.DB.QueryRowContext(ctx, `SELECT f.video_id::text, f.library_id::text, f.start_sec, f.end_sec,
		s.start_sec, s.end_sec, COALESCE(a.track_index, 0), f.scores
		FROM word_flags f
		LEFT JOIN transcript_segments s ON s.transcript_id = f.transcript_id AND s.seq = f.segment_seq
		LEFT JOIN transcripts t ON t.id = f.transcript_id
		LEFT JOIN audio_tracks a ON a.id = t.audio_track_id
		WHERE f.id = $1`, id).Scan(&videoID, &libID, &start, &end, &segStart, &segEnd, &track, &scores)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("flag "+id))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load flag").Wrap(err))
		return
	}
	a, b := ClipWindow(start, end, segStart, segEnd, strings.Contains(string(scores), `"time_estimated": true`) ||
		strings.Contains(string(scores), `"time_estimated":true`))
	if h.Signer == nil {
		httperror.Write(w, r, httperror.Unavailable(30))
		return
	}
	raw := fmt.Sprintf("/stream/clip/%s?track=%d&start=%s&end=%s", videoID, track,
		strconv.FormatFloat(a, 'f', 2, 64), strconv.FormatFloat(b, 'f', 2, 64))
	url, exp, err := h.Signer.SignStreamURL(raw, videoID, p.UserID, libID, ClipTTL)
	if err != nil {
		httperror.Write(w, r, httperror.Unavailable(30).Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"url": url, "start": a, "end": b, "expires_at": exp})
}

// ClipWindow is the clip of a word: 1.5 s either side, at most 12 s; the whole sentence
// (as much of it) when the word's time is only estimated.
func ClipWindow(start, end, segStart, segEnd sql.NullFloat64, estimated bool) (float64, float64) {
	if (estimated || !start.Valid) && segStart.Valid && segEnd.Valid {
		a := math.Max(0, segStart.Float64)
		return a, math.Min(segEnd.Float64, a+ClipMax)
	}
	a := math.Max(0, start.Float64-ClipPad)
	b := end.Float64 + ClipPad
	if b-a > ClipMax {
		mid := (start.Float64 + end.Float64) / 2
		a = math.Max(0, mid-ClipMax/2)
		b = a + ClipMax
	}
	return a, b
}

// ResolveBody is POST /api/review/flags/{id}/resolve's body.
type ResolveBody struct {
	Action         string `json:"action"`
	Text           string `json:"text"`
	ApplyToSameKey bool   `json:"apply_to_same_key"`
	Force          bool   `json:"force"`
	// Scope of a name or dictionary word: "library" (default) or "home"
	Scope string `json:"scope"`
}

func writeEditErr(w http.ResponseWriter, r *http.Request, err error) {
	var be *transcript.BatchError
	switch {
	case errors.As(err, &be):
		httperror.Write(w, r, httperror.Conflict("https://maktaba.tv/problems/"+be.Err.Error(),
			"an edit of the batch cannot be undone").With("code", be.Err.Error()).With("edit_id", be.EditID))
	case errors.Is(err, transcript.ErrStale), errors.Is(err, transcript.ErrNotActive),
		errors.Is(err, transcript.ErrChangedSince), errors.Is(err, transcript.ErrUndone):
		httperror.Write(w, r, httperror.Conflict("https://maktaba.tv/problems/"+err.Error(), err.Error()).With("code", err.Error()))
	case errors.Is(err, transcript.ErrNotFound):
		httperror.Write(w, r, httperror.NotFound("edit or sentence"))
	default:
		httperror.Write(w, r, httperror.Internal("edit").Wrap(err))
	}
}

// Resolve implements POST /api/review/flags/{id}/resolve.
func (h *Handler) Resolve(w http.ResponseWriter, r *http.Request) {
	p, ok := gate(w, r)
	if !ok {
		return
	}
	var body ResolveBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httperror.Write(w, r, httperror.BadRequest("a JSON body").With("code", "bad_body"))
		return
	}
	body.Text = strings.TrimSpace(body.Text)
	if !in(Actions, body.Action) {
		httperror.Write(w, r, httperror.BadRequest("unknown action").With("code", "unknown_action"))
		return
	}
	if body.Action == "correct" && body.Text == "" {
		httperror.Write(w, r, httperror.BadRequest("a correction needs its text").With("code", "text_required"))
		return
	}
	if body.Scope != "" && body.Scope != "library" && body.Scope != "home" {
		httperror.Write(w, r, httperror.BadRequest("scope is library or home").With("code", "bad_scope"))
		return
	}
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("begin").Wrap(err))
		return
	}
	defer func() { _ = tx.Rollback() }()
	f, err := h.loadFlag(ctx, tx, chi.URLParam(r, "id"), true)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("flag"))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load flag").Wrap(err))
		return
	}
	now := h.now()
	if f.LockedBy != nil && *f.LockedBy != p.UserID && f.LockedUntil != nil && f.LockedUntil.After(now) && !body.Force {
		name := ""
		if f.LockedByName != nil {
			name = *f.LockedByName
		}
		httperror.Write(w, r, httperror.Conflict("https://maktaba.tv/problems/locked", "being reviewed by "+name).
			With("code", "locked").With("locked_by_name", name))
		return
	}
	if f.Status == "stale" {
		httperror.Write(w, r, httperror.Conflict("https://maktaba.tv/problems/stale", "the sentence changed: open it again").With("code", "stale"))
		return
	}
	if f.Status != "open" && f.Status != "skipped" {
		httperror.Write(w, r, httperror.Conflict("https://maktaba.tv/problems/resolved", "already resolved").With("code", "resolved"))
		return
	}
	batch := ""
	if body.ApplyToSameKey {
		batch = uuid.NewString()
	}
	editID, err := h.resolveOne(ctx, tx, f, body, p.UserID, batch, now)
	if err != nil {
		writeEditErr(w, r, err)
		return
	}
	changed := 1
	if body.ApplyToSameKey && f.Key != "" {
		rows, err := tx.QueryContext(ctx, `SELECT id::text FROM word_flags WHERE key = $1 AND language = $2
			AND status = 'open' AND kind = 'word' AND id <> $3 ORDER BY id`, f.Key, f.Language, f.ID)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("same key").Wrap(err))
			return
		}
		var ids []string
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		_ = rows.Close()
		for _, id := range ids {
			other, err := h.loadFlag(ctx, tx, id, true)
			if err != nil {
				continue
			}
			// a sentence that changed meanwhile is skipped (its flag goes stale), the rest go on
			sp := "sp" + strings.ReplaceAll(id, "-", "")
			if _, err := tx.ExecContext(ctx, "SAVEPOINT "+sp); err != nil {
				httperror.Write(w, r, httperror.Internal("savepoint").Wrap(err))
				return
			}
			if _, err := h.resolveOne(ctx, tx, other, body, p.UserID, batch, now); err != nil {
				_, _ = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+sp)
				continue
			}
			_, _ = tx.ExecContext(ctx, "RELEASE SAVEPOINT "+sp)
			changed++
		}
	}
	if err := h.learn(ctx, tx, f, body, p.UserID, editID); err != nil {
		httperror.Write(w, r, httperror.Internal("vocabulary").Wrap(err))
		return
	}
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, httperror.Internal("commit").Wrap(err))
		return
	}
	out := map[string]any{"status": statusOf(body.Action), "changed": changed, "edit_id": nil, "batch_id": nil}
	if editID != "" {
		out["edit_id"] = editID
	}
	if batch != "" {
		out["batch_id"] = batch
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// resolveOne resolves one flag in tx: a correction edits the transcript.
func (h *Handler) resolveOne(ctx context.Context, tx *sql.Tx, f Flag, body ResolveBody, user, batch string, now time.Time) (string, error) {
	editID := ""
	resolution := sql.NullString{}
	if body.Action == "correct" {
		res, err := transcript.ApplyEdit(ctx, tx, transcript.Edit{
			TranscriptID: f.TranscriptID, SegmentSeq: f.SegmentSeq, CharStart: f.CharStart,
			CharLen: f.CharLen, Before: f.Original, After: body.Text, FlagID: f.ID, UserID: user, BatchID: batch,
		})
		if err != nil {
			return "", err
		}
		editID = res.EditID
		resolution = sql.NullString{String: body.Text, Valid: true}
	} else if body.Text != "" {
		resolution = sql.NullString{String: body.Text, Valid: true}
	}
	scoreBatch := "{}"
	if batch != "" && body.Action != "correct" {
		b, _ := json.Marshal(map[string]string{"batch": batch})
		scoreBatch = string(b)
	}
	_, err := tx.ExecContext(ctx, `UPDATE word_flags SET status = $2, resolution = $3, resolved_by = $4,
		resolved_at = $5, locked_by = NULL, locked_until = NULL, scores = scores || $6::jsonb WHERE id = $1`,
		f.ID, statusOf(body.Action), resolution, user, now, scoreBatch)
	return editID, err
}

// UndoEdit implements POST /api/review/edits/{id}/undo.
func (h *Handler) UndoEdit(w http.ResponseWriter, r *http.Request) {
	if _, ok := gate(w, r); !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.NotFound("edit "+id))
		return
	}
	h.inTx(w, r, func(tx *sql.Tx) (any, error) {
		if err := transcript.Undo(r.Context(), tx, id); err != nil {
			return nil, err
		}
		return map[string]any{"undone": 1}, nil
	})
}

// UndoBatch implements POST /api/review/batches/{id}/undo: all or nothing.
func (h *Handler) UndoBatch(w http.ResponseWriter, r *http.Request) {
	if _, ok := gate(w, r); !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.NotFound("batch "+id))
		return
	}
	h.inTx(w, r, func(tx *sql.Tx) (any, error) {
		n, err := transcript.UndoBatch(r.Context(), tx, id)
		if err != nil {
			return nil, err
		}
		return map[string]any{"undone": n}, nil
	})
}

func (h *Handler) inTx(w http.ResponseWriter, r *http.Request, fn func(*sql.Tx) (any, error)) {
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("begin").Wrap(err))
		return
	}
	defer func() { _ = tx.Rollback() }()
	out, err := fn(tx)
	if err != nil {
		writeEditErr(w, r, err)
		return
	}
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, httperror.Internal("commit").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// Summary implements GET /api/review/summary: open counts per library, language and
// reason, flags and fixes this week, and the share accepted as is (the live precision).
func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) {
	if _, ok := gate(w, r); !ok {
		return
	}
	ctx := r.Context()
	type lib struct {
		LibraryID     string  `json:"library_id"`
		Name          string  `json:"name"`
		Open          int     `json:"open"`
		FlagsWeek     int     `json:"flags_week"`
		FixesWeek     int     `json:"fixes_week"`
		Resolved      int     `json:"resolved"`
		AcceptedShare float64 `json:"accepted_share"`
	}
	libs := []lib{}
	week := h.now().Add(-7 * 24 * time.Hour)
	rows, err := h.DB.QueryContext(ctx, `SELECT f.library_id::text, COALESCE(l.name, ''),
		COUNT(*) FILTER (WHERE f.status = 'open' AND f.kind = 'word'),
		COUNT(*) FILTER (WHERE f.created_at >= $1),
		COUNT(*) FILTER (WHERE f.status = 'corrected' AND f.resolved_at >= $1),
		COUNT(*) FILTER (WHERE f.resolved_by IS NOT NULL AND f.status <> 'skipped'),
		COUNT(*) FILTER (WHERE f.resolved_by IS NOT NULL AND f.status = 'accepted')
		FROM word_flags f LEFT JOIN libraries l ON l.id = f.library_id
		GROUP BY f.library_id, l.name ORDER BY l.name`, week)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("summary").Wrap(err))
		return
	}
	total := 0
	for rows.Next() {
		var x lib
		var accepted int
		if err := rows.Scan(&x.LibraryID, &x.Name, &x.Open, &x.FlagsWeek, &x.FixesWeek, &x.Resolved, &accepted); err != nil {
			_ = rows.Close()
			httperror.Write(w, r, httperror.Internal("summary").Wrap(err))
			return
		}
		if x.Resolved > 0 {
			x.AcceptedShare = math.Round(float64(accepted)/float64(x.Resolved)*1000) / 1000
		}
		total += x.Open
		libs = append(libs, x)
	}
	_ = rows.Close()
	count := func(sqlText string) map[string]int {
		out := map[string]int{}
		rs, err := h.DB.QueryContext(ctx, sqlText)
		if err != nil {
			return out
		}
		defer func() { _ = rs.Close() }()
		for rs.Next() {
			var k string
			var n int
			if rs.Scan(&k, &n) == nil {
				out[k] = n
			}
		}
		return out
	}
	var last *time.Time
	_ = h.DB.QueryRowContext(ctx, `SELECT MAX(finished_at) FROM processing_jobs WHERE stage = 'word_check' AND state = 'done'`).Scan(&last)
	common.WriteJSON(w, r, http.StatusOK, map[string]any{
		"open":       total,
		"libraries":  libs,
		"languages":  count(`SELECT language, COUNT(*) FROM word_flags WHERE status = 'open' AND kind = 'word' GROUP BY language`),
		"reasons":    count(`SELECT reason, COUNT(*) FROM word_flags, unnest(reasons) AS reason WHERE status = 'open' AND kind = 'word' GROUP BY reason`),
		"last_check": last,
	})
}

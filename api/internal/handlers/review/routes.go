package review

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/transcript"
)

// ---------- edit history (story 52.9) ----------

// EditRow is one transcript_edits row.
type EditRow struct {
	ID          string     `json:"id"`
	SegmentSeq  int        `json:"segment_seq"`
	CharStart   int        `json:"char_start"`
	CharLen     int        `json:"char_len"`
	Before      string     `json:"before"`
	After       string     `json:"after"`
	FlagID      *string    `json:"flag_id"`
	BatchID     *string    `json:"batch_id"`
	User        *string    `json:"user"`
	CreatedAt   time.Time  `json:"created_at"`
	UndoneAt    *time.Time `json:"undone_at"`
	CarriedFrom *string    `json:"carried_from"`
	Active      bool       `json:"active"`
}

// History implements GET /api/videos/{id}/transcript/edits: every fix of the video's
// transcripts, newest first.
func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	if _, ok := gate(w, r); !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT e.id::text, e.segment_seq, e.char_start, e.char_len,
		e.before, e.after, e.flag_id::text, e.batch_id::text, u.username, e.created_at, e.undone_at,
		e.carried_from::text, t.is_active
		FROM transcript_edits e JOIN transcripts t ON t.id = e.transcript_id
		LEFT JOIN users u ON u.id = e.user_id
		WHERE t.video_id = $1 ORDER BY e.created_at DESC, e.id DESC LIMIT 500`, id)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("edits").Wrap(err))
		return
	}
	defer func() { _ = rows.Close() }()
	out := []EditRow{}
	for rows.Next() {
		var e EditRow
		if err := rows.Scan(&e.ID, &e.SegmentSeq, &e.CharStart, &e.CharLen, &e.Before, &e.After, &e.FlagID,
			&e.BatchID, &e.User, &e.CreatedAt, &e.UndoneAt, &e.CarriedFrom, &e.Active); err != nil {
			httperror.Write(w, r, httperror.Internal("edits").Wrap(err))
			return
		}
		out = append(out, e)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": out})
}

// ---------- fix a word from the transcript (story 52.12) ----------

// FixBody is POST /api/videos/{id}/transcript/words's body: a span of one sentence, in
// code points, as the reviewer saw it, and what to do with it.
type FixBody struct {
	SegmentSeq int    `json:"segment_seq"`
	CharStart  int    `json:"char_start"`
	CharLen    int    `json:"char_len"`
	Before     string `json:"before"`
	Text       string `json:"text"`
	Action     string `json:"action"` // correct (default), name, dictionary
	Language   string `json:"language"`
}

// FixWord implements POST /api/videos/{id}/transcript/words: an open flag on the span is
// resolved, else a manual flag is made with the action's status; a correction edits the
// sentence. It answers the sentence as it now reads.
func (h *Handler) FixWord(w http.ResponseWriter, r *http.Request) {
	p, ok := gate(w, r)
	if !ok {
		return
	}
	vid := chi.URLParam(r, "id")
	if _, err := uuid.Parse(vid); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	var body FixBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httperror.Write(w, r, httperror.BadRequest("a JSON body").With("code", "bad_body"))
		return
	}
	if body.Action == "" {
		body.Action = "correct"
	}
	body.Text = strings.TrimSpace(body.Text)
	if body.Action != "correct" && body.Action != "name" && body.Action != "dictionary" {
		httperror.Write(w, r, httperror.BadRequest("unknown action").With("code", "unknown_action"))
		return
	}
	if body.Action == "correct" && body.Text == "" {
		httperror.Write(w, r, httperror.BadRequest("a correction needs its text").With("code", "text_required"))
		return
	}
	if body.CharLen <= 0 || body.CharStart < 0 {
		httperror.Write(w, r, httperror.BadRequest("a span of the sentence").With("code", "bad_span"))
		return
	}
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("begin").Wrap(err))
		return
	}
	defer func() { _ = tx.Rollback() }()
	var tid, lib, lang string
	var track sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT t.id::text, v.library_id::text, COALESCE(t.language, ''), t.audio_track_id
		FROM transcripts t JOIN videos v ON v.id = t.video_id WHERE t.video_id = $1 AND t.is_active`, vid).
		Scan(&tid, &lib, &lang, &track)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("no transcript"))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("transcript").Wrap(err))
		return
	}
	if body.Language != "" {
		lang = body.Language
	}
	var ctxText string
	var segStart, segEnd float64
	if err := tx.QueryRowContext(ctx, `SELECT text, start_sec, end_sec FROM transcript_segments
		WHERE transcript_id = $1 AND seq = $2`, tid, body.SegmentSeq).Scan(&ctxText, &segStart, &segEnd); err != nil {
		httperror.Write(w, r, httperror.NotFound("sentence"))
		return
	}
	var flagID string
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM word_flags WHERE transcript_id = $1 AND segment_seq = $2
		AND status = 'open' AND char_start < $4 AND char_start + char_len > $3 ORDER BY char_start LIMIT 1`,
		tid, body.SegmentSeq, body.CharStart, body.CharStart+body.CharLen).Scan(&flagID)
	now := h.now()
	if errors.Is(err, sql.ErrNoRows) {
		flagID = uuid.NewString()
		scores, _ := json.Marshal(map[string]any{"manual": true, "time_estimated": true})
		_, err = tx.ExecContext(ctx, `INSERT INTO word_flags (id, kind, transcript_id, video_id, library_id,
			audio_track_id, segment_seq, char_start, char_len, start_sec, end_sec, original, context, language,
			reasons, scores, priority, candidates, key, status)
			VALUES ($1, 'word', $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, 0, '[]', $16, 'open')
			ON CONFLICT (transcript_id, segment_seq, char_start) DO UPDATE SET status = 'open',
			char_len = excluded.char_len, original = excluded.original, reasons = excluded.reasons,
			resolved_by = NULL
			RETURNING id::text`,
			flagID, tid, vid, lib, track, body.SegmentSeq, body.CharStart, body.CharLen, segStart, segEnd,
			body.Before, ctxText, lang, pq.Array([]string{"manual"}), string(scores), Key(body.Before))
		if err == nil {
			_ = tx.QueryRowContext(ctx, `SELECT id::text FROM word_flags WHERE transcript_id = $1 AND segment_seq = $2 AND char_start = $3`,
				tid, body.SegmentSeq, body.CharStart).Scan(&flagID)
		}
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("flag").Wrap(err))
		return
	}
	f, err := h.loadFlag(ctx, tx, flagID, true)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("flag").Wrap(err))
		return
	}
	// the reviewer's own span, whatever span the open flag had
	f.CharStart, f.CharLen, f.Original = body.CharStart, body.CharLen, body.Before
	rb := ResolveBody{Action: body.Action, Text: body.Text}
	editID, err := h.resolveOne(ctx, tx, f, rb, p.UserID, "", now)
	if err != nil {
		writeEditErr(w, r, err)
		return
	}
	if err := h.learn(ctx, tx, f, rb, p.UserID, editID); err != nil {
		httperror.Write(w, r, httperror.Internal("vocabulary").Wrap(err))
		return
	}
	var text string
	_ = tx.QueryRowContext(ctx, `SELECT text FROM transcript_segments WHERE transcript_id = $1 AND seq = $2`,
		tid, body.SegmentSeq).Scan(&text)
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, httperror.Internal("commit").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{
		"segment_seq": body.SegmentSeq, "text": text, "flag_id": flagID, "edit_id": nullable(editID),
	})
}

// ---------- a library's word review settings (story 52.14) ----------

// Settings is a library's word_review key: what it sets (absent keys take the home's
// default, rules.word_review).
type Settings struct {
	Enabled       *bool           `json:"enabled,omitempty"`
	Backfill      *bool           `json:"backfill,omitempty"`
	Sensitivity   *string         `json:"sensitivity,omitempty"`
	BudgetPerHour *int            `json:"budget_per_hour,omitempty"`
	Reasons       map[string]bool `json:"reasons,omitempty"`
	ReviewersNote *string         `json:"reviewers_note,omitempty"`
}

// Validate names the first field that cannot be stored.
func (s Settings) Validate() (string, string) {
	if s.Sensitivity != nil && !in([]string{"low", "normal", "high"}, *s.Sensitivity) {
		return "sensitivity", "low, normal or high"
	}
	if s.BudgetPerHour != nil && (*s.BudgetPerHour < 10 || *s.BudgetPerHour > 200) {
		return "budget_per_hour", "from 10 to 200"
	}
	for k := range s.Reasons {
		if !in(Switchable, k) {
			return "reasons." + k, "not a reason a library can turn off"
		}
	}
	if s.ReviewersNote != nil && len([]rune(*s.ReviewersNote)) > 500 {
		return "reviewers_note", "at most 500 characters"
	}
	return "", ""
}

func (h *Handler) libSettings(ctx context.Context, lib string) (Settings, bool, error) {
	var raw []byte
	err := h.DB.QueryRowContext(ctx, `SELECT COALESCE(settings -> 'word_review', 'null'::jsonb) FROM libraries WHERE id = $1`, lib).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{}, false, nil
	}
	if err != nil {
		return Settings{}, false, err
	}
	var s Settings
	_ = json.Unmarshal(raw, &s) // a stored llm_judge key of a test build is simply not read
	return s, true, nil
}

func (h *Handler) reviewersNote(ctx context.Context, lib string) string {
	s, _, _ := h.libSettings(ctx, lib)
	if s.ReviewersNote != nil {
		return *s.ReviewersNote
	}
	var raw []byte
	if h.DB.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = 'rules.word_review'`).Scan(&raw) == nil {
		var home struct {
			Note string `json:"reviewers_note"`
		}
		if json.Unmarshal(raw, &home) == nil {
			return home.Note
		}
	}
	return ""
}

// GetSettings implements GET /api/libraries/{id}/word-review: the library's own values,
// and the backfill's progress (transcripts checked of all).
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := gate(w, r); !ok {
		return
	}
	lib := chi.URLParam(r, "id")
	if _, err := uuid.Parse(lib); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	s, found, err := h.libSettings(r.Context(), lib)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("settings").Wrap(err))
		return
	}
	if !found {
		httperror.Write(w, r, httperror.NotFound("library "+lib))
		return
	}
	var total, checked int
	var hours float64
	_ = h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*), COALESCE(SUM(v.duration_sec), 0) / 3600.0,
		COUNT(*) FILTER (WHERE EXISTS (SELECT 1 FROM processing_done_subjects d
			WHERE d.library_id = v.library_id AND d.stage = 'word_check' AND d.subject_id = v.id))
		FROM transcripts t JOIN videos v ON v.id = t.video_id WHERE v.library_id = $1 AND t.is_active`, lib).
		Scan(&total, &hours, &checked)
	common.WriteJSON(w, r, http.StatusOK, map[string]any{
		"settings": s,
		"backfill": map[string]any{
			"transcripts": total, "checked": checked, "audio_hours": hours,
			// at 10 CPU-seconds an audio hour, the most a check costs
			"cpu_hours_left": float64(total-checked) / max(1, float64(total)) * hours * 10 / 3600,
		},
	})
}

// PutSettings implements PUT /api/libraries/{id}/word-review: validated, stored as the
// library's word_review key, and the library's transcripts checked again (the
// fingerprint decides which have work); a backfill turned off cancels its pending jobs.
func (h *Handler) PutSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := gate(w, r); !ok {
		return
	}
	lib := chi.URLParam(r, "id")
	if _, err := uuid.Parse(lib); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var s Settings
	if err := dec.Decode(&s); err != nil {
		httperror.Write(w, r, httperror.BadRequest(err.Error()).With("code", "bad_body"))
		return
	}
	if field, why := s.Validate(); field != "" {
		httperror.Write(w, r, httperror.BadRequest(field+": "+why).With("field", field).With("code", "invalid"))
		return
	}
	raw, _ := json.Marshal(s)
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("begin").Wrap(err))
		return
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE libraries SET settings = jsonb_set(COALESCE(settings, '{}'::jsonb),
		'{word_review}', $2::jsonb, true) WHERE id = $1`, lib, string(raw))
	if err != nil {
		httperror.Write(w, r, httperror.Internal("save").Wrap(err))
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httperror.Write(w, r, httperror.NotFound("library "+lib))
		return
	}
	if s.Backfill != nil && !*s.Backfill {
		if _, err := tx.ExecContext(ctx, `UPDATE processing_jobs SET state = 'cancelled', finished_at = now()
			WHERE stage = 'word_check' AND library_id = $1 AND state IN ('pending', 'paused')
			  AND (payload ->> 'backfill') = 'true'`, lib); err != nil {
			httperror.Write(w, r, httperror.Internal("cancel backfill").Wrap(err))
			return
		}
	}
	prio := 600
	if _, err := queues.Enqueue(ctx, tx, queues.EnqueueParams{
		SubjectKind: "library", SubjectID: lib, Stage: "word_check", Priority: &prio, Force: true,
		Reason: map[string]any{"why": "word_review_settings", "by": "admin"}, Payload: map[string]any{"recheck": true},
	}); err != nil {
		httperror.Write(w, r, httperror.Internal("recheck").Wrap(err))
		return
	}
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, httperror.Internal("commit").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"settings": s})
}

// ---------- language packs (story 52.14) ----------

// LanguagePacks implements GET /api/language-packs: what each machine's registry loaded
// (the language_packs report), the languages heard in the home's transcripts, and the
// lang.* components (their phase, from Epic 51) when a lister is wired. Read only: the
// switches are Epic 51's routes.
func (h *Handler) LanguagePacks(w http.ResponseWriter, r *http.Request) {
	if _, ok := gate(w, r); !ok {
		return
	}
	ctx := r.Context()
	type pack struct {
		Code       string     `json:"code"`
		Component  *string    `json:"component"`
		Version    string     `json:"version"`
		DataSHA256 *string    `json:"data_sha256"`
		Tokenizer  *string    `json:"tokenizer"`
		State      string     `json:"state"`
		Error      *string    `json:"error"`
		LoadedAt   *time.Time `json:"loaded_at"`
	}
	packs := []pack{}
	rows, err := h.DB.QueryContext(ctx, `SELECT code, component, version, data_sha256, tokenizer, state, error, loaded_at
		FROM language_packs ORDER BY code`)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("packs").Wrap(err))
		return
	}
	for rows.Next() {
		var p pack
		if rows.Scan(&p.Code, &p.Component, &p.Version, &p.DataSHA256, &p.Tokenizer, &p.State, &p.Error, &p.LoadedAt) == nil {
			packs = append(packs, p)
		}
	}
	_ = rows.Close()
	heard := map[string]int{}
	if hr, err := h.DB.QueryContext(ctx, `SELECT COALESCE(NULLIF(detected_language, ''), language, 'und'), COUNT(*)
		FROM transcripts WHERE is_active GROUP BY 1`); err == nil {
		for hr.Next() {
			var l string
			var n int
			if hr.Scan(&l, &n) == nil {
				heard[l] += n
			}
		}
		_ = hr.Close()
	}
	var comps []map[string]any
	if h.Components != nil {
		all, err := h.Components(ctx)
		if err == nil {
			for _, c := range all {
				if id, _ := c["id"].(string); strings.HasPrefix(id, "lang.") || id == "runtime.lang" || id == "review.words" {
					comps = append(comps, c)
				}
			}
		}
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"packs": packs, "heard": heard, "components": comps})
}

// ---------- vocabulary (story 52.10) ----------

// Vocab is one vocabulary row.
type Vocab struct {
	ID        string    `json:"id"`
	Scope     string    `json:"scope"`
	LibraryID *string   `json:"library_id"`
	Language  string    `json:"language"`
	Term      string    `json:"term"`
	Key       string    `json:"key"`
	Kind      string    `json:"kind"`
	Variants  []string  `json:"variants"`
	Source    string    `json:"source"`
	Uses      int       `json:"uses"`
	CreatedAt time.Time `json:"created_at"`
}

// VocabList implements GET /api/vocabulary (filters q, kind, language, library, scope).
func (h *Handler) VocabList(w http.ResponseWriter, r *http.Request) {
	if _, ok := gate(w, r); !ok {
		return
	}
	q := r.URL.Query()
	where, args := []string{"true"}, []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if v := q.Get("q"); v != "" {
		add("(term ILIKE '%%' || $%[1]d || '%%' OR key = $%[1]d)", v)
	}
	for _, f := range []string{"kind", "language", "scope"} {
		if v := q.Get(f); v != "" {
			add(f+" = $%d", v)
		}
	}
	if v := q.Get("library"); v != "" {
		if _, err := uuid.Parse(v); err != nil {
			httperror.Write(w, r, httperror.InvalidQuery("library is not an id"))
			return
		}
		add("library_id = $%d", v)
	}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id::text, scope, library_id::text, language, term, key, kind,
		variants, source, uses, created_at FROM vocabulary WHERE `+strings.Join(where, " AND ")+
		` ORDER BY uses DESC, term LIMIT 1000`, args...)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("vocabulary").Wrap(err))
		return
	}
	defer func() { _ = rows.Close() }()
	out := []Vocab{}
	for rows.Next() {
		var v Vocab
		var variants pq.StringArray
		if err := rows.Scan(&v.ID, &v.Scope, &v.LibraryID, &v.Language, &v.Term, &v.Key, &v.Kind, &variants,
			&v.Source, &v.Uses, &v.CreatedAt); err != nil {
			httperror.Write(w, r, httperror.Internal("vocabulary").Wrap(err))
			return
		}
		v.Variants = append([]string{}, variants...)
		out = append(out, v)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": out})
}

// VocabBody adds or edits an entry.
type VocabBody struct {
	Scope     string   `json:"scope"`
	LibraryID string   `json:"library_id"`
	Language  string   `json:"language"`
	Term      string   `json:"term"`
	Kind      string   `json:"kind"`
	Variants  []string `json:"variants"`
}

func (b *VocabBody) check() (string, string) {
	b.Term = strings.TrimSpace(b.Term)
	if b.Scope == "" {
		b.Scope = "library"
	}
	if b.Kind == "" {
		b.Kind = "word"
	}
	switch {
	case b.Term == "":
		return "term", "required"
	case b.Language == "":
		return "language", "required"
	case !in([]string{"word", "name", "term"}, b.Kind):
		return "kind", "word, name or term"
	case b.Scope != "home" && b.Scope != "library":
		return "scope", "home or library"
	case b.Scope == "library":
		if _, err := uuid.Parse(b.LibraryID); err != nil {
			return "library_id", "a library's id"
		}
	}
	return "", ""
}

func (b VocabBody) lib() any {
	if b.Scope == "home" {
		return nil
	}
	return b.LibraryID
}

// VocabAdd implements POST /api/vocabulary.
func (h *Handler) VocabAdd(w http.ResponseWriter, r *http.Request) {
	p, ok := gate(w, r)
	if !ok {
		return
	}
	var b VocabBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		httperror.Write(w, r, httperror.BadRequest("a JSON body"))
		return
	}
	if field, why := b.check(); field != "" {
		httperror.Write(w, r, httperror.BadRequest(field+": "+why).With("field", field))
		return
	}
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("begin").Wrap(err))
		return
	}
	defer func() { _ = tx.Rollback() }()
	id := uuid.NewString()
	key := Key(b.Term)
	if _, err := tx.ExecContext(ctx, `INSERT INTO vocabulary (id, scope, library_id, language, term, key, kind, variants, source, added_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'manual', $9)`,
		id, b.Scope, b.lib(), b.Language, b.Term, key, b.Kind, pq.Array(nonNilStrings(b.Variants)), p.UserID); err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			httperror.Write(w, r, httperror.Conflict("", "already in the vocabulary"))
			return
		}
		httperror.Write(w, r, httperror.Internal("add").Wrap(err))
		return
	}
	if err := rescore(ctx, tx, key, b.Language); err != nil {
		httperror.Write(w, r, httperror.Internal("rescore").Wrap(err))
		return
	}
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, httperror.Internal("commit").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusCreated, map[string]any{"id": id, "key": key})
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// VocabEdit implements PUT /api/vocabulary/{id}: term, kind and variants.
func (h *Handler) VocabEdit(w http.ResponseWriter, r *http.Request) {
	if _, ok := gate(w, r); !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.NotFound("entry"))
		return
	}
	var b VocabBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		httperror.Write(w, r, httperror.BadRequest("a JSON body"))
		return
	}
	b.Term = strings.TrimSpace(b.Term)
	if b.Term == "" || (b.Kind != "" && !in([]string{"word", "name", "term"}, b.Kind)) {
		httperror.Write(w, r, httperror.BadRequest("term, and kind word, name or term").With("field", "term"))
		return
	}
	res, err := h.DB.ExecContext(r.Context(), `UPDATE vocabulary SET term = $2, key = $3,
		kind = COALESCE(NULLIF($4, ''), kind), variants = $5 WHERE id = $1`,
		id, b.Term, Key(b.Term), b.Kind, pq.Array(nonNilStrings(b.Variants)))
	if err != nil {
		httperror.Write(w, r, httperror.Internal("edit").Wrap(err))
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httperror.Write(w, r, httperror.NotFound("entry"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"id": id})
}

// VocabDelete implements DELETE /api/vocabulary/{id}: the transcripts whose flags the
// word had accepted are checked again, so they open again.
func (h *Handler) VocabDelete(w http.ResponseWriter, r *http.Request) {
	if _, ok := gate(w, r); !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.NotFound("entry"))
		return
	}
	h.inTx(w, r, func(tx *sql.Tx) (any, error) {
		var key, lang string
		err := tx.QueryRowContext(r.Context(), `DELETE FROM vocabulary WHERE id = $1 RETURNING key, language`, id).Scan(&key, &lang)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, transcript.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"deleted": id}, rescore(r.Context(), tx, key, lang)
	})
}

// VocabImport implements POST /api/vocabulary/import: a CSV of term, kind, language,
// variants (separated by |), with scope and library as query parameters. Good rows go in;
// the answer lists the bad ones with why.
func (h *Handler) VocabImport(w http.ResponseWriter, r *http.Request) {
	p, ok := gate(w, r)
	if !ok {
		return
	}
	scope := r.URL.Query().Get("scope")
	lib := r.URL.Query().Get("library")
	rd := csv.NewReader(io.LimitReader(r.Body, 8<<20))
	rd.FieldsPerRecord = -1
	type bad struct {
		Line   int    `json:"line"`
		Reason string `json:"reason"`
	}
	var bads []bad
	added := 0
	ctx := r.Context()
	for line := 1; ; line++ {
		rec, err := rd.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			bads = append(bads, bad{line, "not CSV"})
			break
		}
		if line == 1 && len(rec) > 0 && strings.EqualFold(strings.TrimSpace(rec[0]), "term") {
			continue
		}
		if len(rec) < 3 {
			bads = append(bads, bad{line, "term, kind and language are needed"})
			continue
		}
		b := VocabBody{Scope: scope, LibraryID: lib, Term: rec[0], Kind: strings.TrimSpace(rec[1]), Language: strings.TrimSpace(rec[2])}
		if len(rec) > 3 && strings.TrimSpace(rec[3]) != "" {
			b.Variants = strings.Split(rec[3], "|")
		}
		if field, why := b.check(); field != "" {
			bads = append(bads, bad{line, field + ": " + why})
			continue
		}
		_, err = h.DB.ExecContext(ctx, `INSERT INTO vocabulary (id, scope, library_id, language, term, key, kind, variants, source, added_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'import', $9) ON CONFLICT (scope, owner, language, key, term) DO NOTHING`,
			uuid.NewString(), b.Scope, b.lib(), b.Language, b.Term, Key(b.Term), b.Kind, pq.Array(nonNilStrings(b.Variants)), p.UserID)
		if err != nil {
			bads = append(bads, bad{line, "not stored"})
			continue
		}
		added++
	}
	if bads == nil {
		bads = []bad{}
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"added": added, "bad": bads})
}

// VocabExport implements GET /api/vocabulary/export (CSV).
func (h *Handler) VocabExport(w http.ResponseWriter, r *http.Request) {
	if _, ok := gate(w, r); !ok {
		return
	}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT term, kind, language, array_to_string(variants, '|'),
		scope, COALESCE(library_id::text, ''), uses FROM vocabulary ORDER BY language, term`)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("export").Wrap(err))
		return
	}
	defer func() { _ = rows.Close() }()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="vocabulary.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"term", "kind", "language", "variants", "scope", "library_id", "uses"})
	for rows.Next() {
		var rec [6]string
		var uses int
		if rows.Scan(&rec[0], &rec[1], &rec[2], &rec[3], &rec[4], &rec[5], &uses) == nil {
			_ = cw.Write(append(rec[:], fmt.Sprint(uses)))
		}
	}
	cw.Flush()
}

// ExportCorrections implements GET /api/review/corrections/export?library=: every live
// correction with its audio span and a clip URL that works for 24 hours (for whoever
// later fine-tunes a model; nothing is trained here).
func (h *Handler) ExportCorrections(w http.ResponseWriter, r *http.Request) {
	p, ok := gate(w, r)
	if !ok {
		return
	}
	args := []any{}
	where := "e.undone_at IS NULL"
	if lib := r.URL.Query().Get("library"); lib != "" {
		if _, err := uuid.Parse(lib); err != nil {
			httperror.Write(w, r, httperror.InvalidQuery("library is not an id"))
			return
		}
		args = append(args, lib)
		where += " AND v.library_id = $1"
	}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT t.video_id::text, v.library_id::text, COALESCE(a.track_index, 0),
		COALESCE(f.start_sec, s.start_sec), COALESCE(f.end_sec, s.end_sec), e.before, e.after,
		COALESCE(t.language, ''), COALESCE(u.username, ''), e.created_at
		FROM transcript_edits e JOIN transcripts t ON t.id = e.transcript_id JOIN videos v ON v.id = t.video_id
		LEFT JOIN transcript_segments s ON s.transcript_id = e.transcript_id AND s.seq = e.segment_seq
		LEFT JOIN word_flags f ON f.id = e.flag_id LEFT JOIN audio_tracks a ON a.id = t.audio_track_id
		LEFT JOIN users u ON u.id = e.user_id WHERE `+where+` ORDER BY e.created_at`, args...)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("export").Wrap(err))
		return
	}
	defer func() { _ = rows.Close() }()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="corrections.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"video", "track", "start", "end", "heard", "meant", "language", "user", "time", "clip"})
	for rows.Next() {
		var vid, lib, before, after, lang, user string
		var track int
		var start, end sql.NullFloat64
		var at time.Time
		if rows.Scan(&vid, &lib, &track, &start, &end, &before, &after, &lang, &user, &at) != nil {
			continue
		}
		a, b := ClipWindow(start, end, start, end, false)
		clip := ""
		if h.Signer != nil {
			raw := fmt.Sprintf("/stream/clip/%s?track=%d&start=%.2f&end=%.2f", vid, track, a, b)
			if u, _, err := h.Signer.SignStreamURL(raw, vid, p.UserID, lib, 24*time.Hour); err == nil {
				clip = u
			}
		}
		_ = cw.Write([]string{vid, fmt.Sprint(track), fmt.Sprintf("%.2f", a), fmt.Sprintf("%.2f", b), before, after,
			lang, user, at.UTC().Format(time.RFC3339), clip})
	}
	cw.Flush()
}

package browse

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// LibrarySummary is one library card of GET /api/libraries/summary.
type LibrarySummary struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Videos         int64      `json:"videos"`
	DurationSec    float64    `json:"duration_sec"`
	PosterVideoIDs []string   `json:"poster_video_ids"`
	LatestAddedAt  *time.Time `json:"latest_added_at"`
	Pinned         bool       `json:"pinned"`
	PinPosition    *int       `json:"pin_position"`
}

// Summary implements GET /api/libraries/summary: one card per visible
// library, by name. Three queries: the counts (one grouped pass), the
// posters and the caller's pins.
func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) {
	p := caller(w, r)
	if p == nil {
		return
	}
	s := scopeOf(p)
	items := []LibrarySummary{}
	if s.none() {
		writeJSON(w, r, map[string]any{"items": items})
		return
	}
	ctx := r.Context()
	var args []any
	rows, err := h.DB.QueryContext(ctx, `SELECT l.id, l.name, COUNT(v.id), COALESCE(SUM(v.duration_sec), 0), MAX(v.created_at)
		FROM libraries l LEFT JOIN videos v ON v.library_id = l.id AND `+visible+`
		WHERE `+and(s.cond("l.id", &args))+`
		GROUP BY l.id, l.name ORDER BY l.name, l.id`, args...)
	if err != nil {
		internal(w, r, "count libraries", err)
		return
	}
	var ids []string
	for rows.Next() {
		var it LibrarySummary
		var latest nullTime
		if err := rows.Scan(&it.ID, &it.Name, &it.Videos, &it.DurationSec, &latest); err != nil {
			rows.Close()
			internal(w, r, "scan library", err)
			return
		}
		it.LatestAddedAt = latest.ptr()
		it.PosterVideoIDs = []string{}
		items = append(items, it)
		ids = append(ids, it.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		internal(w, r, "read libraries", err)
		return
	}

	posters, err := h.recentPerLibrary(ctx, ids, posterCount)
	if err != nil {
		internal(w, r, "posters", err)
		return
	}
	pins, err := h.pinPositions(ctx, userID(p))
	if err != nil {
		internal(w, r, "pins", err)
		return
	}
	for i := range items {
		for _, c := range posters[items[i].ID] {
			items[i].PosterVideoIDs = append(items[i].PosterVideoIDs, c.ID)
		}
		if pos, ok := pins[items[i].ID]; ok {
			items[i].Pinned = true
			items[i].PinPosition = &pos
		}
	}
	writeJSON(w, r, map[string]any{"items": items})
}

// pinPositions is the caller's pins, library id to position.
func (h *Handler) pinPositions(ctx context.Context, uid string) (map[string]int, error) {
	out := map[string]int{}
	if uid == "" {
		return out, nil
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT library_id, position FROM user_library_pins WHERE user_id = $1`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var pos int
		if err := rows.Scan(&id, &pos); err != nil {
			return nil, err
		}
		out[id] = pos
	}
	return out, rows.Err()
}

// PinnedLibrary is one of the caller's pins.
type PinnedLibrary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Position int    `json:"position"`
}

// pinned is the caller's pins they may still see, in their order. A pin
// whose library the caller lost access to stays stored but is not shown.
func (h *Handler) pinned(ctx context.Context, uid string, s scope) ([]PinnedLibrary, error) {
	out := []PinnedLibrary{}
	if uid == "" || s.none() {
		return out, nil
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT l.id, l.name, p.position
		FROM user_library_pins p JOIN libraries l ON l.id = p.library_id
		WHERE p.user_id = $1 ORDER BY p.position, l.name`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pl PinnedLibrary
		if err := rows.Scan(&pl.ID, &pl.Name, &pl.Position); err != nil {
			return nil, err
		}
		if s.has(pl.ID) {
			out = append(out, pl)
		}
	}
	return out, rows.Err()
}

func pinsDoc(pins []PinnedLibrary) map[string]any {
	ids := make([]string, len(pins))
	for i, p := range pins {
		ids[i] = p.ID
	}
	return map[string]any{"library_ids": ids, "items": pins}
}

// GetPins implements GET /api/me/pinned-libraries.
func (h *Handler) GetPins(w http.ResponseWriter, r *http.Request) {
	p := caller(w, r)
	if p == nil {
		return
	}
	pins, err := h.pinned(r.Context(), userID(p), scopeOf(p))
	if err != nil {
		internal(w, r, "pins", err)
		return
	}
	writeJSON(w, r, pinsDoc(pins))
}

// PutPinsRequest is the body of PUT /api/me/pinned-libraries.
type PutPinsRequest struct {
	LibraryIDs []string `json:"library_ids"`
}

// PutPins implements PUT /api/me/pinned-libraries: the caller's pins
// become the list, in its order. A repeated id counts once; a library the
// caller may not see is 404, the same as one that does not exist.
func (h *Handler) PutPins(w http.ResponseWriter, r *http.Request) {
	p := caller(w, r)
	if p == nil {
		return
	}
	uid := userID(p)
	if uid == "" {
		httperror.Write(w, r, httperror.Forbidden("", "pins belong to a user account"))
		return
	}
	var req PutPinsRequest
	if e := common.ReadJSON(r, &req, 16<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	var ids []string
	seen := map[string]bool{}
	for _, id := range req.LibraryIDs {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) > MaxPins {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "library_ids",
			Message: "at most " + strconv.Itoa(MaxPins) + " libraries"}}))
		return
	}
	s := scopeOf(p)
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil || !s.has(id) {
			httperror.Write(w, r, httperror.NotFound("library "+id))
			return
		}
	}
	ctx := r.Context()
	if len(ids) > 0 {
		var args []any
		in := inList(ids, &args)
		var n int
		if err := h.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM libraries WHERE id IN (`+in+`)`, args...).Scan(&n); err != nil {
			internal(w, r, "check libraries", err)
			return
		}
		if n != len(ids) {
			httperror.Write(w, r, httperror.NotFound("library"))
			return
		}
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		internal(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	// Two PUTs at once would both clear, then both insert, and the second
	// insert would hit the primary key. On Postgres the user's row is locked
	// first so they run one after the other; SQLite's write lock already does.
	if !isSQLite(h.DB) {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, uid).Scan(&one); err != nil && !errors.Is(err, sql.ErrNoRows) {
			internal(w, r, "lock user", err)
			return
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_library_pins WHERE user_id = $1`, uid); err != nil {
		internal(w, r, "clear pins", err)
		return
	}
	now := h.ts(h.now())
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_library_pins (user_id, library_id, position, created_at)
			VALUES ($1, $2, $3, $4)`, uid, id, i, now); err != nil {
			internal(w, r, "pin", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		internal(w, r, "commit pins", err)
		return
	}
	pins, err := h.pinned(ctx, uid, s)
	if err != nil {
		internal(w, r, "pins", err)
		return
	}
	writeJSON(w, r, pinsDoc(pins))
}

// PinnedRows is one pinned library's rows on the home.
type PinnedRows struct {
	Library          LibraryRef `json:"library"`
	RecentlyAdded    []Card     `json:"recently_added"`
	ContinueWatching []Card     `json:"continue_watching"`
}

// LibraryRef names a library.
type LibraryRef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Videos *int64 `json:"videos,omitempty"`
}

// Home implements GET /api/home: the caller's resumable videos (the overall
// row and each pinned library's are cut from them), recently added, recently
// watched, On Deck, favorites, the pins, and the pinned libraries' recently
// added.
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	p := caller(w, r)
	if p == nil {
		return
	}
	s := scopeOf(p)
	doc := map[string]any{
		"continue_watching": []Card{}, "recently_added": []Card{},
		"recently_watched": []Card{}, "pinned": []PinnedRows{},
		"on_deck": []Card{}, "favorites": []Card{},
		"recently_added_grouped": []AddedEntry{}, "featured": nil,
	}
	if s.none() {
		writeJSON(w, r, doc)
		return
	}
	ctx := r.Context()
	uid := userID(p)
	cont, err := h.continueWatching(ctx, uid, s, "", continueScan)
	if err != nil {
		internal(w, r, "continue watching", err)
		return
	}
	added, err := h.recentlyAdded(ctx, s, "", RowSize)
	if err != nil {
		internal(w, r, "recently added", err)
		return
	}
	watched, err := h.recentlyWatched(ctx, uid, s, "", RowSize)
	if err != nil {
		internal(w, r, "recently watched", err)
		return
	}
	grouped, err := h.recentlyAddedGrouped(ctx, s, "", RowSize)
	if err != nil {
		internal(w, r, "recently added by series", err)
		return
	}
	doc["recently_added_grouped"] = grouped
	feat, err := h.featured(ctx, s)
	if err != nil {
		internal(w, r, "featured", err)
		return
	}
	doc["featured"] = feat
	deck, err := h.onDeck(ctx, uid, s, "", RowSize)
	if err != nil {
		internal(w, r, "on deck", err)
		return
	}
	favs, err := h.favoriteCards(ctx, uid, s, RowSize)
	if err != nil {
		internal(w, r, "favorites", err)
		return
	}
	doc["on_deck"], doc["favorites"] = deck, favs
	pins, err := h.pinned(ctx, uid, s)
	if err != nil {
		internal(w, r, "pins", err)
		return
	}
	pinIDs := make([]string, len(pins))
	for i, pl := range pins {
		pinIDs[i] = pl.ID
	}
	perLib, err := h.recentPerLibrary(ctx, pinIDs, RowSize)
	if err != nil {
		internal(w, r, "pinned recently added", err)
		return
	}
	contByLib := map[string][]Card{}
	for _, c := range cont {
		if len(contByLib[c.LibraryID]) < RowSize {
			contByLib[c.LibraryID] = append(contByLib[c.LibraryID], c)
		}
	}
	rows := []PinnedRows{}
	for _, pl := range pins {
		pr := PinnedRows{Library: LibraryRef{ID: pl.ID, Name: pl.Name},
			RecentlyAdded: perLib[pl.ID], ContinueWatching: contByLib[pl.ID]}
		if pr.RecentlyAdded == nil {
			pr.RecentlyAdded = []Card{}
		}
		if pr.ContinueWatching == nil {
			pr.ContinueWatching = []Card{}
		}
		rows = append(rows, pr)
	}
	if len(cont) > RowSize {
		cont = cont[:RowSize]
	}
	doc["continue_watching"], doc["recently_added"], doc["recently_watched"], doc["pinned"] = cont, added, watched, rows
	writeJSON(w, r, doc)
}

// SeriesRef is one series on a library's hub, with its visible episodes
// in that library.
type SeriesRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Episodes int64  `json:"episodes"`
}

// YearCount is the number of a library's videos published in a year (the
// mtime's year).
type YearCount struct {
	Year   int   `json:"year"`
	Videos int64 `json:"videos"`
}

// Hub implements GET /api/libraries/{id}/hub: the library's rows.
func (h *Handler) Hub(w http.ResponseWriter, r *http.Request) {
	p := caller(w, r)
	if p == nil {
		return
	}
	id := chi.URLParam(r, "id")
	s := scopeOf(p)
	// One answer for a malformed id, an unknown library and one the
	// caller may not see: nothing tells them apart.
	if _, err := uuid.Parse(id); err != nil || !s.has(id) {
		httperror.Write(w, r, httperror.NotFound("library "+id))
		return
	}
	ctx := r.Context()
	lib := LibraryRef{ID: id}
	var n int64
	err := h.DB.QueryRowContext(ctx, `SELECT l.name, (SELECT COUNT(*) FROM videos v WHERE v.library_id = l.id AND `+visible+`)
		FROM libraries l WHERE l.id = $1`, id).Scan(&lib.Name, &n)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("library "+id))
		return
	}
	if err != nil {
		internal(w, r, "load library", err)
		return
	}
	lib.Videos = &n
	uid := userID(p)
	cont, err := h.continueWatching(ctx, uid, s, id, RowSize)
	if err != nil {
		internal(w, r, "continue watching", err)
		return
	}
	added, err := h.recentlyAdded(ctx, s, id, RowSize)
	if err != nil {
		internal(w, r, "recently added", err)
		return
	}
	watched, err := h.recentlyWatched(ctx, uid, s, id, RowSize)
	if err != nil {
		internal(w, r, "recently watched", err)
		return
	}
	most, err := h.mostWatched(ctx, id, RowSize)
	if err != nil {
		internal(w, r, "most watched", err)
		return
	}
	series, err := h.series(ctx, id)
	if err != nil {
		internal(w, r, "series", err)
		return
	}
	years, err := h.years(ctx, id)
	if err != nil {
		internal(w, r, "years", err)
		return
	}
	deck, err := h.onDeck(ctx, uid, s, id, RowSize)
	if err != nil {
		internal(w, r, "on deck", err)
		return
	}
	grouped, err := h.recentlyAddedGrouped(ctx, s, id, RowSize)
	if err != nil {
		internal(w, r, "recently added by series", err)
		return
	}
	writeJSON(w, r, map[string]any{
		"library": lib,
		"rows": map[string]any{
			"continue_watching": cont, "recently_added": added, "recently_watched": watched,
			"most_watched": most, "series": series, "years": years, "on_deck": deck,
			"recently_added_grouped": grouped,
		},
	})
}

// mostWatched is the library's videos by everyone's plays (watch_sessions
// rows) in the last 90 days, the most recently added first among equal
// counts. Only counts are shown, never who played.
func (h *Handler) mostWatched(ctx context.Context, lib string, n int) ([]Card, error) {
	since := h.ts(h.now().AddDate(0, 0, -mostWatchedDays))
	rows, err := h.DB.QueryContext(ctx, `SELECT `+cardColumns+`, c.plays
		FROM (SELECT ws.video_id, COUNT(*) AS plays FROM watch_sessions ws
		      JOIN videos wv ON wv.id = ws.video_id
		      WHERE wv.library_id = $1 AND ws.started_at >= $2 GROUP BY ws.video_id) c
		JOIN videos v ON v.id = c.video_id
		WHERE `+visible+`
		ORDER BY c.plays DESC, v.created_at DESC, v.id DESC LIMIT $3`, lib, since, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Card{}
	for rows.Next() {
		var plays int64
		c, err := scanCard(rows, &plays)
		if err != nil {
			return nil, err
		}
		c.Plays = &plays
		out = append(out, c)
	}
	return out, rows.Err()
}

// series is the series with visible episodes in the library, by name.
func (h *Handler) series(ctx context.Context, lib string) ([]SeriesRef, error) {
	rows, err := h.DB.QueryContext(ctx, `SELECT s.id, COALESCE(s.name_override, s.name) AS sname, COUNT(v.id)
		FROM series s JOIN series_episodes se ON se.series_id = s.id
		JOIN videos v ON v.id = se.video_id
		WHERE v.library_id = $1 AND `+visible+`
		GROUP BY s.id, s.name, s.name_override ORDER BY sname, s.id LIMIT $2`, lib, maxSeries)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SeriesRef{}
	for rows.Next() {
		var sr SeriesRef
		if err := rows.Scan(&sr.ID, &sr.Name, &sr.Episodes); err != nil {
			return nil, err
		}
		out = append(out, sr)
	}
	return out, rows.Err()
}

// years is the library's visible videos per year of mtime, newest year
// first.
func (h *Handler) years(ctx context.Context, lib string) ([]YearCount, error) {
	// SQLite keeps the time as text that starts with the year.
	yearExpr := `CAST(EXTRACT(YEAR FROM v.mtime) AS INTEGER)`
	if isSQLite(h.DB) {
		yearExpr = `CAST(substr(v.mtime, 1, 4) AS INTEGER)`
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT `+yearExpr+` AS y, COUNT(*)
		FROM videos v WHERE v.library_id = $1 AND v.mtime IS NOT NULL AND `+visible+`
		GROUP BY y ORDER BY y DESC`, lib)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []YearCount{}
	for rows.Next() {
		var yc YearCount
		if err := rows.Scan(&yc.Year, &yc.Videos); err != nil {
			return nil, err
		}
		out = append(out, yc)
	}
	return out, rows.Err()
}

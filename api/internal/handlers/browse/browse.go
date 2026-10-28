// Package browse implements the browsing API of Epic 45 (story 45.4): the
// library cards, each user's pinned libraries, the home rows and a
// library's hub.
//
// Routes (every signed-in user; only the libraries they may see):
//
//	GET /api/libraries/summary
//	GET /api/me/pinned-libraries
//	PUT /api/me/pinned-libraries
//	GET /api/home
//	GET /api/libraries/{id}/hub
//
// and Epic 45 Phase 2's (mine.go, playlists.go, facets.go, ondeck.go):
//
//	POST /api/me/watched, POST /api/me/favorites, GET /api/me/videos/{id}
//	/api/me/playlists...
//	GET /api/videos/facets
//
// Every row leaves out the hidden videos (state missing or superseded, or
// soft-deleted). A library the caller may not see answers 404, as one that
// does not exist.
package browse

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

const (
	// RowSize is the number of cards in a home or hub row.
	RowSize = 20
	// posterCount is how many recent videos a library card shows.
	posterCount = 4
	// mostWatchedDays is the window of the Most Watched row.
	mostWatchedDays = 90
	// continueScan caps the caller's resumable videos read for the home;
	// the overall row and each pinned library's row are cut from them.
	continueScan = 500
	// maxSeries caps the hub's series row.
	maxSeries = 100
)

// visible is the predicate on videos v that keeps a video in the rows.
const visible = `v.deleted_at IS NULL AND v.state NOT IN ('missing', 'superseded')`

// publishedAt is a video's publish date: its YouTube upload date (slot 0164),
// else the file's mtime.
const publishedAt = `COALESCE(v.published_at, v.mtime)`

// cardColumns are the card's columns of videos v, as scanCard reads them.
const cardColumns = `v.id, v.title, v.filename, v.library_id, v.duration_sec, v.state, v.created_at, ` +
	publishedAt + ` AS published_at`

// Handler serves the browsing routes.
type Handler struct {
	DB      *sql.DB
	NowFunc func() time.Time
	// Rows, when set, serves the per-library rows (a pinned library's on the
	// home, a hub's recently added) from memory (rowcache.go). Nil: read each
	// time.
	Rows *RowCache
}

// Mount attaches the routes. /api/libraries/summary is a static segment, so
// chi prefers it to the libraries handler's /api/libraries/{id}.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/libraries/summary", h.Summary)
	r.Get("/api/libraries/{id}/hub", h.Hub)
	r.Get("/api/me/pinned-libraries", h.GetPins)
	r.Put("/api/me/pinned-libraries", h.PutPins)
	r.Get("/api/home", h.Home)
	// Epic 45 Phase 3 (story 45.21): the admin's pick for Home's hero
	r.Get("/api/home/featured", h.GetFeatured)
	r.Put("/api/home/featured", h.PutFeatured)
	// Epic 45 Phase 2: a user's marks, favorites and playlists, and the
	// grid's facets (/api/videos/facets is a static segment, which chi
	// prefers to the videos handler's /api/videos/{id})
	r.Post("/api/me/watched", h.Watched)
	r.Post("/api/me/favorites", h.Favorites)
	r.Get("/api/me/videos/{id}", h.MyVideo)
	r.Get("/api/videos/facets", h.Facets)
	h.mountPlaylists(r)
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc().UTC()
	}
	return time.Now().UTC()
}

// Card is a video card. Its picture is /api/videos/{id}/poster.
type Card struct {
	ID          string     `json:"id"`
	Title       *string    `json:"title"`
	Filename    string     `json:"filename"`
	LibraryID   string     `json:"library_id"`
	DurationSec *float64   `json:"duration_sec"`
	State       string     `json:"state"`
	CreatedAt   time.Time  `json:"created_at"`
	PublishedAt *time.Time `json:"published_at"` // publishedAt
	// The caller's own playback, on the rows that read it.
	PositionSec   *float64   `json:"position_sec,omitempty"`
	Completed     *bool      `json:"completed,omitempty"`
	LastWatchedAt *time.Time `json:"last_watched_at,omitempty"`
	// Everyone's plays in the window, on Most Watched.
	Plays *int64 `json:"plays,omitempty"`
	// The series, on On Deck.
	Series *SeriesName `json:"series,omitempty"`
}

// scope is the set of libraries a caller may see.
type scope struct {
	all bool
	ids map[string]bool
}

func scopeOf(p *principal.Principal) scope {
	s := scope{all: p.AccessAllLibraries, ids: map[string]bool{}}
	for _, id := range p.Libraries {
		s.ids[id] = true
	}
	return s
}

func (s scope) none() bool { return !s.all && len(s.ids) == 0 }

func (s scope) has(id string) bool { return s.all || s.ids[id] }

// cond is the predicate that keeps col within the scope, its parameters
// appended to args; "" when the caller sees every library.
func (s scope) cond(col string, args *[]any) string {
	if s.all {
		return ""
	}
	return col + " IN (" + inList(s.list(), args) + ")"
}

// libFilter keeps videos v to one library (already checked against the
// scope) when lib is set, else to the scope.
func libFilter(s scope, lib string, args *[]any) string {
	if lib == "" {
		return s.cond("v.library_id", args)
	}
	*args = append(*args, lib)
	return "v.library_id = " + pidx(len(*args))
}

func (s scope) list() []string {
	out := make([]string, 0, len(s.ids))
	for id := range s.ids {
		out = append(out, id)
	}
	return out
}

// inList renders $n placeholders for ids, appending them to args. An empty
// list renders NULL, which matches nothing.
func inList(ids []string, args *[]any) string {
	if len(ids) == 0 {
		return "NULL"
	}
	ph := make([]string, len(ids))
	for i, id := range ids {
		*args = append(*args, id)
		ph[i] = pidx(len(*args))
	}
	return strings.Join(ph, ",")
}

func pidx(n int) string { return fmt.Sprintf("$%d", n) }

func and(conds ...string) string {
	var out []string
	for _, c := range conds {
		if c != "" {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return "TRUE"
	}
	return strings.Join(out, " AND ")
}

// caller is the signed-in principal, or a 401 written.
func caller(w http.ResponseWriter, r *http.Request) *principal.Principal {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, &httperror.Error{Type: httperror.TypeForbidden, Title: "unauthorized",
			Status: http.StatusUnauthorized, Detail: "authentication required"})
		return nil
	}
	return p
}

// userID is the caller's user id, or "" for a principal with no user row
// (the admin token): such a caller has no playback and no pins.
func userID(p *principal.Principal) string {
	if _, err := uuid.Parse(p.UserID); err != nil {
		return ""
	}
	return p.UserID
}

// isSQLite tells the two databases apart where their SQL differs.
func isSQLite(db *sql.DB) bool {
	return db != nil && strings.Contains(strings.ToLower(fmt.Sprintf("%T", db.Driver())), "sqlite")
}

// ts is a timestamp parameter: native on Postgres, the columns' text on SQLite.
func (h *Handler) ts(t time.Time) any {
	if isSQLite(h.DB) {
		return t.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	return t.UTC()
}

type rowScanner interface{ Scan(...any) error }

// scanCard reads cardColumns, then extra.
func scanCard(rs rowScanner, extra ...any) (Card, error) {
	var c Card
	var created, published nullTime
	dest := append([]any{&c.ID, &c.Title, &c.Filename, &c.LibraryID, &c.DurationSec, &c.State, &created, &published}, extra...)
	if err := rs.Scan(dest...); err != nil {
		return Card{}, err
	}
	c.CreatedAt = created.T
	c.PublishedAt = published.ptr()
	return c, nil
}

func (h *Handler) cards(ctx context.Context, q string, args []any) ([]Card, error) {
	rows, err := h.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Card{}
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// recentlyAdded is the newest visible videos of the scope (or of one
// library when lib is set): created_at, then mtime, newest first.
func (h *Handler) recentlyAdded(ctx context.Context, s scope, lib string, n int) ([]Card, error) {
	var args []any
	libCond := libFilter(s, lib, &args)
	args = append(args, n)
	return h.cards(ctx, `SELECT `+cardColumns+` FROM videos v WHERE `+and(visible, libCond)+`
		ORDER BY v.created_at DESC, v.mtime DESC, v.id DESC LIMIT `+pidx(len(args)), args)
}

// recentPerLibrary is up to n of the newest visible videos of each library
// in libs, in one query.
func (h *Handler) recentPerLibrary(ctx context.Context, libs []string, n int) (map[string][]Card, error) {
	out := map[string][]Card{}
	if len(libs) == 0 {
		return out, nil
	}
	var args []any
	in := inList(libs, &args)
	args = append(args, n)
	var q string
	if isSQLite(h.DB) {
		q = `SELECT id, title, filename, library_id, duration_sec, state, created_at, published_at FROM (
			SELECT ` + cardColumns + `, ROW_NUMBER() OVER (PARTITION BY v.library_id
				ORDER BY v.created_at DESC, v.mtime DESC, v.id DESC) AS rn
			FROM videos v WHERE v.library_id IN (` + in + `) AND ` + visible + `) x
			WHERE rn <= ` + pidx(len(args)) + ` ORDER BY library_id, rn`
	} else {
		// A LATERAL top-n per library reads only the first rows of each
		// library from videos_library_created_idx.
		q = `SELECT ` + cardColumns + ` FROM libraries l CROSS JOIN LATERAL (
			SELECT * FROM videos v WHERE v.library_id = l.id AND ` + visible + `
			ORDER BY v.created_at DESC, v.mtime DESC, v.id DESC LIMIT ` + pidx(len(args)) + `) v
			WHERE l.id IN (` + in + `) ORDER BY l.id, v.created_at DESC, v.mtime DESC, v.id DESC`
	}
	cs, err := h.cards(ctx, q, args)
	if err != nil {
		return nil, err
	}
	for _, c := range cs {
		out[c.LibraryID] = append(out[c.LibraryID], c)
	}
	return out, nil
}

// libraryRows is up to n of the newest visible videos of each library in
// libs (already checked against the caller's scope): from Rows when set.
// The cards are shared: read them, never change them.
func (h *Handler) libraryRows(ctx context.Context, libs []string, n int) (map[string][]Card, error) {
	if h.Rows == nil || len(libs) == 0 {
		return h.recentPerLibrary(ctx, libs, n)
	}
	return h.Rows.rows(ctx, libs, n, h.recentPerLibrary)
}

// continueWatching is the caller's resumable videos, newest first: not
// completed, past 30 s and before 95 % of the duration (a video with no
// known duration counts once past 30 s).
func (h *Handler) continueWatching(ctx context.Context, uid string, s scope, lib string, n int) ([]Card, error) {
	if uid == "" {
		return []Card{}, nil
	}
	args := []any{uid}
	libCond := libFilter(s, lib, &args)
	args = append(args, n)
	rows, err := h.DB.QueryContext(ctx, `SELECT `+cardColumns+`, ps.position_sec, ps.completed, ps.updated_at
		FROM playback_state ps JOIN videos v ON v.id = ps.video_id
		WHERE ps.user_id = $1 AND NOT ps.completed AND ps.position_sec > 30
		  AND (v.duration_sec IS NULL OR v.duration_sec <= 0 OR ps.position_sec < 0.95 * v.duration_sec)
		  AND `+and(visible, libCond)+`
		ORDER BY ps.updated_at DESC, v.id DESC LIMIT `+pidx(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Card{}
	for rows.Next() {
		var pos float64
		var done bool
		var at nullTime
		c, err := scanCard(rows, &pos, &done, &at)
		if err != nil {
			return nil, err
		}
		c.PositionSec, c.Completed, c.LastWatchedAt = &pos, &done, at.ptr()
		out = append(out, c)
	}
	return out, rows.Err()
}

// recentlyWatched is the videos the caller played (playback_state and
// watch_sessions), one card each, by the latest of the two, newest first.
func (h *Handler) recentlyWatched(ctx context.Context, uid string, s scope, lib string, n int) ([]Card, error) {
	if uid == "" {
		return []Card{}, nil
	}
	args := []any{uid}
	libCond := libFilter(s, lib, &args)
	args = append(args, n)
	rows, err := h.DB.QueryContext(ctx, `SELECT `+cardColumns+`, w.last_at, ps.position_sec, ps.completed
		FROM (SELECT video_id, MAX(at) AS last_at FROM (
		        SELECT video_id, updated_at AS at FROM playback_state WHERE user_id = $1
		        UNION ALL
		        SELECT video_id, started_at AS at FROM watch_sessions WHERE user_id = $1
		      ) t GROUP BY video_id) w
		JOIN videos v ON v.id = w.video_id
		LEFT JOIN playback_state ps ON ps.user_id = $1 AND ps.video_id = v.id
		WHERE `+and(visible, libCond)+`
		ORDER BY w.last_at DESC, v.id DESC LIMIT `+pidx(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Card{}
	for rows.Next() {
		var at nullTime
		var pos sql.NullFloat64
		var done sql.NullBool
		c, err := scanCard(rows, &at, &pos, &done)
		if err != nil {
			return nil, err
		}
		c.LastWatchedAt = at.ptr()
		if pos.Valid {
			c.PositionSec = &pos.Float64
		}
		if done.Valid {
			c.Completed = &done.Bool
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// nullTime scans a timestamp stored natively (Postgres) or as text (SQLite).
type nullTime struct {
	T     time.Time
	Valid bool
}

func (n *nullTime) Scan(v any) error {
	n.Valid = false
	switch x := v.(type) {
	case nil:
		return nil
	case time.Time:
		n.T, n.Valid = x.UTC(), true
		return nil
	case string:
		return n.parse(x)
	case []byte:
		return n.parse(string(x))
	}
	return fmt.Errorf("nullTime: cannot scan %T", v)
}

func (n *nullTime) parse(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			n.T, n.Valid = t.UTC(), true
			return nil
		}
	}
	return fmt.Errorf("nullTime: bad timestamp %q", s)
}

func (n nullTime) ptr() *time.Time {
	if !n.Valid {
		return nil
	}
	t := n.T
	return &t
}

func internal(w http.ResponseWriter, r *http.Request, what string, err error) {
	httperror.Write(w, r, httperror.Internal(what).Wrap(err))
}

func writeJSON(w http.ResponseWriter, r *http.Request, v any) {
	common.WriteJSON(w, r, http.StatusOK, v)
}

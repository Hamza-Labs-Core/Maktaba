package browse

// Epic 45 Phase 4 (story 45.24): a library's Series and Collections tabs.
//
//	GET /api/libraries/{id}/series?sort=name|newest|recent&limit=&offset=
//	GET /api/libraries/{id}/collections?kind=all|topic|filler|standalone|curated&limit=&offset=
//	GET /api/libraries/{id}/collections/{kind}/{gid}?limit=&offset=   (kind topic | clip)
//
// Every signed-in user, for a library they may see; any other (unknown,
// malformed, not theirs) answers 404. The collections list merges what the
// pipeline groups (topic collections, "ملف"; clip groups, "مقاطع" filler and
// "منفردات" stand-alone) with the admins' collections (curated lists and
// saved filters). The automatic ones are what the Plex extension's
// plex_groups writes to Plex: every topic and every clip group of the library
// with at least one video (plex_groups skips one with none), its members in
// their stored position; neither stage marks a group as TV-only or hidden, so
// none is left out here. Only hidden videos (missing, superseded, deleted) are.

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/paginate"
)

// tabPageSize is a tab's page when the request names no limit.
const tabPageSize = 60

// The kinds of a library's collections, and the clip groups' subkinds.
const (
	KindTopic   = "topic"   // topic_collections ("ملف")
	KindClip    = "clip"    // clip_groups
	KindCurated = "curated" // collections, a list an admin keeps
	KindSmart   = "smart"   // collections, a saved filter

	SubFiller     = "filler"     // clip_groups.kind 'clips' ("مقاطع")
	SubStandalone = "standalone" // clip_groups.kind 'standalone' ("منفردات")
)

// visibleLibrary is the caller and the library {id} with its name, or the
// 401/404/500 written: one 404 for a malformed id, an unknown library and one
// the caller may not see.
func (h *Handler) visibleLibrary(w http.ResponseWriter, r *http.Request) (*principal.Principal, LibraryRef, bool) {
	p := caller(w, r)
	if p == nil {
		return nil, LibraryRef{}, false
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil || !scopeOf(p).has(id) {
		httperror.Write(w, r, httperror.NotFound("library "+id))
		return nil, LibraryRef{}, false
	}
	lib := LibraryRef{ID: id}
	err := h.DB.QueryRowContext(r.Context(), `SELECT name FROM libraries WHERE id = $1`, id).Scan(&lib.Name)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("library "+id))
		return nil, LibraryRef{}, false
	}
	if err != nil {
		internal(w, r, "load library", err)
		return nil, LibraryRef{}, false
	}
	return p, lib, true
}

// pageParams reads ?limit= (1..200, default 60) and ?offset= (0 and up).
func pageParams(r *http.Request) (limit, offset int, e *httperror.Error) {
	limit = tabPageSize
	if r.URL.Query().Get("limit") != "" {
		if limit, e = paginate.ParseLimit(r.URL.Query()); e != nil {
			return 0, 0, e
		}
	}
	if offset, e = common.QueryInt(r, "offset", 0); e != nil {
		return 0, 0, e
	}
	if offset < 0 {
		return 0, 0, httperror.InvalidQuery("offset must be 0 or more")
	}
	return limit, offset, nil
}

// nextOffset is the next page's offset, nil on the last page.
func nextOffset(offset, shown, total int) *int {
	if offset+shown >= total {
		return nil
	}
	n := offset + shown
	return &n
}

// ---- Series ----

// LibrarySeriesItem is one series card of the Series tab.
type LibrarySeriesItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// PosterPath is the series' own art, when it has some.
	PosterPath string `json:"poster_path,omitempty"`
	// PosterVideoID is its first visible episode here, as the series page orders them.
	PosterVideoID string `json:"poster_video_id,omitempty"`
	Episodes      int64  `json:"episodes"`
	// The caller's own progress: episodes finished, and started but not finished.
	Watched    int64 `json:"watched"`
	InProgress int64 `json:"in_progress"`
	// LatestAddedAt is its newest episode's; LastWatchedAt the caller's latest play of one.
	LatestAddedAt *time.Time `json:"latest_added_at"`
	LastWatchedAt *time.Time `json:"last_watched_at"`
}

// The Series tab's sorts.
const (
	seriesByName   = "name"   // by name, the default
	seriesByNewest = "newest" // the series with the newest episode first
	seriesByRecent = "recent" // the ones the caller watched most recently first, then by name
)

// LibrarySeries implements GET /api/libraries/{id}/series: the series with a
// visible episode in the library (the hub's Series row, unabridged), paged,
// each with its episodes here and the caller's progress through them.
func (h *Handler) LibrarySeries(w http.ResponseWriter, r *http.Request) {
	p, lib, ok := h.visibleLibrary(w, r)
	if !ok {
		return
	}
	sortKey := r.URL.Query().Get("sort")
	order := `sname, id`
	switch sortKey {
	case "", seriesByName:
		sortKey = seriesByName
	case seriesByNewest:
		order = `latest DESC, sname, id`
	case seriesByRecent:
		order = `CASE WHEN last_watched IS NULL THEN 1 ELSE 0 END, last_watched DESC, sname, id`
	default:
		httperror.Write(w, r, httperror.InvalidQuery("sort must be name, newest or recent"))
		return
	}
	limit, offset, e := pageParams(r)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	ctx := r.Context()
	var uid any
	if u := userID(p); u != "" {
		uid = u
	}
	// one grouped pass over the library's episodes (videos by library, then
	// series_episodes by its unique video index) joined to the caller's playback
	rows, err := h.DB.QueryContext(ctx, `SELECT id, sname, poster, episodes, watched, in_progress,
		latest, last_watched, COUNT(*) OVER () FROM (
		SELECT s.id AS id, COALESCE(NULLIF(s.name_override, ''), s.name) AS sname,
		COALESCE(s.poster_path, '') AS poster, COUNT(*) AS episodes,
		SUM(CASE WHEN ps.completed THEN 1 ELSE 0 END) AS watched,
		SUM(CASE WHEN ps.completed THEN 0 WHEN ps.position_sec > 0 THEN 1 ELSE 0 END) AS in_progress,
		MAX(v.created_at) AS latest, MAX(ps.updated_at) AS last_watched
		FROM videos v
		JOIN series_episodes se ON se.video_id = v.id
		JOIN series s ON s.id = se.series_id
		LEFT JOIN playback_state ps ON ps.video_id = v.id AND ps.user_id = $2
		WHERE v.library_id = $1 AND `+visible+`
		GROUP BY s.id, s.name, s.name_override, s.poster_path) x
		ORDER BY `+order+` LIMIT $3 OFFSET $4`, lib.ID, uid, limit, offset)
	if err != nil {
		internal(w, r, "series", err)
		return
	}
	items := []LibrarySeriesItem{}
	total := 0
	for rows.Next() {
		var it LibrarySeriesItem
		var latest, watched nullTime
		if err := rows.Scan(&it.ID, &it.Name, &it.PosterPath, &it.Episodes, &it.Watched, &it.InProgress,
			&latest, &watched, &total); err != nil {
			rows.Close()
			internal(w, r, "scan series", err)
			return
		}
		it.LatestAddedAt, it.LastWatchedAt = latest.ptr(), watched.ptr()
		// a path on the server is not a picture the browser can load
		if !strings.HasPrefix(it.PosterPath, "http") && !strings.HasPrefix(it.PosterPath, "/api/") {
			it.PosterPath = ""
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		internal(w, r, "read series", err)
		return
	}
	if offset > 0 && len(items) == 0 {
		// past the end: the window gave no row to carry the total
		if err := h.DB.QueryRowContext(ctx, `SELECT COUNT(DISTINCT se.series_id) FROM videos v
			JOIN series_episodes se ON se.video_id = v.id WHERE v.library_id = $1 AND `+visible, lib.ID).Scan(&total); err != nil {
			internal(w, r, "count series", err)
			return
		}
	}
	refs := make([]SeriesRef, len(items))
	for i, it := range items {
		refs[i] = SeriesRef{ID: it.ID}
	}
	if err := h.seriesPosters(ctx, lib.ID, refs); err != nil {
		internal(w, r, "series posters", err)
		return
	}
	for i := range items {
		items[i].PosterVideoID = refs[i].PosterVideoID
	}
	writeJSON(w, r, map[string]any{
		"library": lib, "sort": sortKey, "items": items, "total": total,
		"next": nextOffset(offset, len(items), total),
	})
}

// ---- Collections ----

// LibraryCollection is one card of the Collections tab.
type LibraryCollection struct {
	Kind string `json:"kind"` // topic | clip | curated | smart
	// Subkind is a clip group's: filler or standalone.
	Subkind string `json:"subkind,omitempty"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	// Videos is how many visible videos it holds; null for a saved filter
	// (whatever matches it).
	Videos         *int64   `json:"videos"`
	PosterVideoIDs []string `json:"poster_video_ids"`
	// Automatic: the pipeline made it (topic, clip); it is read-only.
	Automatic bool `json:"automatic"`
	// SmartQuery is a saved filter's smart_query, as stored (JSON text).
	SmartQuery *string `json:"smart_query,omitempty"`

	rank int
}

// The kind filter of GET /api/libraries/{id}/collections, and the order of
// the kinds in the list.
var collectionFilters = []string{KindTopic, SubFiller, SubStandalone, KindCurated}

func (c LibraryCollection) filter() string {
	switch {
	case c.Kind == KindClip:
		return c.Subkind
	case c.Kind == KindSmart:
		return KindCurated
	}
	return c.Kind
}

// LibraryCollections implements GET /api/libraries/{id}/collections: the
// library's topic collections, clip groups (filler, then stand-alone) and its
// admins' collections, each kind by name; ?kind= keeps one of them. counts is
// every kind's size, whatever the filter.
func (h *Handler) LibraryCollections(w http.ResponseWriter, r *http.Request) {
	p, lib, ok := h.visibleLibrary(w, r)
	if !ok {
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind == "all" {
		kind = ""
	}
	if kind != "" && !contains(collectionFilters, kind) {
		httperror.Write(w, r, httperror.InvalidQuery("kind must be all, topic, filler, standalone or curated"))
		return
	}
	limit, offset, e := pageParams(r)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	ctx := r.Context()
	all, err := h.libraryCollections(ctx, scopeOf(p), lib.ID)
	if err != nil {
		internal(w, r, "collections", err)
		return
	}
	counts := map[string]int{"all": len(all)}
	for _, f := range collectionFilters {
		counts[f] = 0
	}
	var listed []LibraryCollection
	for _, c := range all {
		counts[c.filter()]++
		if kind == "" || c.filter() == kind {
			listed = append(listed, c)
		}
	}
	total := len(listed)
	page := []LibraryCollection{}
	if offset < total {
		end := offset + limit
		if end > total {
			end = total
		}
		page = append(page, listed[offset:end]...)
	}
	if err := h.collectionPosters(ctx, scopeOf(p), lib.ID, page); err != nil {
		internal(w, r, "collection posters", err)
		return
	}
	if kind == "" {
		kind = "all"
	}
	writeJSON(w, r, map[string]any{
		"library": lib, "kind": kind, "items": page, "counts": counts, "total": total,
		"next": nextOffset(offset, len(page), total),
	})
}

// libraryCollections is every collection of the library in the list's order:
// one grouped count per source. Topic and clip groups count the library's
// visible videos (one with none is left out, as Plex gets none); a curated
// list counts its visible videos the caller may see, as GET /api/collections.
func (h *Handler) libraryCollections(ctx context.Context, s scope, lib string) ([]LibraryCollection, error) {
	args := []any{lib}
	itemScope := s.cond("cv.library_id", &args)
	curatedCount := `(SELECT COUNT(*) FROM collection_items ci JOIN videos cv ON cv.id = ci.video_id
		WHERE ci.collection_id = c.id AND ` + and(strings.ReplaceAll(visible, "v.", "cv."), itemScope) + `)`
	rows, err := h.DB.QueryContext(ctx, `SELECT g.rank, g.kind, g.sub, g.id, g.name, g.n, g.sq FROM (
		SELECT 0 AS rank, 'topic' AS kind, '' AS sub, t.id AS id, t.name AS name, COUNT(*) AS n, NULL AS sq
		FROM topic_collections t JOIN topic_items i ON i.topic_id = t.id JOIN videos v ON v.id = i.video_id
		WHERE t.library_id = $1 AND v.library_id = $1 AND `+visible+`
		GROUP BY t.id, t.name
		UNION ALL
		SELECT CASE WHEN g.kind = 'clips' THEN 1 ELSE 2 END, 'clip',
		       CASE WHEN g.kind = 'clips' THEN 'filler' ELSE 'standalone' END, g.id, g.name, COUNT(*), NULL
		FROM clip_groups g JOIN clip_group_items i ON i.group_id = g.id JOIN videos v ON v.id = i.video_id
		WHERE g.library_id = $1 AND v.library_id = $1 AND `+visible+`
		GROUP BY g.id, g.name, g.kind
		UNION ALL
		SELECT 3, CASE WHEN c.is_smart THEN 'smart' ELSE 'curated' END, '', c.id, c.name,
		       CASE WHEN c.is_smart THEN NULL ELSE `+curatedCount+` END, CAST(c.smart_query AS TEXT)
		FROM collections c WHERE c.library_id = $1 AND c.dismissed_at IS NULL
		) g ORDER BY g.rank, g.name, g.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LibraryCollection{}
	for rows.Next() {
		var c LibraryCollection
		var n sql.NullInt64
		var sq sql.NullString
		if err := rows.Scan(&c.rank, &c.Kind, &c.Subkind, &c.ID, &c.Name, &n, &sq); err != nil {
			return nil, err
		}
		if n.Valid {
			v := n.Int64
			c.Videos = &v
		}
		if sq.Valid && c.Kind == KindSmart {
			c.SmartQuery = &sq.String
		}
		c.Automatic = c.Kind == KindTopic || c.Kind == KindClip
		c.PosterVideoIDs = []string{}
		out = append(out, c)
	}
	return out, rows.Err()
}

// collectionPosters gives each card of the page its first four visible videos,
// in the collection's order: one read per source, never one per card.
func (h *Handler) collectionPosters(ctx context.Context, s scope, lib string, page []LibraryCollection) error {
	byKind := map[string][]string{}
	at := map[string]int{}
	for i, c := range page {
		if c.Kind == KindSmart {
			continue
		}
		byKind[c.Kind] = append(byKind[c.Kind], c.ID)
		at[c.ID] = i
	}
	read := func(q string, args []any) error {
		rows, err := h.DB.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var owner, vid string
			if err := rows.Scan(&owner, &vid); err != nil {
				return err
			}
			if i, ok := at[owner]; ok {
				page[i].PosterVideoIDs = append(page[i].PosterVideoIDs, vid)
			}
		}
		return rows.Err()
	}
	auto := func(ids []string, items, owner string) error {
		if len(ids) == 0 {
			return nil
		}
		args := []any{lib}
		in := inList(ids, &args)
		return read(`SELECT owner, video_id FROM (
			SELECT i.`+owner+` AS owner, i.video_id, ROW_NUMBER() OVER (PARTITION BY i.`+owner+` ORDER BY i.position, i.video_id) AS n
			FROM `+items+` i JOIN videos v ON v.id = i.video_id
			WHERE i.`+owner+` IN (`+in+`) AND v.library_id = $1 AND `+visible+`) firsts
			WHERE n <= 4 ORDER BY owner, n`, args)
	}
	if err := auto(byKind[KindTopic], "topic_items", "topic_id"); err != nil {
		return err
	}
	if err := auto(byKind[KindClip], "clip_group_items", "group_id"); err != nil {
		return err
	}
	if ids := byKind[KindCurated]; len(ids) > 0 {
		var args []any
		in := inList(ids, &args)
		cond := and(visible, s.cond("v.library_id", &args))
		return read(`SELECT owner, video_id FROM (
			SELECT ci.collection_id AS owner, ci.video_id, ROW_NUMBER() OVER (PARTITION BY ci.collection_id
				ORDER BY CASE WHEN c.hand_ordered THEN ci.position END, v.created_at DESC, v.id DESC) AS n
			FROM collection_items ci JOIN collections c ON c.id = ci.collection_id JOIN videos v ON v.id = ci.video_id
			WHERE ci.collection_id IN (`+in+`) AND `+cond+`) firsts
			WHERE n <= 4 ORDER BY owner, n`, args)
	}
	return nil
}

// AutoCollectionRef is the head of a topic collection's or clip group's page.
type AutoCollectionRef struct {
	Kind      string     `json:"kind"` // topic | clip
	Subkind   string     `json:"subkind,omitempty"`
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Library   LibraryRef `json:"library"`
	Videos    int64      `json:"videos"`
	Automatic bool       `json:"automatic"`
}

// AutoCollection implements GET /api/libraries/{id}/collections/{kind}/{gid}:
// a topic collection's or clip group's visible videos in their stored order
// (the order Plex gets), paged, each with the caller's playback. Read-only:
// the pipeline rebuilds them. A group of another library, an unknown kind or
// id answers 404.
func (h *Handler) AutoCollection(w http.ResponseWriter, r *http.Request) {
	p, lib, ok := h.visibleLibrary(w, r)
	if !ok {
		return
	}
	kind, gid := chi.URLParam(r, "kind"), chi.URLParam(r, "gid")
	var head, items, owner string
	switch kind {
	case KindTopic:
		head = `SELECT name, '' FROM topic_collections WHERE id = $1 AND library_id = $2`
		items, owner = "topic_items", "topic_id"
	case KindClip:
		head = `SELECT name, CASE WHEN kind = 'clips' THEN 'filler' ELSE 'standalone' END FROM clip_groups WHERE id = $1 AND library_id = $2`
		items, owner = "clip_group_items", "group_id"
	default:
		httperror.Write(w, r, httperror.NotFound("collection "+kind))
		return
	}
	if _, err := uuid.Parse(gid); err != nil {
		httperror.Write(w, r, httperror.NotFound("collection "+gid))
		return
	}
	limit, offset, e := pageParams(r)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	ctx := r.Context()
	ref := AutoCollectionRef{Kind: kind, ID: gid, Library: lib, Automatic: true}
	err := h.DB.QueryRowContext(ctx, head, gid, lib.ID).Scan(&ref.Name, &ref.Subkind)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("collection "+gid))
		return
	}
	if err != nil {
		internal(w, r, "load collection", err)
		return
	}
	member := `FROM ` + items + ` i JOIN videos v ON v.id = i.video_id
		WHERE i.` + owner + ` = $1 AND v.library_id = $2 AND ` + visible
	if err := h.DB.QueryRowContext(ctx, `SELECT COUNT(*) `+member, gid, lib.ID).Scan(&ref.Videos); err != nil {
		internal(w, r, "count collection", err)
		return
	}
	var uid any
	if u := userID(p); u != "" {
		uid = u
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT `+cardColumns+`, ps.position_sec, ps.completed, ps.updated_at
		`+strings.Replace(member, "WHERE", "LEFT JOIN playback_state ps ON ps.video_id = v.id AND ps.user_id = $3 WHERE", 1)+`
		ORDER BY i.position, v.id LIMIT $4 OFFSET $5`, gid, lib.ID, uid, limit, offset)
	if err != nil {
		internal(w, r, "collection videos", err)
		return
	}
	defer rows.Close()
	cards := []Card{}
	for rows.Next() {
		var pos sql.NullFloat64
		var done sql.NullBool
		var at nullTime
		c, err := scanCard(rows, &pos, &done, &at)
		if err != nil {
			internal(w, r, "scan video", err)
			return
		}
		if pos.Valid {
			c.PositionSec = &pos.Float64
		}
		if done.Valid {
			c.Completed = &done.Bool
		}
		c.LastWatchedAt = at.ptr()
		cards = append(cards, c)
	}
	if err := rows.Err(); err != nil {
		internal(w, r, "read collection videos", err)
		return
	}
	writeJSON(w, r, map[string]any{
		"collection": ref, "items": cards, "total": ref.Videos,
		"next": nextOffset(offset, len(cards), int(ref.Videos)),
	})
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

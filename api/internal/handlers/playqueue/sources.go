package playqueue

// The sources a play queue reads (README §6.1 of Epic 56). Each source is read
// through the route that lists it (GET /api/videos, the series' episodes, a
// collection's items, a playlist), run in process as the caller: the queue's
// order is that route's order and its access rules are that route's, so the two
// never drift apart. A library is paged with the list's own cursor; the others
// are short enough to be read whole and paged by an offset.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/collections"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/videos"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Source kinds.
const (
	KindLibrary    = "library"
	KindSeries     = "series"
	KindSeason     = "season"
	KindCollection = "collection"
	KindPlaylist   = "playlist"
	KindVideo      = "video"
)

// Sources are the list routes a queue reads, as handler funcs (the router wires
// them; a nil one makes its kind unavailable).
type Sources struct {
	Videos          http.HandlerFunc // GET /api/videos
	Video           http.HandlerFunc // GET /api/videos/{id}
	SeriesEpisodes  http.HandlerFunc // GET /api/series/{id}/episodes
	CollectionItems http.HandlerFunc // GET /api/collections/{id}/items
	AutoCollection  http.HandlerFunc // GET /api/libraries/{id}/collections/{kind}/{gid}
	Playlist        http.HandlerFunc // GET /api/me/playlists/{id}
}

// maxListed caps a source read whole (a series, a collection, a playlist).
const maxListed = 20000

// errUnavailable is a source kind the router did not wire.
var errUnavailable = errors.New("playqueue: source not wired")

// recorder is a minimal ResponseWriter for the in-process reads.
type recorder struct {
	code int
	hdr  http.Header
	body bytes.Buffer
}

func (r *recorder) Header() http.Header {
	if r.hdr == nil {
		r.hdr = http.Header{}
	}
	return r.hdr
}
func (r *recorder) Write(b []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.body.Write(b)
}
func (r *recorder) WriteHeader(code int) { r.code = code }

// call runs one GET of h as the caller of ctx (its principal and placement),
// with the chi URL params and the query, and decodes a 200 into out. Another
// answer comes back as an *httperror.Error with the route's status and detail.
func call(ctx context.Context, h http.HandlerFunc, path string, params map[string]string, q url.Values, out any) (*httperror.Error, error) {
	if h == nil {
		return nil, errUnavailable
	}
	u := path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	req, err := http.NewRequestWithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx), http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	rec := &recorder{}
	h(rec, req)
	if rec.code == 0 {
		rec.code = http.StatusOK
	}
	if rec.code != http.StatusOK {
		var e httperror.Error
		_ = json.Unmarshal(rec.body.Bytes(), &e)
		e.Status = rec.code
		if e.Type == "" {
			e.Type = httperror.TypeBadRequest
		}
		switch rec.code {
		case http.StatusNotFound:
			return httperror.NotFound(e.Detail), nil
		case http.StatusBadRequest:
			// a filter value the list refuses: the queue's request was bad
			return httperror.Unprocessable([]httperror.FieldError{{Field: "source.filters", Message: e.Detail}}), nil
		case http.StatusUnauthorized, http.StatusForbidden:
			return &e, nil
		}
		return nil, fmt.Errorf("playqueue: %s answered %d: %s", path, rec.code, e.Detail)
	}
	if out == nil {
		return nil, nil
	}
	return nil, json.Unmarshal(rec.body.Bytes(), out)
}

// chunk is what one read of a source gave.
type chunk struct {
	ids    []string
	cursor string // where the next read starts
	done   bool   // nothing after it
}

// source is one queue's source, ready to read.
type source interface {
	// read returns up to n ids after cursor ("" is the start) in order.
	read(ctx context.Context, cursor string, n int) (chunk, *httperror.Error, error)
}

// ---- a library: GET /api/videos, with its cursor ----

type librarySource struct {
	h       http.HandlerFunc
	lib     string
	filters map[string]string
	shuffle bool
	seed    int64
}

func (s librarySource) query(cursor string, limit int) url.Values {
	q := url.Values{}
	for k, v := range s.filters {
		q.Set(k, v)
	}
	q.Set("library", s.lib)
	if s.shuffle {
		q.Set("sort", "shuffle")
		q.Set("seed", strconv.FormatInt(s.seed, 10))
		q.Del("order")
	}
	q.Set("limit", strconv.Itoa(limit))
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return q
}

func (s librarySource) read(ctx context.Context, cursor string, n int) (chunk, *httperror.Error, error) {
	out := chunk{cursor: cursor}
	for len(out.ids) < n {
		limit := n - len(out.ids)
		if limit > 200 {
			limit = 200
		}
		var page struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
			Next *string `json:"next"`
		}
		if e, err := call(ctx, s.h, "/api/videos", nil, s.query(out.cursor, limit), &page); e != nil || err != nil {
			return chunk{}, e, err
		}
		for _, it := range page.Items {
			out.ids = append(out.ids, it.ID)
		}
		if page.Next == nil {
			out.done = true
			break
		}
		out.cursor = *page.Next
	}
	return out, nil, nil
}

// ---- a list read whole, paged by an offset ----

type listSource struct{ ids []string }

func (s listSource) read(_ context.Context, cursor string, n int) (chunk, *httperror.Error, error) {
	from := 0
	if cursor != "" {
		v, err := strconv.Atoi(cursor)
		if err != nil || v < 0 {
			return chunk{}, nil, fmt.Errorf("playqueue: bad offset %q", cursor)
		}
		from = v
	}
	if from > len(s.ids) {
		from = len(s.ids)
	}
	to := from + n
	if to > len(s.ids) {
		to = len(s.ids)
	}
	return chunk{ids: append([]string(nil), s.ids[from:to]...), cursor: strconv.Itoa(to), done: to >= len(s.ids)}, nil, nil
}

// shuffled orders ids by their shuffle key for seed (videos.ShuffleKey, the
// SQL's md5(id || seed)), then id.
func shuffled(ids []string, seed int64) []string {
	out := append([]string(nil), ids...)
	keys := make(map[string]string, len(out))
	for _, id := range out {
		keys[id] = videos.ShuffleKey(id, seed)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if keys[out[i]] != keys[out[j]] {
			return keys[out[i]] < keys[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// ---- resolving a request's source ----

// SourceRef is a queue's source as the API names it.
type SourceRef struct {
	Kind    string            `json:"kind"`
	ID      string            `json:"id"`
	Filters map[string]string `json:"filters,omitempty"`
}

// resolved is a source ready to read, with where the queue starts.
type resolved struct {
	src source
	// start is the video the queue opens on ("" the first): a series' or a
	// season's next unwatched episode, the video of a video queue
	start string
	// noShuffle: the kind ignores shuffle (one video and its series)
	noShuffle bool
}

// validateRef checks a source's kind, id and filters (422 naming the field).
func validateRef(ref *SourceRef) *httperror.Error {
	bad := func(field, msg string) *httperror.Error {
		return httperror.Unprocessable([]httperror.FieldError{{Field: field, Message: msg}})
	}
	ref.ID = strings.TrimSpace(ref.ID)
	switch ref.Kind {
	case KindLibrary, KindSeries, KindSeason, KindPlaylist, KindVideo:
		if _, err := uuid.Parse(ref.ID); err != nil {
			return bad("source.id", "a "+ref.Kind+" id")
		}
	case KindCollection:
		gid := ref.ID
		if k, rest, ok := strings.Cut(ref.ID, ":"); ok {
			if k != "topic" && k != "clip" {
				return bad("source.id", "a collection id, topic:<id> or clip:<id>")
			}
			gid = rest
		}
		if _, err := uuid.Parse(gid); err != nil {
			return bad("source.id", "a collection id, topic:<id> or clip:<id>")
		}
	default:
		return bad("source.kind", "one of library, series, season, collection, playlist, video")
	}
	if len(ref.Filters) > 0 && ref.Kind != KindLibrary {
		return bad("source.filters", "only a library source takes filters")
	}
	for k, v := range ref.Filters {
		if !collections.FilterKeys[k] {
			return bad("source.filters."+k, "unknown filter "+k)
		}
		if strings.TrimSpace(v) == "" || len(v) > 200 {
			return bad("source.filters."+k, "filter "+k+" needs a value")
		}
	}
	if ref.Filters["sort"] == "random" {
		return bad("source.filters.sort", "sort=random has no order to page: use shuffle")
	}
	return nil
}

// resolve turns a source into one ready to read for the caller of ctx: 404 for
// one they cannot read (the source's own route answers).
func (h *Handler) resolve(ctx context.Context, ref SourceRef, shuffle bool, seed int64) (resolved, *httperror.Error, error) {
	switch ref.Kind {
	case KindLibrary:
		// the library's own visibility is the list's: a library the caller
		// cannot read lists nothing, so it is checked here for the 404
		if e, err := h.visibleLibrary(ctx, ref.ID); e != nil || err != nil {
			return resolved{}, e, err
		}
		return resolved{src: librarySource{h: h.Sources.Videos, lib: ref.ID, filters: ref.Filters, shuffle: shuffle, seed: seed}}, nil, nil
	case KindSeries, KindSeason:
		seriesID, season := ref.ID, -1
		if ref.Kind == KindSeason {
			err := h.DB.QueryRowContext(ctx, `SELECT series_id, season FROM series_seasons WHERE id = $1`, ref.ID).Scan(&seriesID, &season)
			if errors.Is(err, sql.ErrNoRows) {
				return resolved{}, httperror.NotFound("season " + ref.ID), nil
			}
			if err != nil {
				return resolved{}, nil, err
			}
		}
		eps, next, e, err := h.episodes(ctx, seriesID, season)
		if e != nil || err != nil {
			return resolved{}, e, err
		}
		out := resolved{src: listSource{ids: eps}, start: next}
		if shuffle {
			out = resolved{src: listSource{ids: shuffled(eps, seed)}}
		}
		return out, nil, nil
	case KindCollection:
		ids, e, err := h.collectionIDs(ctx, ref.ID)
		if e != nil || err != nil {
			return resolved{}, e, err
		}
		if shuffle {
			ids = shuffled(ids, seed)
		}
		return resolved{src: listSource{ids: ids}}, nil, nil
	case KindPlaylist:
		ids, e, err := h.playlistIDs(ctx, ref.ID)
		if e != nil || err != nil {
			return resolved{}, e, err
		}
		if shuffle {
			ids = shuffled(ids, seed)
		}
		return resolved{src: listSource{ids: ids}}, nil, nil
	case KindVideo:
		if e, err := call(ctx, h.Sources.Video, "/api/videos/"+ref.ID, map[string]string{"id": ref.ID}, nil, nil); e != nil || err != nil {
			return resolved{}, e, err
		}
		var seriesID string
		err := h.DB.QueryRowContext(ctx, `SELECT CAST(series_id AS TEXT) FROM series_episodes WHERE video_id = $1 ORDER BY series_id LIMIT 1`, ref.ID).Scan(&seriesID)
		if errors.Is(err, sql.ErrNoRows) {
			return resolved{src: listSource{ids: []string{ref.ID}}, start: ref.ID, noShuffle: true}, nil, nil
		}
		if err != nil {
			return resolved{}, nil, err
		}
		eps, _, e, err := h.episodes(ctx, seriesID, -1)
		if e != nil || err != nil {
			return resolved{}, e, err
		}
		if !contains(eps, ref.ID) {
			eps = []string{ref.ID}
		}
		return resolved{src: listSource{ids: eps}, start: ref.ID, noShuffle: true}, nil, nil
	}
	return resolved{}, httperror.Unprocessable([]httperror.FieldError{{Field: "source.kind", Message: "unknown kind"}}), nil
}

// visibleLibrary is 404 unless the caller may read the library.
func (h *Handler) visibleLibrary(ctx context.Context, lib string) (*httperror.Error, error) {
	p := principalOf(ctx)
	var one int
	err := h.DB.QueryRowContext(ctx, `SELECT 1 FROM libraries WHERE id = $1`, lib).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !p.AccessAllLibraries && !p.HasLibrary(lib)) {
		return httperror.NotFound("library " + lib), nil
	}
	return nil, err
}

// episodes is a series' episodes in the lesson order (season, then episode),
// only season's when season >= 0, and the first unwatched of them.
func (h *Handler) episodes(ctx context.Context, seriesID string, season int) ([]string, string, *httperror.Error, error) {
	var doc struct {
		Seasons []struct {
			Season   int `json:"season"`
			Episodes []struct {
				VideoID string `json:"video_id"`
				Watched bool   `json:"watched"`
			} `json:"episodes"`
		} `json:"seasons"`
	}
	e, err := call(ctx, h.Sources.SeriesEpisodes, "/api/series/"+seriesID+"/episodes",
		map[string]string{"id": seriesID}, url.Values{"sort": {"episode"}}, &doc)
	if e != nil || err != nil {
		return nil, "", e, err
	}
	var ids []string
	next := ""
	for _, s := range doc.Seasons {
		if season >= 0 && s.Season != season {
			continue
		}
		for _, ep := range s.Episodes {
			ids = append(ids, ep.VideoID)
			if next == "" && !ep.Watched {
				next = ep.VideoID
			}
		}
	}
	return ids, next, nil, nil
}

// collectionIDs is a collection's items in its order: an admin's collection
// (GET /api/collections/{id}/items, smart ones evaluated) or the pipeline's
// topic: and clip: groups (GET /api/libraries/{lib}/collections/{kind}/{gid}).
func (h *Handler) collectionIDs(ctx context.Context, id string) ([]string, *httperror.Error, error) {
	if kind, gid, ok := strings.Cut(id, ":"); ok {
		table := "topic_collections"
		if kind == "clip" {
			table = "clip_groups"
		}
		var lib string
		err := h.DB.QueryRowContext(ctx, `SELECT CAST(library_id AS TEXT) FROM `+table+` WHERE id = $1`, gid).Scan(&lib)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, httperror.NotFound("collection " + id), nil
		}
		if err != nil {
			return nil, nil, err
		}
		var ids []string
		offset := 0
		for len(ids) < maxListed {
			var page struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
				Next *int `json:"next"`
			}
			e, err := call(ctx, h.Sources.AutoCollection, "/api/libraries/"+lib+"/collections/"+kind+"/"+gid,
				map[string]string{"id": lib, "kind": kind, "gid": gid},
				url.Values{"limit": {"200"}, "offset": {strconv.Itoa(offset)}}, &page)
			if e != nil || err != nil {
				return nil, e, err
			}
			for _, it := range page.Items {
				ids = append(ids, it.ID)
			}
			if page.Next == nil {
				break
			}
			offset = *page.Next
		}
		return ids, nil, nil
	}
	var ids []string
	q := url.Values{"limit": {"200"}}
	for len(ids) < maxListed {
		var page struct {
			Items []struct {
				ID      string `json:"id"`
				VideoID string `json:"video_id"`
			} `json:"items"`
			Next       *string `json:"next"`
			NextCursor *int    `json:"next_cursor"`
		}
		e, err := call(ctx, h.Sources.CollectionItems, "/api/collections/"+id+"/items", map[string]string{"id": id}, q, &page)
		if e != nil || err != nil {
			return nil, e, err
		}
		for _, it := range page.Items {
			if it.VideoID != "" {
				ids = append(ids, it.VideoID)
			} else {
				ids = append(ids, it.ID)
			}
		}
		switch {
		case page.Next != nil:
			q.Set("cursor", *page.Next)
		case page.NextCursor != nil:
			q.Set("cursor", strconv.Itoa(*page.NextCursor))
		default:
			return ids, nil, nil
		}
	}
	return ids, nil, nil
}

// playlistIDs is the caller's playlist's items in its order (a smart one
// evaluated): another user's is 404.
func (h *Handler) playlistIDs(ctx context.Context, id string) ([]string, *httperror.Error, error) {
	var doc struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	e, err := call(ctx, h.Sources.Playlist, "/api/me/playlists/"+id, map[string]string{"id": id}, nil, &doc)
	if e != nil || err != nil {
		return nil, e, err
	}
	ids := make([]string, 0, len(doc.Items))
	for _, it := range doc.Items {
		ids = append(ids, it.ID)
	}
	return ids, nil, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

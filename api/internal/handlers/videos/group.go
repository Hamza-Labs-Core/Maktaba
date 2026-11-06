package videos

// Epic 56 stories 56.04 and 56.08 on the video list.
//
// GET /api/videos?group=series lists, with the same filters, one card per
// series that has a matching video, and each matching video in no series as
// itself: sorted by name, newest (the newest matching episode, or the video's
// add) or recent (the caller's last watch), paged by an opaque cursor. Each
// series card carries its episode counts, how many match, how many the caller
// has not finished and the next one to watch in the lesson order.
//
// A library's collection mode (libraries.collection_mode, slot 0186) shapes
// the grid when only the sort is set: hide_items shows a collection's card in
// place of its videos, show_both shows the cards beside them. An admin's
// collections and Plex's take part; YouTube's never replace videos (a channel
// would hide most of a library).

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

const (
	groupSeries = "series"
	groupName   = "name"
	groupNewest = "newest"
	groupRecent = "recent"
)

var groupSorts = map[string]bool{groupName: true, groupNewest: true, groupRecent: true}

// GroupedItem is one card of a group=series list: a series or a loose video.
type GroupedItem struct {
	Kind string `json:"kind"` // series | video
	ID   string `json:"id"`
	// a series
	Name           string       `json:"name,omitempty"`
	PosterVideoID  string       `json:"poster_video_id,omitempty"`
	EpisodeCount   int          `json:"episode_count,omitempty"`
	MatchingCount  int          `json:"matching_count,omitempty"`
	UnwatchedCount *int         `json:"unwatched_count,omitempty"`
	NextEpisode    *NextEpisode `json:"next_episode,omitempty"`
	// a video
	Title         *string        `json:"title,omitempty"`
	Filename      string         `json:"filename,omitempty"`
	LibraryID     string         `json:"library_id,omitempty"`
	DurationSec   *float64       `json:"duration_sec,omitempty"`
	PlaybackState *PlaybackState `json:"playback_state,omitempty"`
}

// NextEpisode is a series card's next episode to watch.
type NextEpisode struct {
	VideoID string  `json:"video_id"`
	Title   *string `json:"title"`
}

// groupedRow is a card before it is paged and filled.
type groupedRow struct {
	kind, id, name string
	key            string // the sort's key, as text
	matching       int
}

// listGrouped answers group=series over the filtered videos (conds and args
// are the list's filters and scope, on videos).
func (h *Handler) listGrouped(w http.ResponseWriter, r *http.Request, p *principal.Principal, conds []string, args []any, gsort, cursor string, limit int) {
	offset := 0
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		var c struct {
			G string `json:"g"`
			O int    `json:"o"`
		}
		if err != nil || json.Unmarshal(raw, &c) != nil || c.G != gsort || c.O < 0 {
			httperror.Write(w, r, invalidCursor("malformed cursor for group=series"))
			return
		}
		offset = c.O
	}
	ctx := r.Context()
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	uid := p.UserID
	if _, err := uuid.Parse(uid); err != nil {
		uid = uuid.Nil.String()
	}
	args = append(append([]any(nil), args...), uid)
	u := pidx(len(args))
	matched := "SELECT id, COALESCE(title, filename) AS t, created_at FROM videos" + where
	lastWatched := "(SELECT MAX(ps.updated_at) FROM playback_state ps WHERE ps.user_id = " + u + " AND ps.video_id = m.id)"
	rows, err := h.DB.QueryContext(ctx, `WITH m AS (`+matched+`)
		SELECT 'series', CAST(s.id AS TEXT), COALESCE(s.name_override, s.name), COUNT(DISTINCT m.id), MAX(m.created_at), MAX(`+lastWatched+`)
		FROM m JOIN series_episodes se ON se.video_id = m.id JOIN series s ON s.id = se.series_id
		GROUP BY s.id, s.name, s.name_override
		UNION ALL
		SELECT 'video', CAST(m.id AS TEXT), m.t, 1, m.created_at, `+lastWatched+`
		FROM m WHERE NOT EXISTS (SELECT 1 FROM series_episodes se WHERE se.video_id = m.id)`, args...)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("group by series").Wrap(err))
		return
	}
	var all []groupedRow
	for rows.Next() {
		var g groupedRow
		var newest, watched nullTime
		var name sql.NullString
		if err := rows.Scan(&g.kind, &g.id, &name, &g.matching, &newest, &watched); err != nil {
			rows.Close()
			httperror.Write(w, r, httperror.Internal("scan group").Wrap(err))
			return
		}
		g.name = name.String
		switch gsort {
		case groupName:
			g.key = strings.ToLower(g.name)
		case groupNewest:
			g.key = newest.T.UTC().Format("2006-01-02T15:04:05.000000000Z")
		case groupRecent:
			if !watched.T.IsZero() {
				g.key = watched.T.UTC().Format("2006-01-02T15:04:05.000000000Z")
			}
		}
		all = append(all, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		httperror.Write(w, r, httperror.Internal("read groups").Wrap(err))
		return
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.key != b.key {
			if gsort == groupName {
				return a.key < b.key
			}
			// newest and recent: the latest first, never-watched last
			if a.key == "" || b.key == "" {
				return b.key == ""
			}
			return a.key > b.key
		}
		return a.id < b.id
	})
	if offset > len(all) {
		offset = len(all)
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	page := all[offset:end]
	items, err := h.fillGrouped(ctx, p, uid, page)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("series cards").Wrap(err))
		return
	}
	var next *string
	if end < len(all) {
		raw, _ := json.Marshal(map[string]any{"g": gsort, "o": end})
		s := base64.RawURLEncoding.EncodeToString(raw)
		next = &s
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items, "next": next, "total": len(all)})
}

// fillGrouped turns a page of rows into cards: a series' episode counts and
// next episode (the lesson order), a video's fields and the caller's playback.
func (h *Handler) fillGrouped(ctx context.Context, p *principal.Principal, uid string, page []groupedRow) ([]GroupedItem, error) {
	out := make([]GroupedItem, 0, len(page))
	var seriesIDs, videoIDs []string
	for _, g := range page {
		if g.kind == "series" {
			seriesIDs = append(seriesIDs, g.id)
		} else {
			videoIDs = append(videoIDs, g.id)
		}
	}
	type ep struct {
		vid      string
		title    *string
		lib      string
		done     bool
		started  bool
		ord1     int
		ord2     int
		absolute sql.NullInt64
	}
	eps := map[string][]ep{}
	if len(seriesIDs) > 0 {
		args := []any{uid}
		ph := make([]string, len(seriesIDs))
		for i, id := range seriesIDs {
			args = append(args, id)
			ph[i] = pidx(len(args))
		}
		rows, err := h.DB.QueryContext(ctx, `SELECT CAST(se.series_id AS TEXT), CAST(v.id AS TEXT), v.title, CAST(v.library_id AS TEXT),
				COALESCE(se.season_override, se.season, 0), COALESCE(se.episode_override, se.episode, 0), se.absolute_number,
				ps.completed, ps.position_sec
			FROM series_episodes se JOIN videos v ON v.id = se.video_id
			LEFT JOIN playback_state ps ON ps.video_id = v.id AND ps.user_id = $1
			WHERE se.series_id IN (`+strings.Join(ph, ",")+`) AND v.deleted_at IS NULL AND v.state NOT IN ('missing', 'superseded')`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var sid string
			var e ep
			var done sql.NullBool
			var pos sql.NullFloat64
			if err := rows.Scan(&sid, &e.vid, &e.title, &e.lib, &e.ord1, &e.ord2, &e.absolute, &done, &pos); err != nil {
				rows.Close()
				return nil, err
			}
			if !p.AccessAllLibraries && !p.HasLibrary(e.lib) {
				continue
			}
			e.done = done.Valid && done.Bool
			e.started = pos.Valid && pos.Float64 > 0
			eps[sid] = append(eps[sid], e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for sid := range eps {
			list := eps[sid]
			sort.SliceStable(list, func(i, j int) bool {
				if list[i].ord1 != list[j].ord1 {
					return list[i].ord1 < list[j].ord1
				}
				if list[i].ord2 != list[j].ord2 {
					return list[i].ord2 < list[j].ord2
				}
				return list[i].vid < list[j].vid
			})
		}
	}
	videos := map[string]GroupedItem{}
	if len(videoIDs) > 0 {
		args := []any{uid}
		ph := make([]string, len(videoIDs))
		for i, id := range videoIDs {
			args = append(args, id)
			ph[i] = pidx(len(args))
		}
		rows, err := h.DB.QueryContext(ctx, `SELECT CAST(v.id AS TEXT), v.title, v.filename, CAST(v.library_id AS TEXT), v.duration_sec,
				ps.position_sec, ps.completed, ps.updated_at
			FROM videos v LEFT JOIN playback_state ps ON ps.video_id = v.id AND ps.user_id = $1
			WHERE v.id IN (`+strings.Join(ph, ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var it GroupedItem
			var pos sql.NullFloat64
			var done sql.NullBool
			var at nullTime
			if err := rows.Scan(&it.ID, &it.Title, &it.Filename, &it.LibraryID, &it.DurationSec, &pos, &done, &at); err != nil {
				rows.Close()
				return nil, err
			}
			if pos.Valid || done.Valid {
				it.PlaybackState = &PlaybackState{PositionSec: pos.Float64, Completed: done.Valid && done.Bool, UpdatedAt: at.T}
			}
			it.Kind = "video"
			videos[it.ID] = it
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	for _, g := range page {
		if g.kind == "video" {
			if it, ok := videos[g.id]; ok {
				out = append(out, it)
			}
			continue
		}
		it := GroupedItem{Kind: "series", ID: g.id, Name: g.name, MatchingCount: g.matching}
		list := eps[g.id]
		it.EpisodeCount = len(list)
		unwatched := 0
		for _, e := range list {
			if !e.done {
				unwatched++
				if it.NextEpisode == nil {
					it.NextEpisode = &NextEpisode{VideoID: e.vid, Title: e.title}
				}
			}
		}
		it.UnwatchedCount = &unwatched
		if len(list) > 0 {
			it.PosterVideoID = list[0].vid
		}
		out = append(out, it)
	}
	return out, nil
}

// gridFilters are the list parameters that make a filtered view (anything
// but the sort, the order, the page and the library).
var gridFilters = []string{"q", "language", "year", "watched", "series", "tag", "speaker", "letter", "duration", "added", "resolution", "favorite"}

func hasGridFilter(q url.Values) bool {
	for _, k := range gridFilters {
		if q.Get(k) != "" {
			return true
		}
	}
	return false
}

// modeCollectionCond keeps the collections c that take part in a library's
// collection mode: an admin's manual ones and Plex's, shown, not dismissed.
const modeCollectionCond = `NOT c.is_smart AND (c.source_kind IS NULL OR c.source_kind = 'plex') AND c.hidden_at IS NULL AND c.dismissed_at IS NULL`

// collectionMode is a library's collection_mode (tab_only when unknown).
func (h *Handler) collectionMode(ctx context.Context, lib string) (string, error) {
	if _, err := uuid.Parse(lib); err != nil {
		return "tab_only", nil
	}
	var mode string
	err := h.DB.QueryRowContext(ctx, `SELECT collection_mode FROM libraries WHERE id = $1`, lib).Scan(&mode)
	if err == sql.ErrNoRows {
		return "tab_only", nil
	}
	return mode, err
}

// CollectionCard is a collection's card in a library grid under its mode.
type CollectionCard struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	SourceKind     string   `json:"source_kind,omitempty"`
	Videos         int      `json:"videos"`
	PosterVideoIDs []string `json:"poster_video_ids"`
}

// modeCollections is the library's collections that take part in its mode,
// each with the caller's visible videos (a collection with none is left out).
func (h *Handler) modeCollections(ctx context.Context, p *principal.Principal, lib string) ([]CollectionCard, error) {
	args := []any{lib}
	scope := ""
	if !p.AccessAllLibraries {
		ph := make([]string, 0, len(p.Libraries))
		for _, l := range p.Libraries {
			args = append(args, l)
			ph = append(ph, pidx(len(args)))
		}
		if len(ph) == 0 {
			return []CollectionCard{}, nil
		}
		scope = " AND v.library_id IN (" + strings.Join(ph, ",") + ")"
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT CAST(c.id AS TEXT), COALESCE(c.name_override, c.name), COALESCE(c.source_kind, ''), CAST(v.id AS TEXT)
		FROM collections c JOIN collection_items ci ON ci.collection_id = c.id JOIN videos v ON v.id = ci.video_id
		WHERE c.library_id = $1 AND `+modeCollectionCond+` AND v.deleted_at IS NULL AND v.state NOT IN ('missing', 'superseded')`+scope+`
		ORDER BY COALESCE(c.sort_title, c.name_override, c.name), c.id,
			CASE WHEN c.hand_ordered THEN ci.position END, v.created_at DESC, v.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CollectionCard{}
	at := map[string]int{}
	for rows.Next() {
		var id, name, sk, vid string
		if err := rows.Scan(&id, &name, &sk, &vid); err != nil {
			return nil, err
		}
		i, ok := at[id]
		if !ok {
			at[id] = len(out)
			i = len(out)
			out = append(out, CollectionCard{ID: id, Name: name, SourceKind: sk, PosterVideoIDs: []string{}})
		}
		out[i].Videos++
		if len(out[i].PosterVideoIDs) < 4 {
			out[i].PosterVideoIDs = append(out[i].PosterVideoIDs, vid)
		}
	}
	return out, rows.Err()
}

package browse

// Epic 45 Phase 2, story 45.15: On Deck, Plex's row of what comes next. For
// each series the caller has finished a lesson of, the lesson after the last
// one finished (in the series' order), newest activity first. A series whose
// next lesson is under way is left out (Continue Watching shows it), as is one
// finished to the end. Lessons the caller may not see, and missing ones, are
// skipped as if they were not in the series.

import (
	"context"
	"database/sql"
	"sort"
)

// onDeckSeries caps the series read for the row.
const onDeckSeries = 50

type deckEp struct {
	seriesID, seriesName, numbering string
	videoID, libraryID              string
	season, episode, absolute       sql.NullInt64
	seasonOvr, episodeOvr           sql.NullInt64
	shown                           bool
	position                        sql.NullFloat64
	completed                       sql.NullBool
}

func (e deckEp) effSeason() int64 {
	if e.seasonOvr.Valid {
		return e.seasonOvr.Int64
	}
	return e.season.Int64
}

func (e deckEp) effEpisode() int64 {
	if e.episodeOvr.Valid {
		return e.episodeOvr.Int64
	}
	return e.episode.Int64
}

func (e deckEp) absOrd() int64 {
	if e.absolute.Valid {
		return e.absolute.Int64
	}
	return e.effEpisode()
}

// lessDeck orders a series' lessons as the series page does
// (series.lessEpisode): by absolute number, or by season then episode; ties
// by video id so the order is stable.
func lessDeck(a, b deckEp) bool {
	if a.numbering == "absolute" {
		if a.absOrd() != b.absOrd() {
			return a.absOrd() < b.absOrd()
		}
	} else {
		if a.effSeason() != b.effSeason() {
			return a.effSeason() < b.effSeason()
		}
		if a.effEpisode() != b.effEpisode() {
			return a.effEpisode() < b.effEpisode()
		}
	}
	return a.videoID < b.videoID
}

// onDeck is the row: up to n cards, each with its series. lib keeps it to
// one library's lessons.
func (h *Handler) onDeck(ctx context.Context, uid string, s scope, lib string, n int) ([]Card, error) {
	out := []Card{}
	if uid == "" || s.none() {
		return out, nil
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT se.series_id, MAX(ps.updated_at) AS at
		FROM playback_state ps JOIN series_episodes se ON se.video_id = ps.video_id
		WHERE ps.user_id = $1 AND ps.completed
		GROUP BY se.series_id ORDER BY at DESC, se.series_id LIMIT $2`, uid, onDeckSeries)
	if err != nil {
		return nil, err
	}
	var order []string
	for rows.Next() {
		var sid string
		var at nullTime
		if err := rows.Scan(&sid, &at); err != nil {
			rows.Close()
			return nil, err
		}
		order = append(order, sid)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(order) == 0 {
		return out, err
	}

	args := []any{uid}
	in := inList(order, &args)
	erows, err := h.DB.QueryContext(ctx, `SELECT se.series_id, COALESCE(s.name_override, s.name), s.numbering,
			se.video_id, v.library_id, se.season, se.episode, se.absolute_number, se.season_override, se.episode_override,
			CASE WHEN `+visible+` THEN 1 ELSE 0 END, ps.position_sec, ps.completed
		FROM series_episodes se JOIN series s ON s.id = se.series_id
		JOIN videos v ON v.id = se.video_id
		LEFT JOIN playback_state ps ON ps.user_id = $1 AND ps.video_id = se.video_id
		WHERE se.series_id IN (`+in+`)`, args...)
	if err != nil {
		return nil, err
	}
	bySeries := map[string][]deckEp{}
	for erows.Next() {
		var e deckEp
		var numbering sql.NullString
		var shown int
		if err := erows.Scan(&e.seriesID, &e.seriesName, &numbering, &e.videoID, &e.libraryID,
			&e.season, &e.episode, &e.absolute, &e.seasonOvr, &e.episodeOvr, &shown, &e.position, &e.completed); err != nil {
			erows.Close()
			return nil, err
		}
		if !s.has(e.libraryID) {
			continue // another library's lesson: not in the series for this caller
		}
		e.numbering, e.shown = numbering.String, shown == 1
		bySeries[e.seriesID] = append(bySeries[e.seriesID], e)
	}
	erows.Close()
	if err := erows.Err(); err != nil {
		return nil, err
	}

	type pick struct{ ep deckEp }
	var picks []pick
	for _, sid := range order {
		eps := bySeries[sid]
		sort.SliceStable(eps, func(i, j int) bool { return lessDeck(eps[i], eps[j]) })
		last := -1
		for i, e := range eps {
			if e.shown && e.completed.Valid && e.completed.Bool {
				last = i
			}
		}
		if last < 0 {
			continue
		}
		for _, e := range eps[last+1:] {
			if !e.shown || (e.completed.Valid && e.completed.Bool) {
				continue
			}
			if e.position.Valid && e.position.Float64 > 30 {
				break // under way: Continue Watching has it
			}
			if lib == "" || e.libraryID == lib {
				picks = append(picks, pick{e})
			}
			break
		}
		if len(picks) >= n {
			break
		}
	}
	if len(picks) == 0 {
		return out, nil
	}
	ids := make([]string, len(picks))
	for i, p := range picks {
		ids[i] = p.ep.videoID
	}
	var cargs []any
	cin := inList(ids, &cargs)
	cards, err := h.cards(ctx, `SELECT `+cardColumns+` FROM videos v WHERE v.id IN (`+cin+`)`, cargs)
	if err != nil {
		return nil, err
	}
	byID := map[string]Card{}
	for _, c := range cards {
		byID[c.ID] = c
	}
	for _, p := range picks {
		c, ok := byID[p.ep.videoID]
		if !ok {
			continue
		}
		c.Series = &SeriesName{ID: p.ep.seriesID, Name: p.ep.seriesName}
		if p.ep.position.Valid {
			pos := p.ep.position.Float64
			c.PositionSec = &pos
		}
		out = append(out, c)
	}
	return out, nil
}

package browse

// Epic 45 Phase 3 (story 45.18): Recently Added grouped by series, on the
// home (`recently_added_grouped`) and a library's hub
// (`rows.recently_added_grouped`).
//
// The newest visible additions (at most groupScan) are read newest first;
// the lessons of one series become one entry where its newest one stood
// ("12 new lessons in Tafsir"), every other video stays its own. A series
// with one new lesson is a video entry that names its series. A video in
// several series counts in the first by id. `recently_added` stays as it was.

import (
	"context"
	"sort"
)

// groupScan caps the additions read to group.
const groupScan = 300

// AddedEntry is one entry of the grouped row: a video, or a series with the
// lessons added to it (Video is the newest of them; Count how many).
type AddedEntry struct {
	Kind   string      `json:"kind"` // "video" or "series"
	Video  Card        `json:"video"`
	Series *SeriesName `json:"series,omitempty"`
	Count  int         `json:"count,omitempty"`
}

// recentlyAddedScan is the scope's (or one library's) newest additions, at
// most groupScan: the grouped row reads them all, Recently Added the first
// RowSize (the same order), so the home and a hub read them once.
func (h *Handler) recentlyAddedScan(ctx context.Context, s scope, lib string) (added, scan []Card, err error) {
	scan, err = h.recentlyAdded(ctx, s, lib, groupScan)
	if err != nil {
		return nil, nil, err
	}
	return scan[:min(len(scan), RowSize)], scan, nil
}

// recentlyAddedGrouped is the additions cards (newest first, as
// recentlyAddedScan reads them) grouped by series, at most n entries.
func (h *Handler) recentlyAddedGrouped(ctx context.Context, cards []Card, n int) ([]AddedEntry, error) {
	out := []AddedEntry{}
	if len(cards) == 0 {
		return out, nil
	}
	ids := make([]string, len(cards))
	for i, c := range cards {
		ids[i] = c.ID
	}
	seriesOf, err := h.firstSeries(ctx, ids)
	if err != nil {
		return nil, err
	}
	at := map[string]int{} // series id -> its entry
	for _, c := range cards {
		sn, ok := seriesOf[c.ID]
		if !ok {
			out = append(out, AddedEntry{Kind: "video", Video: c})
			continue
		}
		if i, seen := at[sn.ID]; seen {
			out[i].Count++
			continue
		}
		at[sn.ID] = len(out)
		snc := sn
		out = append(out, AddedEntry{Kind: "series", Video: c, Series: &snc, Count: 1})
	}
	for i := range out {
		if out[i].Kind == "series" && out[i].Count == 1 {
			out[i].Kind, out[i].Count = "video", 0
		}
	}
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// firstSeries is each video's series (the first by id when it is in
// several), with its shown name.
func (h *Handler) firstSeries(ctx context.Context, videoIDs []string) (map[string]SeriesName, error) {
	out := map[string]SeriesName{}
	if len(videoIDs) == 0 {
		return out, nil
	}
	var args []any
	in := inList(videoIDs, &args)
	rows, err := h.DB.QueryContext(ctx, `SELECT se.video_id, s.id, COALESCE(s.name_override, s.name, '')
		FROM series_episodes se JOIN series s ON s.id = se.series_id
		WHERE se.video_id IN (`+in+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type pair struct {
		video string
		sn    SeriesName
	}
	var all []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.video, &p.sn.ID, &p.sn.Name); err != nil {
			return nil, err
		}
		all = append(all, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].sn.ID < all[j].sn.ID })
	for _, p := range all {
		if _, ok := out[p.video]; !ok {
			out[p.video] = p.sn
		}
	}
	return out, nil
}

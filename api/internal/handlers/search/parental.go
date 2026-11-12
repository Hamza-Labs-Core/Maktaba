package search

// The parental filter's part in search (Epic 57 story 57.03). A restricted account
// searches only what it may see:
//
//   - the title and transcript queries carry the filter clause, so a hidden video
//     takes no place in the ranking and no part in a count;
//   - the meaning search's passages are kept only for the videos that pass;
//   - a kept search (search_runs) is filtered again on every read, with the limit
//     as it is now: a search kept before a block, or before the account was
//     restricted, loses the hidden videos, and its totals are counted after;
//   - Recent searches counts each kept search the same way;
//   - the "From YouTube" block is never asked (a restricted account does not
//     search YouTube).
//
// Everything fails closed: when the check cannot be made, nothing is answered.

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// limited reports whether the request's account has a parental limit.
func limited(ctx context.Context) bool { return parental.From(ctx).Limits() }

// limitWhere is the filter as a condition on videos v for a query's WHERE list
// (nil for an account with no limit: the query is unchanged).
func limitWhere(ctx context.Context) []string {
	e := parental.From(ctx)
	if !e.Limits() {
		return nil
	}
	return []string{parental.Predicate(e, "v")}
}

// allowedIDs is the subset of video ids the request's account may see. With no
// limit every id is in it and no query runs. An id that is not a uuid is never in
// it for a restricted account.
func (h *Handler) allowedIDs(ctx context.Context, ids []string) (map[string]bool, error) {
	out := make(map[string]bool, len(ids))
	e := parental.From(ctx)
	if !e.Limits() {
		for _, id := range ids {
			out[id] = true
		}
		return out, nil
	}
	if h.DB == nil || len(ids) == 0 {
		return out, nil
	}
	seen := map[string]bool{}
	var canon []string
	for _, id := range ids {
		if u, err := uuid.Parse(id); err == nil && !seen[u.String()] {
			seen[u.String()] = true
			canon = append(canon, u.String())
		}
	}
	if len(canon) == 0 {
		return out, nil
	}
	idCol := "v.id::text"
	if isSQLite(h.DB) {
		idCol = "v.id"
	}
	ok := map[string]bool{}
	// in parts: a kept search holds up to rules.search.max_videos ids per list
	for len(canon) > 0 {
		part := canon
		if len(part) > 2000 {
			part = part[:2000]
		}
		canon = canon[len(part):]
		var args []any
		rows, err := h.DB.QueryContext(ctx, `SELECT `+idCol+` FROM videos v
			WHERE v.id IN (`+placeholders(part, &args)+`) AND v.deleted_at IS NULL AND `+parental.Predicate(e, "v"), args...)
		if err != nil {
			return map[string]bool{}, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return map[string]bool{}, err
			}
			ok[strings.ToLower(id)] = true
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return map[string]bool{}, err
		}
	}
	for _, id := range ids {
		if u, err := uuid.Parse(id); err == nil && ok[u.String()] {
			out[id] = true
		}
	}
	return out, nil
}

// allowedHits keeps the passages of the videos the request's account may see.
func (h *Handler) allowedHits(ctx context.Context, hits []Hit) ([]Hit, error) {
	if !limited(ctx) || len(hits) == 0 {
		return hits, nil
	}
	ids := make([]string, len(hits))
	for i, hit := range hits {
		ids[i] = hit.VideoID
	}
	ok, err := h.allowedIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	kept := make([]Hit, 0, len(hits))
	for _, hit := range hits {
		if ok[hit.VideoID] {
			kept = append(kept, hit)
		}
	}
	return kept, nil
}

// setIDs is every video id a kept result names.
func setIDs(s *savedSet) []string {
	ids := make([]string, 0, len(s.Titles)+len(s.Videos)+len(s.Sem))
	for _, t := range s.Titles {
		ids = append(ids, t.V)
	}
	for _, v := range s.Videos {
		ids = append(ids, v.V)
	}
	for _, hit := range s.Sem {
		ids = append(ids, hit.VideoID)
	}
	return ids
}

// keepAllowed drops from s every video ok does not hold.
func keepAllowed(s *savedSet, ok map[string]bool) {
	titles := make([]savedTitle, 0, len(s.Titles))
	for _, t := range s.Titles {
		if ok[t.V] {
			titles = append(titles, t)
		}
	}
	videos := make([]savedVideo, 0, len(s.Videos))
	for _, v := range s.Videos {
		if ok[v.V] {
			videos = append(videos, v)
		}
	}
	var sem []Hit
	for _, hit := range s.Sem {
		if ok[hit.VideoID] {
			sem = append(sem, hit)
		}
	}
	s.Titles, s.Videos, s.Sem = titles, videos, sem
}

// restrict leaves in a kept search only what the request's account may see now.
// An account with no limit costs no query.
func (h *Handler) restrict(ctx context.Context, rn *run) error {
	if !limited(ctx) {
		return nil
	}
	ok, err := h.allowedIDs(ctx, setIDs(&rn.Set))
	if err != nil {
		return err
	}
	keepAllowed(&rn.Set, ok)
	return nil
}

// recount sets a restricted account's recent searches' counts to what each kept
// result holds for it now (results: the kept result of each, by search id). A
// search whose result is no longer kept counts nothing: opening it searches again.
func (h *Handler) recount(ctx context.Context, items []RecentSearch, results map[string]string) error {
	if !limited(ctx) {
		return nil
	}
	sets := make(map[string]*savedSet, len(items))
	var ids []string
	for _, it := range items {
		raw, kept := results[it.SearchID]
		if !kept {
			continue
		}
		s := &savedSet{}
		if err := json.Unmarshal([]byte(raw), s); err != nil {
			continue // counted as nothing
		}
		sets[it.SearchID] = s
		ids = append(ids, setIDs(s)...)
	}
	ok, err := h.allowedIDs(ctx, ids)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].TitleCount, items[i].TranscriptCount = 0, 0
		if s := sets[items[i].SearchID]; s != nil {
			keepAllowed(s, ok)
			items[i].TitleCount, items[i].TranscriptCount = len(s.Titles), len(s.Videos)
		}
	}
	return nil
}

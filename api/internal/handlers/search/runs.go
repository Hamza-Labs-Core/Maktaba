package search

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// A search's results are kept in search_runs (slot 0148), a table rather than
// the API's memory: the home's API is not always one process (an upgrade, a
// switchover to another machine), and Recent searches outlive the results.

// ProblemSearchExpired is the problem type of a search_id whose results are no
// longer kept (or that is not the caller's): search again.
const ProblemSearchExpired = "https://maktaba.tv/problems/search-expired"

// errExpired: the search_id is unknown, not the caller's, or its results
// expired.
var errExpired = errors.New("search expired")

// errSearchPanicked is a search part that stopped on a panic (logged as goroutine_panic).
var errSearchPanicked = errors.New("the search stopped on an internal error")

// searchIDPattern is what newSearchID makes (24 base64url characters).
var searchIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

// newSearchID is an opaque, unguessable id (144 random bits).
func newSearchID() string {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func writeExpired(w http.ResponseWriter, r *http.Request) {
	httperror.Write(w, r, &httperror.Error{
		Type:   ProblemSearchExpired,
		Title:  "search results expired",
		Status: http.StatusGone,
		Detail: "the results of this search are no longer kept; search again",
	})
}

// saveRun keeps rn under a new search_id for userID. It also bounds the table:
// every expired result is emptied, the user's older run of the same query,
// mode and filters is replaced once its results expired (a live one stays: another
// tab may be paging it; Recent searches shows the newest only), and only the
// user's newest RecentLimit runs stay.
func (h *Handler) saveRun(ctx context.Context, userID string, rn *run, rl Rules) error {
	result, err := json.Marshal(rn.Set)
	if err != nil {
		return err
	}
	filters, err := json.Marshal(rn.Filters)
	if err != nil {
		return err
	}
	now := h.now()
	nowMs := now.UnixMilli()
	expires := now.Add(rl.resultTTL())
	id := newSearchID()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`UPDATE search_runs SET result = NULL WHERE result IS NOT NULL AND expires_at_ms <= $1`, nowMs); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM search_runs WHERE user_id = $1 AND query = $2 AND mode = $3 AND filters = $4
			AND result IS NULL`,
		userID, rn.Q, rn.Mode, string(filters)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO search_runs (id, user_id, query, mode, filters, title_count, transcript_count,
		                         degraded, result, created_at_ms, last_used_at_ms, expires_at_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10, $11)`,
		id, userID, rn.Q, rn.Mode, string(filters), len(rn.Set.Titles), len(rn.Set.Videos),
		rn.Degraded, string(result), nowMs, expires.UnixMilli()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM search_runs WHERE user_id = $1 AND id NOT IN (
			SELECT id FROM search_runs WHERE user_id = $1
			ORDER BY created_at_ms DESC, id DESC LIMIT $2)`, userID, rl.RecentLimit); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	rn.ID = id
	rn.ExpiresAt = expires
	return nil
}

// loadRun reads the caller's kept search and slides its expiry: every read
// keeps it another ResultMinutes. Unknown, another user's and expired ids are
// all errExpired (an id never tells whether it exists).
func (h *Handler) loadRun(ctx context.Context, p *principal.Principal, id string, rl Rules) (*run, error) {
	if h.DB == nil || p == nil || p.UserID == "" || !searchIDPattern.MatchString(id) {
		return nil, errExpired
	}
	now := h.now()
	nowMs := now.UnixMilli()
	expires := now.Add(rl.resultTTL())
	var (
		rn       = &run{ID: id, ExpiresAt: expires}
		filters  string
		result   string
		degraded bool
	)
	err := h.DB.QueryRowContext(ctx, `
		UPDATE search_runs SET last_used_at_ms = $3, expires_at_ms = $4
		WHERE id = $1 AND user_id = $2 AND result IS NOT NULL AND expires_at_ms > $3
		RETURNING query, mode, filters, degraded, result`,
		id, p.UserID, nowMs, expires.UnixMilli()).Scan(&rn.Q, &rn.Mode, &filters, &degraded, &result)
	if errors.Is(err, sql.ErrNoRows) {
		// expired: let its result go now rather than at the next search
		_, _ = h.DB.ExecContext(ctx, `UPDATE search_runs SET result = NULL
			WHERE id = $1 AND user_id = $2 AND result IS NOT NULL AND expires_at_ms <= $3`, id, p.UserID, nowMs)
		return nil, errExpired
	}
	if err != nil {
		return nil, err
	}
	rn.Degraded = degraded
	if err := json.Unmarshal([]byte(filters), &rn.Filters); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(result), &rn.Set); err != nil {
		return nil, err
	}
	return rn, nil
}

// runFromRequest loads the {sid} (or ?sid=) search for the handlers below,
// writing the error itself; nil means it was written.
func (h *Handler) runFromRequest(w http.ResponseWriter, r *http.Request, id string, rl Rules) *run {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return nil
	}
	rn, err := h.loadRun(r.Context(), p, id, rl)
	if errors.Is(err, errExpired) {
		writeExpired(w, r)
		return nil
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("search").Wrap(err))
		return nil
	}
	return rn
}

// intParam reads a non-negative integer query parameter (def when absent or bad).
func intParam(r *http.Request, name string, def int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || v < 0 {
		return def
	}
	return v
}

func clampLimit(v, def, most int) int {
	if v <= 0 {
		return def
	}
	if v > most {
		return most
	}
	return v
}

// ResultsPage is GET /api/search/results/{sid}.
type ResultsPage struct {
	SearchID  string    `json:"search_id"`
	ExpiresAt time.Time `json:"expires_at"`
	Q         string    `json:"q"`
	Mode      string    `json:"mode"`
	Degraded  bool      `json:"degraded,omitempty"`
	List      string    `json:"list"`
	Offset    int       `json:"offset"`
	Limit     int       `json:"limit"`
	Total     int       `json:"total"`
	Items     any       `json:"items"`
}

// Results implements GET /api/search/results/{sid}?list=titles|transcripts&offset=&limit=:
// a page of one list of the saved result set (stable order: offsets always
// name the same videos).
func (h *Handler) Results(w http.ResponseWriter, r *http.Request) {
	list := r.URL.Query().Get("list")
	if list != "titles" && list != "transcripts" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "list", Message: "titles or transcripts"}}))
		return
	}
	ctx := r.Context()
	rl := h.rules(ctx)
	rn := h.runFromRequest(w, r, chi.URLParam(r, "sid"), rl)
	if rn == nil {
		return
	}
	p := principal.FromContext(ctx)
	offset := intParam(r, "offset", 0)
	limit := clampLimit(intParam(r, "limit", 0), rl.PageSizeList, 100)
	page := ResultsPage{
		SearchID: rn.ID, ExpiresAt: rn.ExpiresAt.UTC(), Q: rn.Q, Mode: rn.Mode, Degraded: rn.Degraded,
		List: list, Offset: offset, Limit: limit,
	}
	var err error
	if list == "titles" {
		page.Total = len(rn.Set.Titles)
		page.Items, err = h.titlePage(ctx, p, rn, offset, limit)
	} else {
		page.Total = len(rn.Set.Videos)
		page.Items, err = h.transcriptPage(ctx, p, rn, offset, limit, rl.PassagesPerVideo)
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("search").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, page)
}

// PassagesPage is GET /api/search/passages.
type PassagesPage struct {
	SearchID     string    `json:"search_id"`
	ExpiresAt    time.Time `json:"expires_at"`
	Q            string    `json:"q"`
	Mode         string    `json:"mode"`
	VideoID      string    `json:"video_id"`
	Video        VideoCard `json:"video"`
	InTitle      bool      `json:"in_title"`
	TitleSnippet string    `json:"title_snippet"`
	Total        int       `json:"total"`
	Offset       int       `json:"offset"`
	Limit        int       `json:"limit"`
	Items        []Passage `json:"items"`
	// Times are the start of every match, ascending (include_times=1).
	Times []float64 `json:"times,omitempty"`
}

// Passages implements GET /api/search/passages?sid=&video_id=&order=score|time&offset=&limit=[&include_times=1]:
// every matching passage of one video of a saved search ("Show all", and the
// video's matches page in time order).
func (h *Handler) Passages(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	videoID := qs.Get("video_id")
	order := qs.Get("order")
	if order == "" {
		order = "score"
	}
	if videoID == "" || (order != "score" && order != "time") {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
			{Field: "video_id", Message: "required"}, {Field: "order", Message: "score or time"}}))
		return
	}
	ctx := r.Context()
	rl := h.rules(ctx)
	rn := h.runFromRequest(w, r, qs.Get("sid"), rl)
	if rn == nil {
		return
	}
	inTitle, inVideos := rn.hasTitle(videoID), rn.hasVideo(videoID)
	if !inTitle && !inVideos {
		httperror.Write(w, r, httperror.NotFound("video "+videoID+" in this search"))
		return
	}
	cards, err := h.cards(ctx, principal.FromContext(ctx), []string{videoID})
	if err != nil {
		httperror.Write(w, r, httperror.Internal("search").Wrap(err))
		return
	}
	card, ok := cards[videoID]
	if !ok {
		httperror.Write(w, r, httperror.NotFound("video "+videoID+" in this search"))
		return
	}
	all := []Passage{}
	if inVideos {
		ps, err := h.passagesOf(ctx, rn, []string{videoID}, 0)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("search").Wrap(err))
			return
		}
		all = ps[videoID]
	}
	if order == "time" {
		sort.SliceStable(all, func(i, j int) bool {
			if all[i].StartSec != all[j].StartSec {
				return all[i].StartSec < all[j].StartSec
			}
			return all[i].SegmentID < all[j].SegmentID
		})
	}
	offset := intParam(r, "offset", 0)
	limit := clampLimit(intParam(r, "limit", 0), rl.PassagesPageSize, 200)
	from, to := window(len(all), offset, limit)
	page := PassagesPage{
		SearchID: rn.ID, ExpiresAt: rn.ExpiresAt.UTC(), Q: rn.Q, Mode: rn.Mode, VideoID: videoID,
		Video: card, InTitle: inTitle, TitleSnippet: highlightSnippet(card.Title, rn.Q, 500),
		Total: len(all), Offset: offset, Limit: limit, Items: all[from:to],
	}
	if qs.Get("include_times") == "1" {
		page.Times = make([]float64, len(all))
		for i, p := range all {
			page.Times[i] = p.StartSec
		}
		sort.Float64s(page.Times)
	}
	common.WriteJSON(w, r, http.StatusOK, page)
}

// RecentSearch is one of the caller's recent searches.
type RecentSearch struct {
	SearchID        string    `json:"search_id"`
	Q               string    `json:"q"`
	Mode            string    `json:"mode"`
	CreatedAt       time.Time `json:"created_at"`
	LastUsedAt      time.Time `json:"last_used_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	TitleCount      int       `json:"title_count"`
	TranscriptCount int       `json:"transcript_count"`
	// Live: its results are still kept (open it by search_id); otherwise
	// opening it searches again.
	Live bool `json:"live"`
}

// Recent implements GET /api/search/recent: the caller's searches, newest
// first.
func (h *Handler) Recent(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	items := []RecentSearch{}
	if h.DB == nil || p.UserID == "" {
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
		return
	}
	rl := h.rules(r.Context())
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT id, query, mode, filters, title_count, transcript_count, created_at_ms, last_used_at_ms,
		       expires_at_ms, result IS NOT NULL
		FROM search_runs WHERE user_id = $1
		ORDER BY created_at_ms DESC, id DESC LIMIT $2`, p.UserID, rl.RecentLimit)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("recent searches").Wrap(err))
		return
	}
	defer rows.Close()
	nowMs := h.now().UnixMilli()
	seen := map[string]bool{}
	for rows.Next() {
		var it RecentSearch
		var created, used, expires int64
		var kept bool
		var filters string
		if err := rows.Scan(&it.SearchID, &it.Q, &it.Mode, &filters, &it.TitleCount, &it.TranscriptCount,
			&created, &used, &expires, &kept); err != nil {
			httperror.Write(w, r, httperror.Internal("recent searches").Wrap(err))
			return
		}
		it.CreatedAt = time.UnixMilli(created).UTC()
		it.LastUsedAt = time.UnixMilli(used).UTC()
		it.ExpiresAt = time.UnixMilli(expires).UTC()
		it.Live = kept && expires > nowMs
		// the same search run again in another tab: the newest only
		if key := it.Q + "\x00" + it.Mode + "\x00" + filters; !seen[key] {
			seen[key] = true
			items = append(items, it)
		}
	}
	if err := rows.Err(); err != nil {
		httperror.Write(w, r, httperror.Internal("recent searches").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// DeleteRecent implements DELETE /api/search/recent/{sid}.
func (h *Handler) DeleteRecent(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	id := chi.URLParam(r, "sid")
	if h.DB == nil || p.UserID == "" || !searchIDPattern.MatchString(id) {
		httperror.Write(w, r, httperror.NotFound("recent search "+id))
		return
	}
	res, err := h.DB.ExecContext(r.Context(),
		// the entry stands for every run of its query, mode and filters
		`DELETE FROM search_runs WHERE user_id = $2 AND EXISTS (
			SELECT 1 FROM search_runs o WHERE o.id = $1 AND o.user_id = $2 AND o.query = search_runs.query
			AND o.mode = search_runs.mode AND o.filters = search_runs.filters)`, id, p.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("recent searches").Wrap(err))
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httperror.Write(w, r, httperror.NotFound("recent search "+id))
		return
	}
	common.WriteNoContent(w)
}

// ClearRecent implements DELETE /api/search/recent: all of the caller's.
func (h *Handler) ClearRecent(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	if h.DB != nil && p.UserID != "" {
		if _, err := h.DB.ExecContext(r.Context(),
			`DELETE FROM search_runs WHERE user_id = $1`, p.UserID); err != nil {
			httperror.Write(w, r, httperror.Internal("recent searches").Wrap(err))
			return
		}
	}
	common.WriteNoContent(w)
}

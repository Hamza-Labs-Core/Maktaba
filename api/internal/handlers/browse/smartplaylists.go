package browse

// Epic 56 story 56.09: smart playlists. A smart playlist (user_playlists.kind
// 'smart', slot 0186) is a saved grid filter with a sort and a limit, kept per
// user and evaluated whenever it is read: GET /api/videos run in process as its
// owner, so its watched filter is the owner's own and it never lists a video of
// a library the owner cannot read.
//
//	smart_query: {libraries: [ids] | null, filters: {FilterKeys…, favorite}, sort, order, limit}
//
// libraries null is every library the owner can read. limit is 1..5,000 (200
// when unset). sort=random is refused: a playlist's order is stable, Shuffle
// is the play button. A smart playlist holds no items: adding, removing and
// ordering are 409 smart_playlist, and its kind never changes.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/collections"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Playlist kinds.
const (
	PlaylistManual = "manual"
	PlaylistSmart  = "smart"
)

const (
	// MaxSmartLimit caps what a smart playlist lists.
	MaxSmartLimit = 5000
	// DefaultSmartLimit is a smart playlist's limit when none is given.
	DefaultSmartLimit = 200
	// smartCountTTL is how long a smart playlist's count is cached.
	smartCountTTL = 5 * time.Second
)

// TypeSmartPlaylist is the 409 of an item write on a smart playlist.
const TypeSmartPlaylist = "https://maktaba.tv/problems/smart_playlist"

// SmartQuery is a smart playlist's rules.
type SmartQuery struct {
	Libraries []string          `json:"libraries"`
	Filters   map[string]string `json:"filters"`
	Sort      string            `json:"sort,omitempty"`
	Order     string            `json:"order,omitempty"`
	Limit     int               `json:"limit"`
}

// smartSorts are the sorts a smart playlist may keep (GET /api/videos's, not random).
var smartSorts = map[string]bool{"created_at": true, "updated_at": true, "title": true, "published_at": true, "duration_sec": true, "plays": true}

// checkSmartQuery validates and normalizes q (422s naming the field).
func checkSmartQuery(q *SmartQuery) []httperror.FieldError {
	var fe []httperror.FieldError
	bad := func(field, msg string) {
		fe = append(fe, httperror.FieldError{Field: "smart_query." + field, Message: msg})
	}
	if q.Filters == nil {
		q.Filters = map[string]string{}
	}
	for k, v := range q.Filters {
		if k == "sort" || k == "order" {
			// the grid's sort may come inside its filters
			if k == "sort" && q.Sort == "" {
				q.Sort = v
			}
			if k == "order" && q.Order == "" {
				q.Order = v
			}
			delete(q.Filters, k)
			continue
		}
		if !collections.FilterKeys[k] && k != "favorite" {
			bad("filters."+k, "unknown filter "+k)
			continue
		}
		if strings.TrimSpace(v) == "" || len(v) > 200 {
			bad("filters."+k, "filter "+k+" needs a value")
		}
	}
	if q.Sort == "random" {
		bad("sort", "a smart playlist keeps a stable order; Shuffle plays it at random")
	} else if q.Sort != "" && !smartSorts[q.Sort] {
		bad("sort", "one of created_at, updated_at, title, published_at, duration_sec, plays")
	}
	q.Order = strings.ToLower(q.Order)
	if q.Order != "" && q.Order != "asc" && q.Order != "desc" {
		bad("order", "asc or desc")
	}
	if q.Limit == 0 {
		q.Limit = DefaultSmartLimit
	}
	if q.Limit < 1 || q.Limit > MaxSmartLimit {
		bad("limit", "1 to "+strconv.Itoa(MaxSmartLimit))
	}
	seen := map[string]bool{}
	libs := []string{}
	for _, id := range q.Libraries {
		if !seen[id] {
			seen[id] = true
			libs = append(libs, id)
		}
	}
	if q.Libraries != nil {
		q.Libraries = libs
	}
	return fe
}

// smartRecorder is a minimal ResponseWriter for the in-process list.
type smartRecorder struct {
	code int
	hdr  http.Header
	body bytes.Buffer
}

func (r *smartRecorder) Header() http.Header {
	if r.hdr == nil {
		r.hdr = http.Header{}
	}
	return r.hdr
}
func (r *smartRecorder) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *smartRecorder) WriteHeader(code int)        { r.code = code }

// listedVideo is an item of GET /api/videos as the evaluation reads it.
type listedVideo struct {
	ID            string     `json:"id"`
	Title         *string    `json:"title"`
	Filename      string     `json:"filename"`
	LibraryID     string     `json:"library_id"`
	DurationSec   *float64   `json:"duration_sec"`
	State         string     `json:"state"`
	CreatedAt     time.Time  `json:"created_at"`
	PublishedAt   *time.Time `json:"published_at"`
	PlaybackState *struct {
		PositionSec float64 `json:"position_sec"`
		Completed   bool    `json:"completed"`
	} `json:"playback_state"`
}

// evaluateSmart lists a smart playlist's videos for p, up to its limit, as cards.
func (h *Handler) evaluateSmart(ctx context.Context, p *principal.Principal, q SmartQuery) ([]Card, error) {
	if h.ListVideos == nil {
		return []Card{}, nil
	}
	allowed := map[string]bool{}
	for _, id := range q.Libraries {
		if p.AccessAllLibraries || p.HasLibrary(id) {
			allowed[id] = true
		}
	}
	if q.Libraries != nil && len(allowed) == 0 {
		return []Card{}, nil // every library it names is gone, or the owner's no more
	}
	vals := url.Values{}
	for k, v := range q.Filters {
		vals.Set(k, v)
	}
	if q.Sort != "" {
		vals.Set("sort", q.Sort)
	}
	if q.Order != "" {
		vals.Set("order", q.Order)
	}
	if len(allowed) == 1 {
		for id := range allowed {
			vals.Set("library", id)
		}
	}
	path := "/api/videos"
	if vals.Get("favorite") != "" {
		path = "/api/me/videos" // the owner's favorites are read on the home
	}
	out := []Card{}
	cursor := ""
	for pages := 0; len(out) < q.Limit && pages < 100; pages++ {
		vals.Set("limit", "200")
		if cursor != "" {
			vals.Set("cursor", cursor)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, path+"?"+vals.Encode(), nil)
		if err != nil {
			return nil, err
		}
		rec := &smartRecorder{code: http.StatusOK}
		h.ListVideos(rec, req)
		if rec.code != http.StatusOK {
			// a filter value the list refuses (a removed tag, a bad band):
			// the playlist lists nothing rather than failing its page
			return []Card{}, nil
		}
		var page struct {
			Items []listedVideo `json:"items"`
			Next  *string       `json:"next"`
		}
		if err := json.Unmarshal(rec.body.Bytes(), &page); err != nil {
			return nil, err
		}
		for _, v := range page.Items {
			if len(allowed) > 1 && !allowed[v.LibraryID] {
				continue
			}
			c := Card{ID: v.ID, Title: v.Title, Filename: v.Filename, LibraryID: v.LibraryID, DurationSec: v.DurationSec,
				State: v.State, CreatedAt: v.CreatedAt, PublishedAt: v.PublishedAt}
			if v.PlaybackState != nil {
				pos, done := v.PlaybackState.PositionSec, v.PlaybackState.Completed
				c.PositionSec, c.Completed = &pos, &done
			}
			out = append(out, c)
			if len(out) == q.Limit {
				break
			}
		}
		if page.Next == nil {
			break
		}
		cursor = *page.Next
	}
	return out, nil
}

// smartCount caches a smart playlist's count and first posters for a few
// seconds (GET /api/me/playlists lists each one's).
type smartCount struct {
	mu   sync.Mutex
	byID map[string]smartCountEntry
}

type smartCountEntry struct {
	key      string
	at       time.Time
	count    int
	duration float64
	posters  []string
}

var smartCounts = &smartCount{byID: map[string]smartCountEntry{}}

// fillSmart sets a smart summary's count, length and posters, from the cache
// when fresh (the key is the rules and the caller's scope).
func (h *Handler) fillSmart(ctx context.Context, p *principal.Principal, ps *PlaylistSummary) error {
	if ps.SmartQuery == nil {
		return nil
	}
	raw, _ := json.Marshal(ps.SmartQuery)
	key := string(raw) + "|" + strings.Join(p.Libraries, ",") + "|" + strconv.FormatBool(p.AccessAllLibraries) + "|" + ps.UpdatedAt.String()
	smartCounts.mu.Lock()
	e, ok := smartCounts.byID[ps.ID]
	smartCounts.mu.Unlock()
	if !ok || e.key != key || time.Since(e.at) > smartCountTTL {
		cards, err := h.evaluateSmart(ctx, p, *ps.SmartQuery)
		if err != nil {
			return err
		}
		e = smartCountEntry{key: key, at: time.Now(), count: len(cards), posters: []string{}}
		for i, c := range cards {
			if c.DurationSec != nil {
				e.duration += *c.DurationSec
			}
			if i < posterCount {
				e.posters = append(e.posters, c.ID)
			}
		}
		smartCounts.mu.Lock()
		smartCounts.byID[ps.ID] = e
		smartCounts.mu.Unlock()
	}
	ps.Videos, ps.DurationSec, ps.PosterVideoIDs = int64(e.count), e.duration, append([]string{}, e.posters...)
	return nil
}

// smartRefused is the 409 of an item write on a smart playlist.
func smartRefused() *httperror.Error {
	return httperror.Conflict(TypeSmartPlaylist, "a smart playlist changes by its rules; edit the rules instead")
}

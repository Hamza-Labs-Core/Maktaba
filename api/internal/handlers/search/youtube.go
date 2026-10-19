package search

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// youtubeInlineTimeout caps the YouTube leg of POST /api/search
// (include=youtube) so it never holds up local results (Story 26.8 D2).
// Exceeding it returns an empty block, not an error.
const youtubeInlineTimeout = 1500 * time.Millisecond

// youtubeProxyTimeout caps GET /api/search/youtube, which the Search page
// calls on its own after the local results are shown, so it can wait for
// a whole yt-dlp search.
const youtubeProxyTimeout = 20 * time.Second

// youtubeCacheTTL is how long one query's YouTube results are reused.
// Short: search results move, and a cached block only saves a few
// seconds of yt-dlp.
const youtubeCacheTTL = 10 * time.Minute

// YTItem is one YouTube result.
type YTItem struct {
	YouTubeID   string  `json:"youtube_id"`
	Title       string  `json:"title"`
	Channel     string  `json:"channel"`
	Description string  `json:"description_snippet"`
	Thumbnail   string  `json:"thumbnail"`
	PublishedAt string  `json:"published_at,omitempty"`
	DurationSec *int    `json:"duration_sec,omitempty"`
	ViewCount   *int64  `json:"view_count,omitempty"`
	Match       YTMatch `json:"match"`
}

// YTMatch annotates whether the result already maps to a local video.
type YTMatch struct {
	State   string `json:"state"`              // in_library | imported | importable
	VideoID string `json:"video_id,omitempty"` // set when in_library or imported
}

// YTBlock is the "From YouTube" section. A non-empty Reason explains an
// empty Items list (disabled | rate_limited | error).
type YTBlock struct {
	Items  []YTItem `json:"items"`
	Reason string   `json:"reason,omitempty"`
}

// YouTubeSearcher is the rate-limited, cached YouTube adapter (Story
// 26.5). It returns raw items; match-hint annotation is done locally.
// A RateLimited / disabled adapter returns its sentinel error so the
// handler can fill the block's Reason without failing local search.
type YouTubeSearcher interface {
	// SearchYouTube returns up to a handful of YouTube results for the
	// query, or an error. ErrYouTubeRateLimited / ErrYouTubeDisabled
	// are mapped to block reasons; any other error becomes reason "error".
	SearchYouTube(ctx context.Context, query string) ([]YTItem, error)
}

// YouTubeAvailability is implemented by a searcher that can tell whether
// it works on this machine (YtDlpSearcher: is yt-dlp installed). A
// searcher without it is taken as available.
type YouTubeAvailability interface {
	Available() bool
}

// youtubeAvailable reports whether the "From YouTube" section should be
// offered at all.
func (h *Handler) youtubeAvailable() bool {
	if h.YouTube == nil {
		return false
	}
	if a, ok := h.YouTube.(YouTubeAvailability); ok {
		return a.Available()
	}
	return true
}

// Sentinel errors a YouTubeSearcher may return so the handler can map
// them to a block reason rather than a 500.
var (
	ErrYouTubeRateLimited = errors.New("youtube: rate limited")
	ErrYouTubeDisabled    = errors.New("youtube: disabled")
)

// fetchYouTube returns the augmentation block. It is cache-first, hard
// time-bounded, and degrades to an empty block with a reason — it never
// returns nil when include=youtube was requested, and never errors out
// the request.
func (h *Handler) fetchYouTube(ctx context.Context, query string, timeout time.Duration) *YTBlock {
	if !h.youtubeAvailable() {
		return &YTBlock{Items: []YTItem{}, Reason: "disabled"}
	}
	if cached := h.youtubeCacheGet(ctx, query); cached != nil {
		h.annotateMatches(ctx, cached.Items)
		return cached
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	items, err := h.YouTube.SearchYouTube(cctx, query)
	if err != nil {
		return &YTBlock{Items: []YTItem{}, Reason: youtubeReason(err)}
	}
	if items == nil {
		items = []YTItem{}
	}
	block := &YTBlock{Items: items}
	h.youtubeCachePut(ctx, query, block)
	h.annotateMatches(ctx, block.Items)
	return block
}

func youtubeReason(err error) string {
	switch {
	case errors.Is(err, ErrYouTubeRateLimited):
		return "rate_limited"
	case errors.Is(err, ErrYouTubeDisabled):
		return "disabled"
	default:
		return "error"
	}
}

// annotateMatches sets each item's match state by comparing its title
// against local parsed titles / video titles (Story 26.8 D4). Cheap and
// local; a DB-less handler leaves everything "importable".
func (h *Handler) annotateMatches(ctx context.Context, items []YTItem) {
	for i := range items {
		items[i].Match = YTMatch{State: "importable"}
		if h.DB == nil {
			continue
		}
		norm := strings.ToLower(strings.TrimSpace(items[i].Title))
		if norm == "" {
			continue
		}
		var vid string
		err := h.DB.QueryRowContext(ctx, `
			SELECT id FROM videos
			WHERE LOWER(COALESCE(title,'')) = $1
			LIMIT 1
		`, norm).Scan(&vid)
		if err == nil && vid != "" {
			items[i].Match = YTMatch{State: "in_library", VideoID: vid}
			continue
		}
		// Already staged onto a local video by an earlier import.
		var imported string
		err = h.DB.QueryRowContext(ctx, `
			SELECT video_id FROM youtube_imports
			WHERE youtube_id = $1
			ORDER BY imported_at DESC
			LIMIT 1
		`, items[i].YouTubeID).Scan(&imported)
		if err == nil && imported != "" {
			items[i].Match = YTMatch{State: "imported", VideoID: imported}
		}
	}
}

func queryHash(query string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(query))))
	return hex.EncodeToString(sum[:])
}

func (h *Handler) youtubeCacheGet(ctx context.Context, query string) *YTBlock {
	if h.DB == nil {
		return nil
	}
	var raw []byte
	var expires time.Time
	err := h.DB.QueryRowContext(ctx,
		`SELECT response, expires_at FROM youtube_search_cache WHERE query_hash = $1`,
		queryHash(query)).Scan(&raw, &expires)
	if err != nil || time.Now().After(expires) {
		return nil
	}
	var block YTBlock
	if json.Unmarshal(raw, &block) != nil {
		return nil
	}
	if block.Items == nil {
		block.Items = []YTItem{}
	}
	return &block
}

func (h *Handler) youtubeCachePut(ctx context.Context, query string, block *YTBlock) {
	if h.DB == nil || len(block.Items) == 0 {
		return
	}
	raw, err := json.Marshal(block)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	_, _ = h.DB.ExecContext(ctx, `
		INSERT INTO youtube_search_cache (query_hash, response, fetched_at, expires_at)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (query_hash) DO UPDATE
		  SET response = EXCLUDED.response, fetched_at = EXCLUDED.fetched_at, expires_at = EXCLUDED.expires_at
	`, queryHash(query), raw, now, now.Add(youtubeCacheTTL))
}

// SearchYouTube is the GET /api/search/youtube proxy — returns just the
// YouTube block for a query (the search key stays server-side).
func (h *Handler) SearchYouTube(w http.ResponseWriter, r *http.Request) {
	if principal.FromContext(r.Context()) == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		httperror.Write(w, r, httperror.InvalidQuery("q must be non-empty"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, h.fetchYouTube(r.Context(), q, youtubeProxyTimeout))
}

// YouTubeStatus is GET /api/search/youtube/status: whether YouTube search
// works here (yt-dlp is installed). The Search page shows its "From
// YouTube" toggle and section only when it does.
func (h *Handler) YouTubeStatus(w http.ResponseWriter, r *http.Request) {
	if principal.FromContext(r.Context()) == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]bool{"available": h.youtubeAvailable()})
}

// importYouTubeRequest is the POST /api/videos/{id}/import-youtube body:
// the YouTube result the user picked on the Search page.
type importYouTubeRequest struct {
	YouTubeID   string `json:"youtube_id"`
	Title       string `json:"title"`
	Channel     string `json:"channel"`
	Description string `json:"description"`
	Thumbnail   string `json:"thumbnail"`
	PublishedAt string `json:"published_at"`
	DurationSec *int   `json:"duration_sec"`
}

// Field caps for a staged import.
const (
	maxImportTitle       = 500
	maxImportChannel     = 200
	maxImportDescription = 5000
)

// validate trims the request and checks it, returning the field errors.
func (req *importYouTubeRequest) validate() []httperror.FieldError {
	req.YouTubeID = strings.TrimSpace(req.YouTubeID)
	req.Title = strings.TrimSpace(req.Title)
	req.Channel = strings.TrimSpace(req.Channel)
	req.Description = strings.TrimSpace(req.Description)
	req.Thumbnail = strings.TrimSpace(req.Thumbnail)
	req.PublishedAt = strings.TrimSpace(req.PublishedAt)
	var errs []httperror.FieldError
	switch {
	case req.YouTubeID == "":
		errs = append(errs, httperror.FieldError{Field: "youtube_id", Message: "required"})
	case !ValidYouTubeID(req.YouTubeID):
		errs = append(errs, httperror.FieldError{Field: "youtube_id", Message: "not a YouTube video id"})
	}
	if req.Title == "" {
		errs = append(errs, httperror.FieldError{Field: "title", Message: "required"})
	} else if len([]rune(req.Title)) > maxImportTitle {
		errs = append(errs, httperror.FieldError{Field: "title", Message: "too long"})
	}
	if len([]rune(req.Channel)) > maxImportChannel {
		errs = append(errs, httperror.FieldError{Field: "channel", Message: "too long"})
	}
	if len([]rune(req.Description)) > maxImportDescription {
		errs = append(errs, httperror.FieldError{Field: "description", Message: "too long"})
	}
	if req.Thumbnail != "" && !SafeThumbnailURL(req.Thumbnail) {
		errs = append(errs, httperror.FieldError{Field: "thumbnail", Message: "must be a YouTube image URL"})
	}
	if req.PublishedAt != "" {
		if _, err := time.Parse("2006-01-02", req.PublishedAt); err != nil {
			errs = append(errs, httperror.FieldError{Field: "published_at", Message: "must be YYYY-MM-DD"})
		}
	}
	if req.DurationSec != nil && *req.DurationSec < 0 {
		errs = append(errs, httperror.FieldError{Field: "duration_sec", Message: "must not be negative"})
	}
	return errs
}

// mapped is the enrichment candidate's `mapped` document. title,
// description and poster_path are the fields the accept path may write
// onto the video; the rest are facts shown with the candidate.
func (req *importYouTubeRequest) mapped() map[string]any {
	m := map[string]any{
		"provider":   "youtube",
		"youtube_id": req.YouTubeID,
		"title":      req.Title,
		"url":        "https://www.youtube.com/watch?v=" + req.YouTubeID,
	}
	if req.Description != "" {
		m["description"] = req.Description
	}
	if req.Channel != "" {
		m["channel"] = req.Channel
	}
	if req.Thumbnail != "" {
		m["thumbnail"] = req.Thumbnail
		m["poster_path"] = req.Thumbnail
	}
	if req.PublishedAt != "" {
		m["published_at"] = req.PublishedAt
	}
	if req.DurationSec != nil {
		m["duration_sec"] = *req.DurationSec
	}
	return m
}

// ImportYouTube stages a YouTube result's metadata (title, channel,
// description, thumbnail, date, length) for a local video. It writes the
// result as a `provider='youtube'` enrichment candidate and an idempotent
// `youtube_imports` audit row. The fields reach the video through the
// enrichment accept/provenance path (Story 26.6), so user-owned fields
// stay protected and the action is reversible.
func (h *Handler) ImportYouTube(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	var req importYouTubeRequest
	if e := common.ReadJSON(r, &req, 32<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	// Import is a write → editor-only (admin in v1), mirroring video
	// edits; a read-only user gets 403.
	if !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "editor required"))
		return
	}
	if errs := req.validate(); len(errs) > 0 {
		httperror.Write(w, r, httperror.Unprocessable(errs))
		return
	}
	if h.DB == nil {
		httperror.Write(w, r, httperror.Unavailable(0))
		return
	}
	var exists int
	if err := h.DB.QueryRowContext(r.Context(), `SELECT 1 FROM videos WHERE id = $1`, id).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("video "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load video"))
		return
	}

	externalID := "youtube:" + req.YouTubeID
	mapped, _ := json.Marshal(req.mapped())
	now := time.Now().UTC()
	// Stage the candidate (idempotent on (video, provider, external_id)).
	if _, err := h.DB.ExecContext(r.Context(), `
		INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id, mapped, confidence, fetched_at, created_at)
		VALUES ($1,$2,'youtube',$3,$4,1.0,$5,$5)
		ON CONFLICT (video_id, provider, external_id) DO UPDATE
		  SET mapped = EXCLUDED.mapped, is_dismissed = false, fetched_at = EXCLUDED.fetched_at
	`, uuid.NewString(), id, externalID, mapped, now); err != nil {
		httperror.Write(w, r, httperror.Internal("stage candidate"))
		return
	}
	// Idempotent audit row (last-write-wins; no duplicate spam).
	if _, err := h.DB.ExecContext(r.Context(), `
		INSERT INTO youtube_imports (id, video_id, youtube_id, actor_user_id, imported_at)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (video_id, youtube_id) DO UPDATE SET imported_at = EXCLUDED.imported_at
	`, uuid.NewString(), id, req.YouTubeID, importActor(p), now); err != nil {
		httperror.Write(w, r, httperror.Internal("audit import"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{
		"video_id": id, "external_id": externalID, "staged": true,
	})
}

func importActor(p *principal.Principal) any {
	if p == nil || p.UserID == "" {
		return nil
	}
	if _, err := uuid.Parse(p.UserID); err != nil {
		return nil
	}
	return p.UserID
}

package search

import (
	"context"
	"crypto/sha256"
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

// youtubeFetchTimeout caps the YouTube leg so it never holds up local
// results (Story 26.8 D2). Exceeding it returns an empty block, not an
// error.
const youtubeFetchTimeout = 1500 * time.Millisecond

// YTItem is one YouTube result.
type YTItem struct {
	YouTubeID   string  `json:"youtube_id"`
	Title       string  `json:"title"`
	Channel     string  `json:"channel"`
	Description string  `json:"description_snippet"`
	Thumbnail   string  `json:"thumbnail"`
	PublishedAt string  `json:"published_at,omitempty"`
	ViewCount   *int64  `json:"view_count,omitempty"`
	Match       YTMatch `json:"match"`
}

// YTMatch annotates whether the result already maps to a local video.
type YTMatch struct {
	State   string `json:"state"`              // in_library | importable
	VideoID string `json:"video_id,omitempty"` // set when in_library
}

// YTBlock is the "From YouTube" section. A non-empty Reason explains an
// empty Items list (disabled | no_key | rate_limited | error).
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
	// query, or an error. ErrYouTubeRateLimited / ErrYouTubeNoKey are
	// mapped to block reasons; any other error becomes reason "error".
	SearchYouTube(ctx context.Context, query string) ([]YTItem, error)
}

// Sentinel errors a YouTubeSearcher may return so the handler can map
// them to a block reason rather than a 500.
var (
	ErrYouTubeNoKey       = errors.New("youtube: no api key configured")
	ErrYouTubeRateLimited = errors.New("youtube: rate limited")
	ErrYouTubeDisabled    = errors.New("youtube: disabled")
)

// fetchYouTube returns the augmentation block. It is cache-first, hard
// time-bounded, and degrades to an empty block with a reason — it never
// returns nil when include=youtube was requested, and never errors out
// the request.
func (h *Handler) fetchYouTube(ctx context.Context, query string) *YTBlock {
	if h.YouTube == nil {
		return &YTBlock{Items: []YTItem{}, Reason: "disabled"}
	}
	if cached := h.youtubeCacheGet(ctx, query); cached != nil {
		h.annotateMatches(ctx, cached.Items)
		return cached
	}
	cctx, cancel := context.WithTimeout(ctx, youtubeFetchTimeout)
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
	case errors.Is(err, ErrYouTubeNoKey):
		return "no_key"
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
	`, queryHash(query), raw, now, now.Add(6*time.Hour))
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
	common.WriteJSON(w, r, http.StatusOK, h.fetchYouTube(r.Context(), q))
}

// importYouTubeRequest is the POST /api/videos/{id}/import-youtube body.
type importYouTubeRequest struct {
	YouTubeID string `json:"youtube_id"`
}

// ImportYouTube copies a YouTube result's metadata onto a local video.
// It writes the result as a `provider='youtube'` enrichment candidate
// and an idempotent `youtube_imports` audit row. The actual field
// promotion flows through the enrichment accept/provenance path (Story
// 26.6) so user-owned fields stay protected and the action is
// reversible; here we stage the candidate and record the import.
func (h *Handler) ImportYouTube(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	var req importYouTubeRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if strings.TrimSpace(req.YouTubeID) == "" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "youtube_id", Message: "required"}}))
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
	if h.DB == nil {
		httperror.Write(w, r, httperror.Unavailable(0))
		return
	}

	externalID := "youtube:" + req.YouTubeID
	mapped, _ := json.Marshal(map[string]any{"provider": "youtube", "youtube_id": req.YouTubeID})
	// Stage the candidate (idempotent on (video, provider, external_id)).
	if _, err := h.DB.ExecContext(r.Context(), `
		INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id, mapped, confidence, fetched_at, created_at)
		VALUES ($1,$2,'youtube',$3,$4,1.0,$5,$5)
		ON CONFLICT (video_id, provider, external_id) DO UPDATE
		  SET mapped = EXCLUDED.mapped, is_dismissed = false, fetched_at = EXCLUDED.fetched_at
	`, uuid.NewString(), id, externalID, mapped, time.Now().UTC()); err != nil {
		httperror.Write(w, r, httperror.Internal("stage candidate"))
		return
	}
	// Idempotent audit row (last-write-wins; no duplicate spam).
	if _, err := h.DB.ExecContext(r.Context(), `
		INSERT INTO youtube_imports (id, video_id, youtube_id, actor_user_id, imported_at)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (video_id, youtube_id) DO UPDATE SET imported_at = EXCLUDED.imported_at
	`, uuid.NewString(), id, req.YouTubeID, importActor(p), time.Now().UTC()); err != nil {
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

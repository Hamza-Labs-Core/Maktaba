package browse

// Epic 45 Phase 3 (story 45.21): the featured hero at the top of Home.
//
//	GET /api/home                  -> featured: {kind, video, series?, picked} | null
//	GET /api/home/featured         (admins) -> {video_id: string|null, video: Card|null}
//	PUT /api/home/featured         (admins) {video_id: string|null}
//
// By default the hero is the newest series (series.created_at) with a
// visible episode the caller may see, shown with its first lesson. An admin
// may pick a lesson instead (app_settings "home.featured"); a caller who
// may not see the picked lesson, or one now hidden, gets the default.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/libraries"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// FeaturedKey is the app_settings key of the admin's pick.
const FeaturedKey = "home.featured"

// Featured is Home's hero: a series (shown with its first lesson) or the
// lesson an admin picked (Picked, with its series when it has one).
type Featured struct {
	Kind   string          `json:"kind"` // "series" or "video"
	Video  Card            `json:"video"`
	Series *FeaturedSeries `json:"series,omitempty"`
	Picked bool            `json:"picked"`
}

// FeaturedSeries names the hero's series; Episodes counts its visible ones.
type FeaturedSeries struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Episodes int64  `json:"episodes,omitempty"`
}

type featuredSetting struct {
	VideoID string `json:"video_id"`
}

// pickedID is the admin's pick, "" when there is none (or it cannot be read).
func (h *Handler) pickedID(ctx context.Context) string {
	var raw []byte
	if err := h.DB.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, FeaturedKey).Scan(&raw); err != nil {
		return ""
	}
	var s featuredSetting
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	if _, err := uuid.Parse(s.VideoID); err != nil {
		return ""
	}
	return s.VideoID
}

// visibleCard is the video's card when it is visible within s, else nil.
func (h *Handler) visibleCard(ctx context.Context, s scope, id string) (*Card, error) {
	args := []any{id}
	cond := s.cond("v.library_id", &args)
	cs, err := h.cards(ctx, `SELECT `+cardColumns+` FROM videos v WHERE v.id = $1 AND `+and(visible, cond), args)
	if err != nil || len(cs) == 0 {
		return nil, err
	}
	return &cs[0], nil
}

// featured is the caller's hero, nil when there is nothing to feature.
func (h *Handler) featured(ctx context.Context, s scope) (*Featured, error) {
	if s.none() {
		return nil, nil
	}
	if id := h.pickedID(ctx); id != "" {
		c, err := h.visibleCard(ctx, s, id)
		if err != nil {
			return nil, err
		}
		if c != nil {
			f := &Featured{Kind: "video", Video: *c, Picked: true}
			series, err := h.firstSeries(ctx, []string{id})
			if err != nil {
				return nil, err
			}
			if sn, ok := series[id]; ok {
				f.Series = &FeaturedSeries{ID: sn.ID, Name: sn.Name}
			}
			return f, nil
		}
	}
	var args []any
	cond := s.cond("v.library_id", &args)
	var fs FeaturedSeries
	err := h.DB.QueryRowContext(ctx, `SELECT s.id, COALESCE(s.name_override, s.name, ''), COUNT(v.id)
		FROM series s JOIN series_episodes se ON se.series_id = s.id
		JOIN videos v ON v.id = se.video_id
		WHERE `+and(visible, cond)+`
		GROUP BY s.id, s.name, s.name_override, s.created_at
		ORDER BY s.created_at DESC, s.id DESC LIMIT 1`, args...).Scan(&fs.ID, &fs.Name, &fs.Episodes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	args = []any{fs.ID}
	cond = s.cond("v.library_id", &args)
	cs, err := h.cards(ctx, `SELECT `+cardColumns+` FROM series_episodes se JOIN videos v ON v.id = se.video_id
		WHERE se.series_id = $1 AND `+and(visible, cond)+`
		ORDER BY COALESCE(se.season_override, se.season, 0),
		         COALESCE(se.episode_override, se.episode, se.absolute_number, 0), v.created_at, v.id
		LIMIT 1`, args)
	if err != nil || len(cs) == 0 {
		return nil, err
	}
	return &Featured{Kind: "series", Video: cs[0], Series: &fs}, nil
}

// adminCaller is the signed-in admin, or the 401/403 written.
func adminCaller(w http.ResponseWriter, r *http.Request) bool {
	p := caller(w, r)
	if p == nil {
		return false
	}
	if !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return false
	}
	return true
}

// GetFeatured implements GET /api/home/featured: the admin's pick.
func (h *Handler) GetFeatured(w http.ResponseWriter, r *http.Request) {
	if !adminCaller(w, r) {
		return
	}
	doc := map[string]any{"video_id": nil, "video": nil}
	if id := h.pickedID(r.Context()); id != "" {
		doc["video_id"] = id
		c, err := h.visibleCard(r.Context(), scope{all: true}, id)
		if err != nil {
			internal(w, r, "featured video", err)
			return
		}
		if c != nil {
			doc["video"] = c
		}
	}
	writeJSON(w, r, doc)
}

// PutFeaturedRequest is the body of PUT /api/home/featured.
type PutFeaturedRequest struct {
	VideoID *string `json:"video_id"`
}

// PutFeatured implements PUT /api/home/featured: picks the lesson Home
// features, or (null) goes back to the newest series. An unknown or hidden
// video is 404.
func (h *Handler) PutFeatured(w http.ResponseWriter, r *http.Request) {
	if !adminCaller(w, r) {
		return
	}
	p := caller(w, r)
	var req PutFeaturedRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	ctx := r.Context()
	if req.VideoID == nil || *req.VideoID == "" {
		if _, err := h.DB.ExecContext(ctx, `DELETE FROM app_settings WHERE key = $1`, FeaturedKey); err != nil {
			internal(w, r, "clear featured", err)
			return
		}
		h.GetFeatured(w, r)
		return
	}
	id := *req.VideoID
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.NotFound("video "+id))
		return
	}
	c, err := h.visibleCard(ctx, scope{all: true}, id)
	if err != nil {
		internal(w, r, "featured video", err)
		return
	}
	if c == nil {
		httperror.Write(w, r, httperror.NotFound("video "+id))
		return
	}
	b, _ := json.Marshal(featuredSetting{VideoID: id})
	var by any
	if uid := userID(p); uid != "" {
		by = uid
	}
	if _, err := h.DB.ExecContext(ctx, `INSERT INTO app_settings (key, value, updated_at, updated_by) VALUES ($1, $2, $3, $4)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at, updated_by = EXCLUDED.updated_by`,
		FeaturedKey, string(b), h.ts(h.now()), by); err != nil {
		internal(w, r, "save featured", err)
		return
	}
	libraries.WriteAudit(ctx, h.DB, "home.featured-set", libraries.ActorOf(p), c.LibraryID, map[string]any{"video_id": id})
	h.GetFeatured(w, r)
}

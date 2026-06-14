package guide

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Handler bundles deps for the guide/EPG read surface.
type Handler struct {
	DB      *sql.DB
	NowFunc func() time.Time
}

// Mount wires the guide routes. The static segments (guide / now / xmltv
// / playlist.m3u) are registered alongside the channels handler's
// `/api/channels/{id}` param route; chi matches the static segments with
// priority, so there is no conflict.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/channels/guide", h.Grid)
	r.Get("/api/channels/now", h.Now)
	r.Get("/api/channels/xmltv", h.XMLTV)
	r.Get("/api/channels/playlist.m3u", h.M3U)
	r.Get("/api/channels/{id}/guide", h.ChannelGuide)
}

func (h *Handler) repo() *repo { return &repo{db: h.DB} }

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now().UTC()
}

// defaultWindow is the guide range used when start/end are omitted.
const defaultWindow = 3 * time.Hour

// parseRange reads ?start=&end= (RFC3339). Missing start → now; missing
// end → start + defaultWindow. An unparseable value is a 400.
func (h *Handler) parseRange(r *http.Request) (time.Time, time.Time, *httperror.Error) {
	now := h.now()
	start := now
	if s := r.URL.Query().Get("start"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, time.Time{}, httperror.InvalidQuery("start must be RFC3339")
		}
		start = t
	}
	end := start.Add(defaultWindow)
	if s := r.URL.Query().Get("end"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, time.Time{}, httperror.InvalidQuery("end must be RFC3339")
		}
		end = t
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, httperror.InvalidQuery("end must be after start")
	}
	return start, end, nil
}

// visibleChannels returns the enabled channels (optionally category-
// filtered) the principal may read.
func (h *Handler) visibleChannels(r *http.Request, p *principal.Principal, category string) ([]ChannelMeta, error) {
	all, err := h.repo().channelsVisible(r.Context(), category)
	if err != nil {
		return nil, err
	}
	out := make([]ChannelMeta, 0, len(all))
	for _, c := range all {
		if canRead(p, c.LibraryID) {
			out = append(out, c)
		}
	}
	return out, nil
}

// Grid implements GET /api/channels/guide (AC1).
func (h *Handler) Grid(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	start, end, e := h.parseRange(r)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	chans, err := h.visibleChannels(r, p, r.URL.Query().Get("category"))
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list channels"))
		return
	}
	ids := metaIDs(chans)
	rows, err := h.repo().programsOverlapping(r.Context(), ids, start, end)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("read programs"))
		return
	}
	now := h.now()
	byChan := map[string][]GuideBlock{}
	for _, pr := range rows {
		byChan[pr.ChannelID] = append(byChan[pr.ChannelID], toBlock(pr, now))
	}
	// Filler/bumper collapse defaults on (AC10); ?collapse=false|off|0 disables.
	collapse := true
	if v := r.URL.Query().Get("collapse"); v == "false" || v == "off" || v == "0" {
		collapse = false
	}
	type chanGuide struct {
		Channel ChannelHeader `json:"channel"`
		Blocks  []GuideBlock  `json:"blocks"`
	}
	out := make([]chanGuide, 0, len(chans))
	for _, c := range chans {
		blocks := byChan[c.ID]
		if collapse {
			blocks = collapseFiller(blocks)
		}
		if blocks == nil {
			blocks = []GuideBlock{}
		}
		out = append(out, chanGuide{Channel: toHeader(c), Blocks: blocks})
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{
		"start":    start.UTC().Format(time.RFC3339),
		"end":      end.UTC().Format(time.RFC3339),
		"channels": out,
	})
}

// ChannelGuide implements GET /api/channels/{id}/guide (AC2) with a
// horizon_until marker.
func (h *Handler) ChannelGuide(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	id := chi.URLParam(r, "id")
	c, err := h.repo().oneChannel(r.Context(), id)
	if err != nil {
		httperror.Write(w, r, httperror.NotFound("channel "+id))
		return
	}
	if !canRead(p, c.LibraryID) {
		httperror.Write(w, r, httperror.Forbidden("", "no access to this channel"))
		return
	}
	start, end, e := h.parseRange(r)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	rows, err := h.repo().programsOverlapping(r.Context(), []string{id}, start, end)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("read programs"))
		return
	}
	now := h.now()
	blocks := make([]GuideBlock, 0, len(rows))
	for _, pr := range rows {
		blocks = append(blocks, toBlock(pr, now))
	}
	if v := r.URL.Query().Get("collapse"); v != "false" && v != "off" && v != "0" {
		blocks = collapseFiller(blocks)
	}
	resp := map[string]any{
		"channel": toHeader(c),
		"blocks":  blocks,
	}
	if hu, ok := h.repo().horizonUntil(r.Context(), id); ok {
		resp["horizon_until"] = hu.UTC().Format(time.RFC3339)
	}
	common.WriteJSON(w, r, http.StatusOK, resp)
}

// Now implements GET /api/channels/now (AC3) — current+next per channel.
func (h *Handler) Now(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	chans, err := h.visibleChannels(r, p, r.URL.Query().Get("category"))
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list channels"))
		return
	}
	now := h.now()
	type nowEntry struct {
		Channel ChannelHeader `json:"channel"`
		Current *GuideBlock   `json:"current"`
		Next    *GuideBlock   `json:"next"`
	}
	out := make([]nowEntry, 0, len(chans))
	for _, c := range chans {
		cur, next, err := h.repo().currentAndNext(r.Context(), c.ID, now)
		if err != nil {
			continue
		}
		e := nowEntry{Channel: toHeader(c)}
		if cur != nil {
			b := toBlock(*cur, now)
			e.Current = &b
		}
		if next != nil {
			b := toBlock(*next, now)
			e.Next = &b
		}
		out = append(out, e)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"channels": out})
}

// ChannelHeader is the channel descriptor embedded in guide payloads.
type ChannelHeader struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
	Name   string `json:"name"`
	Slug   string `json:"slug"`
	Logo   string `json:"logo,omitempty"`
}

func toHeader(c ChannelMeta) ChannelHeader {
	h := ChannelHeader{ID: c.ID, Number: c.Number, Name: c.Name, Slug: c.Slug}
	if c.LogoPath != nil {
		h.Logo = *c.LogoPath
	}
	return h
}

// toBlock maps a channel_programs row to the guide payload, computing
// is_live / progress from the shared clock (AC3/AC7).
func toBlock(p ProgramRow, now time.Time) GuideBlock {
	b := GuideBlock{
		ChannelID: p.ChannelID,
		Kind:      p.Kind,
		Start:     p.StartAt.UTC().Format(time.RFC3339),
		Stop:      p.EndAt.UTC().Format(time.RFC3339),
		Title:     p.Snapshot.Title,
		SubTitle:  p.Snapshot.EpisodeTitle,
		Desc:      p.Snapshot.Description,
		Poster:    p.Snapshot.Poster,
		Genre:     p.Snapshot.Genre,
		Rating:    p.Snapshot.Rating,
		Series:    p.Snapshot.Series,
		Season:    p.Snapshot.Season,
		Episode:   p.Snapshot.Episode,
	}
	if !now.Before(p.StartAt) && now.Before(p.EndAt) {
		b.IsLive = true
		b.Progress = progress(p.StartAt, p.EndAt, now)
	}
	return b
}

// progress is the fraction (0..1) of the block elapsed at `now`.
func progress(start, end, now time.Time) float64 {
	total := end.Sub(start).Seconds()
	if total <= 0 {
		return 0
	}
	f := now.Sub(start).Seconds() / total
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// canRead mirrors the channels handler's ACL rule: admins / all-library
// principals see everything; a multi-library (nil) channel is visible to
// any authenticated user; a library-scoped one needs that library.
func canRead(p *principal.Principal, libraryID *string) bool {
	if p.AccessAllLibraries || p.IsAdmin {
		return true
	}
	if libraryID == nil {
		return true
	}
	return p.HasLibrary(*libraryID)
}

func metaIDs(cs []ChannelMeta) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

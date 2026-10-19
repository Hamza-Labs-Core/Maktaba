package tvchange

// What the Changes page (story 33.11) reads beyond the change rows:
//
//	GET /api/tv/changes/{id}/schedule-diff?page=      the regenerated channels (10 a page)
//	GET /api/tv/changes/{id}/schedule-diff?channel=   one channel: next 24 h before / after
//	GET /api/tv/changes/{id}/boundaries?at=&swap_wait= expected per-channel switch times
//
// and, in every change reply, the names of the users it mentions, its audit rows and,
// for a draft, a summary of what it holds (the full diff exists once it is staged).

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	core "github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
)

// schedulePage is how many channels one schedule-diff page lists (story 33.11 EC1).
const schedulePage = 10

// Detail is a change as the Changes page reads it.
type Detail struct {
	*core.Change
	// Users are the display names of the user ids the change and its events name.
	Users map[string]string `json:"users"`
	// Audit are the change's audit_log rows (story 33.13 AC3).
	Audit []core.AuditRow `json:"audit"`
	// Pending summarises a draft's edits (the computed diff exists once staged).
	Pending *PendingSummary `json:"pending_summary,omitempty"`
	// Release is the release the change builds (version, generation).
	Release *Release `json:"release,omitempty"`
}

// PendingChannel is one channel a draft touches.
type PendingChannel struct {
	ID     string `json:"id"`
	Number int    `json:"number,omitempty"`
	Name   string `json:"name,omitempty"`
	// Op: added, removed, edited, art, regenerate.
	Op string `json:"op"`
}

// PendingSummary is what a draft holds.
type PendingSummary struct {
	Channels []PendingChannel `json:"channels"`
	Settings []string         `json:"settings"`
}

// PageConfig is the part of rules.tv_staging the page shows next to the actions.
type PageConfig struct {
	Enabled            bool              `json:"enabled"`
	RequireSecondAdmin bool              `json:"require_second_admin"`
	SwapWait           string            `json:"swap_wait"`
	BoundaryMaxWaitMin int               `json:"boundary_max_wait_minutes"`
	QuietWindow        string            `json:"quiet_window"`
	QuietMaxViewers    int               `json:"quiet_max_viewers"`
	AutoPromote        map[string]string `json:"auto_promote"`
	KeepWarmMinutes    int               `json:"keep_warm_minutes"`
	RetentionHours     int               `json:"rollback_retention_hours"`
	ParamChangePush    string            `json:"param_change_push"`
}

func pageConfig(c core.Config) PageConfig {
	return PageConfig{Enabled: c.Enabled, RequireSecondAdmin: c.RequireSecondAdmin, SwapWait: c.SwapWait,
		BoundaryMaxWaitMin: c.BoundaryMaxWaitMin, QuietWindow: c.QuietWindow, QuietMaxViewers: c.QuietMaxViewers,
		AutoPromote: c.AutoPromote, KeepWarmMinutes: c.KeepWarmMinutes, RetentionHours: c.RetentionHours,
		ParamChangePush: c.ParamChangePush}
}

// userNames looks the ids up in users (unknown ids are left out).
func userNames(ctx context.Context, db *sql.DB, ids map[string]bool) map[string]string {
	out := map[string]string{}
	for id := range ids {
		if id == "" {
			continue
		}
		var name string
		if err := db.QueryRowContext(ctx, `SELECT username FROM users WHERE id = $1`, id).Scan(&name); err == nil {
			out[id] = name
		}
	}
	return out
}

func (h *Handler) detail(ctx context.Context, c *core.Change) *Detail {
	d := &Detail{Change: c}
	ids := map[string]bool{}
	if c.CreatedBy != nil {
		ids[*c.CreatedBy] = true
	}
	for _, u := range c.ApprovedBy {
		ids[u] = true
	}
	for _, e := range c.Events {
		if e.ActorUserID != nil {
			ids[*e.ActorUserID] = true
		}
	}
	d.Audit, _ = h.Store.AuditRows(ctx, c.ID)
	if d.Audit == nil {
		d.Audit = []core.AuditRow{}
	}
	for _, a := range d.Audit {
		if a.ActorUserID != nil {
			ids[*a.ActorUserID] = true
		}
	}
	d.Users = userNames(ctx, h.DB, ids)
	if c.State == core.StateDraft {
		d.Pending = h.pendingSummary(ctx, c)
	}
	if c.ReleaseID != nil {
		d.Release = h.release(ctx, *c.ReleaseID)
	}
	return d
}

func (h *Handler) release(ctx context.Context, id string) *Release {
	var rl Release
	var kinds []byte
	var base sql.NullString
	var at timeScan
	if err := h.DB.QueryRowContext(ctx, `SELECT id, digest, kinds, sched_gen, streaming_version, base_release_id,
		created_at FROM tv_releases WHERE id = $1`, id).Scan(&rl.ID, &rl.Digest, &kinds, &rl.SchedGen,
		&rl.StreamingVersion, &base, &at); err != nil {
		return nil
	}
	rl.Kinds = []string{}
	decode(kinds, &rl.Kinds)
	if base.Valid {
		v := base.String
		rl.BaseReleaseID = &v
	}
	rl.CreatedAt = at.t
	var live sql.NullString
	_ = h.DB.QueryRowContext(ctx, `SELECT live_release_id FROM tv_routing WHERE scope = 'cluster'`).Scan(&live)
	rl.Live = live.Valid && live.String == rl.ID
	rl.Changes = []string{}
	return &rl
}

// pendingSummary lists the channels and settings sections a draft touches, named from
// the draft's definitions or the live lineup.
func (h *Handler) pendingSummary(ctx context.Context, c *core.Change) *PendingSummary {
	pd := c.Pending()
	out := &PendingSummary{Channels: []PendingChannel{}, Settings: []string{}}
	seen := map[string]int{}
	add := func(id, op string, def map[string]any) {
		pc := PendingChannel{ID: id, Op: op}
		if def != nil {
			if n, ok := def["number"].(float64); ok {
				pc.Number = int(n)
			}
			pc.Name, _ = def["name"].(string)
		}
		if pc.Name == "" {
			var num sql.NullInt64
			var name sql.NullString
			if err := h.DB.QueryRowContext(ctx, `SELECT number, name FROM channels WHERE id = $1`, id).
				Scan(&num, &name); err == nil {
				pc.Number, pc.Name = int(num.Int64), name.String
				if op == "edited" && def != nil {
					if n, ok := def["number"].(float64); ok {
						pc.Number = int(n)
					}
				}
			}
		}
		if i, dup := seen[id]; dup {
			if out.Channels[i].Op == "art" || out.Channels[i].Op == "regenerate" {
				out.Channels[i].Op = op
			}
			return
		}
		seen[id] = len(out.Channels)
		out.Channels = append(out.Channels, pc)
	}
	ids := []string{}
	for id := range pd.Channels() {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		def, removed, _ := pd.Channel(id)
		switch {
		case removed:
			add(id, "removed", nil)
		default:
			var n int
			_ = h.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM channels WHERE id = $1 AND lifecycle = 'live'`, id).Scan(&n)
			if n == 0 {
				add(id, "added", def)
			} else {
				add(id, "edited", def)
			}
		}
	}
	if art, ok := pd.M["art"].(map[string]any); ok {
		aids := []string{}
		for id := range art {
			aids = append(aids, id)
		}
		sort.Strings(aids)
		for _, id := range aids {
			add(id, "art", nil)
		}
	}
	if sched, ok := pd.M["sched"].([]any); ok {
		for _, x := range sched {
			if id, ok := x.(string); ok {
				add(id, "regenerate", nil)
			}
		}
	}
	if st, ok := pd.M["settings"].(map[string]any); ok {
		for k := range st {
			out.Settings = append(out.Settings, k)
		}
		sort.Strings(out.Settings)
	}
	sort.SliceStable(out.Channels, func(i, j int) bool { return out.Channels[i].Number < out.Channels[j].Number })
	return out
}

// ScheduleDiff serves GET /api/tv/changes/{id}/schedule-diff (plan 33.11 D3: computed
// on the server, a page of channels at a time; one channel's programmes with ?channel=).
func (h *Handler) ScheduleDiff(w http.ResponseWriter, r *http.Request) {
	if _, ok := admin(w, r); !ok {
		return
	}
	c, ok := h.load(w, r)
	if !ok {
		return
	}
	chans := c.ScheduleChannels()
	if id := strings.TrimSpace(r.URL.Query().Get("channel")); id != "" {
		var sc *core.ScheduleChannel
		for i := range chans {
			if chans[i].ID == id {
				sc = &chans[i]
			}
		}
		if sc == nil {
			httperror.Write(w, r, httperror.NotFound("channel "+id+" is not regenerated by this change"))
			return
		}
		live, staged, err := h.Store.Gens(r.Context(), c)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("generations: "+err.Error()))
			return
		}
		from := time.Now().UTC()
		if h.Controller != nil && h.Controller.Now != nil {
			from = h.Controller.Now().UTC()
		}
		if sc.DivergenceAt != nil && sc.DivergenceAt.After(from) {
			from = *sc.DivergenceAt
		}
		to := from.Add(24 * time.Hour)
		before, err := h.Store.Programmes(r.Context(), id, live, from, to)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("schedule: "+err.Error()))
			return
		}
		after := []core.Programme{}
		if staged >= 0 {
			if after, err = h.Store.Programmes(r.Context(), id, staged, from, to); err != nil {
				httperror.Write(w, r, httperror.Internal("schedule: "+err.Error()))
				return
			}
		}
		if sc.Reason == "new" {
			before = []core.Programme{}
		}
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"channel": sc, "from": from, "to": to,
			"before": before, "after": after, "staged": staged >= 0,
			"unchanged": staged >= 0 && core.SameProgrammes(before, after)})
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pages := (len(chans) + schedulePage - 1) / schedulePage
	if pages == 0 {
		pages = 1
	}
	lo := (page - 1) * schedulePage
	hi := lo + schedulePage
	if lo > len(chans) {
		lo = len(chans)
	}
	if hi > len(chans) {
		hi = len(chans)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"channels": chans[lo:hi], "page": page,
		"pages": pages, "total": len(chans)})
}

// Boundaries serves GET /api/tv/changes/{id}/boundaries?at=&swap_wait=: when each
// changed channel would switch for a swap at `at` (default now; the change's own
// swap_wait, else the setting).
func (h *Handler) Boundaries(w http.ResponseWriter, r *http.Request) {
	if _, ok := admin(w, r); !ok {
		return
	}
	c, ok := h.load(w, r)
	if !ok {
		return
	}
	cfg, err := core.LoadConfig(r.Context(), h.DB)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tv_staging rules: "+err.Error()))
		return
	}
	at := time.Now().UTC()
	if h.Controller != nil && h.Controller.Now != nil {
		at = h.Controller.Now().UTC()
	}
	if s := r.URL.Query().Get("at"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			httperror.Write(w, r, httperror.InvalidQuery("at must be RFC 3339"))
			return
		}
		at = t.UTC()
	}
	wait := cfg.SwapWait
	if c.SwapWait != nil {
		wait = *c.SwapWait
	}
	if s := r.URL.Query().Get("swap_wait"); s == "boundary" || s == "immediate" {
		wait = s
	}
	maxWait := time.Duration(cfg.BoundaryMaxWaitMin) * time.Minute
	bs, err := h.Store.Boundaries(r.Context(), c, at, wait, maxWait)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("boundaries: "+err.Error()))
		return
	}
	var last *time.Time
	for i := range bs {
		if last == nil || bs[i].SwitchAt.After(*last) {
			t := bs[i].SwitchAt
			last = &t
		}
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"at": at, "swap_wait": wait,
		"boundary_max_wait_minutes": cfg.BoundaryMaxWaitMin, "channels": bs, "last_switch_at": last})
}

func mountPage(r chi.Router, h *Handler) {
	r.Get("/api/tv/changes/{id}/schedule-diff", h.ScheduleDiff)
	r.Get("/api/tv/changes/{id}/boundaries", h.Boundaries)
}

package filler

// TV staging (Epic 33, story 33.12 AC6): with rules.tv_staging.enabled a filler
// pool or item edit goes into the channel editor's open draft change, like a
// channel or art save (channels/staging_writes.go): nothing on the air changes
// until the change is approved and swapped (the swap applies pending["filler"],
// pipeline tvrelease/commit.py). The list shows the pools as the draft will have
// them, each touched pool naming its draft. With staging off the handlers write
// the tables at once, as before.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
)

// Draft names the draft change that holds a pool's pending edit.
type Draft struct {
	ChangeID string `json:"change_id"`
	Number   int64  `json:"number"`
	// Op: added | edited | removed (the pool), items (only its clips changed).
	Op string `json:"op"`
}

func (h *Handler) staging(ctx context.Context) bool { return h.Drafts != nil && h.Drafts.Enabled(ctx) }

// editorDraft is the open editor draft (nil when none).
func (h *Handler) editorDraft(ctx context.Context) *tvchange.Change {
	if h.Drafts == nil {
		return nil
	}
	c, err := h.Drafts.Open(ctx)
	if err != nil {
		return nil
	}
	return c
}

func (h *Handler) editorPending(ctx context.Context) *tvchange.Pending {
	if dr := h.editorDraft(ctx); dr != nil {
		return dr.Pending()
	}
	return &tvchange.Pending{}
}

func (h *Handler) saveDraft(w http.ResponseWriter, r *http.Request, what string,
	fn func(pd *tvchange.Pending) error) (tvchange.DraftRef, bool) {
	ref, err := h.Drafts.Save(r.Context(), principal.FromContext(r.Context()), tvchange.SourceEditor,
		"Channel editor", what, fn)
	if err != nil {
		var he *httperror.Error
		if errors.As(err, &he) {
			httperror.Write(w, r, he)
			return ref, false
		}
		httperror.Write(w, r, httperror.Internal("save to draft change: "+err.Error()))
		return ref, false
	}
	return ref, true
}

// livePool is a filler_pools row (portable SQL: the draft path also runs on SQLite).
func (h *Handler) livePool(ctx context.Context, id string) (Pool, bool, error) {
	var p Pool
	var ch, key sql.NullString
	err := h.DB.QueryRowContext(ctx, `SELECT id, channel_id, name, external_key FROM filler_pools WHERE id = $1`, id).
		Scan(&p.ID, &ch, &p.Name, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return p, false, nil
	}
	if err != nil {
		return p, false, err
	}
	p.ChannelID, p.ExternalKey, p.Items = ch.String, key.String, []Item{}
	return p, true, nil
}

// liveView is what a draft edit of one pool reads from the live tables, read
// before the draft's transaction (the save runs fn inside it).
type liveView struct {
	pool     Pool
	exists   bool
	items    [][2]string // (item id, video id)
	channels []string    // live channels whose definition names the pool
}

func (h *Handler) readLive(ctx context.Context, poolID string) (liveView, error) {
	var v liveView
	var err error
	if v.pool, v.exists, err = h.livePool(ctx, poolID); err != nil {
		return v, err
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT id, video_id FROM filler_items WHERE pool_id = $1`, poolID)
	if err != nil {
		return v, err
	}
	for rows.Next() {
		var iid, vid string
		if err := rows.Scan(&iid, &vid); err != nil {
			rows.Close()
			return v, err
		}
		v.items = append(v.items, [2]string{iid, vid})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return v, err
	}
	for _, ref := range v.refs(poolID) {
		rows, err := h.DB.QueryContext(ctx, `SELECT id FROM channels WHERE CAST(mode_config AS TEXT) LIKE $1 ESCAPE '!'`,
			`%"`+escapeLike(ref)+`"%`)
		if err != nil {
			return v, err
		}
		for rows.Next() {
			var c string
			if rows.Scan(&c) == nil {
				v.channels = append(v.channels, c)
			}
		}
		rows.Close()
	}
	return v, nil
}

// refs are the names a channel definition gives the pool: its id, and a plan
// pool's key.
func (v liveView) refs(id string) []string {
	if v.pool.ExternalKey != "" {
		return []string{id, v.pool.ExternalKey}
	}
	return []string{id}
}

// asDrafted is the pool as the draft will have it: (pool, exists).
func (v liveView) asDrafted(pd *tvchange.Pending, id string) (Pool, bool) {
	if x, ok := pd.FillerPools()[id]; ok {
		d, _ := x.(map[string]any)
		if d == nil {
			return Pool{}, false // removed in the draft
		}
		p := v.pool
		if !v.exists {
			p = Pool{ID: id, Items: []Item{}}
		}
		applyPoolDef(&p, d)
		return p, true
	}
	return v.pool, v.exists
}

// sched are the channels whose schedule a change of the pool touches: the channels
// it is made for (before and after) and every channel whose definition names it (a
// filler list, a slot's filler pool), live or as the draft has it.
func (v liveView) sched(pd *tvchange.Pending, id string, tied ...string) []string {
	set := map[string]bool{}
	for _, c := range append(tied, v.channels...) {
		if c != "" {
			set[c] = true
		}
	}
	for cid, def := range pd.Channels() {
		if def == nil {
			continue
		}
		b, _ := json.Marshal(def)
		for _, ref := range v.refs(id) {
			if strings.Contains(string(b), `"`+ref+`"`) {
				set[cid] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// videos are the videos the pool holds as the draft will have it.
func (v liveView) videos(pd *tvchange.Pending, poolID string) map[string]bool {
	out := map[string]bool{}
	items := pd.FillerItems()
	for _, it := range v.items {
		if x, ok := items[it[0]]; ok && x == nil {
			continue
		}
		out[it[1]] = true
	}
	for _, x := range items {
		if d, _ := x.(map[string]any); d != nil && d["pool_id"] == poolID {
			if s, _ := d["video_id"].(string); s != "" {
				out[s] = true
			}
		}
	}
	return out
}

func applyPoolDef(p *Pool, d map[string]any) {
	if s, ok := d["name"].(string); ok {
		p.Name = s
	}
	if _, ok := d["channel_id"]; ok {
		p.ChannelID, _ = d["channel_id"].(string)
	}
}

// escapeLike escapes a LIKE pattern for ESCAPE '!'.
func escapeLike(s string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(s)
}

func nullable(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.TrimSpace(s)
}

func writeLiveErr(w http.ResponseWriter, r *http.Request, err error) {
	httperror.Write(w, r, httperror.Internal("load filler pool: "+err.Error()))
}

// createPoolDraft is CreatePool while staging is on.
func (h *Handler) createPoolDraft(w http.ResponseWriter, r *http.Request, req CreatePoolRequest) {
	id := uuid.NewString()
	ref, ok := h.saveDraft(w, r, "filler_pool_create", func(pd *tvchange.Pending) error {
		pd.SetFillerPool(id, map[string]any{"name": req.Name, "channel_id": nullable(req.ChannelID), "created": true})
		if ch := strings.TrimSpace(req.ChannelID); ch != "" {
			pd.AddSched(ch) // a hand-made pool tied to a channel joins its filler
		}
		return nil
	})
	if !ok {
		return
	}
	common.WriteJSON(w, r, http.StatusCreated, Pool{ID: id, ChannelID: req.ChannelID, Name: req.Name, Items: []Item{},
		Draft: &Draft{ChangeID: ref.ID, Number: ref.Number, Op: "added"}})
}

// updatePoolDraft is UpdatePool while staging is on.
func (h *Handler) updatePoolDraft(w http.ResponseWriter, r *http.Request, id string, req UpdatePoolRequest) {
	lv, err := h.readLive(r.Context(), id)
	if err != nil {
		writeLiveErr(w, r, err)
		return
	}
	var out Pool
	op := "edited"
	ref, ok := h.saveDraft(w, r, "filler_pool_update", func(pd *tvchange.Pending) error {
		p, exists := lv.asDrafted(pd, id)
		if !exists {
			return httperror.NotFound("filler pool " + id)
		}
		before := p.ChannelID
		def := map[string]any{"name": p.Name, "channel_id": nullable(p.ChannelID)}
		if x, _ := pd.FillerPools()[id].(map[string]any); x != nil && x["created"] == true {
			def["created"], op = true, "added"
		}
		if req.Name != nil {
			def["name"] = strings.TrimSpace(*req.Name)
		}
		if req.ChannelID != nil {
			def["channel_id"] = nullable(*req.ChannelID)
		}
		pd.SetFillerPool(id, def)
		applyPoolDef(&p, def)
		if before != p.ChannelID {
			if ids := lv.sched(pd, id, before, p.ChannelID); len(ids) > 0 {
				pd.AddSched(ids...)
			}
		}
		out = p
		return nil
	})
	if !ok {
		return
	}
	out.Draft = &Draft{ChangeID: ref.ID, Number: ref.Number, Op: op}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// deletePoolDraft is DeletePool while staging is on.
func (h *Handler) deletePoolDraft(w http.ResponseWriter, r *http.Request, id string) {
	lv, err := h.readLive(r.Context(), id)
	if err != nil {
		writeLiveErr(w, r, err)
		return
	}
	ref, ok := h.saveDraft(w, r, "filler_pool_delete", func(pd *tvchange.Pending) error {
		p, exists := lv.asDrafted(pd, id)
		if !exists {
			return httperror.NotFound("filler pool " + id)
		}
		if ids := lv.sched(pd, id, p.ChannelID, lv.pool.ChannelID); len(ids) > 0 {
			pd.AddSched(ids...)
		}
		pd.DropFillerPool(id) // the draft's own additions to it go with it
		if lv.exists {
			pd.RemoveFillerPool(id)
		}
		return nil
	})
	if !ok {
		return
	}
	common.WriteJSON(w, r, http.StatusAccepted, map[string]any{"pool_id": id, "draft_change": ref})
}

// addItemsDraft is AddItems while staging is on: a clip the pool already has (live
// or in the draft) is a no-op, as the live insert is.
func (h *Handler) addItemsDraft(w http.ResponseWriter, r *http.Request, poolID string, req AddItemsRequest) {
	lv, err := h.readLive(r.Context(), poolID)
	if err != nil {
		writeLiveErr(w, r, err)
		return
	}
	var created []Item
	ref, ok := h.saveDraft(w, r, "filler_items_add", func(pd *tvchange.Pending) error {
		created = []Item{}
		p, exists := lv.asDrafted(pd, poolID)
		if !exists {
			return httperror.NotFound("filler pool " + poolID)
		}
		have := lv.videos(pd, poolID)
		for _, in := range req.Items {
			vid, kind := strings.TrimSpace(in.VideoID), strings.TrimSpace(in.Type)
			if kind == "" {
				kind = "filler"
			}
			if have[vid] {
				continue
			}
			have[vid] = true
			id := uuid.NewString()
			pd.SetFillerItem(id, map[string]any{"pool_id": poolID, "video_id": vid, "type": kind})
			created = append(created, Item{ID: id, PoolID: poolID, VideoID: vid, Type: kind, Pending: true})
		}
		if ids := lv.sched(pd, poolID, p.ChannelID); len(ids) > 0 {
			pd.AddSched(ids...)
		}
		return nil
	})
	if !ok {
		return
	}
	common.WriteJSON(w, r, http.StatusCreated, map[string]any{"items": created, "draft_change": ref})
}

// deleteItemDraft is DeleteItem while staging is on.
func (h *Handler) deleteItemDraft(w http.ResponseWriter, r *http.Request, id string) {
	// the item's pool: a live row's, or one the open draft added it to
	var livePoolID string
	err := h.DB.QueryRowContext(r.Context(), `SELECT pool_id FROM filler_items WHERE id = $1`, id).Scan(&livePoolID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeLiveErr(w, r, err)
		return
	}
	poolID := livePoolID
	if poolID == "" {
		if d, _ := h.editorPending(r.Context()).FillerItems()[id].(map[string]any); d != nil {
			poolID, _ = d["pool_id"].(string)
		}
	}
	if poolID == "" {
		httperror.Write(w, r, httperror.NotFound("filler item "+id))
		return
	}
	lv, err := h.readLive(r.Context(), poolID)
	if err != nil {
		writeLiveErr(w, r, err)
		return
	}
	ref, ok := h.saveDraft(w, r, "filler_item_delete", func(pd *tvchange.Pending) error {
		x, inDraft := pd.FillerItems()[id]
		switch {
		case inDraft && x == nil:
			return httperror.NotFound("filler item " + id) // already removed
		case inDraft:
			pd.DropFillerItem(id)
		case livePoolID != "":
			pd.RemoveFillerItem(id)
		default:
			return httperror.NotFound("filler item " + id)
		}
		p, _ := lv.asDrafted(pd, poolID)
		if ids := lv.sched(pd, poolID, p.ChannelID); len(ids) > 0 {
			pd.AddSched(ids...)
		}
		return nil
	})
	if !ok {
		return
	}
	common.WriteJSON(w, r, http.StatusAccepted, map[string]any{"item_id": id, "draft_change": ref})
}

// project is the live pools with the open draft's filler edits over them.
func (h *Handler) project(ctx context.Context, live []Pool, dr *tvchange.Change) []Pool {
	if dr == nil {
		return live
	}
	pd := dr.Pending()
	pools, items := pd.FillerPools(), pd.FillerItems()
	if len(pools) == 0 && len(items) == 0 {
		return live
	}
	mark := func(op string) *Draft { return &Draft{ChangeID: dr.ID, Number: dr.Number, Op: op} }
	out := []Pool{}
	byID := map[string]int{}
	for _, p := range live {
		if v, ok := pools[p.ID]; ok {
			d, _ := v.(map[string]any)
			if d == nil {
				continue // removed in the draft
			}
			applyPoolDef(&p, d)
			p.Draft = mark("edited")
		}
		kept := []Item{}
		for _, it := range p.Items {
			if v, ok := items[it.ID]; ok && v == nil {
				if p.Draft == nil {
					p.Draft = mark("items")
				}
				continue
			}
			kept = append(kept, it)
		}
		p.Items = kept
		byID[p.ID] = len(out)
		out = append(out, p)
	}
	ids := make([]string, 0, len(pools))
	for id := range pools {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		d, _ := pools[id].(map[string]any)
		if d == nil || d["created"] != true {
			continue
		}
		if _, dup := byID[id]; dup {
			continue
		}
		p := Pool{ID: id, Items: []Item{}, Draft: mark("added")}
		applyPoolDef(&p, d)
		byID[id] = len(out)
		out = append(out, p)
	}
	itemIDs := make([]string, 0, len(items))
	for id := range items {
		itemIDs = append(itemIDs, id)
	}
	sort.Strings(itemIDs)
	for _, iid := range itemIDs {
		d, _ := items[iid].(map[string]any)
		if d == nil {
			continue
		}
		pid, _ := d["pool_id"].(string)
		i, ok := byID[pid]
		if !ok {
			continue
		}
		it := Item{ID: iid, PoolID: pid, Pending: true}
		it.VideoID, _ = d["video_id"].(string)
		it.Type, _ = d["type"].(string)
		var title sql.NullString
		var dur sql.NullFloat64
		if h.DB.QueryRowContext(ctx, `SELECT title, duration_sec FROM videos WHERE id = $1`, it.VideoID).Scan(&title, &dur) == nil {
			it.Title = title.String
			if dur.Valid {
				ms := int64(dur.Float64 * 1000)
				it.DurationMS = &ms
			}
		}
		out[i].Items = append(out[i].Items, it)
		if out[i].Draft == nil {
			out[i].Draft = mark("items")
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

// listDraft is ListPools while staging is on and a draft holds filler edits.
func (h *Handler) listDraft(w http.ResponseWriter, r *http.Request, channelID string, live []Pool, dr *tvchange.Change) {
	all := h.project(r.Context(), live, dr)
	if channelID != "" {
		kept := []Pool{}
		for _, p := range all {
			if p.ChannelID == channelID {
				kept = append(kept, p)
			}
		}
		all = kept
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": all})
}

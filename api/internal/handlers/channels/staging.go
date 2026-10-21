package channels

// Epic 33, story 33.12 AC6: with TV staging on (rules.tv_staging.enabled), the channel
// editor never changes the air. Every save - a definition, a new channel, a removal, a
// renumbering, the strip, the art - goes into the open editor draft (a TV change,
// api/internal/tvchange) as the channel's full definition as it will be; the editor
// reads the draft's values back (the channel carries `draft`), and the swap of that
// change puts it on the air. Art files are still written at once (content-addressed,
// so they never replace what is on the air) but an old one is no longer removed: the
// release on the air may still show it (the GC of story 33.3 owns removals).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
)

// ChannelDraft says a channel is shown as the open editor draft has it.
type ChannelDraft struct {
	ChangeID string `json:"change_id"`
	Number   int64  `json:"number"`
	Added    bool   `json:"added,omitempty"`
	Removed  bool   `json:"removed,omitempty"`
}

// staging reports whether editor saves go to a draft now.
func (h *Handler) staging(ctx context.Context) bool {
	return h.Drafts != nil && h.Drafts.Enabled(ctx)
}

// defOf is a channel as a release definition (the channels row; pipeline
// tvrelease/snapshot.py DEF_COLUMNS).
func defOf(c Channel) map[string]any {
	d := map[string]any{
		"id": c.ID, "library_id": nil, "number": c.Number, "name": c.Name, "slug": c.Slug,
		"logo_path": nil, "category": c.Category, "mode": c.Mode, "mode_config": decodeMap(c.ModeConfig),
		"source_filter": nil, "transition": c.Transition, "enabled": c.Enabled, "sort_order": c.SortOrder,
	}
	if c.LibraryID != nil {
		d["library_id"] = *c.LibraryID
	}
	if c.LogoPath != nil && strings.TrimSpace(*c.LogoPath) != "" {
		d["logo_path"] = *c.LogoPath
	}
	if len(c.SourceFilter) > 0 && string(c.SourceFilter) != "null" {
		var sf any
		if json.Unmarshal(c.SourceFilter, &sf) == nil {
			d["source_filter"] = sf
		}
	}
	return d
}

// withDef lays a definition over c.
func withDef(c Channel, d map[string]any) Channel {
	if v, ok := d["id"].(string); ok && v != "" {
		c.ID = v
	}
	c.LibraryID = nil
	if v, ok := d["library_id"].(string); ok && v != "" {
		c.LibraryID = &v
	}
	if v, ok := d["number"].(float64); ok {
		c.Number = int(v)
	} else if v, ok := d["number"].(int); ok {
		c.Number = v
	}
	if v, ok := d["name"].(string); ok {
		c.Name = v
	}
	if v, ok := d["slug"].(string); ok {
		c.Slug = v
	}
	c.LogoPath = nil
	if v, ok := d["logo_path"].(string); ok && v != "" {
		c.LogoPath = &v
	}
	if v, ok := d["category"].(string); ok {
		c.Category = v
	}
	if v, ok := d["mode"].(string); ok {
		c.Mode = v
	}
	if v, ok := d["mode_config"]; ok {
		if b, err := json.Marshal(v); err == nil && string(b) != "null" {
			c.ModeConfig = b
		}
	}
	c.SourceFilter = nil
	if v, ok := d["source_filter"]; ok && v != nil {
		if b, err := json.Marshal(v); err == nil {
			c.SourceFilter = b
		}
	}
	if v, ok := d["transition"].(string); ok {
		c.Transition = v
	}
	if v, ok := d["enabled"].(bool); ok {
		c.Enabled = v
	}
	if v, ok := d["sort_order"].(float64); ok {
		c.SortOrder = int(v)
	} else if v, ok := d["sort_order"].(int); ok {
		c.SortOrder = v
	}
	return withArt(withLogoURL(c))
}

// roundTrip normalises a definition through JSON (numbers become float64, as a
// definition read back from the draft has them).
func roundTrip(d map[string]any) map[string]any {
	b, _ := json.Marshal(d)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return out
}

// editorDraft is the open editor draft (nil when none or staging is off).
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

// overlay is c as the open draft has it (unchanged when the draft does not name it).
func overlay(dr *tvchange.Change, c Channel) Channel {
	if dr == nil {
		return c
	}
	d, removed, ok := dr.Pending().Channel(c.ID)
	if !ok {
		return c
	}
	ref := &ChannelDraft{ChangeID: dr.ID, Number: dr.Number, Removed: removed}
	if !removed && d != nil {
		c = withDef(c, d)
	}
	c.Draft = ref
	return c
}

// draftAdded are the channels the open draft adds (ids the live lineup lacks).
func draftAdded(dr *tvchange.Change, live map[string]bool) []Channel {
	if dr == nil {
		return nil
	}
	out := []Channel{}
	for id, v := range dr.Pending().Channels() {
		d, ok := v.(map[string]any)
		if !ok || live[id] {
			continue
		}
		c := withDef(Channel{ID: id, CreatedAt: dr.CreatedAt, UpdatedAt: dr.UpdatedAt}, d)
		c.Draft = &ChannelDraft{ChangeID: dr.ID, Number: dr.Number, Added: true}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

// loadEffective is a channel as the editor sees it while staging is on: the live row
// with the draft over it, or a channel the draft adds. ok false: answered (404/500).
func (h *Handler) loadEffective(w http.ResponseWriter, r *http.Request, id string) (Channel, *tvchange.Change, bool) {
	dr := h.editorDraft(r.Context())
	c, err := h.repo().get(r.Context(), id)
	if err == nil {
		c = overlay(dr, c)
		if c.Draft != nil && c.Draft.Removed {
			httperror.Write(w, r, httperror.NotFound("channel "+id+" (removed in draft change)"))
			return Channel{}, nil, false
		}
		return c, dr, true
	}
	for _, a := range draftAdded(dr, map[string]bool{}) {
		if a.ID == id {
			return a, dr, true
		}
	}
	httperror.Write(w, r, httperror.NotFound("channel "+id))
	return Channel{}, nil, false
}

// saveDraft runs fn on the open editor draft and answers the error, if any.
func (h *Handler) saveDraft(w http.ResponseWriter, r *http.Request, what string,
	fn func(pd *tvchange.Pending) error) (tvchange.DraftRef, bool) {
	p := principal.FromContext(r.Context())
	ref, err := h.Drafts.Save(r.Context(), p, tvchange.SourceEditor, "Channel editor", what, fn)
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

// projected is the lineup as it will be after the open draft: id -> channel.
func (h *Handler) projected(ctx context.Context, dr *tvchange.Change) map[string]Channel {
	out := map[string]Channel{}
	all, err := h.repo().list(ctx, listFilter{})
	if err != nil {
		return out
	}
	live := map[string]bool{}
	for _, c := range all {
		live[c.ID] = true
		c = overlay(dr, c)
		if c.Draft != nil && c.Draft.Removed {
			continue
		}
		out[c.ID] = c
	}
	for _, c := range draftAdded(dr, live) {
		out[c.ID] = c
	}
	return out
}

// conflictsIn reports a number or slug used twice in a scope of the projected lineup.
func conflictsIn(lineup map[string]Channel) string {
	nums, slugs := map[string]string{}, map[string]string{}
	for id, c := range lineup {
		scope := ""
		if c.LibraryID != nil {
			scope = *c.LibraryID
		}
		nk := scope + "#" + itoa(c.Number)
		if o, dup := nums[nk]; dup && o != id {
			return "number " + itoa(c.Number)
		}
		nums[nk] = id
		sk := scope + "#" + c.Slug
		if o, dup := slugs[sk]; dup && o != id {
			return "slug " + c.Slug
		}
		slugs[sk] = id
	}
	return ""
}

func numberConflict(what string) *httperror.Error {
	return httperror.Conflict("https://maktaba.dev/problems/channel-number-exists",
		"the draft would have two channels with "+what)
}

// putDef saves c's definition into the draft after checking the projected lineup
// stays unique; edited marks it edited by a person at the swap.
func (h *Handler) putDef(w http.ResponseWriter, r *http.Request, c Channel, what string, edited bool) (tvchange.DraftRef, bool) {
	return h.saveDraft(w, r, what, func(pd *tvchange.Pending) error {
		pd.SetChannel(c.ID, roundTrip(defOf(c)))
		dr := &tvchange.Change{Diff: map[string]any{"pending": pd.M}}
		if msg := conflictsIn(h.projected(r.Context(), dr)); msg != "" {
			return numberConflict(msg)
		}
		if edited {
			pd.AddEdited(c.ID)
		}
		return nil
	})
}

// writeDraftChannel answers with c as the draft now has it.
func (h *Handler) writeDraftChannel(w http.ResponseWriter, r *http.Request, id string, status int) {
	c, _, ok := h.loadEffective(w, r, id)
	if !ok {
		return
	}
	h.attachDraftOverrides(r.Context(), &c)
	common.WriteJSON(w, r, status, c)
}

// attachDraftOverrides sets the Overridden flags from the live overrides with the
// draft's art over them.
func (h *Handler) attachDraftOverrides(ctx context.Context, c *Channel) {
	kinds := h.overriddenKinds(ctx, []string{c.ID})[c.ID]
	if kinds == nil {
		kinds = map[string]bool{}
	}
	if dr := h.editorDraft(ctx); dr != nil {
		for kind, o := range dr.Pending().Art(c.ID) {
			kinds[kind] = o != nil
		}
	}
	applyOverrideFlags(c, kinds)
}

// currentOverride is kind's hand-set art of c as the draft will have it.
func (h *Handler) currentOverride(ctx context.Context, pd *tvchange.Pending, c Channel, kind string) (artOverride, error) {
	if a := pd.Art(c.ID); a != nil {
		if v, ok := a[kind]; ok {
			o := artOverride{kind: kind}
			m, _ := v.(map[string]any)
			if m == nil {
				return o, nil // reverted in the draft
			}
			o.exists = true
			if p, ok := m["path"].(string); ok && p != "" {
				o.path.String, o.path.Valid = p, true
			}
			o.settings, _ = m["settings"].(map[string]any)
			if b, ok := m["base"]; ok && b != nil {
				o.base, _ = json.Marshal(b)
			}
			return o, nil
		}
	}
	return h.loadOverride(ctx, h.DB, c.ID, kind)
}

// effectiveOn puts value as kind's effective art on c (in memory: writeEffective's
// twin for a draft).
func effectiveOn(c Channel, kind string, value any) (Channel, error) {
	if kind == ArtLogo {
		c.LogoPath = nil
		if s, ok := value.(string); ok && s != "" {
			c.LogoPath = &s
		}
		return withArt(withLogoURL(c)), nil
	}
	key := "loading_path"
	if kind == ArtWatermark {
		key = "watermark"
	}
	if s, ok := value.(string); ok && s == "" {
		value = nil
	}
	mc, err := setLookKey(c.ModeConfig, key, value)
	if err != nil {
		return c, err
	}
	c.ModeConfig = json.RawMessage(mc)
	return withArt(c), nil
}

// setOverrideDraft is setOverride for a draft.
func (h *Handler) setOverrideDraft(w http.ResponseWriter, r *http.Request, c Channel, kind string,
	path *string, settings map[string]any) bool {
	_, ok := h.saveDraft(w, r, "art_"+kind, func(pd *tvchange.Pending) error {
		o, err := h.currentOverride(r.Context(), pd, c, kind)
		if err != nil {
			return err
		}
		var base any
		if !o.exists {
			base = effectiveArtValue(c, kind)
		} else if len(o.base) > 0 {
			_ = json.Unmarshal(o.base, &base)
		}
		row := map[string]any{"path": nil, "settings": nil, "base": base}
		if o.path.Valid {
			row["path"] = o.path.String
		}
		if o.settings != nil {
			row["settings"] = o.settings
		}
		if path != nil {
			row["path"] = *path
		}
		if settings != nil {
			row["settings"] = settings
		}
		var value any
		switch kind {
		case ArtWatermark:
			value = watermarkWith(c, path, settings)
		default:
			if path != nil {
				value = *path
			} else if p, ok := row["path"].(string); ok {
				value = p
			}
		}
		c2, err := effectiveOn(c, kind, value)
		if err != nil {
			return err
		}
		pd.SetArt(c.ID, kind, roundTrip(row))
		pd.SetChannel(c.ID, roundTrip(defOf(c2)))
		return nil
	})
	return ok
}

// revertArtDraft is revertArt for a draft.
func (h *Handler) revertArtDraft(w http.ResponseWriter, r *http.Request, c Channel, kind string) bool {
	target, owned := h.planArt(r.Context(), c.ID, kind)
	_, ok := h.saveDraft(w, r, "art_revert_"+kind, func(pd *tvchange.Pending) error {
		if !owned {
			target = nil
			o, err := h.currentOverride(r.Context(), pd, c, kind)
			if err != nil {
				return err
			}
			if len(o.base) > 0 {
				var b any
				if json.Unmarshal(o.base, &b) == nil {
					target = b
				}
			}
		}
		c2, err := effectiveOn(c, kind, target)
		if err != nil {
			return err
		}
		pd.SetArt(c.ID, kind, nil)
		pd.SetChannel(c.ID, roundTrip(defOf(c2)))
		return nil
	})
	return ok
}

// listWithDraft lays the open draft over a list an admin reads: changed channels as
// they will be, removed ones flagged, added ones appended.
func (h *Handler) listWithDraft(ctx context.Context, items []Channel) []Channel {
	dr := h.editorDraft(ctx)
	if dr == nil {
		return items
	}
	live := map[string]bool{}
	for i := range items {
		live[items[i].ID] = true
		np := items[i].NowPlaying
		items[i] = overlay(dr, items[i])
		items[i].NowPlaying = np
	}
	return append(items, draftAdded(dr, live)...)
}

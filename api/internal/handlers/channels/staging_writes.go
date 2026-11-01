package channels

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
)

// createDraft is Create while staging is on: the new channel goes into the draft
// (number and slug unique in the lineup as the draft will have it).
func (h *Handler) createDraft(w http.ResponseWriter, r *http.Request, req CreateRequest, c Channel) {
	lineup := h.projected(r.Context(), h.editorDraft(r.Context()))
	scope := func(x Channel) string {
		if x.LibraryID != nil {
			return *x.LibraryID
		}
		return ""
	}
	mine := scope(c)
	usedSlug, maxNum := map[string]bool{}, 0
	for _, x := range lineup {
		if scope(x) != mine {
			continue
		}
		usedSlug[x.Slug] = true
		if x.Number > maxNum {
			maxNum = x.Number
		}
	}
	base := Slugify(req.Name)
	c.Slug = base + "-" + c.ID[:8]
	for n := 1; n <= 1000; n++ {
		if cand := SlugWithSuffix(base, n); !usedSlug[cand] {
			c.Slug = cand
			break
		}
	}
	if req.Number != nil {
		c.Number = *req.Number
	} else {
		c.Number = maxNum + 1
	}
	c = withArt(withLogoURL(c))
	if _, ok := h.putDef(w, r, c, "create", false); !ok {
		return
	}
	h.writeDraftChannel(w, r, c.ID, http.StatusCreated)
}

// patchDraft is Patch while staging is on.
func (h *Handler) patchDraft(w http.ResponseWriter, r *http.Request, existing Channel, req PatchRequest,
	logoChange bool, newLogo string) {
	c := existing
	def := false
	if req.Name != nil {
		c.Name, def = strings.TrimSpace(*req.Name), true
	}
	if req.Number != nil {
		c.Number, def = *req.Number, true
	}
	if req.Category != nil {
		c.Category, def = *req.Category, true
	}
	if req.Mode != nil {
		c.Mode, def = *req.Mode, true
	}
	if len(req.ModeConfig) > 0 {
		mc, err := keepLook(req.ModeConfig, existing.ModeConfig)
		if err != nil {
			httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "mode_config", Message: "must be a JSON object"}}))
			return
		}
		c.ModeConfig, def = json.RawMessage(mc), true
	}
	if len(req.SourceFilter) > 0 {
		c.SourceFilter, def = req.SourceFilter, true
	}
	if req.Transition != nil {
		c.Transition, def = *req.Transition, true
	}
	if req.Enabled != nil {
		c.Enabled, def = *req.Enabled, true
	}
	if req.SortOrder != nil {
		c.SortOrder, def = *req.SortOrder, true
	}
	c = withArt(withLogoURL(c))
	if def {
		if _, ok := h.putDef(w, r, c, "patch", true); !ok {
			return
		}
	}
	if logoChange {
		var ok bool
		if newLogo == "" {
			ok = h.revertArtDraft(w, r, c, ArtLogo)
		} else {
			ok = h.setOverrideDraft(w, r, c, ArtLogo, &newLogo, nil)
		}
		if !ok {
			return
		}
	}
	h.writeDraftChannel(w, r, c.ID, http.StatusOK)
}

// reorderDraft is Reorder while staging is on.
func (h *Handler) reorderDraft(w http.ResponseWriter, r *http.Request, entries []ReorderEntry) {
	lineup := h.projected(r.Context(), h.editorDraft(r.Context()))
	moved := []string{}
	for _, e := range entries {
		c, ok := lineup[e.ID]
		if !ok {
			httperror.Write(w, r, httperror.NotFound("channel "+e.ID))
			return
		}
		if c.Number != e.Number {
			c.Number = e.Number
			lineup[e.ID] = c
			moved = append(moved, e.ID)
		}
	}
	if msg := conflictsIn(lineup); msg != "" {
		httperror.Write(w, r, httperror.Conflict("https://maktaba.tv/problems/channel-number-exists",
			"reorder would collide on a channel "+msg))
		return
	}
	ref, ok := h.saveDraft(w, r, "reorder", func(pd *tvchange.Pending) error {
		for _, id := range moved {
			pd.SetChannel(id, roundTrip(defOf(lineup[id])))
		}
		if len(moved) > 0 {
			pd.AddEdited(moved...)
		}
		return nil
	})
	if !ok {
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"reordered": len(entries), "draft_change": ref})
}

// watermarkDraft is ArtSettings while staging is on: the settings merge over the
// draft's (or the live) watermark override.
func (h *Handler) watermarkDraft(w http.ResponseWriter, r *http.Request, c Channel, settings map[string]any) bool {
	cur := &tvchange.Pending{}
	if dr := h.editorDraft(r.Context()); dr != nil {
		cur = dr.Pending()
	}
	o, err := h.currentOverride(r.Context(), cur, c, ArtWatermark)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load channel art").Wrap(err))
		return false
	}
	merged := map[string]any{}
	for k, v := range o.settings {
		merged[k] = v
	}
	for k, v := range settings {
		merged[k] = v
	}
	return h.setOverrideDraft(w, r, c, ArtWatermark, nil, merged)
}

// regenerateDraft is Regenerate while staging is on: a forced regeneration is a
// schedule change (story 33.12 AC7), so it goes into the draft.
func (h *Handler) regenerateDraft(w http.ResponseWriter, r *http.Request, id string) {
	if _, _, ok := h.loadEffective(w, r, id); !ok {
		return
	}
	ref, ok := h.saveDraft(w, r, "regenerate", func(pd *tvchange.Pending) error {
		pd.AddSched(id)
		return nil
	})
	if !ok {
		return
	}
	common.WriteJSON(w, r, http.StatusAccepted, map[string]any{
		"channel_id": id, "status": "regeneration-drafted", "draft_change": ref,
	})
}

// stripDraft is writeStrip while staging is on.
func (h *Handler) stripDraft(r *http.Request, c Channel, mc string) error {
	c.ModeConfig = json.RawMessage(mc)
	_, err := h.Drafts.Save(r.Context(), principalOf(r), tvchange.SourceEditor, "Channel editor", "strip",
		func(pd *tvchange.Pending) error {
			pd.SetChannel(c.ID, roundTrip(defOf(c)))
			return nil
		})
	return err
}

func principalOf(r *http.Request) *principal.Principal { return principal.FromContext(r.Context()) }

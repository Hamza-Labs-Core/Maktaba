package watch

import (
	"net/http"
	"sort"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

const (
	defaultActivityLimit = 50
	maxActivityLimit     = 200
)

// Activity implements GET /api/me/activity (Story 29.4): the caller's own
// merged, reverse-chronological timeline of watches + searches (+ ratings
// when that surface exists). Owner-scoped.
func (h *Handler) Activity(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	limit, e := common.QueryInt(r, "limit", defaultActivityLimit)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	offset, e := common.QueryInt(r, "offset", 0)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	limit = clampLimit(limit, defaultActivityLimit, maxActivityLimit)
	if offset < 0 {
		offset = 0
	}
	kinds := wantedKinds(common.QueryCSV(r, "types"))

	// Cap each source at offset+limit so the in-Go merge stays bounded.
	fetchN := offset + limit
	var sources [][]ActivityItem
	if kinds["watched"] {
		got, err := h.repo().watchedActivity(r.Context(), p.UserID, fetchN)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("load watched activity"))
			return
		}
		sources = append(sources, got)
	}
	if kinds["searched"] {
		got, err := h.repo().searchedActivity(r.Context(), p.UserID, fetchN)
		if err != nil {
			// search_history is a core table, but degrade rather than 500
			// the whole feed if it is unexpectedly absent.
			got = nil
		}
		sources = append(sources, got)
	}
	// "rated" is intentionally omitted: there is no ratings table in this
	// deployment's schema. The feed degrades gracefully (Story 29.4 AC)
	// and gains ratings automatically once that surface lands and is
	// wired here.

	merged := MergeActivity(sources, offset, limit)
	common.WriteJSON(w, r, http.StatusOK, map[string]any{
		"items": merged, "limit": limit, "offset": offset,
	})
}

// GetPrivacy implements GET /api/me/activity/settings.
func (h *Handler) GetPrivacy(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	prefs, err := h.repo().getPrefs(r.Context(), p.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load privacy settings"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, prefs)
}

// PutPrivacy implements PUT /api/me/activity/settings: toggles tracking.
func (h *Handler) PutPrivacy(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	var req PrivacySettings
	if e := common.ReadJSON(r, &req, 1<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if err := h.repo().setPrefs(r.Context(), p.UserID, req.TrackEnabled, h.now()); err != nil {
		httperror.Write(w, r, httperror.Internal("save privacy settings"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, PrivacySettings{TrackEnabled: req.TrackEnabled})
}

// wantedKinds resolves the ?types= filter into a set. Empty ⇒ all kinds.
func wantedKinds(types []string) map[string]bool {
	all := map[string]bool{"watched": true, "searched": true, "rated": true}
	if len(types) == 0 {
		return all
	}
	want := map[string]bool{}
	for _, t := range types {
		if all[t] {
			want[t] = true
		}
	}
	if len(want) == 0 {
		return all
	}
	return want
}

// MergeActivity flattens the per-source slices, sorts newest-first, and
// applies offset/limit. Pure (no DB) so it is unit-tested directly.
func MergeActivity(sources [][]ActivityItem, offset, limit int) []ActivityItem {
	var all []ActivityItem
	for _, s := range sources {
		all = append(all, s...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.After(all[j].At) })
	if offset >= len(all) {
		return []ActivityItem{}
	}
	all = all[offset:]
	if limit >= 0 && limit < len(all) {
		all = all[:limit]
	}
	if all == nil {
		return []ActivityItem{}
	}
	return all
}

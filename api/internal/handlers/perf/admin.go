// Package perf wires the admin endpoints documented in
// plan-18-08 (cache flush) and plan-18-01 (budget surface):
//
//	POST /admin/cache/{name}/flush  — flush a named cache wholesale
//	GET  /api/admin/caches          — list caches + hit-rate snapshots
//	GET  /api/admin/perf/budgets    — dump the parsed perf budgets
package perf

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/perf"
)

// Handler bundles deps.
type Handler struct {
	Registry *perf.Registry
	Budgets  *perf.Budgets
}

// Mount attaches the admin routes.
func (h *Handler) Mount(r chi.Router) {
	r.Post("/admin/cache/{name}/flush", h.FlushCache)
	r.Get("/api/admin/caches", h.ListCaches)
	r.Get("/api/admin/perf/budgets", h.GetBudgets)
}

// FlushCache flushes the named cache. Returns 404 when the cache id is
// unknown — handlers can't flush what they didn't register.
func (h *Handler) FlushCache(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin required"))
		return
	}
	name := chi.URLParam(r, "name")
	c, ok := h.Registry.Lookup(name)
	if !ok {
		httperror.Write(w, r, httperror.NotFound("cache not found: "+name))
		return
	}
	c.Flush()
	w.WriteHeader(http.StatusNoContent)
}

// ListCaches returns the registered names. Useful for /admin UI auto-
// suggestion.
func (h *Handler) ListCaches(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin required"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"caches": h.Registry.Names()})
}

// GetBudgets dumps the parsed perf budget file. Surface is admin-only
// because the bundle leaks hardware-profile labels.
func (h *Handler) GetBudgets(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin required"))
		return
	}
	if h.Budgets == nil {
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"endpoints": []any{}})
		return
	}
	common.WriteJSON(w, r, http.StatusOK, h.Budgets)
}

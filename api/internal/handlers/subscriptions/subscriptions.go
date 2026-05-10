// Package subscriptions wires the entitlement HTTP surface (Epic 16):
//
//	GET    /api/entitlements              — current tier + per-feature map
//	POST   /api/admin/license             — admin pastes a signed license
//	DELETE /api/admin/license             — admin reverts to free tier
//
// Story 16.4 requirement: license keys are never echoed back. GET on
// /api/admin/license returns only the license_id, tier, seats, and
// expiry — not the signature or the raw JSON.
package subscriptions

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
)

// Handler bundles deps.
type Handler struct {
	Store    *subscriptions.Store
	Verifier *subscriptions.Verifier
}

// Mount attaches the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/entitlements", h.GetEntitlements)
	r.Post("/api/admin/license", h.SetLicense)
	r.Delete("/api/admin/license", h.RevokeLicense)
}

// Response is the public entitlement payload.
type Response struct {
	Tier      subscriptions.Tier              `json:"tier"`
	LicenseID string                          `json:"license_id,omitempty"`
	Seats     int                             `json:"seats,omitempty"`
	Features  map[subscriptions.Feature]bool  `json:"features"`
}

// GetEntitlements is publicly readable (any authenticated principal).
func (h *Handler) GetEntitlements(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	ent := h.Store.Current()
	resp := Response{
		Tier:     subscriptions.TierFree,
		Features: map[subscriptions.Feature]bool{},
	}
	for f := range subscriptions.FreeFeatures {
		resp.Features[f] = true
	}
	if ent != nil {
		resp.Tier = ent.Tier
		resp.LicenseID = ent.LicenseID
		resp.Seats = ent.Seats
		for f, on := range ent.Features {
			resp.Features[f] = on
		}
	}
	common.WriteJSON(w, r, http.StatusOK, resp)
}

// SetLicense is admin-only. Body is the full signed License JSON.
func (h *Handler) SetLicense(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin required"))
		return
	}
	if h.Verifier == nil {
		httperror.Write(w, r, httperror.Internal("verifier not configured"))
		return
	}
	var lic subscriptions.License
	if e := common.ReadJSON(r, &lic, 32<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	ent, err := h.Verifier.Verify(&lic, nowUTC())
	if err != nil {
		httperror.Write(w, r, httperror.BadRequest(err.Error()))
		return
	}
	h.Store.Set(ent)
	w.WriteHeader(http.StatusNoContent)
}

// RevokeLicense is admin-only and reverts the running instance to free.
func (h *Handler) RevokeLicense(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin required"))
		return
	}
	h.Store.Set(nil)
	w.WriteHeader(http.StatusNoContent)
}

// nowUTC is a small indirection to keep tests deterministic.
var nowUTC = func() time.Time { return time.Now().UTC() }

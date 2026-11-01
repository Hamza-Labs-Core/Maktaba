// Package subscriptions wires the entitlement HTTP surface (Epic 16):
//
//	GET    /api/entitlements              — current tier + per-feature map (any signed-in user)
//	GET    /api/admin/license             — the license's state for Settings (admin)
//	POST   /api/admin/license             — admin pastes a signed license
//	DELETE /api/admin/license             — admin reverts to free tier
//	POST   /api/admin/license/refresh     — ask the license server now (admin)
//
// Story 16.4 requirement: license keys are never echoed back. The admin
// view returns the license id, tier, seats, holder and dates — never the
// signature or the signed JSON — so the pasted key is write-only.
package subscriptions

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
)

// TypeLicenseInvalid is the problem type of a pasted license the home
// refuses; the detail says why in plain words.
const TypeLicenseInvalid = "https://maktaba.tv/problems/license-invalid"

// CloudLink describes this home's place in Maktaba Cloud, for Settings.
type CloudLink struct {
	Claimed  bool   `json:"claimed"`
	ServerID string `json:"server_id,omitempty"`
	Slug     string `json:"slug,omitempty"`
	CloudAPI string `json:"cloud_api,omitempty"`
}

// Handler bundles deps.
type Handler struct {
	Store    *subscriptions.Store
	Verifier *subscriptions.Verifier
	// Refresher, when set, is kicked after an apply and runs "Check now".
	Refresher *subscriptions.Refresher
	// SeatCount counts the users (seats used). Nil: not reported.
	SeatCount interface {
		CountUsers(ctx context.Context) (int, error)
	}
	// Cloud reports whether the home is claimed in Maktaba Cloud. Nil:
	// never.
	Cloud func() CloudLink
	// ManageURL is the cloud's billing page ("" ⇒ none shown).
	ManageURL string
	// Audit, when set, records a license change.
	Audit func(ctx context.Context, actorUserID, event string, detail map[string]any)
}

// Mount attaches the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/entitlements", h.GetEntitlements)
	r.Get("/api/admin/license", h.GetLicense)
	r.Post("/api/admin/license", h.SetLicense)
	r.Delete("/api/admin/license", h.RevokeLicense)
	r.Post("/api/admin/license/refresh", h.Refresh)
}

// Response is the public entitlement payload.
type Response struct {
	Tier         subscriptions.Tier             `json:"tier"`
	LicenseID    string                         `json:"license_id,omitempty"`
	Seats        int                            `json:"seats,omitempty"`
	SeatsAllowed int                            `json:"seats_allowed"`
	Features     map[subscriptions.Feature]bool `json:"features"`
}

// GetEntitlements is readable by any signed-in principal: what the UI
// may show (story 16.6: clients only render; the server gates).
func (h *Handler) GetEntitlements(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, entitlements(h.Store.Current()))
}

func entitlements(ent *subscriptions.Entitlements) Response {
	resp := Response{
		Tier:         subscriptions.TierFree,
		SeatsAllowed: ent.SeatLimit(),
		Features:     map[subscriptions.Feature]bool{},
	}
	for _, f := range subscriptions.AllFeatures {
		resp.Features[f] = ent.Allows(f)
	}
	if ent != nil {
		resp.Tier = ent.Tier
		resp.LicenseID = ent.LicenseID
		resp.Seats = ent.Seats
	}
	return resp
}

// LicenseView is what Settings → Subscription shows an admin.
type LicenseView struct {
	// Available is false when this build has no license-server key: no
	// license can be applied (the free tier is unaffected).
	Available    bool                           `json:"available"`
	Tier         subscriptions.Tier             `json:"tier"`
	Features     map[subscriptions.Feature]bool `json:"features"`
	SeatsAllowed int                            `json:"seats_allowed"`
	SeatsUsed    *int                           `json:"seats_used,omitempty"`
	OverSeats    bool                           `json:"over_seats"`
	// Status: none | active | grace | expired | revoked.
	Status  subscriptions.Status `json:"status"`
	License *licenseSummary      `json:"license,omitempty"`
	// Message is the plain-words banner for an admin, "" when all is well.
	Message          string     `json:"message,omitempty"`
	LastRefreshAt    *time.Time `json:"last_refresh_at,omitempty"`
	LastRefreshError string     `json:"last_refresh_error,omitempty"`
	Cloud            CloudLink  `json:"cloud"`
	ManageURL        string     `json:"manage_url,omitempty"`
}

type licenseSummary struct {
	LicenseID string             `json:"license_id"`
	Tier      subscriptions.Tier `json:"tier"`
	Seats     int                `json:"seats"`
	Holder    string             `json:"holder,omitempty"`
	IssuedAt  time.Time          `json:"issued_at"`
	ExpiresAt time.Time          `json:"expires_at"`
	// Source is "cloud" for a plan license (refreshed daily), "offline"
	// for one pasted by hand.
	Source string `json:"source"`
}

func (h *Handler) view(ctx context.Context) LicenseView {
	st := h.Store.State()
	v := LicenseView{
		Available: h.Verifier != nil,
		Status:    st.Status,
		ManageURL: h.ManageURL,
	}
	e := entitlements(st.Effective)
	v.Tier, v.Features, v.SeatsAllowed = e.Tier, e.Features, e.SeatsAllowed
	if h.SeatCount != nil {
		if n, err := h.SeatCount.CountUsers(ctx); err == nil {
			v.SeatsUsed = &n
			v.OverSeats = v.SeatsAllowed != subscriptions.SeatsUnlimited && n > v.SeatsAllowed
		}
	}
	if l := st.License; l != nil {
		src := "offline"
		if l.ServerID != "" {
			src = "cloud"
		}
		v.License = &licenseSummary{LicenseID: l.LicenseID, Tier: l.Tier, Seats: l.Seats, Holder: l.Holder,
			IssuedAt: l.IssuedAt, ExpiresAt: l.ExpiresAt, Source: src}
	}
	if !st.LastRefreshAt.IsZero() {
		t := st.LastRefreshAt
		v.LastRefreshAt = &t
	}
	v.LastRefreshError = st.LastRefreshError
	if h.Cloud != nil {
		v.Cloud = h.Cloud()
	}
	v.Message = message(v, st)
	return v
}

// message is the admin banner (story 16.4: a clear message on a lock;
// the over-seat warning).
func message(v LicenseView, st subscriptions.State) string {
	switch st.Status {
	case subscriptions.StatusRevoked:
		return "This home's license was revoked by Maktaba, so the home is on the Free plan. " +
			"Existing users keep working; contact support or apply another license."
	case subscriptions.StatusExpired:
		if v.License != nil && v.License.Source == "cloud" {
			return "This home could not reach Maktaba Cloud to renew its license, so it is on the Free plan. " +
				"Reconnect to validate the license."
		}
		return "This home's license expired, so the home is on the Free plan. Apply a new license to restore it."
	case subscriptions.StatusGrace:
		return "This home's license has expired; paid features stop within 3 days. " +
			"Reconnect to Maktaba Cloud or apply a new license."
	}
	if v.OverSeats && v.SeatsUsed != nil {
		return "This home has more users than its plan allows. They all keep working, " +
			"but no new user can be added until the plan allows more or users are removed."
	}
	return ""
}

func requireAdmin(w http.ResponseWriter, r *http.Request) *principal.Principal {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin required"))
		return nil
	}
	return p
}

// GetLicense is admin-only: the license's state, never its key.
func (h *Handler) GetLicense(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	common.WriteJSON(w, r, http.StatusOK, h.view(r.Context()))
}

// SetLicense is admin-only. The body is the signed License JSON, as the
// license server issued it; a pasted copy with surrounding whitespace is
// fine. The answer is the admin view, never the key.
func (h *Handler) SetLicense(w http.ResponseWriter, r *http.Request) {
	p := requireAdmin(w, r)
	if p == nil {
		return
	}
	if h.Verifier == nil {
		// No build-time license-server key was embedded. The endpoint is
		// *disabled*, not erroring — 503 so clients/operators can tell
		// "feature off in this build" from "we broke" (500). Fail-closed,
		// never trust a zero key.
		httperror.Write(w, r, (&httperror.Error{
			Type: "https://maktaba.tv/problems/licensing-unavailable", Title: "Licensing unavailable",
			Status: http.StatusServiceUnavailable,
			Detail: "This build of Maktaba cannot check licenses. Install an official release to apply one.",
		}))
		return
	}
	var lic subscriptions.License
	if e := common.ReadJSON(r, &lic, 32<<10); e != nil {
		httperror.Write(w, r, invalid("This is not a Maktaba license. Paste the whole license, including the braces."))
		return
	}
	ent, err := h.Verifier.Verify(&lic, h.Store.Now())
	if err != nil {
		httperror.Write(w, r, invalid(reason(err)))
		return
	}
	if ent.ServerID != "" && h.Cloud != nil && ent.ServerID != h.Cloud().ServerID {
		httperror.Write(w, r, invalid("This license belongs to another home in Maktaba Cloud."))
		return
	}
	if st := h.Store.State(); st.License != nil && st.Status == subscriptions.StatusRevoked && st.License.LicenseID == ent.LicenseID {
		httperror.Write(w, r, invalid("This license was revoked by Maktaba."))
		return
	}
	// SetLicense persists the signed license (when the store has a DB
	// backend) so the entitlement survives a restart.
	if err := h.Store.SetLicense(r.Context(), &lic, ent); err != nil {
		httperror.Write(w, r, httperror.Internal("persist license").Wrap(err))
		return
	}
	h.audit(r.Context(), p.UserID, "license.applied", map[string]any{"license_id": ent.LicenseID, "tier": string(ent.Tier)})
	h.Refresher.Kick()
	common.WriteJSON(w, r, http.StatusOK, h.view(r.Context()))
}

func invalid(detail string) *httperror.Error {
	return &httperror.Error{Type: TypeLicenseInvalid, Title: "License not accepted", Status: http.StatusBadRequest, Detail: detail}
}

func reason(err error) string {
	switch {
	case errors.Is(err, subscriptions.ErrExpired):
		return "This license has expired."
	case errors.Is(err, subscriptions.ErrBadSignature):
		return "This license's signature does not match: it was changed, or it is not a Maktaba license."
	case strings.Contains(err.Error(), "unknown tier"):
		return "This license is for a plan this version of Maktaba does not know. Update Maktaba and try again."
	}
	return "This license could not be read."
}

// RevokeLicense is admin-only and reverts the running instance to free.
func (h *Handler) RevokeLicense(w http.ResponseWriter, r *http.Request) {
	p := requireAdmin(w, r)
	if p == nil {
		return
	}
	prev := h.Store.State().License
	if err := h.Store.Revoke(r.Context()); err != nil {
		httperror.Write(w, r, httperror.Internal("persist revoke").Wrap(err))
		return
	}
	if prev != nil {
		h.audit(r.Context(), p.UserID, "license.removed", map[string]any{"license_id": prev.LicenseID})
	}
	w.WriteHeader(http.StatusNoContent)
}

// Refresh is admin-only: ask the license server now. A failure is in
// the view (last_refresh_error); the answer is still 200.
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	if h.Refresher != nil {
		_ = h.Refresher.RefreshNow(r.Context())
	}
	common.WriteJSON(w, r, http.StatusOK, h.view(r.Context()))
}

func (h *Handler) audit(ctx context.Context, actor, event string, detail map[string]any) {
	if h.Audit != nil {
		h.Audit(ctx, actor, event, detail)
	}
}

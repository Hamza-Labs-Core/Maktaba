package parental

// Turning parental controls on in an existing home (Epic 57 story 57.12).
//
//	GET  /api/parental/settings                 admin
//	PUT  /api/parental/settings {enabled}       admin; off is 409 while an account is restricted
//	POST /api/parental/settings/notice/dismiss  admin
//	GET  /api/parental/library-suggestions      admin
//
// Every other caller gets 403 (`restricted` for a restricted account, from the gate;
// `admin_only` for the rest), no one 401.

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	parentalpkg "github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// TypeRestrictionsExist is the 409 of turning parental controls off while an account
// is restricted (`code: restrictions_exist`).
const TypeRestrictionsExist = "https://maktaba.tv/problems/parental-restrictions-exist"

// MountSettings attaches the routes.
func (h *Handler) MountSettings(r chi.Router) {
	r.Get("/api/parental/settings", h.GetSettings)
	r.Put("/api/parental/settings", h.PutSettings)
	r.Post("/api/parental/settings/notice/dismiss", h.DismissNotice)
	r.Get("/api/parental/library-suggestions", h.LibrarySuggestions)
}

// homeAdmin lets an admin through; anyone else gets 403 `admin_only`, with no
// query. A restricted account never reaches here: the gate refuses it first with
// `restricted` (the surface registry classes these routes Refuse).
func (h *Handler) homeAdmin(w http.ResponseWriter, r *http.Request) *principal.Principal {
	p := h.caller(w, r)
	if p == nil {
		return nil
	}
	if !p.IsAdmin {
		httperror.Write(w, r, adminOnly())
		return nil
	}
	return p
}

func (h *Handler) writeSettings(w http.ResponseWriter, r *http.Request) {
	out, err := h.Store.Settings(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("parental store").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// GetSettings implements GET /api/parental/settings.
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	if h.homeAdmin(w, r) == nil {
		return
	}
	h.writeSettings(w, r)
}

// PutSettings implements PUT /api/parental/settings.
func (h *Handler) PutSettings(w http.ResponseWriter, r *http.Request) {
	p := h.homeAdmin(w, r)
	if p == nil {
		return
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if e := common.ReadJSON(r, &in, 1<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if in.Enabled == nil {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "enabled", Message: "must be true or false"}}))
		return
	}
	err := h.Store.SetEnabled(r.Context(), p.UserID, *in.Enabled)
	if errors.Is(err, parentalpkg.ErrRestrictionsExist) {
		httperror.Write(w, r, httperror.Conflict(TypeRestrictionsExist,
			"lift every restriction before turning parental controls off").With("code", "restrictions_exist"))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("parental store").Wrap(err))
		return
	}
	h.changed("")
	h.writeSettings(w, r)
}

// DismissNotice implements POST /api/parental/settings/notice/dismiss.
func (h *Handler) DismissNotice(w http.ResponseWriter, r *http.Request) {
	p := h.homeAdmin(w, r)
	if p == nil {
		return
	}
	if err := h.Store.DismissNotice(r.Context(), p.UserID); err != nil {
		httperror.Write(w, r, httperror.Internal("parental store").Wrap(err))
		return
	}
	h.writeSettings(w, r)
}

// LibrarySuggestions implements GET /api/parental/library-suggestions.
func (h *Handler) LibrarySuggestions(w http.ResponseWriter, r *http.Request) {
	if h.homeAdmin(w, r) == nil {
		return
	}
	out, err := h.Store.LibrarySuggestions(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("parental store").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"libraries": out})
}

// CapabilitiesBlock is the `parental` block of GET /api/capabilities (README §6.6,
// story 57.12): what the apps need before they draw anything.
type CapabilitiesBlock struct {
	// Enabled: parental controls are turned on in this home.
	Enabled bool `json:"enabled"`
	// Restricted: this account is restricted. TierLimit is the limit this sign-in
	// is held to, by the account's policy or its device (null: none).
	Restricted bool `json:"restricted"`
	TierLimit  *int `json:"tier_limit"`
	// CanManage: the caller is an admin, or a guardian of at least one account.
	CanManage bool `json:"can_manage"`
}

// Capabilities answers the block for p. An error means the policy could not be read:
// the caller answers no capabilities at all rather than a block that says nothing
// limits the account.
func Capabilities(r *http.Request, cache *parentalpkg.Cache, p *principal.Principal) (*CapabilitiesBlock, error) {
	ctx := r.Context()
	e, err := cache.For(ctx, p)
	if err != nil {
		return nil, err
	}
	on, err := cache.Store().Enabled(ctx)
	if err != nil {
		return nil, err
	}
	out := &CapabilitiesBlock{Enabled: on, Restricted: e.Restricted && !e.SignalOnly, CanManage: p.IsAdmin}
	if e.Restricted {
		tier := e.MaxTier
		out.TierLimit = &tier
	}
	if !out.CanManage && !out.Restricted {
		if out.CanManage, err = cache.Store().HasChildren(ctx, p.UserID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

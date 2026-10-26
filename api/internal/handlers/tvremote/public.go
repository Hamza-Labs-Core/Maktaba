package tvremote

// The public TV site (Epic 34, first slice), admin only:
//
//	GET  /api/tv/public                     settings, every channel's public state, the publisher's last post
//	PUT  /api/tv/public                     {enabled, site_url}
//	PUT  /api/tv/public/channels/{id}       {public?, slug?, rights_note?, rights_confirmed?}
//	                                        422 rights_confirmation_required | 409 slug_taken | 400 invalid_slug
//	POST /api/tv/public/site-secrets        a new site signing key and HMAC secret, shown once
//	                                        (for the Worker's secrets); the old key stops working

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	remote "github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
)

func (h *Handler) mountPublic(r chi.Router) {
	r.Get("/api/tv/public", h.PublicStatus)
	r.Put("/api/tv/public", h.SetPublicSettings)
	r.Put("/api/tv/public/channels/{id}", h.SetPublicChannel)
	r.Post("/api/tv/public/site-secrets", h.NewSiteSecrets)
}

// PublicView is GET /api/tv/public.
type PublicView struct {
	Settings   remote.PublicSettings  `json:"settings"`
	Channels   []remote.PublicChannel `json:"channels"`
	SecretsSet bool                   `json:"secrets_set"`
	Publisher  remote.PublishStatus   `json:"publisher"`
}

// PublicStatus answers GET /api/tv/public.
func (h *Handler) PublicStatus(w http.ResponseWriter, r *http.Request) {
	if !h.simulcastAdmin(w, r) {
		return
	}
	chs, err := h.Remote.PublicChannels(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tv public channels").Wrap(err))
		return
	}
	v := PublicView{Settings: remote.LoadPublicSettings(r.Context(), h.Remote.DB), Channels: chs}
	if h.Integration != nil && h.Integration.Secrets != nil {
		v.SecretsSet, _ = h.Integration.Secrets.Has(r.Context(), remote.SecretSiteHMAC)
	}
	if h.Publisher != nil {
		v.Publisher = h.Publisher.Status()
	}
	w.Header().Set("Cache-Control", "no-store")
	common.WriteJSON(w, r, http.StatusOK, v)
}

// SetPublicSettings answers PUT /api/tv/public.
func (h *Handler) SetPublicSettings(w http.ResponseWriter, r *http.Request) {
	if !h.simulcastAdmin(w, r) {
		return
	}
	var body remote.PublicSettings
	if e := common.ReadJSON(r, &body, 4096); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if err := h.Remote.SavePublicSettings(r.Context(), actor(r), body); err != nil {
		writeTuneError(w, r, err, "tv public settings")
		return
	}
	if h.Publisher != nil {
		h.Publisher.Wake()
	}
	h.PublicStatus(w, r)
}

// SetPublicChannel answers PUT /api/tv/public/channels/{id}.
func (h *Handler) SetPublicChannel(w http.ResponseWriter, r *http.Request) {
	if !h.simulcastAdmin(w, r) {
		return
	}
	var body remote.PublicChange
	if e := common.ReadJSON(r, &body, 4096); e != nil {
		httperror.Write(w, r, e)
		return
	}
	x, err := h.Remote.SetPublic(r.Context(), actor(r), chi.URLParam(r, "id"), body)
	if err != nil {
		writeTuneError(w, r, err, "tv public channel")
		return
	}
	if h.Publisher != nil {
		h.Publisher.Wake()
	}
	common.WriteJSON(w, r, http.StatusOK, x)
}

// NewSiteSecrets answers POST /api/tv/public/site-secrets.
func (h *Handler) NewSiteSecrets(w http.ResponseWriter, r *http.Request) {
	if !h.simulcastAdmin(w, r) {
		return
	}
	s, err := h.Remote.NewSiteSecrets(r.Context(), actor(r))
	if err != nil {
		writeTuneError(w, r, err, "tv public site secrets")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	common.WriteJSON(w, r, http.StatusOK, s)
}

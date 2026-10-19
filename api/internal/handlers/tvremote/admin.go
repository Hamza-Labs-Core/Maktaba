// Package tvremote is the admin HTTP surface of Epic 32's remote viewing
// (story 32.6: the Cloudflare Stream integration).
//
//	GET    /api/tv/remote               integration status, last test (AC4)
//	POST   /api/tv/remote/test          Test connection (AC3)
//	PUT    /api/tv/remote/enabled       {enabled} — on needs a green test < 24 h (AC4)
//	DELETE /api/tv/remote/integration   {confirm:true} — remove it all (AC5)
//
// The credentials themselves are saved through PUT /api/extensions/
// cloudflare_stream (the token goes to home_secrets, never answered).
package tvremote

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	remote "github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
)

// Handler serves the routes above and the remote viewer routes (viewer.go).
type Handler struct {
	Integration *remote.Integration
	// Remote is the remote controller (32.7-32.10); nil: the routes answer 503.
	Remote *remote.Controller
}

// Mount registers the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/tv/remote", h.Status)
	r.Post("/api/tv/remote/test", h.Test)
	r.Put("/api/tv/remote/enabled", h.SetEnabled)
	r.Delete("/api/tv/remote/integration", h.Remove)
	r.Post("/api/tv/remote/tune", h.Tune)
	r.Post("/api/tv/remote/sessions/{id}/heartbeat", h.Heartbeat)
	r.Delete("/api/tv/remote/sessions/{id}", h.EndSession)
	r.Post("/api/tv/remote/pushes/{id}/stop", h.StopPush)
	r.Post("/api/tv/remote/signing-key/rotate", h.RotateKey)
	r.Post("/api/tv/remote/webhook-secret", h.NewWebhookSecret)
	r.Post("/api/tv/remote/webhook", h.Webhook)
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return false
	}
	if !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return false
	}
	return true
}

// Status answers GET /api/tv/remote.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	st, err := h.Integration.Status(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tv remote status"))
		return
	}
	out := map[string]any{"integration": st}
	if h.Remote != nil {
		rs, err := h.Remote.Status(r.Context())
		if err != nil {
			httperror.Write(w, r, httperror.Internal("tv remote status"))
			return
		}
		out["remote"] = rs
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// Test answers POST /api/tv/remote/test.
func (h *Handler) Test(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	res, err := h.Integration.Test(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tv remote test"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, res)
}

// SetEnabled answers PUT /api/tv/remote/enabled.
func (h *Handler) SetEnabled(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if e := common.ReadJSON(r, &body, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if body.Enabled == nil {
		httperror.Write(w, r, httperror.BadRequest("send enabled"))
		return
	}
	if err := h.Integration.SetEnabled(r.Context(), *body.Enabled); err != nil {
		if errors.Is(err, remote.ErrNeedsGreenTest) {
			httperror.Write(w, r, httperror.Conflict("https://maktaba.dev/problems/tv-remote-needs-test", err.Error()))
			return
		}
		httperror.Write(w, r, httperror.Internal("tv remote enable"))
		return
	}
	h.Status(w, r)
}

// Remove answers DELETE /api/tv/remote/integration.
func (h *Handler) Remove(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if e := common.ReadJSON(r, &body, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if !body.Confirm {
		httperror.Write(w, r, httperror.BadRequest("removing the integration deletes Maktaba's live inputs and signing key at Cloudflare: send {\"confirm\":true}"))
		return
	}
	res, err := h.Integration.Remove(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tv remote remove"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, res)
}

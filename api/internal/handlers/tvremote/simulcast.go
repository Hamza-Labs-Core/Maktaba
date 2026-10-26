package tvremote

// Simulcast (story 32.13), admin only:
//
//	GET    /api/tv/simulcast                                destinations, attachments, outputs (status), output-hours vs cap
//	POST   /api/tv/simulcast/destinations                   {name, kind, ingest_url?, stream_key}
//	PUT    /api/tv/simulcast/destinations/{id}              {name?, ingest_url?, stream_key?}
//	DELETE /api/tv/simulcast/destinations/{id}              409 destination_in_use while simulcasting
//	POST   /api/tv/simulcast/destinations/{id}/test         the URL parses and the key is set
//	GET    /api/channels/{id}/simulcast                     the channel's attachments and next windows
//	PUT    /api/channels/{id}/simulcast                     [{destination_id, mode, rules, enabled, rights_confirmed?}]
//	                                                        409 rights_confirmation_required | too_many_outputs
//	POST   /api/tv/simulcast/outputs/{attachment_id}/start  manual start until the next window boundary
//	POST   /api/tv/simulcast/outputs/{attachment_id}/stop
//
// A destination's stream key is written to home_secrets and answered masked
// only.

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	remote "github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
)

func (h *Handler) mountSimulcast(r chi.Router) {
	r.Get("/api/tv/simulcast", h.SimulcastStatus)
	r.Post("/api/tv/simulcast/destinations", h.CreateDestination)
	r.Put("/api/tv/simulcast/destinations/{id}", h.UpdateDestination)
	r.Delete("/api/tv/simulcast/destinations/{id}", h.DeleteDestination)
	r.Post("/api/tv/simulcast/destinations/{id}/test", h.TestDestination)
	r.Get("/api/channels/{id}/simulcast", h.ChannelSimulcast)
	r.Put("/api/channels/{id}/simulcast", h.SetChannelSimulcast)
	r.Post("/api/tv/simulcast/outputs/{id}/start", h.StartOutput)
	r.Post("/api/tv/simulcast/outputs/{id}/stop", h.StopOutput)
}

func actor(r *http.Request) string {
	if p := principal.FromContext(r.Context()); p != nil {
		return p.UserID
	}
	return ""
}

func (h *Handler) simulcastAdmin(w http.ResponseWriter, r *http.Request) bool {
	return requireAdmin(w, r) && h.remoteReady(w, r)
}

// SimulcastStatus answers GET /api/tv/simulcast.
func (h *Handler) SimulcastStatus(w http.ResponseWriter, r *http.Request) {
	if !h.simulcastAdmin(w, r) {
		return
	}
	v, err := h.Remote.SimulcastStatus(r.Context(), "")
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tv simulcast status").Wrap(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	common.WriteJSON(w, r, http.StatusOK, v)
}

// CreateDestination answers POST /api/tv/simulcast/destinations.
func (h *Handler) CreateDestination(w http.ResponseWriter, r *http.Request) {
	if !h.simulcastAdmin(w, r) {
		return
	}
	var body remote.DestinationInput
	if e := common.ReadJSON(r, &body, 8192); e != nil {
		httperror.Write(w, r, e)
		return
	}
	d, err := h.Remote.CreateDestination(r.Context(), actor(r), body)
	if err != nil {
		writeTuneError(w, r, err, "tv simulcast destination")
		return
	}
	common.WriteJSON(w, r, http.StatusCreated, d)
}

// UpdateDestination answers PUT /api/tv/simulcast/destinations/{id}.
func (h *Handler) UpdateDestination(w http.ResponseWriter, r *http.Request) {
	if !h.simulcastAdmin(w, r) {
		return
	}
	var body remote.DestinationInput
	if e := common.ReadJSON(r, &body, 8192); e != nil {
		httperror.Write(w, r, e)
		return
	}
	d, err := h.Remote.UpdateDestination(r.Context(), actor(r), chi.URLParam(r, "id"), body)
	if err != nil {
		writeTuneError(w, r, err, "tv simulcast destination")
		return
	}
	common.WriteJSON(w, r, http.StatusOK, d)
}

// DeleteDestination answers DELETE /api/tv/simulcast/destinations/{id}.
func (h *Handler) DeleteDestination(w http.ResponseWriter, r *http.Request) {
	if !h.simulcastAdmin(w, r) {
		return
	}
	if err := h.Remote.DeleteDestination(r.Context(), actor(r), chi.URLParam(r, "id")); err != nil {
		writeTuneError(w, r, err, "tv simulcast destination")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TestDestination answers POST /api/tv/simulcast/destinations/{id}/test.
func (h *Handler) TestDestination(w http.ResponseWriter, r *http.Request) {
	if !h.simulcastAdmin(w, r) {
		return
	}
	res, err := h.Remote.TestDestination(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeTuneError(w, r, err, "tv simulcast test")
		return
	}
	common.WriteJSON(w, r, http.StatusOK, res)
}

// ChannelSimulcast answers GET /api/channels/{id}/simulcast.
func (h *Handler) ChannelSimulcast(w http.ResponseWriter, r *http.Request) {
	if !h.simulcastAdmin(w, r) {
		return
	}
	v, err := h.Remote.SimulcastStatus(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeTuneError(w, r, err, "tv simulcast status")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	common.WriteJSON(w, r, http.StatusOK, v)
}

// SetChannelSimulcast answers PUT /api/channels/{id}/simulcast.
func (h *Handler) SetChannelSimulcast(w http.ResponseWriter, r *http.Request) {
	if !h.simulcastAdmin(w, r) {
		return
	}
	var body []remote.AttachmentInput
	if e := common.ReadJSON(r, &body, 64<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	ch := chi.URLParam(r, "id")
	if _, err := h.Remote.SetChannelSimulcast(r.Context(), actor(r), ch, body); err != nil {
		writeTuneError(w, r, err, "tv simulcast attachments")
		return
	}
	h.ChannelSimulcast(w, r)
}

// StartOutput answers POST /api/tv/simulcast/outputs/{attachment_id}/start.
func (h *Handler) StartOutput(w http.ResponseWriter, r *http.Request) {
	if !h.simulcastAdmin(w, r) {
		return
	}
	if err := h.Remote.StartOutput(r.Context(), actor(r), chi.URLParam(r, "id")); err != nil {
		writeTuneError(w, r, err, "tv simulcast start")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// StopOutput answers POST /api/tv/simulcast/outputs/{attachment_id}/stop.
func (h *Handler) StopOutput(w http.ResponseWriter, r *http.Request) {
	if !h.simulcastAdmin(w, r) {
		return
	}
	if err := h.Remote.StopOutput(r.Context(), actor(r), chi.URLParam(r, "id")); err != nil {
		writeTuneError(w, r, err, "tv simulcast stop")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

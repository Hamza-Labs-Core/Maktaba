package frontdoor

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/placement"
)

// Internal routes the home's API calls on loopback (signed with the cluster stream
// key, never through the relay):
//
//	POST /internal/tv/ensure/{channel}          place (or warm) a channel
//	POST /internal/tv/place/{channel}           {action: move|stop, host_id?}
//	POST /internal/tv/drain/{host}              move every channel off a draining host now
func (h *Handler) MountInternal(r chi.Router) {
	r.Group(func(g chi.Router) {
		g.Use(cluster.VerifyMiddleware(h.Keyring, h.Logger))
		g.Post("/internal/tv/ensure/{channel}", h.internalEnsure)
		g.Post("/internal/tv/place/{channel}", h.internalPlace)
		g.Post("/internal/tv/drain/{host}", h.internalDrain)
	})
}

type placeBody struct {
	Action string `json:"action"`
	HostID string `json:"host_id"`
}

type placeAnswer struct {
	ChannelID  string `json:"channel_id"`
	HostID     string `json:"host_id,omitempty"`
	Generation int64  `json:"generation,omitempty"`
	State      string `json:"state,omitempty"`
	Moved      int    `json:"moved,omitempty"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handler) internalEnsure(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "channel"))
	if err != nil {
		http.Error(w, "bad channel", http.StatusBadRequest)
		return
	}
	p, err := h.Svc.Resolve(r.Context(), id)
	if err != nil {
		writeResolveError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, placeAnswer{ChannelID: id.String(), HostID: p.Host, Generation: p.Gen, State: p.State})
}

func (h *Handler) internalPlace(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "channel"))
	if err != nil {
		http.Error(w, "bad channel", http.StatusBadRequest)
		return
	}
	var b placeBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	switch b.Action {
	case "stop":
		if err := h.Svc.Stop(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, placeAnswer{ChannelID: id.String(), State: placement.StateReleased})
	case "move":
		h.Svc.Hosts.Reload(r.Context())
		p, err := h.Svc.Place(r.Context(), id, placement.ReasonMove, b.HostID)
		if errors.Is(err, placement.ErrUnknownHost) {
			http.Error(w, "that host cannot take the channel", http.StatusConflict)
			return
		}
		if err != nil {
			writeResolveError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, placeAnswer{ChannelID: id.String(), HostID: p.Host, Generation: p.Gen, State: p.State})
	default:
		http.Error(w, "action must be move or stop", http.StatusBadRequest)
	}
}

func (h *Handler) internalDrain(w http.ResponseWriter, r *http.Request) {
	host := chi.URLParam(r, "host")
	h.Svc.Hosts.Reload(r.Context()) // the API just set draining
	n, err := h.Svc.DrainNow(r.Context(), host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, placeAnswer{HostID: host, Moved: n})
}

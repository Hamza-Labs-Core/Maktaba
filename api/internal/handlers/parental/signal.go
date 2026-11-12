package parental

// The device's signal (Epic 57 story 57.04).
//
//	POST /api/me/age-signal                              any native sign-in, about itself
//	POST /api/users/{id}/parental/signals/{family}/clear admin; the account's guardian
//
// The first is the Player saying what its device said (Apple's Declared Age Range,
// the Screen Time rating limits). It can only tighten the sign-in it came through:
// a looser statement is kept and waits. The second is how a looser statement is
// accepted. A sign-in that itself carries a device limit clears nothing, whoever
// it is: the device that is limited is the one a child holds.

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	parentalpkg "github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// Problem types of the signal routes.
const (
	TypeNoSignIn      = "https://maktaba.tv/problems/age-signal-no-sign-in"
	TypeDeviceLimited = "https://maktaba.tv/problems/device-limited"
)

// MountSignal attaches the signal routes.
func (h *Handler) MountSignal(r chi.Router) {
	r.Post("/api/me/age-signal", h.PostSignal)
	r.Post("/api/users/{id}/parental/signals/{family}/clear", h.ClearSignal)
}

func noSignIn() *httperror.Error {
	return httperror.Conflict(TypeNoSignIn,
		"this credential names no app sign-in; refresh the access token and send the signal again").With("code", "no_sign_in")
}

type signalResponse struct {
	// TierLimit is the device limit now in force on this sign-in (null: none).
	TierLimit *int `json:"tier_limit"`
	// PendingLooser says what was sent is looser than the limit in force and waits
	// for an admin or a guardian.
	PendingLooser bool `json:"pending_looser"`
	// Refresh tells the app to refresh its access token now, so the next one
	// carries the limit (`ac`). False while parental controls are off: nothing
	// changed for this sign-in.
	Refresh bool `json:"refresh"`
	// Enabled is false while parental controls are not turned on in this home
	// (story 57.12): the statement was recorded and limits nothing.
	Enabled bool `json:"enabled"`
}

// PostSignal implements POST /api/me/age-signal.
func (h *Handler) PostSignal(w http.ResponseWriter, r *http.Request) {
	p := h.caller(w, r)
	if p == nil {
		return
	}
	var in parentalpkg.SignalInput
	if e := common.ReadJSON(r, &in, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if problems := in.Normalize(); len(problems) > 0 {
		errs := make([]httperror.FieldError, 0, len(problems))
		for _, f := range problems {
			errs = append(errs, httperror.FieldError{Field: f.Field, Message: f.Message})
		}
		httperror.Write(w, r, httperror.Unprocessable(errs))
		return
	}
	// only an app's sign-in (an access token minted beside a refresh token) has a
	// device to speak for: a web session or an API token has none
	if p.Source != principal.SourceJWT || p.RefreshFamily == "" {
		httperror.Write(w, r, noSignIn())
		return
	}
	st, err := h.Store.RecordSignal(r.Context(), p.UserID, p.RefreshFamily, in)
	if errors.Is(err, parentalpkg.ErrNoSignIn) {
		httperror.Write(w, r, noSignIn())
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("parental store").Wrap(err))
		return
	}
	h.changed(p.UserID)
	common.WriteJSON(w, r, http.StatusOK, signalResponse{TierLimit: st.TierLimit, PendingLooser: st.Pending, Refresh: !st.Off, Enabled: !st.Off})
}

type clearResponse struct {
	// TierLimit is the device limit now in force on that sign-in (null: none).
	TierLimit *int `json:"tier_limit"`
	// Removed says nothing looser was waiting, so the signal was taken away: the
	// sign-in is held to the account's policy alone until its device speaks again.
	Removed bool `json:"removed"`
}

// ClearSignal implements POST /api/users/{id}/parental/signals/{family}/clear.
func (h *Handler) ClearSignal(w http.ResponseWriter, r *http.Request) {
	id, family := chi.URLParam(r, "id"), chi.URLParam(r, "family")
	p, ok := h.manager(w, r, id)
	if !ok {
		return
	}
	// fail closed: a sign-in whose own limit cannot be read clears nothing
	limited := p.AgeCeiling != nil
	if !limited && p.RefreshFamily != "" {
		ac, err := h.Store.AgeCeiling(r.Context(), p.UserID, p.RefreshFamily)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("parental store").Wrap(err))
			return
		}
		limited = ac != nil
	}
	if limited {
		httperror.Write(w, r, httperror.Forbidden(TypeDeviceLimited,
			"this device is limited by its own settings; clear a signal from another device").With("code", "device_limited"))
		return
	}
	st, err := h.Store.ClearSignal(r.Context(), p.UserID, id, family)
	if errors.Is(err, parentalpkg.ErrSignalNotFound) {
		httperror.Write(w, r, httperror.NotFound("signal"))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("parental store").Wrap(err))
		return
	}
	h.changed(id)
	common.WriteJSON(w, r, http.StatusOK, clearResponse{TierLimit: st.TierLimit, Removed: st.Removed})
}

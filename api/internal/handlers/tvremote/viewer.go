package tvremote

// Remote viewers (story 32.8), signed-in users reaching the home through the
// Epic 25 relay. These answer small JSON documents only; the video comes from
// the user's own Cloudflare account, so the relay never carries it.
//
//	POST   /api/tv/remote/tune                    {channel_id, device?} → 200 | 202 | 402 | 403 | 404 | 429 | 503
//	POST   /api/tv/remote/sessions/{id}/heartbeat → {state, expires_at, token?, hls_url?}
//	DELETE /api/tv/remote/sessions/{id}
//
// Admin (story 32.7, 32.8):
//
//	POST   /api/tv/remote/pushes/{id}/stop
//	POST   /api/tv/remote/signing-key/rotate      every outstanding token stops working
//	POST   /api/tv/remote/webhook-secret          a new Notifications webhook secret (shown once)
//
// Public (allowlisted; the secret header is the auth):
//
//	POST   /api/tv/remote/webhook                 Cloudflare Notifications (live_input.*)

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	remote "github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
)

// WebhookHeader carries the Notifications webhook secret.
const WebhookHeader = "cf-webhook-auth"

func viewerOf(p *principal.Principal) remote.Viewer {
	return remote.Viewer{UserID: p.UserID, IsAdmin: p.IsAdmin, CanRead: func(lib *string) bool {
		if lib == nil {
			return true // a multi-library channel: any signed-in user
		}
		return p.HasLibrary(*lib)
	}}
}

func signedIn(w http.ResponseWriter, r *http.Request) (*principal.Principal, bool) {
	p := principal.FromContext(r.Context())
	if p == nil || p.UserID == "" {
		httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.dev/problems/unauthorized",
			Title: "Unauthorized", Status: http.StatusUnauthorized, Detail: "sign in to watch away from home"})
		return nil, false
	}
	return p, true
}

// writeTuneError answers a refused tune with its status and stable code.
func writeTuneError(w http.ResponseWriter, r *http.Request, err error, what string) {
	var te *remote.TuneError
	if errors.As(err, &te) {
		if te.Status == http.StatusServiceUnavailable || te.Status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "30")
		}
		e := (&httperror.Error{Type: "https://maktaba.dev/problems/" + strings.ReplaceAll(te.Code, "_", "-"),
			Title: http.StatusText(te.Status), Status: te.Status, Detail: te.Detail}).With("code", te.Code)
		if te.Hint != "" {
			e = e.With("hint", te.Hint)
		}
		httperror.Write(w, r, e)
		return
	}
	httperror.Write(w, r, httperror.Internal(what))
}

func (h *Handler) remoteReady(w http.ResponseWriter, r *http.Request) bool {
	if h.Remote == nil {
		httperror.Write(w, r, (&httperror.Error{Type: "https://maktaba.dev/problems/remote-unavailable",
			Title: "Service Unavailable", Status: http.StatusServiceUnavailable,
			Detail: "remote viewing is not available on this server"}).With("code", "remote_unavailable"))
		return false
	}
	return true
}

// Tune answers POST /api/tv/remote/tune.
func (h *Handler) Tune(w http.ResponseWriter, r *http.Request) {
	p, ok := signedIn(w, r)
	if !ok || !h.remoteReady(w, r) {
		return
	}
	var body struct {
		ChannelID string `json:"channel_id"`
		Device    string `json:"device"`
	}
	if e := common.ReadJSON(r, &body, 4096); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if strings.TrimSpace(body.ChannelID) == "" {
		httperror.Write(w, r, httperror.BadRequest("send channel_id"))
		return
	}
	res, err := h.Remote.Tune(r.Context(), viewerOf(p), strings.TrimSpace(body.ChannelID), body.Device)
	if err != nil {
		writeTuneError(w, r, err, "tv remote tune")
		return
	}
	if res.State != "live" {
		w.Header().Set("Retry-After", "3")
		common.WriteJSON(w, r, http.StatusAccepted, res)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	common.WriteJSON(w, r, http.StatusOK, res)
}

// Heartbeat answers POST /api/tv/remote/sessions/{id}/heartbeat.
func (h *Handler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	p, ok := signedIn(w, r)
	if !ok || !h.remoteReady(w, r) {
		return
	}
	b, err := h.Remote.Heartbeat(r.Context(), viewerOf(p), chi.URLParam(r, "id"))
	if err != nil {
		writeTuneError(w, r, err, "tv remote heartbeat")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	common.WriteJSON(w, r, http.StatusOK, b)
}

// EndSession answers DELETE /api/tv/remote/sessions/{id}.
func (h *Handler) EndSession(w http.ResponseWriter, r *http.Request) {
	p, ok := signedIn(w, r)
	if !ok || !h.remoteReady(w, r) {
		return
	}
	if err := h.Remote.End(r.Context(), viewerOf(p), chi.URLParam(r, "id")); err != nil {
		writeTuneError(w, r, err, "tv remote end")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// StopPush answers POST /api/tv/remote/pushes/{id}/stop.
func (h *Handler) StopPush(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) || !h.remoteReady(w, r) {
		return
	}
	if err := h.Remote.StopPush(r.Context(), chi.URLParam(r, "id")); err != nil {
		httperror.Write(w, r, httperror.NotFound("no such push"))
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// RotateKey answers POST /api/tv/remote/signing-key/rotate (the panic button).
func (h *Handler) RotateKey(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) || !h.remoteReady(w, r) {
		return
	}
	res, err := h.Remote.RotateSigningKey(r.Context(), principal.FromContext(r.Context()).UserID)
	if err != nil {
		httperror.Write(w, r, httperror.BadGateway("Cloudflare: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, res)
}

// NewWebhookSecret answers POST /api/tv/remote/webhook-secret: a new secret
// for the Cloudflare Notifications webhook, shown once.
func (h *Handler) NewWebhookSecret(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if h.Integration == nil || h.Integration.Secrets == nil {
		httperror.Write(w, r, httperror.Internal("no secret store"))
		return
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	sec := hex.EncodeToString(b)
	if err := h.Integration.Secrets.Put(r.Context(), remote.SecretWebhook, []byte(sec), principal.FromContext(r.Context()).UserID); err != nil {
		httperror.Write(w, r, httperror.Internal("store webhook secret").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]string{"secret": sec, "header": WebhookHeader,
		"url_path": "/api/tv/remote/webhook"})
}

// Webhook answers POST /api/tv/remote/webhook: a Cloudflare Notifications
// event (live_input.connected|disconnected|errored). Only with the secret;
// it records an errored event's code and asks for a reconcile, nothing more.
func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	if h.Remote == nil || h.Integration == nil || h.Integration.Secrets == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	want, err := h.Integration.Secrets.Get(r.Context(), remote.SecretWebhook)
	got := r.Header.Get(WebhookHeader)
	if err != nil || len(want) == 0 || subtle.ConstantTimeCompare(want, []byte(got)) != 1 {
		httperror.Write(w, r, httperror.Forbidden("", "bad webhook secret"))
		return
	}
	// Cloudflare reaches this home (a switch of the home checks it moved with it)
	if h.Integration.DB != nil {
		_, _ = h.Integration.DB.ExecContext(r.Context(), `INSERT INTO app_settings (key, value, updated_at)
		    VALUES ('tv_remote.webhook_seen', to_jsonb(now()), now())
		    ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`)
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	// Cloudflare's shape (developers.cloudflare.com/stream/stream-live/webhooks):
	// data.input_id, data.event_type, and for live_input.errored the code at
	// data.live_input_errored.error.code. A flat data.error_code is still read.
	var ev struct {
		Data struct {
			InputID          string `json:"input_id"`
			EventType        string `json:"event_type"`
			ErrorCode        string `json:"error_code"`
			LiveInputUID     string `json:"live_input_id"`
			LiveInputErrored struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			} `json:"live_input_errored"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &ev)
	input := ev.Data.InputID
	if input == "" {
		input = ev.Data.LiveInputUID
	}
	code := ev.Data.LiveInputErrored.Error.Code
	if code == "" {
		code = ev.Data.ErrorCode
	}
	if input != "" {
		_ = h.Remote.InputEvent(r.Context(), input, ev.Data.EventType, code)
	} else {
		h.Remote.Wake()
	}
	w.WriteHeader(http.StatusNoContent)
}

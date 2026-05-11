// Package push (handlers) exposes the registration and ingest
// endpoints. Server agents POST to /v1/push/dispatch over the relay
// tunnel; user devices register via /v1/push/devices.
package push

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	pushpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/push"
)

type Deps struct {
	DB         *sql.DB
	Dispatcher *pushpkg.Dispatcher
}

func Mount(r interface {
	Post(string, http.HandlerFunc)
	Delete(string, http.HandlerFunc)
}, d Deps) {
	r.Post("/v1/push/devices", d.RegisterDevice)
	r.Delete("/v1/push/devices", d.UnregisterDevice)
	r.Post("/v1/push/dispatch", d.Dispatch)
}

type deviceReq struct {
	Platform   string `json:"platform"`
	Token      string `json:"token"`
	AppVersion string `json:"app_version"`
}

// RegisterDevice upserts a (platform, token) row for the caller. Idem-
// potent so a client can call it on every app launch.
func (d *Deps) RegisterDevice(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	if uid == "" {
		writeErr(w, 401, "unauthorized", "")
		return
	}
	var req deviceReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid_body", err.Error())
		return
	}
	if req.Platform == "" || req.Token == "" {
		writeErr(w, 422, "missing_fields", "platform and token required")
		return
	}
	switch req.Platform {
	case "ios", "android", "web":
	default:
		writeErr(w, 422, "bad_platform", req.Platform)
		return
	}
	_, err := d.DB.ExecContext(r.Context(), `
        INSERT INTO push_devices (user_id, platform, token, app_version, last_seen_at)
        VALUES ($1,$2,$3,$4,now())
        ON CONFLICT (platform, token) DO UPDATE SET user_id = EXCLUDED.user_id, app_version = EXCLUDED.app_version, last_seen_at = now()
    `, uid, req.Platform, req.Token, req.AppVersion)
	if err != nil {
		writeErr(w, 500, "store_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d *Deps) UnregisterDevice(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	platform := r.URL.Query().Get("platform")
	tok := r.URL.Query().Get("token")
	_, _ = d.DB.ExecContext(r.Context(), `DELETE FROM push_devices WHERE user_id = $1 AND platform = $2 AND token = $3`, uid, platform, tok)
	w.WriteHeader(http.StatusNoContent)
}

type dispatchReq struct {
	UserID string            `json:"user_id"`
	Title  string            `json:"title"`
	Body   string            `json:"body"`
	Topic  string            `json:"topic"`
	Data   map[string]string `json:"data"`
}

// Dispatch is called by an on-prem server (over the relay) to push to
// its user's devices. Authentication is via the relay's server-side
// auth — the request is signed by the tunnel before it reaches us.
func (d *Deps) Dispatch(w http.ResponseWriter, r *http.Request) {
	var req dispatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid_body", err.Error())
		return
	}
	if req.UserID == "" {
		writeErr(w, 422, "missing_user_id", "")
		return
	}
	err := d.Dispatcher.Send(r.Context(), pushpkg.Notification{
		UserID: req.UserID,
		Title:  req.Title,
		Body:   req.Body,
		Topic:  req.Topic,
		Data:   req.Data,
	})
	if errors.Is(err, errors.New("push: no devices registered")) {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if err != nil {
		writeErr(w, 500, "dispatch_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": kind, "message": msg})
}

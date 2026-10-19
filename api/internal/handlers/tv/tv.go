// Package tv is the admin + client surface that makes Maktaba TV
// reachable from outside the web app:
//
//	GET   /api/tv/info   (any signed-in user) where the TV streams live:
//	                     the streaming base URL, the HDHomeRun address Plex
//	                     wants, the XMLTV + M3U URLs, and the same-origin
//	                     paths the web app plays through the web port.
//	GET   /api/tv/hdhr   (admin) the emulated HDHomeRun tuner (slot 0084
//	                     hdhr_device), provisioned on first read.
//	PATCH /api/tv/hdhr   (admin) {enabled?, friendly_name?, tuner_count?}
//	GET   /api/tv/plex, POST /api/tv/plex/refresh, POST /api/tv/plex/register
//	                     (admin) Plex Live TV status and actions (plex.go)
//
// The streaming service owns the HDHomeRun protocol itself (discover.json,
// lineup.json, /auto/v{n}); it re-reads hdhr_device per request, so an
// enable here takes effect for a manually entered address immediately.
// SSDP auto-discovery is only started when streaming boots with the
// device enabled, so that part needs a restart.
package tv

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// MaxTuners caps tuner_count (D8): each tuner is one live MPEG-TS
// consumer of the channel engine, so a sane host ceiling.
const MaxTuners = 16

// Same-origin paths the web listener proxies to the streaming service.
const (
	XMLTVPath       = "/xmltv.xml"
	M3UPath         = "/lineup.m3u"
	HLSPathTemplate = "/stream/channel/{id}/index.m3u8"
)

// Handler bundles deps.
type Handler struct {
	DB *sql.DB
	// PublicBaseURL is the streaming service's address as other devices
	// on the network reach it (MAKTABA_PUBLIC_BASE_URL, set by
	// maktaba-server from [server] public_url / stream_addr). Empty falls
	// back to the request's host on DefaultStreamPort.
	PublicBaseURL string
	// DefaultStreamPort is used only by that fallback (default 8081).
	DefaultStreamPort string
	// Enqueue overrides how plex_livetv is queued (tests); nil uses
	// processing_jobs_enqueue (queues.Enqueue).
	Enqueue EnqueueFunc
	// NowFunc overrides the clock (tests).
	NowFunc func() time.Time
}

// Mount wires the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/tv/info", h.Info)
	r.Get("/api/tv/hdhr", h.GetHDHR)
	r.Patch("/api/tv/hdhr", h.PatchHDHR)
	h.mountPlex(r)
}

// Info is the GET /api/tv/info body.
type Info struct {
	StreamBaseURL   string `json:"stream_base_url"`
	HDHRAddress     string `json:"hdhr_address"`
	XMLTVURL        string `json:"xmltv_url"`
	M3UURL          string `json:"m3u_url"`
	BaseFromConfig  bool   `json:"base_from_config"`
	XMLTVPath       string `json:"xmltv_path"`
	M3UPath         string `json:"m3u_path"`
	HLSPathTemplate string `json:"hls_path_template"`
}

// Device is the GET/PATCH /api/tv/hdhr body.
type Device struct {
	DeviceID     string `json:"device_id"`
	FriendlyName string `json:"friendly_name"`
	TunerCount   int    `json:"tuner_count"`
	Enabled      bool   `json:"enabled"`
	MaxTuners    int    `json:"max_tuners"`
}

// PatchRequest is the PATCH body; every field optional.
type PatchRequest struct {
	Enabled      *bool   `json:"enabled,omitempty"`
	FriendlyName *string `json:"friendly_name,omitempty"`
	TunerCount   *int    `json:"tuner_count,omitempty"`
}

// Info answers where the TV is reachable.
func (h *Handler) Info(w http.ResponseWriter, r *http.Request) {
	if principal.FromContext(r.Context()) == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, h.info(r))
}

func (h *Handler) info(r *http.Request) Info {
	base, fromCfg := strings.TrimRight(strings.TrimSpace(h.PublicBaseURL), "/"), true
	if base == "" {
		fromCfg = false
		port := h.DefaultStreamPort
		if port == "" {
			port = "8081"
		}
		host := r.Host
		if hn, _, err := net.SplitHostPort(host); err == nil {
			host = hn
		}
		if host == "" {
			host = "127.0.0.1"
		}
		base = "http://" + net.JoinHostPort(host, port)
	}
	return Info{
		StreamBaseURL:   base,
		HDHRAddress:     hostPort(base),
		XMLTVURL:        base + XMLTVPath,
		M3UURL:          base + M3UPath,
		BaseFromConfig:  fromCfg,
		XMLTVPath:       XMLTVPath,
		M3UPath:         M3UPath,
		HLSPathTemplate: HLSPathTemplate,
	}
}

// hostPort strips the scheme/path from a base URL: Plex's "enter the
// HDHomeRun address" field wants host:port.
func hostPort(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return base
	}
	return u.Host
}

// GetHDHR returns the tuner device (admin).
func (h *Handler) GetHDHR(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	d, err := h.device(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load hdhr device"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, d)
}

// PatchHDHR updates the tuner device (admin).
func (h *Handler) PatchHDHR(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	var req PatchRequest
	if e := common.ReadJSON(r, &req, 16<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	var fieldErrs []httperror.FieldError
	if req.FriendlyName != nil {
		n := strings.TrimSpace(*req.FriendlyName)
		if n == "" || len(n) > 64 {
			fieldErrs = append(fieldErrs, httperror.FieldError{Field: "friendly_name", Message: "must be 1-64 characters"})
		}
		req.FriendlyName = &n
	}
	if req.TunerCount != nil && (*req.TunerCount < 1 || *req.TunerCount > MaxTuners) {
		fieldErrs = append(fieldErrs, httperror.FieldError{Field: "tuner_count", Message: "must be between 1 and 16"})
	}
	if len(fieldErrs) > 0 {
		httperror.Write(w, r, httperror.Unprocessable(fieldErrs))
		return
	}
	if _, err := h.device(r.Context()); err != nil { // provision first
		httperror.Write(w, r, httperror.Internal("load hdhr device"))
		return
	}
	sets, args := []string{}, []any{}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, col+" = $"+strconv.Itoa(len(args)))
	}
	if req.Enabled != nil {
		add("enabled", *req.Enabled)
	}
	if req.FriendlyName != nil {
		add("friendly_name", *req.FriendlyName)
	}
	if req.TunerCount != nil {
		add("tuner_count", *req.TunerCount)
	}
	if len(sets) > 0 {
		q := "UPDATE hdhr_device SET " + strings.Join(sets, ", ") + " WHERE id = 1"
		if _, err := h.DB.ExecContext(r.Context(), q, args...); err != nil {
			httperror.Write(w, r, httperror.Internal("update hdhr device"))
			return
		}
	}
	d, err := h.device(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("reload hdhr device"))
		return
	}
	if len(sets) > 0 {
		h.NotifyPlex(r.Context()) // the tuner's name or state is part of what Plex shows
	}
	common.WriteJSON(w, r, http.StatusOK, d)
}

// device loads the singleton, provisioning the same disabled row the
// streaming service would (stable 8-hex device id, Plex binds to it).
func (h *Handler) device(ctx context.Context) (Device, error) {
	d, err := h.load(ctx)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return d, err
	}
	if _, err := h.DB.ExecContext(ctx, `
		INSERT INTO hdhr_device (id, device_id, device_uuid, friendly_name, tuner_count, enabled)
		VALUES (1, $1, $2, 'Maktaba', 4, $3)
		ON CONFLICT (id) DO NOTHING
	`, newDeviceID(), uuid.NewString(), false); err != nil {
		return Device{}, err
	}
	return h.load(ctx)
}

func (h *Handler) load(ctx context.Context) (Device, error) {
	var d Device
	err := h.DB.QueryRowContext(ctx, `
		SELECT device_id, friendly_name, tuner_count, enabled
		FROM hdhr_device WHERE id = 1
	`).Scan(&d.DeviceID, &d.FriendlyName, &d.TunerCount, &d.Enabled)
	d.MaxTuners = MaxTuners
	return d, err
}

func isAdmin(r *http.Request) bool {
	p := principal.FromContext(r.Context())
	return p != nil && p.IsAdmin
}

func newDeviceID() string {
	u := uuid.New()
	return strings.ToUpper(strings.ReplaceAll(u.String(), "-", "")[:8])
}

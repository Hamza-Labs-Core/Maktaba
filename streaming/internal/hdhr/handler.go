package hdhr

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Streamer is the channel-engine surface the tuner endpoint needs: stream
// a channel as continuous MPEG-TS joined at the wall clock (Story 27.3
// D9). channel.Engine satisfies it.
type Streamer interface {
	StreamChannelTS(ctx context.Context, channelID uuid.UUID, w io.Writer) error
}

// Authorizer scopes which channels a request may see/pull (AC8). nil ⇒
// permissive (LAN-only deployments). A real implementation checks the
// per-device access token embedded in the advertised URLs.
type Authorizer interface {
	Allows(r *http.Request, channelID uuid.UUID) bool
	// DeviceAuth returns the token to advertise in /discover.json.
	DeviceAuth() string
}

// Handler serves the HDHomeRun protocol surface. Routes 404 when the
// feature is disabled (AC9); the tuner endpoint leases a slot and streams
// MPEG-TS from the channel engine.
type Handler struct {
	Repo     Repo
	Streamer Streamer
	Auth     Authorizer // optional
	Now      func() time.Time
	// PublicBase (MAKTABA_PUBLIC_BASE_URL) overrides the request-derived
	// base in discover/lineup URLs, e.g. "http://192.168.1.10:8781".
	PublicBase string

	leases *leaseRegistry
}

// New builds a Handler with a tuner-lease registry sized to the device's
// configured tuner count (re-checked per request from the device row).
func New(repo Repo, streamer Streamer) *Handler {
	return &Handler{Repo: repo, Streamer: streamer, Now: time.Now, leases: newLeaseRegistry(1)}
}

// Mount wires the HDHomeRun routes onto the streaming mux.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/discover.json", h.Discover)
	r.Get("/device.xml", h.DeviceXML)
	r.Get("/lineup.json", h.Lineup)
	r.Get("/lineup_status.json", h.LineupStatus)
	r.Get("/lineup.post", h.LineupPost)
	r.Post("/lineup.post", h.LineupPost)
	r.Get("/auto/v{channel}", h.Auto)
}

// device loads the singleton, syncs the lease cap, and reports enabled.
func (h *Handler) device(ctx context.Context) (Device, bool) {
	dev, err := h.Repo.Device(ctx)
	if err != nil {
		return Device{}, false
	}
	h.leases.setCap(dev.TunerCount)
	return dev, dev.Enabled
}

func (h *Handler) Discover(w http.ResponseWriter, r *http.Request) {
	dev, on := h.device(r.Context())
	if !on {
		http.NotFound(w, r)
		return
	}
	auth := ""
	if h.Auth != nil {
		auth = h.Auth.DeviceAuth()
	}
	writeJSON(w, buildDiscover(dev, h.base(r), auth))
}

func (h *Handler) DeviceXML(w http.ResponseWriter, r *http.Request) {
	dev, on := h.device(r.Context())
	if !on {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = io.WriteString(w, deviceXML(dev, h.base(r)))
}

func (h *Handler) Lineup(w http.ResponseWriter, r *http.Request) {
	if _, on := h.device(r.Context()); !on {
		http.NotFound(w, r)
		return
	}
	channels, err := h.Repo.Lineup(r.Context())
	if err != nil {
		http.Error(w, "lineup error", http.StatusInternalServerError)
		return
	}
	// AC8: scope to token-permitted channels.
	if h.Auth != nil {
		filtered := channels[:0]
		for _, c := range channels {
			if h.Auth.Allows(r, c.ID) {
				filtered = append(filtered, c)
			}
		}
		channels = filtered
	}
	writeJSON(w, buildLineup(channels, h.base(r)))
}

func (h *Handler) LineupStatus(w http.ResponseWriter, r *http.Request) {
	if _, on := h.device(r.Context()); !on {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, idleStatus())
}

// LineupPost is the no-op scan ack Plex expects to succeed (AC4).
func (h *Handler) LineupPost(w http.ResponseWriter, r *http.Request) {
	if _, on := h.device(r.Context()); !on {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// Auto streams a channel as MPEG-TS for one tuner connection (AC5/AC6/AC7).
func (h *Handler) Auto(w http.ResponseWriter, r *http.Request) {
	if _, on := h.device(r.Context()); !on {
		http.NotFound(w, r)
		return
	}
	number, err := parseChannelParam(chi.URLParam(r, "channel"))
	if err != nil {
		http.Error(w, "bad channel", http.StatusBadRequest)
		return
	}
	channels, err := h.Repo.Lineup(r.Context())
	if err != nil {
		http.Error(w, "lineup error", http.StatusInternalServerError)
		return
	}
	var target *LineupChannel
	for i := range channels {
		if channels[i].Number == number {
			target = &channels[i]
			break
		}
	}
	if target == nil {
		http.NotFound(w, r)
		return
	}
	// AC8: token must permit this channel.
	if h.Auth != nil && !h.Auth.Allows(r, target.ID) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// AC6: lease a tuner slot.
	lease, err := h.leases.acquire()
	if err != nil {
		// HDHomeRun "all tuners in use" — Plex shows the expected message.
		slog.Info("hdhr: all tuners in use", "channel_id", target.ID.String(), "event", "hdhr_tuners_busy")
		http.Error(w, "all tuners in use", http.StatusServiceUnavailable)
		return
	}
	defer lease.Release() // AC7: release → engine consumer stop → reaper

	w.Header().Set("Content-Type", "video/mp2t")
	w.WriteHeader(http.StatusOK)
	// Blocks until the source ends or the consumer disconnects (ctx done).
	if err := h.Streamer.StreamChannelTS(r.Context(), target.ID, w); err != nil && r.Context().Err() == nil {
		slog.Warn("hdhr: channel stream ended with an error", "channel_id", target.ID.String(),
			"number", target.Number, "err", err.Error(), "event", "hdhr_stream_failed")
	}
}

// parseChannelParam strips an optional "v" prefix and parses the number.
func parseChannelParam(s string) (int, error) {
	s = strings.TrimPrefix(s, "v")
	return strconv.Atoi(s)
}

// writeJSON writes v as UTF-8 JSON. Channel names (Arabic) go out as raw UTF-8
// bytes, not escapes; the charset is explicit for clients that guess.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

// base is PublicBase when configured, else the request-derived base.
func (h *Handler) base(r *http.Request) string { return publicBase(h.PublicBase, r) }

// publicBase returns the configured public base (trailing slash
// trimmed) or, when empty, the base derived from the request.
func publicBase(configured string, r *http.Request) string {
	if b := strings.TrimRight(strings.TrimSpace(configured), "/"); b != "" {
		return b
	}
	return baseURL(r)
}

// baseURL derives scheme://host from the inbound request (D4), honouring
// reverse-proxy headers so it works over LAN IP and the relay alike.
//
// X-Forwarded-* headers are client-controlled unless a trusted reverse
// proxy sets them, so they are honoured only when
// MAKTABA_TRUST_PROXY_HEADERS=1 (set MAKTABA_PUBLIC_BASE_URL otherwise).
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if trustProxyHeaders() {
		if xf := r.Header.Get("X-Forwarded-Proto"); xf == "http" || xf == "https" {
			scheme = xf
		}
		if xf := r.Header.Get("X-Forwarded-Host"); xf != "" && !strings.ContainsAny(xf, "/\\ \"<>") {
			host = xf
		}
	}
	return scheme + "://" + host
}

func trustProxyHeaders() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("MAKTABA_TRUST_PROXY_HEADERS")))
	return v == "1" || v == "true" || v == "yes"
}

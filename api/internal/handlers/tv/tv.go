// Package tv is the admin + client surface that makes Maktaba TV
// reachable from outside the web app:
//
//	GET   /api/tv/info   (any signed-in user) where the TV streams live:
//	                     the streaming base URL, the HDHomeRun address Plex
//	                     wants, the XMLTV + M3U URLs, the same-origin
//	                     paths the web app plays through the web port, and
//	                     remote{available, reason, via_relay, ...}: whether
//	                     the caller is away from home (remote.go, Epic 32.9).
//	GET   /api/tv/hdhr   (admin) the emulated HDHomeRun tuner (slot 0084
//	                     hdhr_device), provisioned on first read.
//	PATCH /api/tv/hdhr   (admin) {enabled?, friendly_name?, tuner_count?}
//	GET   /api/tv/plex, POST /api/tv/plex/refresh, POST /api/tv/plex/register
//	                     (admin) Plex Live TV status and actions (plex.go)
//
// The streaming service owns the HDHomeRun protocol itself (discover.json,
// lineup.json, /auto/v{n}); it re-reads hdhr_device per request, so an
// enable here takes effect for a manually entered address immediately,
// and the streaming service starts or stops SSDP auto-discovery within
// about ten seconds (only on the home: cluster nodes never advertise).
//
//	GET   /api/tv/onair  (admin) the channels on air now on every streaming
//	                     host (channel_runtime: host, encoder, viewers, muxer
//	                     restarts, NVENC fallbacks, heartbeat) and each
//	                     host's capacity (streaming_hosts).
package tv

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
)

// MaxTuners caps tuner_count (D8). A tuner is one client pull of a
// channel's MPEG-TS; pulls of the same channel share its one encode, so the
// count is independent of how many channels the host encodes at once.
const MaxTuners = 32

// DefaultTunerCount is a new device's tuner count (slot 0117 default).
const DefaultTunerCount = 20

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
	// Now is the clock (tests); nil: time.Now.
	Now func() time.Time
	// Enqueue overrides how plex_livetv is queued (tests); nil uses
	// processing_jobs_enqueue (queues.Enqueue).
	Enqueue EnqueueFunc
	// NowFunc overrides the clock (tests).
	NowFunc func() time.Time
	// Secrets is the home's sealed store (slot 0120); the Plex token is
	// there (ext.plex.token), not in extensions.settings. nil: only a token
	// still in the settings (before the start-up move) counts.
	Secrets secrets.Store
	// Remote reports remote viewing for /api/tv/info (Epic 32 story 32.9);
	// nil: not available on this server.
	Remote func(ctx context.Context) RemoteAvailability
	// Staging reports TV staging for /api/tv/info (Epic 33); nil: not reported.
	Staging func(ctx context.Context) any
}

// Mount wires the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/tv/info", h.Info)
	r.Get("/api/tv/hdhr", h.GetHDHR)
	r.Patch("/api/tv/hdhr", h.PatchHDHR)
	r.Get("/api/tv/onair", h.OnAir)
	h.mountPlex(r)
}

// OnAirChannel is one channel on air (a channel_runtime row, the
// cluster-wide record of where the channel encodes).
type OnAirChannel struct {
	ChannelID      string     `json:"channel_id"`
	Number         int        `json:"number"`
	Name           string     `json:"name"`
	HostID         string     `json:"host_id,omitempty"`
	Host           string     `json:"host"`
	StreamBaseURL  string     `json:"stream_base_url,omitempty"`
	PID            *int64     `json:"pid,omitempty"`
	State          string     `json:"state"`
	Encoder        string     `json:"encoder,omitempty"`
	Viewers        int        `json:"viewers"`
	Restarts       int        `json:"restarts"`
	NVENCFallbacks int        `json:"nvenc_fallbacks"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	HeartbeatAt    *time.Time `json:"heartbeat_at,omitempty"`
	// Stale: the host stopped heartbeating (it died or was cut off); the
	// row no longer owns the channel.
	Stale bool `json:"stale"`
}

// StreamingHost is one streaming host's capacity (streaming_hosts).
type StreamingHost struct {
	HostID         string     `json:"host_id"`
	Host           string     `json:"host"`
	Role           string     `json:"role"`
	StreamBaseURL  string     `json:"stream_base_url,omitempty"`
	Encoder        string     `json:"encoder,omitempty"`
	MaxActive      int        `json:"max_active"`
	ActiveChannels int        `json:"active_channels"`
	NVENCFree      int        `json:"nvenc_free"` // -1: no cap
	VTFree         int        `json:"vt_free"`    // -1: no cap
	HeartbeatAt    *time.Time `json:"heartbeat_at,omitempty"`
	Stale          bool       `json:"stale"`
}

// OnAirResponse is the GET /api/tv/onair body.
type OnAirResponse struct {
	Channels []OnAirChannel  `json:"channels"`
	Hosts    []StreamingHost `json:"hosts"`
}

// RuntimeStaleAfter mirrors the streaming service: a row whose heartbeat
// is older than this belongs to a dead host.
const RuntimeStaleAfter = 45 * time.Second

// OnAir lists the channels on air now on every streaming host, and the
// hosts' capacity (admin).
func (h *Handler) OnAir(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}
	stale := func(t dbTime) bool { return !t.ok || now.Sub(t.t) > RuntimeStaleAfter }
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT rt.channel_id, COALESCE(c.number, 0), COALESCE(c.name, ''), rt.host_id, rt.host,
		       rt.stream_base_url, rt.pid, rt.state, rt.encoder, rt.viewer_count, rt.restarts,
		       rt.nvenc_fallbacks, rt.started_at, COALESCE(rt.heartbeat_at, rt.last_segment_at)
		FROM channel_runtime rt
		LEFT JOIN channels c ON c.id = rt.channel_id
		ORDER BY c.number, rt.host`)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("read channel runtime").Wrap(err))
		return
	}
	defer rows.Close()
	out := OnAirResponse{Channels: []OnAirChannel{}, Hosts: []StreamingHost{}}
	for rows.Next() {
		var c OnAirChannel
		var pid sql.NullInt64
		var hostID, base, enc sql.NullString
		var started, beat dbTime
		if err := rows.Scan(&c.ChannelID, &c.Number, &c.Name, &hostID, &c.Host, &base, &pid, &c.State, &enc,
			&c.Viewers, &c.Restarts, &c.NVENCFallbacks, &started, &beat); err != nil {
			httperror.Write(w, r, httperror.Internal("read channel runtime").Wrap(err))
			return
		}
		if pid.Valid {
			c.PID = &pid.Int64
		}
		c.HostID, c.StreamBaseURL, c.Encoder = hostID.String, base.String, enc.String
		c.StartedAt, c.HeartbeatAt, c.Stale = started.ptr(), beat.ptr(), stale(beat)
		out.Channels = append(out.Channels, c)
	}
	if rows.Err() != nil {
		httperror.Write(w, r, httperror.Internal("read channel runtime"))
		return
	}
	hrows, err := h.DB.QueryContext(r.Context(), `
		SELECT host_id, host, role, stream_base_url, encoder, max_active, active_channels,
		       nvenc_free, vt_free, heartbeat_at
		FROM streaming_hosts ORDER BY role, host`)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("read streaming hosts").Wrap(err))
		return
	}
	defer hrows.Close()
	for hrows.Next() {
		var s StreamingHost
		var base, enc sql.NullString
		var beat dbTime
		if err := hrows.Scan(&s.HostID, &s.Host, &s.Role, &base, &enc, &s.MaxActive, &s.ActiveChannels,
			&s.NVENCFree, &s.VTFree, &beat); err != nil {
			httperror.Write(w, r, httperror.Internal("read streaming hosts").Wrap(err))
			return
		}
		s.StreamBaseURL, s.Encoder = base.String, enc.String
		s.HeartbeatAt, s.Stale = beat.ptr(), stale(beat)
		out.Hosts = append(out.Hosts, s)
	}
	if hrows.Err() != nil {
		httperror.Write(w, r, httperror.Internal("read streaming hosts"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// dbTime scans a timestamp from Postgres (time.Time) or SQLite (text).
type dbTime struct {
	t  time.Time
	ok bool
}

func (d *dbTime) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		d.ok = false
	case time.Time:
		d.t, d.ok = x, true
	case string:
		return d.parse(x)
	case []byte:
		return d.parse(string(x))
	default:
		return fmt.Errorf("tv: cannot scan %T as a time", v)
	}
	return nil
}

func (d *dbTime) parse(s string) error {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			d.t, d.ok = t, true
			return nil
		}
	}
	d.ok = false
	return nil
}

func (d dbTime) ptr() *time.Time {
	if !d.ok {
		return nil
	}
	t := d.t.UTC()
	return &t
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
	// Remote: whether this request came from away from home and whether
	// remote viewing can serve it (story 32.9).
	Remote RemoteInfo `json:"remote"`
	// Cluster: the streaming hosts on air (Epic 32).
	Cluster ClusterInfo `json:"cluster"`
	// Staging: TV staging on or off, the live release, the change in flight (Epic 33).
	Staging any `json:"staging,omitempty"`
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
			port = ports.StreamingPublic.PortString()
		}
		host := r.Host
		if hn, _, err := net.SplitHostPort(host); err == nil {
			host = hn
		}
		if host == "" || isLoopbackHost(host) {
			// an admin on the server itself: Plex needs the LAN address
			if ip := lanIP(); ip != "" {
				host = ip
			} else if host == "" {
				host = "127.0.0.1"
			}
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
		Remote:          h.remoteInfo(r),
		Cluster:         ClusterInfo{HostsOnAir: h.hostsOnAir(r.Context())},
		Staging:         h.stagingInfo(r.Context()),
	}
}

func (h *Handler) stagingInfo(ctx context.Context) any {
	if h.Staging == nil {
		return nil
	}
	return h.Staging(ctx)
}

// hostPort strips the scheme/path from a base URL: Plex's "enter the
// HDHomeRun address" field wants host:port.
func isLoopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

// interfaceAddrs is net.InterfaceAddrs (a test seam; reading it opens no socket).
var interfaceAddrs = net.InterfaceAddrs

// lanIP is this host's first private IPv4 address, or "".
func lanIP() string {
	addrs, err := interfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() {
			return n.IP.To4().String()
		}
	}
	return ""
}

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
		httperror.Write(w, r, httperror.Internal("load hdhr device").Wrap(err))
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
		fieldErrs = append(fieldErrs, httperror.FieldError{Field: "tuner_count", Message: "must be between 1 and " + strconv.Itoa(MaxTuners)})
	}
	if len(fieldErrs) > 0 {
		httperror.Write(w, r, httperror.Unprocessable(fieldErrs))
		return
	}
	if _, err := h.device(r.Context()); err != nil { // provision first
		httperror.Write(w, r, httperror.Internal("load hdhr device").Wrap(err))
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
			httperror.Write(w, r, httperror.Internal("update hdhr device").Wrap(err))
			return
		}
	}
	d, err := h.device(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("reload hdhr device").Wrap(err))
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
		VALUES (1, $1, $2, 'Maktaba', $3, $4)
		ON CONFLICT (id) DO NOTHING
	`, newDeviceID(), uuid.NewString(), DefaultTunerCount, false); err != nil {
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

// newDeviceID is a fresh 8-hex-digit HDHomeRun DeviceID whose last digit is
// the check digit libhdhomerun validates (hdhomerun_discover.c,
// hdhomerun_discover_validate_device_id); the streaming service generates and
// repairs ids the same way (streaming/internal/hdhr/hdhrpkt.go).
func newDeviceID() string {
	for {
		u := uuid.New()
		id := withCheckDigit(uint32(u[0])<<24 | uint32(u[1])<<16 | uint32(u[2])<<8 | uint32(u[3]))
		if id != 0 && id != 0xFFFFFFFF {
			return fmt.Sprintf("%08X", id)
		}
	}
}

// deviceIDTable is hdhomerun_discover_validate_device_id's lookup_table.
var deviceIDTable = [16]uint32{0xA, 0x5, 0xF, 0x6, 0x7, 0xC, 0x1, 0xB, 0x9, 0x2, 0x8, 0xD, 0x4, 0x3, 0xE, 0x0}

// withCheckDigit sets id's last nibble so the libhdhomerun checksum is zero:
// it xors the 1st/3rd/5th/7th nibbles through the table and the others as is,
// and the last nibble enters unmapped.
func withCheckDigit(id uint32) uint32 {
	id &^= 0x0F
	var c uint32
	for shift := 28; shift >= 4; shift -= 8 {
		c ^= deviceIDTable[(id>>uint(shift))&0x0F]
		c ^= (id >> uint(shift-4)) & 0x0F
	}
	return id | c
}

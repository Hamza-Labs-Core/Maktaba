// Package tvcluster is the admin surface of Maktaba TV across the cluster (Epic 32,
// stories 32.4 and 32.5):
//
//	GET    /api/tv/cluster                          settings, streaming hosts (health and why
//	                                                a host takes no channels), channels on air
//	PUT    /api/tv/cluster/hosts/{host_id}          {enabled?, drain?, move_now?, capacity_override?,
//	                                                weight?, stream_base_url?}
//	POST   /api/tv/cluster/channels/{id}/move       {host_id?} (empty: the balancer chooses)
//	DELETE /api/tv/cluster/channels/{id}/placement  stop the channel everywhere
//
// The decisions are the home streaming service's (the balancer runs there): move, stop
// and "move now" are forwarded to it on loopback, signed with the cluster stream key
// (MAKTABA_STREAMING_HTTP_URL, default http://127.0.0.1:8081).
package tvcluster

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// ProtocolVersion mirrors the streaming cluster protocol (streaming/internal/cluster).
const ProtocolVersion = 1

// Handler bundles deps.
type Handler struct {
	DB *sql.DB
	// StreamingURL is the home streaming service's HTTP base (loopback).
	StreamingURL string
	Client       *http.Client
	Now          func() time.Time
}

// New builds a Handler reading MAKTABA_STREAMING_HTTP_URL.
func New(db *sql.DB) *Handler {
	u := strings.TrimRight(os.Getenv("MAKTABA_STREAMING_HTTP_URL"), "/")
	if u == "" {
		u = "http://127.0.0.1:8081"
	}
	return &Handler{DB: db, StreamingURL: u}
}

// Mount wires the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/tv/cluster", h.Get)
	r.Put("/api/tv/cluster/hosts/{host_id}", h.PutHost)
	r.Post("/api/tv/cluster/channels/{id}/move", h.Move)
	r.Delete("/api/tv/cluster/channels/{id}/placement", h.Stop)
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func isAdmin(r *http.Request) bool {
	p := principal.FromContext(r.Context())
	return p != nil && p.IsAdmin
}

// Settings is rules.tv_cluster as the page shows it.
type Settings struct {
	NodesStream      bool                `json:"nodes_stream"`
	Policy           string              `json:"policy"`
	SpillAt          int                 `json:"spill_at"`
	HLSMode          string              `json:"hls_mode"`
	Failover         bool                `json:"failover"`
	DeadAfterSeconds int                 `json:"dead_after_seconds"`
	Pins             map[string][]string `json:"pins"`
}

func defaultSettings() Settings {
	return Settings{Policy: "home_first", SpillAt: 100, HLSMode: "proxy", Failover: true,
		DeadAfterSeconds: 15, Pins: map[string][]string{}}
}

// parseSettings overlays stored changes (the streaming service's rules, same limits).
func parseSettings(raw []byte) Settings {
	s := defaultSettings()
	var m map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return s
	}
	var b bool
	var str string
	var n float64
	if v, ok := m["nodes_stream"]; ok && json.Unmarshal(v, &b) == nil {
		s.NodesStream = b
	}
	if v, ok := m["failover"]; ok && json.Unmarshal(v, &b) == nil {
		s.Failover = b
	}
	if v, ok := m["policy"]; ok && json.Unmarshal(v, &str) == nil && (str == "home_first" || str == "balanced") {
		s.Policy = str
	}
	if v, ok := m["hls_mode"]; ok && json.Unmarshal(v, &str) == nil && (str == "proxy" || str == "redirect") {
		s.HLSMode = str
	}
	if v, ok := m["spill_at"]; ok && json.Unmarshal(v, &n) == nil && n >= 1 && n <= 100 {
		s.SpillAt = int(n)
	}
	if v, ok := m["dead_after_seconds"]; ok && json.Unmarshal(v, &n) == nil && n >= 5 && n <= 300 {
		s.DeadAfterSeconds = int(n)
	}
	if v, ok := m["pins"]; ok {
		var pins map[string]json.RawMessage
		if json.Unmarshal(v, &pins) == nil {
			for slug, pv := range pins {
				var list []string
				if json.Unmarshal(pv, &list) == nil && len(list) > 0 {
					s.Pins[slug] = list[:1]
				} else if json.Unmarshal(pv, &str) == nil && str != "" {
					s.Pins[slug] = []string{str}
				}
			}
		}
	}
	return s
}

// Host is one streaming host on the page.
type Host struct {
	HostID              string          `json:"host_id"`
	Role                string          `json:"role"`
	Name                string          `json:"name"`
	State               string          `json:"state"`
	Reason              *string         `json:"reason"`
	Enabled             bool            `json:"enabled"`
	Draining            bool            `json:"draining"`
	Encoders            []string        `json:"encoders"`
	Capacity            int             `json:"capacity"`
	CapacityOverride    *int            `json:"capacity_override"`
	Used                int             `json:"used"`
	Weight              int             `json:"weight"`
	Load                map[string]any  `json:"load"`
	CanPush             map[string]bool `json:"can_push"`
	CanLive             bool            `json:"can_live"`
	Version             string          `json:"version"`
	LastBeatAt          *time.Time      `json:"last_beat_at"`
	StreamBaseURL       string          `json:"stream_base_url"`
	StreamBaseURLSource string          `json:"stream_base_url_source"`
	Channels            []string        `json:"channels"`

	reported int
	protocol int
}

// Viewers of a channel on air.
type Viewers struct {
	HLS    int `json:"hls"`
	Tuners int `json:"tuners"`
	Remote int `json:"remote"`
}

// OnAir is one channel on air (a placement, or a channel_runtime row without one).
type OnAir struct {
	ChannelID      string     `json:"channel_id"`
	Number         int        `json:"number"`
	Name           string     `json:"name"`
	Slug           string     `json:"slug"`
	HostID         string     `json:"host_id"`
	HostName       string     `json:"host_name"`
	Generation     int64      `json:"generation"`
	State          string     `json:"state"`
	Reason         string     `json:"reason"`
	Since          *time.Time `json:"since"`
	Encoder        string     `json:"encoder"`
	Viewers        Viewers    `json:"viewers"`
	Restarts       int        `json:"restarts"`
	NVENCFallbacks int        `json:"nvenc_fallbacks"`
	Push           any        `json:"push"`
	Stale          bool       `json:"stale"`
}

// Notice is something the admin should know.
type Notice struct {
	Kind      string     `json:"kind"`
	Message   string     `json:"message"`
	Hosts     []string   `json:"hosts,omitempty"`
	ChannelID string     `json:"channel_id,omitempty"`
	At        *time.Time `json:"at,omitempty"`
}

// Response is the GET /api/tv/cluster body.
type Response struct {
	Settings Settings `json:"settings"`
	Hosts    []Host   `json:"hosts"`
	OnAir    []OnAir  `json:"on_air"`
	Notices  []Notice `json:"notices"`
	// Failovers24h: channels whose current placement came from a failover in
	// the last 24 h (the System Health TV line, story 32.11 AC5).
	Failovers24h int `json:"failovers_24h"`
}

// Get answers the whole page (one poll every 5 s).
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	resp, err := h.read(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("read the streaming cluster"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, resp)
}

func (h *Handler) settings(ctx context.Context) Settings {
	var raw sql.NullString
	if err := h.DB.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = 'rules.tv_cluster'`).Scan(&raw); err != nil {
		return defaultSettings()
	}
	return parseSettings([]byte(raw.String))
}

func jsonInto(s sql.NullString, v any) {
	if s.Valid && s.String != "" {
		_ = json.Unmarshal([]byte(s.String), v)
	}
}

func (h *Handler) hosts(ctx context.Context, st Settings) ([]Host, error) {
	rows, err := h.DB.QueryContext(ctx, `
		SELECT host_id, host, role, stream_base_url, stream_base_url_source, enabled, draining,
		       encoders, max_active, capacity_override, weight, active, load, can_push, can_live,
		       unfit, version, heartbeat_at
		FROM streaming_hosts ORDER BY role, host, host_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := h.now()
	out := []Host{} // JSON [] when no machine streams TV yet, never null
	for rows.Next() {
		var x Host
		var base, src, encs, active, load, push, unfit, ver sql.NullString
		var override sql.NullInt64
		var beat dbTime
		if err := rows.Scan(&x.HostID, &x.Name, &x.Role, &base, &src, &x.Enabled, &x.Draining, &encs,
			&x.reported, &override, &x.Weight, &active, &load, &push, &x.CanLive, &unfit, &ver, &beat); err != nil {
			return nil, err
		}
		x.StreamBaseURL, x.StreamBaseURLSource, x.Version = base.String, src.String, ver.String
		x.Encoders, x.Load, x.CanPush, x.Channels = []string{}, map[string]any{}, map[string]bool{}, []string{}
		jsonInto(encs, &x.Encoders)
		jsonInto(load, &x.Load)
		jsonInto(push, &x.CanPush)
		x.Capacity = x.reported
		if override.Valid {
			o := int(override.Int64)
			x.CapacityOverride, x.Capacity = &o, o
		}
		if p, ok := x.Load["protocol"].(float64); ok {
			x.protocol = int(p)
		}
		x.LastBeatAt = beat.ptr()
		state, reason := hostState(x, unfit.String, beat, now, st)
		x.State = state
		if reason != "" {
			x.Reason = &reason
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// hostState mirrors the streaming balancer's health (design §2.4).
func hostState(x Host, unfit string, beat dbTime, now time.Time, st Settings) (string, string) {
	dead := time.Duration(st.DeadAfterSeconds) * time.Second
	switch {
	case !beat.ok || now.Sub(beat.t) >= dead:
		return "down", "no heartbeat for over " + strconv.Itoa(st.DeadAfterSeconds) + " s"
	case x.protocol != 0 && x.protocol != ProtocolVersion:
		return "unfit", "version"
	case unfit != "":
		return "unfit", unfit
	case !x.Enabled:
		return "disabled", "\"Stream on this machine\" is off"
	case x.Role != "home" && !st.NodesStream:
		return "disabled", "other machines may not stream (tv_cluster.nodes_stream)"
	case x.Draining:
		return "draining", "draining: takes no new channels"
	}
	return "online", ""
}

func (h *Handler) read(ctx context.Context) (Response, error) {
	st := h.settings(ctx)
	resp := Response{Settings: st, Hosts: []Host{}, OnAir: []OnAir{}, Notices: []Notice{}}
	hosts, err := h.hosts(ctx, st)
	if err != nil {
		return resp, err
	}
	byID := map[string]*Host{}
	for i := range hosts {
		byID[hosts[i].HostID] = &hosts[i]
	}
	// the home reports its front door's viewers and its last refusal in its beat
	viewers := map[string]Viewers{}
	var homes []string
	for _, x := range hosts {
		if x.Role != "home" {
			continue
		}
		homes = append(homes, x.Name)
		if v, ok := x.Load["viewers"].(map[string]any); ok {
			for ch, raw := range v {
				b, _ := json.Marshal(raw)
				var vv Viewers
				_ = json.Unmarshal(b, &vv)
				viewers[ch] = vv
			}
		}
		if nr, ok := x.Load["no_room"].(map[string]any); ok {
			n := Notice{Kind: "no_room", Message: fmt.Sprint("no host could take a channel: ", nr["reason"])}
			if c, ok := nr["channel_id"].(string); ok {
				n.ChannelID = c
			}
			if at, ok := nr["at"].(string); ok {
				if t, err := time.Parse(time.RFC3339Nano, at); err == nil {
					if h.now().Sub(t) > 10*time.Minute {
						continue
					}
					n.At = &t
				}
			}
			resp.Notices = append(resp.Notices, n)
		}
		if ps, ok := x.Load["pin_skips"].(map[string]any); ok {
			keys := make([]string, 0, len(ps))
			for k := range ps {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				resp.Notices = append(resp.Notices, Notice{Kind: "pin_skipped",
					Message: fmt.Sprintf("preferred machine %s skipped: %v", k, ps[k])})
			}
		}
	}
	if len(homes) > 1 {
		resp.Notices = append([]Notice{{Kind: "two_homes", Hosts: homes,
			Message: "more than one machine streams as the home: " + strings.Join(homes, ", ")}}, resp.Notices...)
	}
	onAir, err := h.onAir(ctx, byID, viewers)
	if err != nil {
		return resp, err
	}
	resp.Hosts, resp.OnAir = hosts, onAir
	resp.Failovers24h = h.failovers(ctx, 24*time.Hour)
	return resp, nil
}

// failovers counts the placements made by a failover within d (0 on error).
func (h *Handler) failovers(ctx context.Context, d time.Duration) int {
	rows, err := h.DB.QueryContext(ctx, `SELECT placed_at FROM channel_placements WHERE reason = 'failover'`)
	if err != nil {
		return 0
	}
	defer rows.Close()
	since := h.now().Add(-d)
	n := 0
	for rows.Next() {
		var at dbTime
		if rows.Scan(&at) == nil && at.ok && !at.t.Before(since) {
			n++
		}
	}
	return n
}

func (h *Handler) onAir(ctx context.Context, byID map[string]*Host, viewers map[string]Viewers) ([]OnAir, error) {
	out := []OnAir{}
	placed := map[string]bool{}
	rows, err := h.DB.QueryContext(ctx, `
		SELECT p.channel_id, COALESCE(c.number, 0), COALESCE(c.name, ''), COALESCE(c.slug, ''), p.host_id,
		       p.generation, p.state, p.reason, p.placed_at, p.acked_at,
		       rt.host_id, rt.encoder, rt.restarts, rt.nvenc_fallbacks
		FROM channel_placements p
		LEFT JOIN channels c ON c.id = p.channel_id
		LEFT JOIN channel_runtime rt ON rt.channel_id = p.channel_id
		WHERE p.state IN ('placing', 'live', 'moving')
		ORDER BY COALESCE(c.number, 0), p.channel_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var o OnAir
		var placedAt, acked dbTime
		var rtHost, enc sql.NullString
		var restarts, fb sql.NullInt64
		if err := rows.Scan(&o.ChannelID, &o.Number, &o.Name, &o.Slug, &o.HostID, &o.Generation, &o.State,
			&o.Reason, &placedAt, &acked, &rtHost, &enc, &restarts, &fb); err != nil {
			rows.Close()
			return nil, err
		}
		o.Since = acked.ptr()
		if o.Since == nil {
			o.Since = placedAt.ptr()
		}
		if rtHost.String == o.HostID {
			o.Encoder, o.Restarts, o.NVENCFallbacks = enc.String, int(restarts.Int64), int(fb.Int64)
		}
		if x := byID[o.HostID]; x != nil {
			o.HostName = x.Name
			x.Used++
			x.Channels = append(x.Channels, o.ChannelID)
			o.Stale = x.State == "down"
		}
		v := viewers[o.ChannelID]
		o.Viewers = Viewers{HLS: v.HLS, Tuners: v.Tuners}
		placed[o.ChannelID] = true
		out = append(out, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// channel_runtime rows no placement explains: stale (the home cleans them)
	rrows, err := h.DB.QueryContext(ctx, `
		SELECT rt.channel_id, COALESCE(c.number, 0), COALESCE(c.name, ''), COALESCE(c.slug, ''),
		       COALESCE(rt.host_id, ''), rt.host, rt.encoder, rt.restarts, rt.nvenc_fallbacks, rt.started_at
		FROM channel_runtime rt LEFT JOIN channels c ON c.id = rt.channel_id
		ORDER BY COALESCE(c.number, 0)`)
	if err != nil {
		return nil, err
	}
	defer rrows.Close()
	for rrows.Next() {
		var o OnAir
		var hostName string
		var enc sql.NullString
		var started dbTime
		if err := rrows.Scan(&o.ChannelID, &o.Number, &o.Name, &o.Slug, &o.HostID, &hostName, &enc,
			&o.Restarts, &o.NVENCFallbacks, &started); err != nil {
			return nil, err
		}
		if placed[o.ChannelID] {
			continue
		}
		o.HostName, o.Encoder, o.Since = hostName, enc.String, started.ptr()
		o.State, o.Reason, o.Stale = "runtime", "", true
		out = append(out, o)
	}
	return out, rrows.Err()
}

// HostPatch is the PUT body (absent fields stay as they are).
type HostPatch struct {
	Enabled          *bool           `json:"enabled"`
	Drain            *bool           `json:"drain"`
	MoveNow          bool            `json:"move_now"`
	CapacityOverride json.RawMessage `json:"capacity_override"`
	Weight           *int            `json:"weight"`
	StreamBaseURL    json.RawMessage `json:"stream_base_url"`
}

// PutHost changes a host's switches.
func (h *Handler) PutHost(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	id := chi.URLParam(r, "host_id")
	var p HostPatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&p); err != nil {
		httperror.Write(w, r, httperror.BadRequest("body must be a JSON object"))
		return
	}
	var role string
	if err := h.DB.QueryRowContext(r.Context(), `SELECT role FROM streaming_hosts WHERE host_id = $1`, id).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("no such streaming host"))
			return
		}
		httperror.Write(w, r, httperror.Internal("read the streaming host"))
		return
	}
	sets, args, herr := buildPatch(p, role)
	if herr != nil {
		httperror.Write(w, r, herr)
		return
	}
	if len(sets) > 0 {
		args = append(args, id)
		q := `UPDATE streaming_hosts SET ` + strings.Join(sets, ", ") + ` WHERE host_id = $` + strconv.Itoa(len(args))
		if _, err := h.DB.ExecContext(r.Context(), q, args...); err != nil {
			httperror.Write(w, r, httperror.Internal("update the streaming host"))
			return
		}
	}
	if p.MoveNow {
		if _, _, err := h.call(r.Context(), "/internal/tv/drain/"+url.PathEscape(id), nil); err != nil {
			httperror.Write(w, r, httperror.BadGateway("the home's streaming service did not answer: "+err.Error()))
			return
		}
	}
	resp, err := h.read(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("read the streaming host"))
		return
	}
	for _, x := range resp.Hosts {
		if x.HostID == id {
			common.WriteJSON(w, r, http.StatusOK, x)
			return
		}
	}
	httperror.Write(w, r, httperror.NotFound("no such streaming host"))
}

// buildPatch validates the patch (TC1 of 32.5): the home cannot be switched off;
// capacity override 0-64 or null; weight 1-100; stream base URL http(s), or null/""
// to go back to the reported one (nodes only).
func buildPatch(p HostPatch, role string) ([]string, []any, *httperror.Error) {
	var sets []string
	var args []any
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, col+" = $"+strconv.Itoa(len(args)))
	}
	if p.Enabled != nil {
		if role == "home" && !*p.Enabled {
			return nil, nil, httperror.Conflict("", "the home always streams")
		}
		add("enabled", *p.Enabled)
	}
	if p.Drain != nil {
		add("draining", *p.Drain)
	}
	if len(p.CapacityOverride) > 0 {
		if string(p.CapacityOverride) == "null" {
			sets = append(sets, "capacity_override = NULL")
		} else {
			var n float64
			if json.Unmarshal(p.CapacityOverride, &n) != nil || n != float64(int(n)) || n < 0 || n > 64 {
				return nil, nil, httperror.BadRequest("capacity_override must be a whole number 0-64, or null")
			}
			add("capacity_override", int(n))
		}
	}
	if p.Weight != nil {
		if *p.Weight < 1 || *p.Weight > 100 {
			return nil, nil, httperror.BadRequest("weight must be 1-100")
		}
		add("weight", *p.Weight)
	}
	if len(p.StreamBaseURL) > 0 {
		var s string
		if string(p.StreamBaseURL) != "null" && json.Unmarshal(p.StreamBaseURL, &s) != nil {
			return nil, nil, httperror.BadRequest("stream_base_url must be a URL, or null")
		}
		s = strings.TrimRight(strings.TrimSpace(s), "/")
		if s == "" {
			sets = append(sets, "stream_base_url_source = 'reported'")
		} else {
			if role == "home" {
				return nil, nil, httperror.Conflict("", "the home's address comes from its configuration (public_url)")
			}
			u, err := url.Parse(s)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return nil, nil, httperror.BadRequest("stream_base_url must be an http(s) URL")
			}
			add("stream_base_url", s)
			sets = append(sets, "stream_base_url_source = 'admin'")
		}
	}
	return sets, args, nil
}

// Move re-places one channel (to a host, or the balancer's choice).
func (h *Handler) Move(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httperror.Write(w, r, httperror.BadRequest("bad channel id"))
		return
	}
	var body struct {
		HostID string `json:"host_id"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			httperror.Write(w, r, httperror.BadRequest("body must be {host_id?}"))
			return
		}
	}
	h.forward(w, r, "/internal/tv/place/"+id.String(), map[string]string{"action": "move", "host_id": body.HostID}, http.StatusAccepted)
}

// Stop stops a channel everywhere.
func (h *Handler) Stop(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httperror.Write(w, r, httperror.BadRequest("bad channel id"))
		return
	}
	code, body, err := h.call(r.Context(), "/internal/tv/place/"+id.String(), map[string]string{"action": "stop"})
	if err != nil {
		httperror.Write(w, r, httperror.BadGateway("the home's streaming service did not answer: "+err.Error()))
		return
	}
	if code >= 300 {
		writeUpstreamError(w, r, code, body)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) forward(w http.ResponseWriter, r *http.Request, path string, body any, okCode int) {
	code, b, err := h.call(r.Context(), path, body)
	if err != nil {
		httperror.Write(w, r, httperror.BadGateway("the home's streaming service did not answer: "+err.Error()))
		return
	}
	if code >= 300 {
		writeUpstreamError(w, r, code, b)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(okCode)
	_, _ = w.Write(b)
}

func writeUpstreamError(w http.ResponseWriter, r *http.Request, code int, body []byte) {
	msg := strings.TrimSpace(string(body))
	switch code {
	case http.StatusConflict:
		httperror.Write(w, r, httperror.Conflict("", msg))
	case http.StatusServiceUnavailable:
		httperror.Write(w, r, httperror.Conflict("", "no host can take the channel: "+msg))
	case http.StatusBadRequest:
		httperror.Write(w, r, httperror.BadRequest(msg))
	default:
		httperror.Write(w, r, httperror.BadGateway(msg))
	}
}

// call POSTs to the home streaming service, signed with the cluster stream key.
func (h *Handler) call(ctx context.Context, path string, body any) (int, []byte, error) {
	key, err := h.streamKey(ctx)
	if err != nil {
		return 0, nil, err
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.StreamingURL, "/")+path, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Maktaba-Front", Sign(key, http.MethodPost, path, h.now().Add(60*time.Second)))
	cl := h.Client
	if cl == nil {
		cl = http.DefaultClient
	}
	resp, err := cl.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, b, nil
}

func (h *Handler) streamKey(ctx context.Context) ([]byte, error) {
	var key []byte
	if err := h.DB.QueryRowContext(ctx, `SELECT stream_key FROM tv_cluster_secret WHERE id = 1`).Scan(&key); err != nil {
		return nil, errors.New("no cluster stream key yet (the home's streaming service creates it at boot)")
	}
	return key, nil
}

// Sign is the front-door signature (streaming/internal/cluster.Keyring.Sign): HMAC-SHA256
// over "METHOD\npath\nexp\ngen" with the cluster stream key, "<exp>.<hex>". These
// requests carry no placement generation: gen is 0 (streaming cluster protocol 2).
func Sign(key []byte, method, path string, exp time.Time) string {
	e := strconv.FormatInt(exp.Unix(), 10)
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(method + "\n" + path + "\n" + e + "\n0"))
	return e + "." + hex.EncodeToString(m.Sum(nil))
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
		d.parse(x)
	case []byte:
		d.parse(string(x))
	default:
		return fmt.Errorf("tvcluster: cannot scan %T as a time", v)
	}
	return nil
}

func (d *dbTime) parse(s string) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			d.t, d.ok = t, true
			return
		}
	}
	d.ok = false
}

func (d dbTime) ptr() *time.Time {
	if !d.ok {
		return nil
	}
	t := d.t.UTC()
	return &t
}

// Read is the GET /api/tv/cluster body without the HTTP layer (the System
// health page, api/internal/handlers/systemstatus).
func (h *Handler) Read(ctx context.Context) (Response, error) {
	return h.read(ctx)
}

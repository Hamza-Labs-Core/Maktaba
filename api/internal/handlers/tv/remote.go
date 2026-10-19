package tv

// Epic 32 story 32.9: GET /api/tv/info tells the player whether this
// request came from away from home (through the Epic 25 relay, or — with
// tv_remote.remote_detection = relay_or_public_ip — from a public address)
// and whether remote viewing can serve it. The web player then plays the
// LAN stream or tunes through /api/tv/remote/tune.

import (
	"context"
	"net"
	"net/http"
	"time"
)

// ViaHeader is set to "relay" by the relay agent on its loopback calls; the
// API strips it from any request that does not come from loopback
// (middleware.StripVia), so a client cannot set it.
const ViaHeader = "X-Maktaba-Via"

// Detection modes (tv_remote.remote_detection).
const (
	DetectRelay           = "relay"
	DetectRelayOrPublicIP = "relay_or_public_ip"
)

// RemoteAvailability is what the remote controller says about remote
// viewing for /api/tv/info.
type RemoteAvailability struct {
	Available       bool
	Reason          string
	MaxStreams      int
	TokenTTLMinutes int
	// Detection is tv_remote.remote_detection ("" = relay).
	Detection string
}

// RemoteInfo is /api/tv/info's "remote" object.
type RemoteInfo struct {
	Available bool    `json:"available"`
	Reason    *string `json:"reason"`
	// ViaRelay: this request came from away from home; the player plays
	// from Cloudflare (tune, heartbeat, end) instead of the LAN stream.
	ViaRelay        bool `json:"via_relay"`
	MaxStreams      int  `json:"max_streams"`
	TokenTTLMinutes int  `json:"token_ttl_minutes"`
}

// ClusterInfo is /api/tv/info's "cluster" object.
type ClusterInfo struct {
	// HostsOnAir: streaming hosts heartbeating now with at least one channel.
	HostsOnAir int `json:"hosts_on_air"`
}

func (h *Handler) remoteInfo(r *http.Request) RemoteInfo {
	a := RemoteAvailability{Reason: "remote viewing is not available on this server", MaxStreams: 2, TokenTTLMinutes: 60}
	if h.Remote != nil {
		a = h.Remote(r.Context())
	}
	out := RemoteInfo{Available: a.Available, ViaRelay: viaRelay(r, a.Detection),
		MaxStreams: a.MaxStreams, TokenTTLMinutes: a.TokenTTLMinutes}
	if !a.Available && a.Reason != "" {
		reason := a.Reason
		out.Reason = &reason
	}
	return out
}

// viaRelay reports whether the request reached the API from away from home.
func viaRelay(r *http.Request, detection string) bool {
	if r.Header.Get(ViaHeader) == "relay" {
		return true
	}
	if detection != DetectRelayOrPublicIP {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && PublicIP(ip)
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// PublicIP reports whether ip is a public address: not RFC 1918, not ULA,
// not loopback, not link-local, not CGNAT 100.64/10, not unspecified.
func PublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil && cgnat.Contains(v4) {
		return false
	}
	return true
}

// hostsOnAir counts the streaming hosts heartbeating now with a channel on
// air (0 on any error: the field is informational).
func (h *Handler) hostsOnAir(ctx context.Context) int {
	if h.DB == nil {
		return 0
	}
	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT active_channels, heartbeat_at FROM streaming_hosts`)
	if err != nil {
		return 0
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var active int
		var beat dbTime
		if rows.Scan(&active, &beat) != nil {
			return n
		}
		if active > 0 && beat.ok && now.Sub(beat.t) <= RuntimeStaleAfter {
			n++
		}
	}
	return n
}

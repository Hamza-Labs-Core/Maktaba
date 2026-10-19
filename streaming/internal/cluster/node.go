package cluster

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
)

// HealthPath is a node's signed health route.
const HealthPath = "/internal/tv/health"

// Health is what /internal/tv/health answers.
type Health struct {
	HostID   string         `json:"host_id"`
	Version  string         `json:"version"`
	Protocol int            `json:"protocol"`
	Active   []string       `json:"active"`
	Load     map[string]any `json:"load"`
}

// MountNode wires a node's streaming port (AC3 of 32.1): only the channel routes
// behind the front-door signature (the placement generation the request carries
// goes into its context for the node's fence) and the signed health route. No
// HDHomeRun, XMLTV, M3U or video-session route exists here.
func MountNode(r chi.Router, k *Keyring, ch *channel.HTTPHandler, health func() Health) {
	r.Group(func(g chi.Router) {
		g.Use(VerifyMiddleware(k, nil))
		g.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if gen := PlacementGen(r); gen > 0 {
					r = r.WithContext(channel.WithPlacementGen(r.Context(), gen))
				}
				next.ServeHTTP(w, r)
			})
		})
		ch.Mount(g)
		g.Get(HealthPath, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(health())
		})
	})
}

// LocalAddrFor returns this machine's address on the route to target (a URL or
// host:port): the stream base URL a node reports (EC1 of 32.1). "" when unknown.
func LocalAddrFor(target string) string {
	host := target
	if u, err := url.Parse(target); err == nil && u.Host != "" {
		host = u.Host
	}
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	c, err := net.Dial("udp", host) // no packet is sent
	if err != nil {
		return ""
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok && a.IP != nil && !a.IP.IsLoopback() {
		return a.IP.String()
	}
	return ""
}

// StreamBaseURL builds http://<ip>:<port> from a listen address like ":8081".
func StreamBaseURL(ip, listen string) string {
	if ip == "" {
		return ""
	}
	port := "8081"
	if i := strings.LastIndexByte(listen, ':'); i >= 0 && i+1 < len(listen) {
		if _, err := strconv.Atoi(listen[i+1:]); err == nil {
			port = listen[i+1:]
		}
	}
	return "http://" + net.JoinHostPort(ip, port)
}

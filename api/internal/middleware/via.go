package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// ViaHeader marks a request the Epic 25 relay agent forwarded from the
// internet to the loopback API (Epic 32 story 32.9): /api/tv/info then
// reports remote.via_relay and the web player plays from Cloudflare.
const ViaHeader = "X-Maktaba-Via"

// StripVia drops X-Maktaba-Via from every request that does not come from
// loopback, so only the relay agent on this machine can set it (story 32.9
// AC1). It must run before RealIP: RealIP rewrites RemoteAddr from
// forwarding headers a client controls.
func StripVia(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(ViaHeader) != "" && !FromLoopback(r) {
			r.Header.Del(ViaHeader)
		}
		next.ServeHTTP(w, r)
	})
}

// relayRefusedPrefixes are the routes for this home's machines and its own
// processes alone: the cluster's join, channel, link, delegation and
// handover routes and the release mirror. Machines reach them on the
// network, never through the relay (Epic 32: "never through the relay"),
// so RefuseRelayToMachines answers them 404 there. The people's routes
// (the Machines page an admin uses) are not in the list.
var relayRefusedPrefixes = []string{
	"/api/cluster/join", "/api/cluster/agent", "/api/cluster/link",
	"/api/cluster/delegate/", "/api/cluster/moved/", "/api/cluster/kept-aside/",
	"/api/cluster/term-claim", "/api/cluster/updates/artifacts/",
	"/api/cluster/updates/wheelhouse/", "/api/cluster/updates/tuf/", "/internal/",
}

// RefuseRelayToMachines answers 404 to a request the relay forwarded
// (X-Maktaba-Via, which StripVia keeps only from loopback) for a
// machine-only route. It must run after StripVia.
func RefuseRelayToMachines(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(ViaHeader) != "" && relayRefused(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func relayRefused(path string) bool {
	for _, p := range relayRefusedPrefixes {
		if path == p || strings.HasPrefix(path, p) && (strings.HasSuffix(p, "/") || path[len(p)] == '/') {
			return true
		}
	}
	return false
}

// localCallerKey marks a request this machine itself sent (MarkLocalCaller).
type localCallerKey struct{}

// MarkLocalCaller records whether a request comes from a program on this machine: a
// loopback peer, every address a proxy forwarded loopback too (the web port's proxy adds
// its client's to X-Forwarded-For), and not the relay agent's (X-Maktaba-Via: the internet,
// through loopback). LocalCaller reads it. It judges the real peer address, so it must run
// before RealIP, which rewrites that from headers a client controls.
func MarkLocalCaller(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if localCaller(r) {
			r = r.WithContext(context.WithValue(r.Context(), localCallerKey{}, true))
		}
		next.ServeHTTP(w, r)
	})
}

// LocalCaller reports whether MarkLocalCaller found the request a program's on this
// machine (false where it did not run).
func LocalCaller(r *http.Request) bool {
	on, _ := r.Context().Value(localCallerKey{}).(bool)
	return on
}

func localCaller(r *http.Request) bool {
	if !FromLoopback(r) || r.Header.Get(ViaHeader) != "" {
		return false
	}
	for _, h := range []string{"X-Forwarded-For", "X-Real-Ip", "True-Client-Ip", "Forwarded"} {
		for _, v := range r.Header.Values(h) {
			for _, a := range strings.Split(v, ",") {
				// a Forwarded header's for=… parses as no address: not taken as local
				if ip := net.ParseIP(strings.TrimSpace(a)); ip == nil || !ip.IsLoopback() {
					return false
				}
			}
		}
	}
	return true
}

// FromLoopback reports whether the request's peer address is loopback.
func FromLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

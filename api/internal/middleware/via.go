package middleware

import (
	"net"
	"net/http"
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

// FromLoopback reports whether the request's peer address is loopback.
func FromLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

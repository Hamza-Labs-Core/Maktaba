package middleware

import (
	"net/http"
	"strings"
)

// PackagedTVOrigin reports whether origin is a packaged TV app's (Epic 58 story 58.09): the
// Samsung (Tizen) and LG (webOS) launchers are pages opened from the package, which the TVs'
// runtimes report as `file://` (Tizen) or `null` (webOS and Chromium's file:// pages), and a
// few runtimes as `app://<id>`. The home answers the same set (api/internal/httpsec/cors.go).
func PackagedTVOrigin(origin string) bool {
	return origin == "null" || origin == "file://" || strings.HasPrefix(origin, "app://")
}

// tvHeaders are the request headers the launcher's cloud calls carry.
const tvHeaders = "Content-Type, X-Maktaba-Client"

// TVCORS answers a packaged TV app's origin on paths (the TV's own public routes, story
// 58.03's POST /v1/tv/codes and /v1/tv/codes/poll), and nothing else:
//
//   - the origin is echoed (with Vary: Origin), never `*` and never
//     Access-Control-Allow-Credentials: those routes take no cookie and no token (the poll
//     secret is in the body), so no credential rides cross-site;
//   - only POST, with Content-Type and X-Maktaba-Client;
//   - a preflight is answered here, before the rate limits (it creates nothing);
//   - any other origin, or any other path, passes through untouched to CORS (the web
//     allow-list), which gives a TV origin nothing.
//
// A web page can claim `null` too (a sandboxed frame): it reads only what any client can
// read from these routes by itself, a fresh code and its own poll, with no one's credential.
func TVCORS(paths []string) func(http.Handler) http.Handler {
	open := make(map[string]bool, len(paths))
	for _, p := range paths {
		open[p] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if !open[r.URL.Path] || !PackagedTVOrigin(origin) {
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
				h.Set("Access-Control-Allow-Headers", tvHeaders)
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

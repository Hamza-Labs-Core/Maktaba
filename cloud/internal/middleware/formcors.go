package middleware

import (
	"context"
	"net/http"
)

// formHeaders are the request headers the public forms' posts carry.
const formHeaders = "Content-Type"

// FormCORS answers the public forms' own origins on paths (Epic 50 story 50.24: the copyright
// notice and the viewer report, which the watch pages on watch.maktaba.tv post to the API), and
// nothing else:
//
//   - the origin is echoed (with Vary: Origin), never `*` and never
//     Access-Control-Allow-Credentials: the forms take no cookie and no token;
//   - only POST, with Content-Type;
//   - a preflight is answered here, before the rate limits (it creates nothing);
//   - any other origin, or any other path, passes through untouched to CORS (the web
//     allow-list).
//
// The handler still refuses any Origin outside its own list (moderation.formGuard), so CORS is
// never the only check.
func FormCORS(paths, origins []string) func(http.Handler) http.Handler {
	open := make(map[string]bool, len(paths))
	for _, p := range paths {
		open[p] = true
	}
	allow := make(map[string]bool, len(origins))
	for _, o := range origins {
		allow[o] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if !open[r.URL.Path] || !allow[origin] {
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
				h.Set("Access-Control-Allow-Headers", formHeaders)
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			// the web allow-list behind leaves this answer alone, so it never adds credentials
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), formCORSKey{}, true)))
		})
	}
}

// formCORSKey marks a request FormCORS has answered: CORS (the web allow-list) skips it.
type formCORSKey struct{}

// answeredByForm reports whether FormCORS has answered r's origin.
func answeredByForm(r *http.Request) bool {
	v, _ := r.Context().Value(formCORSKey{}).(bool)
	return v
}

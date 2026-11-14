package httpsec

import (
	"net/http"
	"strings"
)

// CORSConfig holds the policy applied by `CORS`. Story 10.15 AC-4: an
// allow-list of origins is the only CORS path we expose; wildcard
// `*` is intentionally not supported because every API surface in
// Maktaba serves authenticated content.
type CORSConfig struct {
	// AllowedOrigins is the exact-match list. Origins not on this list
	// receive no `Access-Control-Allow-*` headers; the request fails
	// browser-side.
	AllowedOrigins []string

	// AllowedMethods is the list of HTTP methods returned in
	// `Access-Control-Allow-Methods` for preflight.
	AllowedMethods []string

	// AllowedHeaders is the list returned in
	// `Access-Control-Allow-Headers` for preflight.
	AllowedHeaders []string

	// AllowCredentials sets `Access-Control-Allow-Credentials: true`
	// on responses so cookie-based auth works cross-origin (the SPA
	// being on a different host than the API).
	AllowCredentials bool

	// MaxAge is the preflight cache duration, in seconds.
	MaxAge int

	// PublicPaths are routes whose answer is public (Epic 58 story 58.09: GET
	// /api/discovery/home): any origin may read them, `Access-Control-Allow-Origin: *`,
	// never with credentials. The Samsung and LG launcher reads the home's identity and
	// level there from its packaged file:// page before anything else.
	PublicPaths []string

	// PackagedApps answers a packaged TV app's origin (story 58.09, the --bundle-ui
	// build): `null` and `file://` (Tizen's and webOS's packaged pages) and `app://…`.
	// The answer echoes the origin and NEVER allows credentials, so the browser sends no
	// cookie and shows no answer to a request that carried one: the app reaches the API
	// only with a bearer token it holds (Authorization), like a native app, and every
	// route keeps its own sign-in and role checks. A web page that claims such an origin
	// (a sandboxed frame says `null`) gains nothing it could not ask without CORS.
	PackagedApps bool
}

// packagedOrigin reports whether origin is a packaged TV app's (see PackagedApps).
func packagedOrigin(origin string) bool {
	return origin == "null" || origin == "file://" || strings.HasPrefix(origin, "app://")
}

// packagedHeaders are the request headers a packaged app's calls carry.
const packagedHeaders = "Authorization, Content-Type, X-Maktaba-Client, X-Request-Id"

// exposedHeaders are the answer headers a cross-origin app reads.
const exposedHeaders = "Retry-After, X-Request-Id, X-Maktaba-Home"

// DefaultCORS returns the policy used in the default deployment:
// nothing allowed until the operator names origins. AllowCredentials
// is on because the web SPA uses cookies (Story 10.2). The home's
// identity is public, and a packaged TV app's origin gets the
// credential-less answer (Epic 58 story 58.09).
func DefaultCORS() CORSConfig {
	return CORSConfig{
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Maktaba-CSRF", "X-Maktaba-Client"},
		AllowCredentials: true,
		MaxAge:           300,
		PublicPaths:      []string{"/api/discovery/home"},
		PackagedApps:     true,
	}
}

// CORS returns a middleware enforcing `cfg`.
//
//   - Preflight (`OPTIONS` with `Access-Control-Request-Method`):
//     responds 204 with the allow-list headers when the origin is on
//     the list; 204 with no CORS headers otherwise.
//   - Other methods: when origin is allowed, the standard
//     `Access-Control-Allow-Origin` and friends are added; when not,
//     no headers are set and the inner handler runs normally.
func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		allowed[o] = struct{}{}
	}
	methods := strings.Join(cfg.AllowedMethods, ", ")
	headers := strings.Join(cfg.AllowedHeaders, ", ")
	public := make(map[string]struct{}, len(cfg.PublicPaths))
	for _, p := range cfg.PublicPaths {
		public[p] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			isPreflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""

			_, isPublic := public[r.URL.Path]
			_, listed := allowed[origin]
			switch {
			case origin != "" && isPublic && !listed:
				// a public answer: any origin, no credentials
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", "*")
				if isPreflight {
					h.Set("Access-Control-Allow-Methods", "GET, OPTIONS")
					h.Set("Access-Control-Allow-Headers", "Content-Type, X-Maktaba-Client, X-Request-Id")
					if cfg.MaxAge > 0 {
						h.Set("Access-Control-Max-Age", itoa(cfg.MaxAge))
					}
				}
			case origin != "" && cfg.PackagedApps && packagedOrigin(origin) && !listed:
				// a packaged TV app: its origin, never credentials (bearer only)
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Add("Vary", "Origin")
				h.Set("Access-Control-Expose-Headers", exposedHeaders)
				if isPreflight {
					if methods != "" {
						h.Set("Access-Control-Allow-Methods", methods)
					}
					h.Set("Access-Control-Allow-Headers", packagedHeaders)
					if cfg.MaxAge > 0 {
						h.Set("Access-Control-Max-Age", itoa(cfg.MaxAge))
					}
				}
			}
			if origin != "" {
				if listed {
					h := w.Header()
					h.Set("Access-Control-Allow-Origin", origin)
					h.Add("Vary", "Origin")
					if cfg.AllowCredentials {
						h.Set("Access-Control-Allow-Credentials", "true")
					}
					if isPreflight {
						if methods != "" {
							h.Set("Access-Control-Allow-Methods", methods)
						}
						if headers != "" {
							h.Set("Access-Control-Allow-Headers", headers)
						}
						if cfg.MaxAge > 0 {
							h.Set("Access-Control-Max-Age", itoa(cfg.MaxAge))
						}
					}
				}
			}

			if isPreflight {
				// Preflight always terminates here regardless of the
				// origin decision: an unknown origin gets 204 with no
				// CORS headers, which is the intended deny path
				// (browser-side rejection per AC-4).
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// itoa is a stripped-down int→string used to avoid a strconv import in
// the cors.go header, where every byte of allocation matters under
// load. Equivalent to strconv.Itoa for non-negative ints.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

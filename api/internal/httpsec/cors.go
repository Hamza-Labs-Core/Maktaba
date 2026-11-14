package httpsec

import (
	"bufio"
	"context"
	"errors"
	"net"
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

	// BearerOrigins are origins answered for bearer requests only (Epic 58 story
	// 58.07: Maktaba's Cast receiver, which holds a cast grant and no cookie): their
	// responses carry `Access-Control-Allow-Origin` but never
	// `Access-Control-Allow-Credentials`, so a browser sends them no cookie and reads no
	// answer to one. An origin also in AllowedOrigins is answered as one of those.
	BearerOrigins []string

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
	// route keeps its own sign-in and role checks. Only a call that carries a bearer token
	// (or a preflight asking to send `Authorization`) is answered: a web page that claims
	// such an origin (a sandboxed frame says `null`) reads nothing it could not read with a
	// token of its own, never an answer that needs no sign-in.
	PackagedApps bool
}

// bearerHeaders are the request headers a bearer origin may send.
const bearerHeaders = "Authorization, Content-Type, X-Maktaba-Client"

// packagedOrigin reports whether origin is a packaged TV app's (see PackagedApps).
func packagedOrigin(origin string) bool {
	return origin == "null" || origin == "file://" || strings.HasPrefix(origin, "app://")
}

// packagedOpenPaths are answered for a packaged origin before it holds a token: whether it
// is signed in (a 401 at most), renewing a token it holds, and redeeming a device code it
// was given. Starting a code (POST /api/pairing/device) is not among them: a web page that
// claims `null` could otherwise read a fresh home code and phish its approval.
var packagedOpenPaths = map[string]bool{
	"/api/auth/me":              true,
	"/api/auth/refresh":         true,
	"/api/pairing/device/token": true,
}

// asksBearer reports whether a preflight asks to send Authorization: only a bearer call's
// preflight is answered for a packaged origin (a preflight carries no credential; the call
// itself is answered only once its token is verified, see packagedGate).
func asksBearer(r *http.Request) bool {
	for _, h := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
		if strings.EqualFold(strings.TrimSpace(h), "authorization") {
			return true
		}
	}
	return false
}

// packagedGate holds a packaged origin's answer until the response is written: it is given
// only when the API verified the caller's token (MarkVerified), or the answer is a 401 (a
// TV must read it to renew its token, and it tells nothing). The header alone proves
// nothing: an invalid bearer is treated as no sign-in, and a page claiming `null` could
// send `Bearer garbage` to a route that needs no sign-in.
type packagedGate struct{ verified bool }

type gateKey struct{}

// MarkVerified opens the packaged-origin gate when verified says the request's credential
// was checked (placed after the API's credential middlewares). Outside a gated request it
// does nothing.
func MarkVerified(verified func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if g, ok := r.Context().Value(gateKey{}).(*packagedGate); ok && verified(r) {
				g.verified = true
			}
			next.ServeHTTP(w, r)
		})
	}
}

// gatedWriter sets the packaged origin's CORS headers at WriteHeader when the gate is open.
type gatedWriter struct {
	http.ResponseWriter
	gate    *packagedGate
	origin  string
	written bool
}

func (g *gatedWriter) WriteHeader(code int) {
	if !g.written {
		g.written = true
		if g.gate.verified || code == http.StatusUnauthorized {
			h := g.Header()
			h.Set("Access-Control-Allow-Origin", g.origin)
			h.Set("Access-Control-Expose-Headers", exposedHeaders)
		}
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gatedWriter) Write(b []byte) (int, error) {
	if !g.written {
		g.WriteHeader(http.StatusOK)
	}
	return g.ResponseWriter.Write(b)
}

// Flush passes a streaming answer's flush through.
func (g *gatedWriter) Flush() {
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		if !g.written {
			g.WriteHeader(http.StatusOK)
		}
		f.Flush()
	}
}

// Hijack passes a WebSocket upgrade through (a socket answers no CORS headers).
func (g *gatedWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := g.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, errors.New("httpsec: the response writer cannot be hijacked")
}

// Unwrap lets http.ResponseController reach the writer underneath.
func (g *gatedWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

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
	bearer := make(map[string]struct{}, len(cfg.BearerOrigins))
	for _, o := range cfg.BearerOrigins {
		bearer[o] = struct{}{}
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
				// an allow-listed origin gets its own answer here: no shared cache serves it "*"
				h.Add("Vary", "Origin")
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
				h.Add("Vary", "Origin")
				switch {
				case isPreflight:
					if !packagedOpenPaths[r.URL.Path] && !asksBearer(r) {
						break
					}
					h.Set("Access-Control-Allow-Origin", origin)
					if methods != "" {
						h.Set("Access-Control-Allow-Methods", methods)
					}
					h.Set("Access-Control-Allow-Headers", packagedHeaders)
					if cfg.MaxAge > 0 {
						h.Set("Access-Control-Max-Age", itoa(cfg.MaxAge))
					}
				case packagedOpenPaths[r.URL.Path]:
					h.Set("Access-Control-Allow-Origin", origin)
					h.Set("Access-Control-Expose-Headers", exposedHeaders)
				default:
					// answered once the token is verified (see packagedGate)
					gate := &packagedGate{}
					r = r.WithContext(context.WithValue(r.Context(), gateKey{}, gate))
					w = &gatedWriter{ResponseWriter: w, gate: gate, origin: origin}
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
				} else if _, ok := bearer[origin]; ok {
					h := w.Header()
					h.Set("Access-Control-Allow-Origin", origin)
					h.Add("Vary", "Origin")
					h.Set("Access-Control-Expose-Headers", "Retry-After")
					if isPreflight {
						if methods != "" {
							h.Set("Access-Control-Allow-Methods", methods)
						}
						h.Set("Access-Control-Allow-Headers", bearerHeaders)
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

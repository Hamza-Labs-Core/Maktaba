package router

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
)

// The home's own callers (the pipeline minting and ending plugin tokens, the launcher's
// version and health checks) carry the supervisor's per-boot local token and draw on
// their own budget; clients arriving over loopback (the web port's proxy, a trusted
// reverse proxy, the relay agent) keep the client limits. The update rehearsal's 4 GB
// leg (20,000 videos) saw the plugin token mints refused with 429: every local caller
// shared one anon-ip:127.0.0.1 bucket of the per-user limit (600 a minute).

const localToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func withLocalToken(t *testing.T) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "local.token")
	if err := os.WriteFile(f, []byte(localToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_SUPERVISOR_TOKEN_FILE", f)
}

// the production caps (api/main.go)
func limitedRouter(t *testing.T) http.Handler {
	t.Helper()
	r := New(Deps{IdempotencyStore: idempotency.NewMemoryStore(), IPRatePerMin: 6000, UserRatePerMin: 600})
	r.Post("/internal/plugins/invocations", func(w http.ResponseWriter, req *http.Request) {
		// the limiter gives no one a principal: the route's own check decides who it is
		if principal.FromContext(req.Context()) != nil {
			t.Error("a principal appeared")
		}
		w.WriteHeader(http.StatusOK)
	})
	r.Get("/api/client", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	return r
}

// fire sends n requests shaped by set and counts the 429s.
func fire(h http.Handler, n int, method, path string, set func(*http.Request)) (limited int) {
	for i := 0; i < n; i++ {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "127.0.0.1:41000"
		set(req)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			limited++
		}
	}
	return limited
}

// A busy home's plugin work: 2,000 token mints in a burst (four times the client budget)
// from the pipeline on loopback, with the local token, are never refused; nor are the
// launcher's checks through the web port (X-Forwarded-For: 127.0.0.1) meanwhile.
func TestInternalCallersNotThrottledByClientLimits(t *testing.T) {
	withLocalToken(t)
	h := limitedRouter(t)
	if n := fire(h, 2000, http.MethodPost, "/internal/plugins/invocations", func(r *http.Request) {
		r.Header.Set(mw.LocalTokenHeader, localToken)
	}); n != 0 {
		t.Fatalf("%d of the home's own plugin calls refused with 429", n)
	}
	if n := fire(h, 50, http.MethodGet, "/api/client", func(r *http.Request) {
		r.Header.Set(mw.LocalTokenHeader, localToken)
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
	}); n != 0 {
		t.Fatalf("%d of the launcher's checks refused", n)
	}
}

// Clients keep their limits, whatever they send.
func TestClientsOverLoopbackStillLimited(t *testing.T) {
	withLocalToken(t)
	cases := []struct {
		name string
		set  func(*http.Request)
	}{
		{"a client through a reverse proxy on loopback", func(r *http.Request) {
			r.Header.Set("X-Forwarded-For", "203.0.113.9")
		}},
		{"a client through the relay agent", func(r *http.Request) {
			r.Header.Set("X-Forwarded-For", "198.51.100.7")
			r.Header.Set(mw.ViaHeader, "relay")
		}},
		{"a forged local token through a proxy", func(r *http.Request) {
			r.Header.Set("X-Forwarded-For", "203.0.113.10")
			r.Header.Set(mw.LocalTokenHeader, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdee")
		}},
		{"an empty local token", func(r *http.Request) {
			r.Header.Set(mw.LocalTokenHeader, "")
		}},
		{"the token's prefix", func(r *http.Request) {
			r.Header.Set(mw.LocalTokenHeader, localToken[:32])
		}},
		{"a program on this machine without the token", func(*http.Request) {}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := limitedRouter(t)
			// 700 against a budget of 600: about 100 refused (a few tokens refill while it runs)
			if n := fire(h, 700, http.MethodGet, "/api/client", c.set); n < 50 {
				t.Fatalf("only %d of 700 refused: the client limit did not hold", n)
			}
		})
	}
}

// With no token file configured (a bare API), a header claims nothing.
func TestLocalTokenWithoutFileClaimsNothing(t *testing.T) {
	t.Setenv("MAKTABA_SUPERVISOR_TOKEN_FILE", "")
	h := limitedRouter(t)
	if n := fire(h, 700, http.MethodGet, "/api/client", func(r *http.Request) {
		r.Header.Set(mw.LocalTokenHeader, localToken)
	}); n < 50 {
		t.Fatalf("only %d of 700 refused", n)
	}
}

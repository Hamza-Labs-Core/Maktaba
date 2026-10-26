package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/client"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/usage"
)

// Client reads X-Maktaba-Client into the request's context (Epic 44 story
// 44.2): client.From names the app, its version, platform and device. A label
// only, never a right.
func Client(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := client.Parse(r.Header.Get(client.Header))
		next.ServeHTTP(w, r.WithContext(client.With(r.Context(), info)))
	})
}

// Usage counts every request for the API's use per app (Epic 44 story 44.5):
// its app and platform, the person signed in, its status and time. It runs
// after Client. A nil counter counts nothing.
//
// The security chain sits outside the router, so the principal is on the
// context already; a request it refused (401) never reaches here. An event
// stream's time is not counted (it is open for as long as a page is).
func Usage(c *usage.Counter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if c == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sc := &statusCapture{ResponseWriter: w}
			t0 := time.Now()
			next.ServeHTTP(sc, r)
			took := time.Since(t0)
			status := sc.status
			if status == 0 {
				status = http.StatusOK
			}
			if status == http.StatusSwitchingProtocols ||
				strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
				took = 0
			}
			userID := ""
			if p := principal.FromContext(r.Context()); p != nil {
				userID = p.UserID
			}
			c.Record(client.From(r.Context()), userID, status, took)
		})
	}
}

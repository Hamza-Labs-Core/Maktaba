package compat

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/client"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Code is the problem code of a sign-in refused below the client floor.
const Code = "client_too_old"

// TypeClientTooOld is its problem type.
var TypeClientTooOld = httperror.TypeForCode(Code)

// SignInRoutes are the routes that hand out credentials: a build below the floor is refused
// there, so it never gets a session to use the routes it would get wrong.
var SignInRoutes = map[string]bool{
	"POST /api/auth/login":           true,
	"POST /api/auth/refresh":         true,
	"POST /api/pairing/device/token": true,
	"POST /api/pairing/exchange":     true,
}

// Gate refuses a sign-in from an app build below floor (nil: ReleaseFloor) with 426
// client_too_old, naming the floor. It runs after middleware.Client. The header stays a
// label, never a right: a request without it (the web) passes, and so does a version the
// floor cannot compare. Each refused app/platform is logged once a day, so an admin sees in
// Logs which TVs need an update.
func Gate(floor Floor) func(http.Handler) http.Handler {
	if floor == nil {
		floor = ReleaseFloor
	}
	log := &refusalLog{every: 24 * time.Hour, now: time.Now, last: map[string]time.Time{}}
	return gate(floor, log)
}

func gate(floor Floor, log *refusalLog) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if len(floor) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !SignInRoutes[r.Method+" "+r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}
			info := client.From(r.Context())
			min, below := floor.Below(info.App, info.Platform, info.Version)
			if !below {
				next.ServeHTTP(w, r)
				return
			}
			log.refused(r.Context(), info, min)
			e := &httperror.Error{
				Type:   TypeClientTooOld,
				Title:  "This app is too old for this home",
				Status: http.StatusUpgradeRequired,
				Detail: "update the app: this home serves " + Key(info.App, info.Platform) + " " + min + " or newer",
			}
			httperror.Write(w, r, e.With("code", Code).With("floor", min).With("client", Key(info.App, info.Platform)))
		})
	}
}

// refusalLog logs each refused app/platform at most once per every.
type refusalLog struct {
	mu    sync.Mutex
	every time.Duration
	now   func() time.Time
	last  map[string]time.Time
	// logger, when set, takes the lines (tests); else slog's default
	logger *slog.Logger
}

func (l *refusalLog) refused(ctx context.Context, info client.Info, floor string) {
	key := Key(info.App, info.Platform)
	l.mu.Lock()
	now := l.now()
	if t, ok := l.last[key]; ok && now.Sub(t) < l.every {
		l.mu.Unlock()
		return
	}
	l.last[key] = now
	l.mu.Unlock()
	lg := l.logger
	if lg == nil {
		lg = slog.Default()
	}
	lg.WarnContext(ctx, "compat: refused an app build below the client floor; it needs an update",
		"component", "compat", "app", info.App, "platform", info.Platform,
		"version", info.Version, "floor", floor, "device", info.Device)
}

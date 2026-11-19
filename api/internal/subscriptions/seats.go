package subscriptions

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Story 16.4, the owner's decision of 2026-11-19: when a home has more users than its
// license's seats, admins are exempt and only the most recently created accounts beyond
// the seat count go read-only. A read-only account can still watch and save its own
// progress and history, and keep its own playlists and favourites (the owner's decision
// of 2026-11-19: they are personal, like progress), but cannot upload, edit metadata or
// manage the home. Removing
// users or adding seats restores them at once: nothing is cached, each write asks.

// TypeSeatReadOnly is the problem type of a write a read-only account makes.
var TypeSeatReadOnly = httperror.Type("seat-read-only")

// SeatReadOnlyText is what a read-only account reads when a change is refused.
const SeatReadOnlyText = "This home has more users than its plan allows, so this account can watch " +
	"and keep its progress, playlists and favourites but not change anything else. Ask the home's admin to add seats or remove a user."

// disabledHash is the pw_hash of the sentinel rows that are no seat (users.CountUsers).
const disabledHash = "<unsalted-disabled>"

// SeatUser is an account that is read-only for want of seats.
type SeatUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// SeatGuard decides which accounts are read-only: the newest accounts beyond the seat
// count that are not admins.
type SeatGuard struct {
	DB    *sql.DB
	Store *Store
	Log   *slog.Logger
}

// ReadOnlyUsers are the accounts read-only now, newest first (none when the plan has
// the seats).
func (g *SeatGuard) ReadOnlyUsers(ctx context.Context) ([]SeatUser, error) {
	if g == nil || g.DB == nil {
		return nil, nil
	}
	limit := g.Store.SeatLimit()
	if limit == SeatsUnlimited {
		return nil, nil
	}
	var n int
	if err := g.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE pw_hash <> $1`, disabledHash).Scan(&n); err != nil {
		return nil, err
	}
	over := n - limit
	if over <= 0 {
		return nil, nil
	}
	rows, err := g.DB.QueryContext(ctx, `SELECT id, username FROM users
		WHERE pw_hash <> $1 AND NOT is_admin ORDER BY created_at DESC, id DESC LIMIT $2`, disabledHash, over)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SeatUser
	for rows.Next() {
		var u SeatUser
		if err := rows.Scan(&u.ID, &u.Username); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ReadOnly reports whether userID is read-only now.
func (g *SeatGuard) ReadOnly(ctx context.Context, userID string) (bool, error) {
	if userID == "" {
		return false, nil
	}
	us, err := g.ReadOnlyUsers(ctx)
	if err != nil {
		return false, err
	}
	for _, u := range us {
		if u.ID == userID {
			return true, nil
		}
	}
	return false, nil
}

// AllowedWhileSeatReadOnly lists the writes a read-only account may still make:
// watching (playback sessions, the watch log, live TV, casting, a play queue, a TV
// remote's session), its own progress and history, its own playlists and favourites,
// signing in and out, its own password and sessions, searching, its notifications and
// the parental requests a child makes to keep watching. Every read is allowed (it is not
// a write).
func AllowedWhileSeatReadOnly(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	p := r.URL.Path
	switch {
	case !strings.HasPrefix(p, "/api/"):
		return true
	case strings.HasPrefix(p, "/api/auth/"),
		strings.HasPrefix(p, "/api/system/"),
		strings.HasPrefix(p, "/api/.well-known/"),
		strings.HasPrefix(p, "/api/stream/sessions"),
		strings.HasPrefix(p, "/api/watch/"),
		strings.HasPrefix(p, "/api/me/history/"),
		strings.HasPrefix(p, "/api/me/play-queues"),
		strings.HasPrefix(p, "/api/me/playlists"),
		strings.HasPrefix(p, "/api/me/favorites"),
		strings.HasPrefix(p, "/api/me/sessions/"),
		strings.HasPrefix(p, "/api/me/parental/"),
		strings.HasPrefix(p, "/api/notifications/"),
		strings.HasPrefix(p, "/api/tv/remote/sessions/"),
		strings.HasPrefix(p, "/api/cast/grants"):
		return true
	case p == "/api/me/watched", p == "/api/me/change-password", p == "/api/me/age-signal",
		p == "/api/search", p == "/api/direct/check", p == "/api/telemetry/app":
		return true
	case strings.HasPrefix(p, "/api/channels/") && strings.HasSuffix(p, "/play") && r.Method == http.MethodPost:
		return true
	case strings.HasPrefix(p, "/api/videos/") && strings.HasSuffix(p, "/downloaded"):
		return true // a download kept for watching offline
	}
	return false
}

// Gate refuses a read-only account's writes beyond AllowedWhileSeatReadOnly with a 403
// seat-read-only. Admins are exempt (the list never names one); a request with no
// account (the admin token, a public route) passes. A failure to read the users lets
// the request through, logged: the seats never stop a home because its database
// hiccupped.
func (g *SeatGuard) Gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g == nil || AllowedWhileSeatReadOnly(r) {
			next.ServeHTTP(w, r)
			return
		}
		p := principal.FromContext(r.Context())
		if p == nil || p.UserID == "" || p.Source == principal.SourceAdminToken {
			next.ServeHTTP(w, r)
			return
		}
		ro, err := g.ReadOnly(r.Context(), p.UserID)
		if err != nil {
			if g.Log != nil {
				g.Log.Warn("seats: could not read the users; the request goes through", "err", err,
					"event", "seat_check_failed")
			}
			next.ServeHTTP(w, r)
			return
		}
		if ro {
			httperror.Write(w, r, httperror.Forbidden(TypeSeatReadOnly, SeatReadOnlyText))
			return
		}
		next.ServeHTTP(w, r)
	})
}

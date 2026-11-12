package parental

// Live TV under parental controls (Epic 57, story 57.10): the guard in front of
// every route that names a channel, and the routes a player and the streaming
// service ask before and while a channel plays.
//
//	GET  /api/channels/{id}/access      signed in   may this account watch the channel now
//	POST /api/channels/{id}/play        signed in   the channel's signed stream URL
//	GET  /api/tv/parental/verdict       this machine's streaming service only (local token)
//
// The checks themselves are internal/parental's (livetv.go): ChannelPermitted and
// ProgrammePermitted over the account's effective policy.

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// VerdictPath is the route the streaming service asks (it is on the auth gate's
// public list: the handler takes this machine's local token and nothing else).
const VerdictPath = "/api/tv/parental/verdict"

// channelPath matches every API route under one channel.
var channelPath = regexp.MustCompile(`^/api/channels/([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})(?:/.*)?$`)

// ProgrammeRestricted is the 403 a restricted account gets for a channel it may
// see while the programme on air is above its limit. retry_at is when the next
// permitted programme starts (absent when the schedule read holds none).
func ProgrammeRestricted(until *time.Time) *httperror.Error {
	e := httperror.Forbidden(TypeRestricted, "not available right now").
		With("code", "restricted").With("reason", "programme")
	if until != nil {
		e = e.With("retry_at", until.UTC().Format(time.RFC3339))
	}
	return e
}

// LiveGuard refuses a restricted account every /api/channels/{id}… route of a
// channel it is not permitted, with the 404 an unknown channel gets. It reads the
// path, so it guards whatever is mounted behind it (the channel, its logo and art,
// its schedule and guide). A policy that cannot be read refuses.
func LiveGuard(l *parental.LiveTV) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m := channelPath.FindStringSubmatch(r.URL.Path)
			p := principal.FromContext(r.Context())
			if m == nil || p == nil || l == nil {
				next.ServeHTTP(w, r)
				return
			}
			v, err := l.View(r.Context(), p)
			if err != nil {
				httperror.Write(w, r, httperror.Internal("load parental policy").Wrap(err))
				return
			}
			if v.Restricted() {
				_, ok, err := v.Channel(r.Context(), m[1])
				if err != nil {
					httperror.Write(w, r, httperror.Internal("load parental policy").Wrap(err))
					return
				}
				if !ok {
					httperror.Write(w, r, httperror.NotFound("channel "+m[1]))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ChannelSigner signs a channel's stream URL for a user (signedurl.Minter).
type ChannelSigner interface {
	SignChannelURL(rawURL, channelID, userID string, ttl time.Duration) (string, time.Time, error)
	// SignChannelURLForAdminToken signs for the admin token (no account).
	SignChannelURLForAdminToken(rawURL, channelID string, ttl time.Duration) (string, time.Time, error)
}

// LiveHandler serves the live TV routes of the parental controls.
type LiveHandler struct {
	DB   *sql.DB
	Live *parental.LiveTV
	// Signer signs channel stream URLs; nil: POST …/play answers 503.
	Signer ChannelSigner
}

// Mount registers the routes.
func (h *LiveHandler) Mount(r chi.Router) {
	r.Get("/api/channels/{id}/access", h.Access)
	r.Post("/api/channels/{id}/play", h.Play)
	r.Get(VerdictPath, h.Verdict)
}

// LiveAccess answers GET /api/channels/{id}/access.
type LiveAccess struct {
	ChannelID string `json:"channel_id"`
	// Restricted: the account has a parental policy.
	Restricted bool `json:"restricted"`
	// Allowed: what the channel plays now is within the account's limit.
	Allowed bool `json:"allowed"`
	// Until is when Allowed next changes; null when the schedule read holds no change.
	Until *time.Time `json:"until"`
}

// LivePlay answers POST /api/channels/{id}/play.
type LivePlay struct {
	ChannelID string `json:"channel_id"`
	// URL is the channel's HLS playlist with its signature, relative to the home.
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
	// Until is when a restricted account's stream next stops (the start of a
	// programme above its limit); null when the schedule read holds none.
	Until *time.Time `json:"until"`
}

// channelFor loads the channel the caller asked for and judges it: the library
// rule first, then the parental one. Every refusal for a channel the caller may
// not see is the 404 of an unknown id.
func (h *LiveHandler) channelFor(w http.ResponseWriter, r *http.Request) (*principal.Principal, string, parental.LiveVerdict, bool) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return nil, "", parental.LiveVerdict{}, false
	}
	id := strings.ToLower(chi.URLParam(r, "id"))
	notFound := func() (*principal.Principal, string, parental.LiveVerdict, bool) {
		httperror.Write(w, r, httperror.NotFound("channel "+id))
		return nil, "", parental.LiveVerdict{}, false
	}
	if _, err := uuid.Parse(id); err != nil {
		return notFound()
	}
	var lib sql.NullString
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT library_id::text FROM channels WHERE id = $1::uuid AND lifecycle = 'live'`, id).Scan(&lib)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound()
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load channel").Wrap(err))
		return nil, "", parental.LiveVerdict{}, false
	}
	if !p.IsAdmin && !p.AccessAllLibraries && lib.Valid && !p.HasLibrary(lib.String) {
		return notFound()
	}
	v, err := h.Live.View(r.Context(), p)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load parental policy").Wrap(err))
		return nil, "", parental.LiveVerdict{}, false
	}
	verdict, err := v.Verdict(r.Context(), id)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load parental policy").Wrap(err))
		return nil, "", parental.LiveVerdict{}, false
	}
	if !verdict.Channel {
		return notFound()
	}
	return p, id, verdict, true
}

// Access implements GET /api/channels/{id}/access: whether the caller may watch the
// channel now, and when that next changes (a player shows its slate from it).
func (h *LiveHandler) Access(w http.ResponseWriter, r *http.Request) {
	_, id, v, ok := h.channelFor(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	common.WriteJSON(w, r, http.StatusOK, LiveAccess{ChannelID: id, Restricted: v.Restricted, Allowed: v.Allowed, Until: utc(v.Until)})
}

// Play implements POST /api/channels/{id}/play: the channel's signed stream URL. A
// restricted account whose channel plays a programme above its limit gets 403
// restricted (reason programme, retry_at).
func (h *LiveHandler) Play(w http.ResponseWriter, r *http.Request) {
	p, id, v, ok := h.channelFor(w, r)
	if !ok {
		return
	}
	if !v.Allowed {
		httperror.Write(w, r, ProgrammeRestricted(v.Until))
		return
	}
	if h.Signer == nil {
		httperror.Write(w, r, httperror.Unavailable(5))
		return
	}
	var (
		signed string
		exp    time.Time
		err    error
	)
	const path = "/stream/channel/"
	switch {
	case p.UserID != "":
		signed, exp, err = h.Signer.SignChannelURL(path+id+"/index.m3u8", id, p.UserID, 0)
	case p.IsAdmin:
		// the admin token: no account, so no policy to ask about; the URL says so
		// in a signed claim the streaming service checks
		signed, exp, err = h.Signer.SignChannelURLForAdminToken(path+id+"/index.m3u8", id, 0)
	default:
		// no account and not an admin: no one to sign for
		httperror.Write(w, r, httperror.Forbidden("no_account", "this sign-in has no account to play a channel for"))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Unavailable(5).Wrap(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	common.WriteJSON(w, r, http.StatusOK, LivePlay{ChannelID: id, URL: signed, ExpiresAt: exp, Until: utc(v.Until)})
}

// LiveVerdictBody answers GET /api/tv/parental/verdict.
type LiveVerdictBody struct {
	Restricted     bool       `json:"restricted"`
	ChannelAllowed bool       `json:"channel_allowed"`
	Allowed        bool       `json:"allowed"`
	Until          *time.Time `json:"until"`
}

// Verdict implements GET /api/tv/parental/verdict?channel=&user=: the streaming
// service's question at a request and at a programme boundary. Only this machine's
// own services may ask (the supervisor's local token).
func (h *LiveHandler) Verdict(w http.ResponseWriter, r *http.Request) {
	if !mw.FromSupervisor(r) {
		httperror.Write(w, r, httperror.Forbidden("", "this machine's services only"))
		return
	}
	channel, user := r.URL.Query().Get("channel"), r.URL.Query().Get("user")
	if _, err := uuid.Parse(channel); err != nil {
		httperror.Write(w, r, httperror.BadRequest("channel must be a channel id"))
		return
	}
	if _, err := uuid.Parse(user); err != nil {
		httperror.Write(w, r, httperror.BadRequest("user must be a user id"))
		return
	}
	view, err := h.Live.ViewOf(r.Context(), user)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load parental policy").Wrap(err))
		return
	}
	v, err := view.Verdict(r.Context(), channel)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load parental policy").Wrap(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	common.WriteJSON(w, r, http.StatusOK, LiveVerdictBody{
		Restricted: v.Restricted, ChannelAllowed: v.Channel, Allowed: v.Allowed, Until: utc(v.Until),
	})
}

// RemoteGate is the check the remote viewing routes make (POST /api/tv/remote/tune
// with a channel id; POST /api/tv/remote/sessions/{id}/heartbeat with the caller's
// session id): a channel the account may not see is the 404 of an unknown one, a
// programme above its limit is ProgrammeRestricted. An account without a policy is
// never asked anything of the database.
func RemoteGate(db *sql.DB, l *parental.LiveTV) func(r *http.Request, p *principal.Principal, channelID, sessionID string) *httperror.Error {
	return func(r *http.Request, p *principal.Principal, channelID, sessionID string) *httperror.Error {
		if p == nil {
			return nil // the route's own sign-in check answers
		}
		view, err := l.View(r.Context(), p)
		if err != nil {
			return httperror.Internal("load parental policy").Wrap(err)
		}
		if !view.Restricted() {
			return nil
		}
		if channelID == "" {
			if _, err := uuid.Parse(sessionID); err != nil {
				return nil // the route answers its own 404
			}
			err := db.QueryRowContext(r.Context(),
				`SELECT channel_id::text FROM tv_remote_sessions WHERE id = $1::uuid AND user_id = $2`,
				sessionID, p.UserID).Scan(&channelID)
			if errors.Is(err, sql.ErrNoRows) {
				return nil // not this account's session: the route answers its own 404
			}
			if err != nil {
				return httperror.Internal("load parental policy").Wrap(err)
			}
		}
		if _, err := uuid.Parse(channelID); err != nil {
			return httperror.NotFound("channel " + channelID)
		}
		v, err := view.Verdict(r.Context(), channelID)
		if err != nil {
			return httperror.Internal("load parental policy").Wrap(err)
		}
		if !v.Channel {
			return httperror.NotFound("channel " + strings.ToLower(channelID))
		}
		if !v.Allowed {
			return ProgrammeRestricted(v.Until)
		}
		return nil
	}
}

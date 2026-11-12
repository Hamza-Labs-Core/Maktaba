package parental

// Live TV under parental controls (Epic 57, story 57.10): the guard in front of
// every route that names a channel, and the routes a player and the streaming
// service ask before and while a channel plays.
//
//	GET  /api/channels/{id}/access      signed in   may this account watch the channel now
//	POST /api/channels/{id}/play        signed in   the channel's signed stream URL
//	GET  /api/tv/parental/verdict       this machine's streaming service only (local token)
//
// Live TV counts toward the time rules like any playback (story 57.14): access and
// play refuse an account whose time is over with 403 parental-time, and the verdict
// counts what the streaming service says it served and carries the time's answer.
//
// The checks themselves are internal/parental's (livetv.go): ChannelPermitted and
// ProgrammePermitted over the account's effective policy.

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strconv"
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

// TimeOver is the 403 a restricted account gets for a channel while its watching
// time is over (story 57.14): the same parental-time problem a video's stream
// answers, with the reason and, when the rules allow playback again, when. It is
// not ProgrammeRestricted: the player shows its time's-up screen, not the slate.
func TimeOver(st parental.TimeState) *httperror.Error {
	e := httperror.Forbidden(TypeParentalTime, "this account's watching time is over for now").
		With("code", "parental_time").With("reason", st.Reason)
	if st.NextAllowedAt != nil {
		e = e.With("next_allowed_at", st.NextAllowedAt.UTC().Format(time.RFC3339))
	}
	return e
}

// TimeUnknown is the 503 when an account's time could not be read or counted:
// never free time on an error.
func TimeUnknown(err error) *httperror.Error {
	return httperror.Unavailable(5).With("code", "parental_time_unknown").Wrap(err)
}

// LiveTime is the time keeper as live TV asks it (parental.TimeKeeper).
type LiveTime interface {
	// Time is the request's account's time now in a library.
	Time(ctx context.Context, p *principal.Principal, libraryID string) (parental.TimeState, error)
	// LiveServed counts live TV the streaming service served the account, and
	// answers its time after that.
	LiveServed(ctx context.Context, userID, libraryID string, served time.Duration) (parental.TimeState, error)
	// RemoteBeat counts one call for a remote viewing session, and answers the
	// account's time after that.
	RemoteBeat(ctx context.Context, userID, libraryID, sessionID string, lastBeat time.Time) (parental.TimeState, error)
}

// ChannelSigner signs a channel's stream URL for a user (signedurl.Minter).
type ChannelSigner interface {
	// SignChannelURLForSignIn signs for an account and the sign-in the request
	// came through (its family and its device's age limit).
	SignChannelURLForSignIn(rawURL, channelID, userID, family string, ceiling *int, ttl time.Duration) (string, time.Time, error)
	// SignChannelURLForAdminToken signs for the admin token (no account).
	SignChannelURLForAdminToken(rawURL, channelID string, ttl time.Duration) (string, time.Time, error)
}

// LiveHandler serves the live TV routes of the parental controls.
type LiveHandler struct {
	DB   *sql.DB
	Live *parental.LiveTV
	// Signer signs channel stream URLs; nil: POST …/play answers 503.
	Signer ChannelSigner
	// Time is the time keeper (story 57.14): live TV counts like any playback and
	// is refused by the same rules. Nil (a test, or a build with no time keeper):
	// no time rule is asked.
	Time LiveTime
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
	// Now is the home's clock for this answer: a client waits Until minus Now on
	// its own monotonic clock, so a device clock that is wrong changes nothing.
	Now time.Time `json:"now"`
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
	// Now is the home's clock for this answer (see LiveAccess.Now).
	Now time.Time `json:"now"`
}

// withNow adds the home's clock to a refusal that carries a time to come back at
// (retry_at, next_allowed_at): the client waits that time minus now.
func withNow(e *httperror.Error, now time.Time) *httperror.Error {
	return e.With("now", homeNow(now).Format(time.RFC3339))
}

// homeNow is the clock an answer carries: UTC, in whole seconds like the times it is
// measured against. Cutting the fraction only ever makes a client's wait longer,
// never shorter than the home's own.
func homeNow(now time.Time) time.Time { return now.UTC().Truncate(time.Second) }

// channelFor loads the channel the caller asked for and judges it: the library
// rule first, then the parental one. Every refusal for a channel the caller may
// not see is the 404 of an unknown id.
//
// The time it returns is the home's clock the verdict was judged at: the answer's
// `now`.
func (h *LiveHandler) channelFor(w http.ResponseWriter, r *http.Request) (*principal.Principal, string, parental.LiveVerdict, time.Time, bool) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return nil, "", parental.LiveVerdict{}, time.Time{}, false
	}
	id := strings.ToLower(chi.URLParam(r, "id"))
	notFound := func() (*principal.Principal, string, parental.LiveVerdict, time.Time, bool) {
		httperror.Write(w, r, httperror.NotFound("channel "+id))
		return nil, "", parental.LiveVerdict{}, time.Time{}, false
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
		return nil, "", parental.LiveVerdict{}, time.Time{}, false
	}
	if !p.IsAdmin && !p.AccessAllLibraries && lib.Valid && !p.HasLibrary(lib.String) {
		return notFound()
	}
	v, err := h.Live.View(r.Context(), p)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load parental policy").Wrap(err))
		return nil, "", parental.LiveVerdict{}, time.Time{}, false
	}
	verdict, err := v.Verdict(r.Context(), id)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load parental policy").Wrap(err))
		return nil, "", parental.LiveVerdict{}, time.Time{}, false
	}
	if !verdict.Channel {
		return notFound()
	}
	// Story 57.14: live TV is refused by the time rules like any playback. After
	// the 404s (a channel the account may not see says nothing), before the
	// programme's own answer: an account out of time is told so.
	if h.Time != nil {
		st, err := h.Time.Time(r.Context(), p, lib.String)
		if err != nil {
			w.Header().Set("Retry-After", "5")
			httperror.Write(w, r, TimeUnknown(err))
			return nil, "", parental.LiveVerdict{}, time.Time{}, false
		}
		if !st.Allowed {
			httperror.Write(w, r, withNow(TimeOver(st), v.Now))
			return nil, "", parental.LiveVerdict{}, time.Time{}, false
		}
	}
	return p, id, verdict, v.Now, true
}

// Access implements GET /api/channels/{id}/access: whether the caller may watch the
// channel now, and when that next changes (a player shows its slate from it). A
// restricted account whose watching time is over gets 403 parental-time. Every
// answer with a time to wait for (until, retry_at, next_allowed_at) carries `now`,
// the home's clock: the wait is that time minus now, whatever the device's clock.
func (h *LiveHandler) Access(w http.ResponseWriter, r *http.Request) {
	_, id, v, now, ok := h.channelFor(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	common.WriteJSON(w, r, http.StatusOK, LiveAccess{ChannelID: id, Restricted: v.Restricted, Allowed: v.Allowed, Until: utc(v.Until), Now: homeNow(now)})
}

// Play implements POST /api/channels/{id}/play: the channel's signed stream URL. A
// restricted account whose channel plays a programme above its limit gets 403
// restricted (reason programme, retry_at); one whose watching time is over gets 403
// parental-time (reason, next_allowed_at). The URL carries the sign-in's family and
// its device's age limit, so the stream is held to them for as long as it lives.
func (h *LiveHandler) Play(w http.ResponseWriter, r *http.Request) {
	p, id, v, now, ok := h.channelFor(w, r)
	if !ok {
		return
	}
	if !v.Allowed {
		httperror.Write(w, r, withNow(ProgrammeRestricted(v.Until), now))
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
		signed, exp, err = h.Signer.SignChannelURLForSignIn(path+id+"/index.m3u8", id, p.UserID, p.RefreshFamily, p.AgeCeiling, 0)
	case p.IsAdmin:
		// the admin token: no account, so no policy to ask about; the URL says so
		// in a signed claim the streaming service checks
		signed, exp, err = h.Signer.SignChannelURLForAdminToken(path+id+"/index.m3u8", id, 0)
	default:
		// no account and not an admin: no one to sign for
		httperror.Write(w, r, httperror.Forbidden(httperror.Type("no-account"), "this sign-in has no account to play a channel for").With("code", "no_account"))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Unavailable(5).Wrap(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	common.WriteJSON(w, r, http.StatusOK, LivePlay{ChannelID: id, URL: signed, ExpiresAt: exp, Until: utc(v.Until), Now: homeNow(now)})
}

// LiveVerdictBody answers GET /api/tv/parental/verdict.
type LiveVerdictBody struct {
	Restricted     bool       `json:"restricted"`
	ChannelAllowed bool       `json:"channel_allowed"`
	Allowed        bool       `json:"allowed"`
	Until          *time.Time `json:"until"`
	// TimeAllowed: the viewer's time rules allow playback now (story 57.14). When
	// false the stream is refused with the parental-time problem, whatever the
	// programme: TimeReason and TimeNextAllowedAt are that problem's fields.
	TimeAllowed       bool       `json:"time_allowed"`
	TimeReason        string     `json:"time_reason,omitempty"`
	TimeNextAllowedAt *time.Time `json:"time_next_allowed_at,omitempty"`
	// TimeEndsAt is when a stream running now must end by the time rules; absent
	// when nothing ends it.
	TimeEndsAt *time.Time `json:"time_ends_at,omitempty"`
}

// maxServedParam bounds the `served` a verdict question may carry, in seconds.
const maxServedParam = 24 * 3600

// Verdict implements GET /api/tv/parental/verdict?channel=&user=[&family=][&ac=]
// [&served=]: the streaming service's question at a request and at a programme
// boundary. Only this machine's own services may ask (the supervisor's local token).
//
// family and ac are the viewer's sign-in as the stream's credential carries it (a
// signed channel URL's `fam` and `ac`, or what the web port's gate learned of the
// session): the answer is held to that sign-in's device limit as well as to the
// account's policy, the way every request of the sign-in is (Cache.For). With a
// family the home reads the signal it holds for it now, so one tightened or
// cleared since the URL was signed counts as it is; `ac` is believed only for a
// sign-in with no family.
//
// served is the whole seconds of this channel the streaming service served the
// viewer since it last said so (story 57.14): they are counted before the time
// rules are read. Anything that cannot be read is an error, never an allowance.
func (h *LiveHandler) Verdict(w http.ResponseWriter, r *http.Request) {
	if !mw.FromSupervisor(r) {
		httperror.Write(w, r, httperror.Forbidden("", "this machine's services only"))
		return
	}
	q := r.URL.Query()
	channel, user := q.Get("channel"), q.Get("user")
	if _, err := uuid.Parse(channel); err != nil {
		httperror.Write(w, r, httperror.BadRequest("channel must be a channel id"))
		return
	}
	if _, err := uuid.Parse(user); err != nil {
		httperror.Write(w, r, httperror.BadRequest("user must be a user id"))
		return
	}
	who := &principal.Principal{UserID: strings.ToLower(user)}
	if fam := q.Get("family"); fam != "" {
		if _, err := uuid.Parse(fam); err != nil {
			httperror.Write(w, r, httperror.BadRequest("family must be a sign-in's family id"))
			return
		}
		who.RefreshFamily = strings.ToLower(fam)
	}
	if ac := q.Get("ac"); ac != "" {
		n, err := strconv.Atoi(ac)
		if err != nil {
			httperror.Write(w, r, httperror.BadRequest("ac must be an age limit"))
			return
		}
		if who.RefreshFamily == "" {
			who.AgeCeiling = &n
		}
	}
	served := 0
	if sv := q.Get("served"); sv != "" {
		n, err := strconv.Atoi(sv)
		if err != nil || n < 0 || n > maxServedParam {
			httperror.Write(w, r, httperror.BadRequest("served must be whole seconds"))
			return
		}
		served = n
	}
	view, err := h.Live.View(r.Context(), who)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load parental policy").Wrap(err))
		return
	}
	v, err := view.Verdict(r.Context(), channel)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load parental policy").Wrap(err))
		return
	}
	body := LiveVerdictBody{Restricted: v.Restricted, ChannelAllowed: v.Channel, Allowed: v.Allowed, Until: utc(v.Until),
		TimeAllowed: true}
	if h.Time != nil {
		var lib sql.NullString
		err := h.DB.QueryRowContext(r.Context(), `SELECT library_id::text FROM channels WHERE id = $1::uuid`, channel).Scan(&lib)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.Internal("load channel").Wrap(err))
			return
		}
		st, err := h.Time.LiveServed(r.Context(), who.UserID, lib.String, time.Duration(served)*time.Second)
		if err != nil {
			w.Header().Set("Retry-After", "5")
			httperror.Write(w, r, TimeUnknown(err))
			return
		}
		body.TimeAllowed = st.Allowed
		if !st.Allowed {
			body.TimeReason, body.TimeNextAllowedAt = st.Reason, utc(st.NextAllowedAt)
		}
		body.TimeEndsAt = utc(st.EndsAt)
	}
	w.Header().Set("Cache-Control", "no-store")
	common.WriteJSON(w, r, http.StatusOK, body)
}

// RemoteGate is the check the remote viewing routes make (POST /api/tv/remote/tune
// with a channel id; POST /api/tv/remote/sessions/{id}/heartbeat with the caller's
// session id): a channel the account may not see is the 404 of an unknown one, an
// account whose watching time is over gets TimeOver, a programme above its limit is
// ProgrammeRestricted. An account without a policy is never asked anything of the
// database.
//
// With a time keeper (story 57.14) remote viewing counts like any playback. The
// video comes from the home's Cloudflare account, which the home cannot measure,
// so the session's own calls are counted: a heartbeat adds the time since the one
// before, and a tune adds it for the account's sessions already open on that
// channel (a client that tunes again instead of sending heartbeats is counted the
// same). A session ends without heartbeats, so a client cannot keep one and stay
// uncounted.
func RemoteGate(db *sql.DB, l *parental.LiveTV, keeper LiveTime) func(r *http.Request, p *principal.Principal, channelID, sessionID string) *httperror.Error {
	type openSession struct {
		id   string
		beat time.Time
	}
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
		var open []openSession
		if channelID == "" {
			if _, err := uuid.Parse(sessionID); err != nil {
				return nil // the route answers its own 404
			}
			var beat time.Time
			var ended bool
			err := db.QueryRowContext(r.Context(),
				`SELECT channel_id::text, last_beat_at, ended_at IS NOT NULL FROM tv_remote_sessions WHERE id = $1::uuid AND user_id = $2`,
				sessionID, p.UserID).Scan(&channelID, &beat, &ended)
			if errors.Is(err, sql.ErrNoRows) {
				return nil // not this account's session: the route answers its own 404
			}
			if err != nil {
				return httperror.Internal("load parental policy").Wrap(err)
			}
			if !ended {
				open = append(open, openSession{strings.ToLower(sessionID), beat})
			}
		} else if _, err := uuid.Parse(channelID); err == nil && keeper != nil {
			rows, err := db.QueryContext(r.Context(),
				`SELECT id::text, last_beat_at FROM tv_remote_sessions
				  WHERE user_id = $1 AND channel_id = $2::uuid AND ended_at IS NULL`, p.UserID, channelID)
			if err != nil {
				return httperror.Internal("load parental policy").Wrap(err)
			}
			for rows.Next() {
				var s openSession
				if err := rows.Scan(&s.id, &s.beat); err != nil {
					_ = rows.Close()
					return httperror.Internal("load parental policy").Wrap(err)
				}
				open = append(open, s)
			}
			if err := rows.Close(); err != nil {
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
		if keeper != nil {
			var lib sql.NullString
			if err := db.QueryRowContext(r.Context(), `SELECT library_id::text FROM channels WHERE id = $1::uuid`, channelID).Scan(&lib); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return httperror.Internal("load channel").Wrap(err)
			}
			st, err := keeper.Time(r.Context(), p, lib.String)
			for _, s := range open {
				if err != nil {
					break
				}
				st, err = keeper.RemoteBeat(r.Context(), p.UserID, lib.String, s.id, s.beat)
			}
			if err != nil {
				return TimeUnknown(err)
			}
			if !st.Allowed {
				return TimeOver(st)
			}
		}
		if !v.Allowed {
			return ProgrammeRestricted(v.Until)
		}
		return nil
	}
}

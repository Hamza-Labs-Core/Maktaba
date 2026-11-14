package castgrant

// The grant's gate: what a cast grant may reach. A receiver opens and keeps stream sessions
// for its items, reports their progress, reads their titles, posters, chapters and subtitles,
// and walks its play queue; every other route, and any other item, is 403 `cast_scope`.
// Every other principal passes untouched.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// check is how a route's object is held to the grant.
type check int

const (
	// open: the route names no item (the home's identity).
	open check = iota + 1
	// videoParam: the path's {id} is a video.
	videoParam
	// sessionParam: the path's {id} is a stream session; its video is held to the grant.
	sessionParam
	// sessionBody: the body's video_id is the video a session is opened for.
	sessionBody
	// queueParam: the path's {id} is the grant's play queue.
	queueParam
)

// Routes are what a grant may call, as "METHOD pattern", and how each is checked. The
// gate's test holds every entry to a mounted route.
var Routes = map[string]check{
	"GET /api/discovery/home":                 open,
	"POST /api/stream/sessions":               sessionBody,
	"GET /api/stream/sessions/{id}":           sessionParam,
	"DELETE /api/stream/sessions/{id}":        sessionParam,
	"POST /api/stream/sessions/{id}/progress": sessionParam,
	"POST /api/stream/sessions/{id}/errors":   sessionParam,
	"GET /api/videos/{id}":                    videoParam,
	"GET /api/videos/{id}/chapters":           videoParam,
	"GET /api/videos/{id}/subtitles":          videoParam,
	"GET /api/videos/{id}/poster":             videoParam,
	"GET /api/me/play-queues/{id}":            queueParam,
	"PUT /api/me/play-queues/{id}/current":    queueParam,
}

// Gate holds cast grants to Routes. Its database is set once the home's is open (SetDB);
// until then a grant reaches only the open routes.
type Gate struct {
	db atomic.Pointer[sql.DB]
}

// NewGate is a gate with no database yet.
func NewGate() *Gate { return &Gate{} }

// SetDB gives the gate the home's database (the session and queue lookups).
func (g *Gate) SetDB(db *sql.DB) {
	if g != nil && db != nil {
		g.db.Store(db)
	}
}

// ScopeProblem is the answer to a grant asking outside its scope.
func ScopeProblem(detail string) *httperror.Error {
	return httperror.Forbidden("", detail).With("code", "cast_scope")
}

// bodyLimit bounds the body the gate reads to find the video a session is for (the
// handler's own limit).
const bodyLimit = 16 << 10

// Middleware is the gate for the router whose routes are given (the router it is
// installed in).
func (g *Gate) Middleware(routes chi.Routes) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := principal.FromContext(r.Context())
			if p == nil || p.Source != principal.SourceCast {
				next.ServeHTTP(w, r)
				return
			}
			if p.Cast == nil {
				httperror.Write(w, r, ScopeProblem("a cast grant names no item"))
				return
			}
			method := r.Method
			if method == http.MethodHead {
				method = http.MethodGet
			}
			rctx := chi.NewRouteContext()
			pattern := routes.Find(rctx, method, r.URL.Path)
			c, ok := Routes[method+" "+strings.TrimSuffix(pattern, "/")]
			if pattern == "" || !ok {
				httperror.Write(w, r, ScopeProblem("a cast grant reaches only the routes a Cast receiver needs"))
				return
			}
			if c == open {
				next.ServeHTTP(w, r)
				return
			}
			db := g.db.Load()
			if db == nil && c != queueParam {
				httperror.Write(w, r, ScopeProblem("the home cannot check this cast grant yet"))
				return
			}
			allowed, pass, err := g.allows(r, db, p, c, rctx.URLParam("id"))
			if err != nil {
				mlog.From(r.Context()).Error("cast grant not checked", "path", pattern, "err", err.Error(),
					"event", "cast_grant_check_failed")
				httperror.Write(w, r, httperror.Unavailable(5))
				return
			}
			if pass || allowed {
				next.ServeHTTP(w, r)
				return
			}
			httperror.Write(w, r, ScopeProblem("this cast grant does not name that item"))
		})
	}
}

// allows reports whether the grant may act on the route's object. pass is set when the
// gate cannot name the object (a malformed or unknown id, a body without a video): the
// handler answers that as it would for anyone (400 or 404), and acts on nothing.
func (g *Gate) allows(r *http.Request, db *sql.DB, p *principal.Principal, c check, id string) (allowed, pass bool, err error) {
	ctx := r.Context()
	switch c {
	case queueParam:
		return p.Cast.QueueID != "" && strings.EqualFold(id, p.Cast.QueueID), false, nil
	case videoParam:
		ok, err := MayPlay(ctx, db, p, id)
		return ok, false, err
	case sessionParam:
		if _, err := uuid.Parse(id); err != nil {
			return false, true, nil // the handler's 400
		}
		var video string
		err := db.QueryRowContext(ctx, `SELECT CAST(video_id AS TEXT) FROM streaming_sessions WHERE id = $1 AND user_id = $2`,
			strings.ToLower(id), p.UserID).Scan(&video)
		if errors.Is(err, sql.ErrNoRows) {
			return false, true, nil // the handler's 404 (or 400 for a malformed id)
		}
		if err != nil {
			return false, false, err
		}
		ok, err := MayPlay(ctx, db, p, video)
		return ok, false, err
	case sessionBody:
		raw, err := io.ReadAll(io.LimitReader(r.Body, bodyLimit+1))
		if err != nil {
			return false, false, err
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var body struct {
			VideoID string `json:"video_id"`
		}
		// fail closed: a body the gate cannot read names no video of the grant, so it is
		// refused here rather than passed on; the gate never relies on the handler's decoder
		// refusing the same bodies json.Unmarshal does.
		if json.Unmarshal(raw, &body) != nil || body.VideoID == "" {
			return false, false, nil
		}
		ok, err := MayPlay(ctx, db, p, body.VideoID)
		return ok, false, err
	}
	return false, false, nil
}

// MayPlay reports whether a grant names video: its own video, or an item of its play queue
// (the queue its user owns).
func MayPlay(ctx context.Context, db *sql.DB, p *principal.Principal, video string) (bool, error) {
	if p == nil || p.Cast == nil || video == "" {
		return false, nil
	}
	if p.Cast.VideoID != "" {
		return strings.EqualFold(video, p.Cast.VideoID), nil
	}
	if _, err := uuid.Parse(video); err != nil {
		return false, nil
	}
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM play_queue_items i JOIN play_queues q ON q.id = i.queue_id
		WHERE q.id = $1 AND q.user_id = $2 AND i.video_id = $3)`,
		strings.ToLower(p.Cast.QueueID), p.UserID, strings.ToLower(video)).Scan(&ok)
	return ok, err
}

// Package vodfallback serves video on demand on a full node (Epic 35 story 35.8 AC9,
// story 35.10 AC4a, design §11.5). Media is read where it is needed: a node's VOD
// session is opened on the node's own streaming role, which reads the file from the NAS
// through this machine's mounts (the canonical share-path resolver, story 31.3); no
// media byte goes through the home.
//
// Fallback only: when this machine cannot read the video's file (its share is not
// mounted or not readable here), the session is opened on the home instead and the
// player's URLs are rewritten under /stream/via-home/, which the node's web port relays
// from the home's streaming port with the session's signed URL (the legacy path of the
// ticketed VOD stream, story 35.16). The state is reported to the home — the Machines
// page shows "VOD served by <home>: share not readable here" — and clears by itself
// with the next video this machine can read.
package vodfallback

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/nodemode"
)

// ViaHome is the web-port prefix of a session served by the home.
const ViaHome = "/stream/via-home/"

// State is what the node reports (cluster_nodes.vod).
type State struct {
	Fallback  bool       `json:"fallback"`
	ServedBy  string     `json:"served_by,omitempty"`
	Since     *time.Time `json:"since,omitempty"`
	Detail    string     `json:"detail,omitempty"`
	CheckedAt time.Time  `json:"checked_at"`
}

// Fallback wraps the node's /api/stream/sessions routes.
type Fallback struct {
	DB *sql.DB
	// ToLocal maps a stored path to this machine's path (sharepath + the stream path map).
	ToLocal func(string) string
	// Stat checks a local path (nil: os.Stat).
	Stat func(string) error
	// Home serves a request on the home (the node's home proxy).
	Home http.Handler
	// HomeName is shown as served_by.
	HomeName func() string
	// HomeDown reports that the home does not answer (nil: never): a video this machine
	// cannot read is then not available at all (WriteUnavailableOffline).
	HomeDown func() bool
	// Catalog stands in for the home's database while it does not answer (nil: none):
	// a video's path from this machine's catalog.
	Catalog Catalog
	// OnChange reports a new state (to the home).
	OnChange func(State)
	Logger   *slog.Logger

	mu     sync.Mutex
	remote map[string]time.Time
	state  State
}

// Current is the last state.
func (f *Fallback) Current() State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *Fallback) stat(p string) error {
	if f.Stat != nil {
		return f.Stat(p)
	}
	_, err := os.Stat(p)
	return err
}

func (f *Fallback) set(fallback bool, detail string) {
	f.mu.Lock()
	now := time.Now().UTC()
	prev := f.state
	f.state.CheckedAt = now
	f.state.Detail = detail
	if fallback {
		if !prev.Fallback {
			f.state.Since = &now
		}
		f.state.Fallback = true
		f.state.ServedBy = "home"
		if f.HomeName != nil {
			if n := f.HomeName(); n != "" {
				f.state.ServedBy = n
			}
		}
	} else {
		f.state.Fallback, f.state.ServedBy, f.state.Since = false, "", nil
	}
	st, changed := f.state, prev.Fallback != f.state.Fallback || prev.Detail != f.state.Detail
	f.mu.Unlock()
	if changed {
		if f.Logger != nil {
			if fallback {
				f.Logger.Warn("node: this machine cannot read the share; VOD is served by the home",
					"detail", detail, "event", "node_vod_fallback")
			} else {
				f.Logger.Info("node: the share is readable again; VOD is served here", "event", "node_vod_local")
			}
		}
		if f.OnChange != nil {
			f.OnChange(st)
		}
	}
}

// Catalog is a full node's own record of the videos (cluster/catalog).
type Catalog interface {
	DBDown() bool
	DBFailed(err error)
	Path(videoID string) (string, bool)
}

// catalogDBTimeout bounds the path lookup when a catalog can answer instead.
const catalogDBTimeout = 3 * time.Second

// Readable reports whether this machine can read the video's file, and why not.
func (f *Fallback) Readable(ctx context.Context, videoID string) (bool, string) {
	var p string
	err := sql.ErrConnDone // not asked: the catalog answers
	if f.Catalog == nil || !f.Catalog.DBDown() {
		qctx, cancel := ctx, context.CancelFunc(func() {})
		if f.Catalog != nil {
			qctx, cancel = context.WithTimeout(ctx, catalogDBTimeout)
		}
		err = f.DB.QueryRowContext(qctx, `SELECT path FROM videos WHERE id = $1`, videoID).Scan(&p)
		cancel()
		switch {
		case err == nil || errors.Is(err, sql.ErrNoRows) || f.Catalog == nil:
		case ctx.Err() != nil:
			// the request went away: that says nothing about the database
			return true, ""
		case nodemode.Unreachable(err) || f.Catalog.DBDown():
			f.Catalog.DBFailed(err)
		default:
			// an error the database answered with: not an outage
			return true, ""
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) && f.Catalog != nil {
		// the home's database does not answer: the path this machine last read
		if cp, ok := f.Catalog.Path(videoID); ok {
			p, err = cp, nil
		}
	}
	if err != nil {
		// unknown video: the local handler answers 404 as usual (or, with the
		// home's database down, that it is not available meanwhile)
		return true, ""
	}
	local := p
	if f.ToLocal != nil {
		local = f.ToLocal(p)
	}
	if err := f.stat(local); err != nil {
		return false, "cannot open " + filepath.Dir(local)
	}
	return true, ""
}

func (f *Fallback) remember(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.remote == nil {
		f.remote = map[string]time.Time{}
	}
	now := time.Now()
	for k, t := range f.remote {
		if now.Sub(t) > 24*time.Hour {
			delete(f.remote, k)
		}
	}
	f.remote[id] = now
}

func (f *Fallback) isRemote(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.remote[id]
	return ok
}

// Rewrite moves a session's stream URLs under /stream/via-home/.
func Rewrite(u string) string {
	if strings.HasPrefix(u, "/stream/") && !strings.HasPrefix(u, ViaHome) {
		return ViaHome + strings.TrimPrefix(u, "/stream/")
	}
	return u
}

// rewriteBody rewrites manifest_url / direct_url of a session answer.
func rewriteBody(b []byte) []byte {
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return b
	}
	for _, k := range []string{"manifest_url", "direct_url"} {
		if s, ok := m[k].(string); ok && s != "" {
			m[k] = Rewrite(s)
		}
	}
	m["served_by"] = "home"
	out, err := json.Marshal(m)
	if err != nil {
		return b
	}
	return out
}

// serveHome runs the request on the home and rewrites the answer's URLs.
func (f *Fallback) serveHome(w http.ResponseWriter, r *http.Request, body []byte, rewrite bool) {
	rec := httptest.NewRecorder()
	r2 := r.Clone(r.Context())
	r2.Body = io.NopCloser(bytes.NewReader(body))
	r2.ContentLength = int64(len(body))
	f.Home.ServeHTTP(rec, r2)
	res := rec.Result()
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	if rewrite && homeDidNotAnswer(res.StatusCode, out) {
		WriteUnavailableOffline(w)
		return
	}
	if rewrite && res.StatusCode < 300 {
		out = rewriteBody(out)
		var m map[string]any
		if json.Unmarshal(out, &m) == nil {
			if id, ok := m["session_id"].(string); ok && id != "" {
				f.remember(id)
			}
		}
	}
	keys := make([]string, 0, len(res.Header))
	for k := range res.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range res.Header[k] {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("X-Maktaba-VOD", "home")
	w.WriteHeader(res.StatusCode)
	_, _ = w.Write(out)
}

// Wrap guards the local stream session routes.
func (f *Fallback) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == http.MethodPost && path == "/api/stream/sessions":
			body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
			if err != nil {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			var req struct {
				VideoID string `json:"video_id"`
			}
			_ = json.Unmarshal(body, &req)
			if req.VideoID != "" && f.DB != nil && f.Home != nil {
				ok, why := f.Readable(r.Context(), req.VideoID)
				if !ok && f.HomeDown != nil && f.HomeDown() {
					WriteUnavailableOffline(w)
					return
				}
				if !ok {
					f.set(true, why)
					f.serveHome(w, r, body, true)
					return
				}
				f.set(false, "")
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
			w.Header().Set("X-Maktaba-VOD", "local")
			next.ServeHTTP(w, r)
		case strings.HasPrefix(path, "/api/stream/sessions/") && f.Home != nil:
			id, _, _ := strings.Cut(strings.TrimPrefix(path, "/api/stream/sessions/"), "/")
			if f.isRemote(id) {
				body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
				f.serveHome(w, r, body, r.Method == http.MethodGet)
				return
			}
			next.ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// ProblemUnavailableOffline is the problem type of a video this machine cannot read
// while the home, which could serve it, does not answer.
const ProblemUnavailableOffline = "https://maktaba.dev/problems/video-unavailable-offline"

// WriteUnavailableOffline answers 503 {code: video_unavailable_offline}: this machine
// cannot open the video's file, and the home does not answer to serve it instead.
func WriteUnavailableOffline(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Retry-After", "30")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": ProblemUnavailableOffline,
		"title": "this video isn't available while the home is unreachable", "status": http.StatusServiceUnavailable,
		"code":   "video_unavailable_offline",
		"detail": "this machine cannot open the video's file, and the home that could serve it does not answer"})
}

// homeDidNotAnswer reports whether the home proxy's answer says the home did not
// answer: 503 home_unreachable (refused, or past the grace), or 504 home_timeout (a
// home switched off leaves what is sent to it hanging).
func homeDidNotAnswer(status int, body []byte) bool {
	var p struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(body, &p) != nil {
		return false
	}
	return (status == http.StatusServiceUnavailable && p.Code == "home_unreachable") ||
		(status == http.StatusGatewayTimeout && p.Code == "home_timeout")
}

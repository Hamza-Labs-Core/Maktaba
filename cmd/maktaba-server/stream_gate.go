package main

// The web port's /stream/ gate. The browser plays live TV and videos
// through the web port (one origin), so /stream/ there is reachable by
// anyone who can reach the web port. The gate:
//
//   - serves only the stream paths the web app loads (allowedStreamPath):
//     a channel's HLS (/stream/channel/{uuid}/index|master|manifest.m3u8,
//     seg-N.ts, s/{slot}/seg-N.ts, live.ts) and a video session's HLS/DASH, subtitles and
//     chapters (/stream/{session uuid}/...) and direct play
//     (/stream/direct/{video uuid}); everything else is a 404;
//   - requires a signed-in session: the request's Cookie / Authorization
//     are checked by the API (GET /api/auth/me must answer 200); a good
//     answer is cached for streamAuthTTL so segment fetches do not each
//     cost an API round trip;
//   - strips Cookie and Authorization before proxying, so the session
//     never reaches the streaming service.
//
// /xmltv.xml and /lineup.m3u stay public (players read them); external
// players tune channels on the streaming port itself.
//
// The staging preview (Epic 33 story 33.6), /stream/staging/channel/{uuid}/
// index.m3u8 and its segments, is for admins only: the session must be an
// admin's (is_admin from /api/auth/me), and the front door also wants the
// change's preview token (?pt=). A signed-in non-admin gets 403.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"time"
)

// streamAuthTTL is how long a session the API accepted stays accepted.
const streamAuthTTL = 30 * time.Second

// streamAuthCacheMax bounds the accepted-session cache.
const streamAuthCacheMax = 1024

const uuidRe = `[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`

var streamPathRes = []*regexp.Regexp{
	// a channel's live HLS (streaming/internal/channel/handler.go)
	regexp.MustCompile(`^/stream/channel/` + uuidRe + `/(index\.m3u8|master\.m3u8|manifest\.m3u8|live\.ts|seg-[0-9]{1,12}\.ts)$`),
	// with TV slots (Epic 33 story 33.1) the front door's stitched playlist
	// lists each segment under the slot it came from: s/{slot}/seg-N.ts
	regexp.MustCompile(`^/stream/channel/` + uuidRe + `/s/(blue|green|amber)/seg-[0-9]{1,12}\.ts$`),
	// a video session (streaming/internal/server/server.go): master
	// playlist / DASH manifest, rendition playlists and segments,
	// subtitles and chapters
	regexp.MustCompile(`^/stream/` + uuidRe + `/(manifest\.m3u8|manifest\.mpd|chapters\.json|subs/auto\.vtt|subs/sidecar/[A-Za-z0-9_-]{1,32}\.vtt)$`),
	regexp.MustCompile(`^/stream/` + uuidRe + `/[A-Za-z0-9_-]{1,32}/(index\.m3u8|[A-Za-z0-9_-]{1,64}\.(ts|m4s|mp4|aac|vtt))$`),
	// direct play of a video
	regexp.MustCompile(`^/stream/direct/` + uuidRe + `$`),
}

// stagingPathRe is the staging preview of a channel (story 33.6): admins only.
var stagingPathRe = regexp.MustCompile(`^/stream/staging/channel/` + uuidRe +
	`/(index\.m3u8|s/(blue|green|amber)/seg-[0-9]{1,12}\.ts)$`)

// allowedStreamPath reports whether path is one the web app loads.
func allowedStreamPath(path string) bool {
	for _, re := range streamPathRes {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

// streamGate guards the web port's /stream/ (see the file comment).
type streamGate struct {
	next    http.Handler // the streaming proxy
	authURL string       // the API's GET /api/auth/me
	client  *http.Client
	now     func() time.Time

	mu    sync.Mutex
	ok    map[string]time.Time // credential hash → accepted until
	admin map[string]bool      // credential hash → an admin's session (while in ok)
}

func newStreamGate(next http.Handler, apiOrigin *url.URL) *streamGate {
	return &streamGate{
		next:    next,
		authURL: apiOrigin.String() + "/api/auth/me",
		client:  &http.Client{Timeout: 5 * time.Second},
		now:     time.Now,
		ok:      map[string]time.Time{},
		admin:   map[string]bool{},
	}
}

func (g *streamGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	staging := stagingPathRe.MatchString(r.URL.Path)
	if !staging && !allowedStreamPath(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	cookie, authz := r.Header.Get("Cookie"), r.Header.Get("Authorization")
	if cookie == "" && authz == "" {
		http.Error(w, "sign in to watch", http.StatusUnauthorized)
		return
	}
	ok, admin := g.session(r, cookie, authz)
	if !ok {
		http.Error(w, "sign in to watch", http.StatusUnauthorized)
		return
	}
	if staging && !admin {
		http.Error(w, "the staging preview is for admins", http.StatusForbidden)
		return
	}
	out := r.Clone(r.Context())
	out.Header.Del("Cookie")
	out.Header.Del("Authorization")
	g.next.ServeHTTP(w, out)
}

// session reports whether the credentials are a signed-in session and whether
// it is an admin's (GET /api/auth/me: 200, is_admin), with a short cache of the
// accepted ones.
func (g *streamGate) session(r *http.Request, cookie, authz string) (bool, bool) {
	sum := sha256.Sum256([]byte(cookie + "\x00" + authz))
	key := hex.EncodeToString(sum[:])
	now := g.now()
	g.mu.Lock()
	until, hit := g.ok[key]
	adm := g.admin[key]
	g.mu.Unlock()
	if hit && now.Before(until) {
		return true, adm
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, g.authURL, nil)
	if err != nil {
		return false, false
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		return false, false
	}
	var me struct {
		IsAdmin bool `json:"is_admin"`
	}
	if resp.StatusCode == http.StatusOK {
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&me)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		g.mu.Lock()
		delete(g.ok, key)
		delete(g.admin, key)
		g.mu.Unlock()
		return false, false
	}
	g.mu.Lock()
	if len(g.ok) >= streamAuthCacheMax {
		for k, t := range g.ok {
			if !now.Before(t) {
				delete(g.ok, k)
				delete(g.admin, k)
			}
		}
		if len(g.ok) >= streamAuthCacheMax {
			g.ok = map[string]time.Time{}
			g.admin = map[string]bool{}
		}
	}
	g.ok[key] = now.Add(streamAuthTTL)
	g.admin[key] = me.IsAdmin
	g.mu.Unlock()
	return true, me.IsAdmin
}

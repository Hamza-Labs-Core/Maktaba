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
//   - a channel's stream (/stream/channel/{id}/…, also behind a node's
//     /stream/via-home/) follows library access, as the guide does (Epic 48
//     story 48.3, gap G1): unless the session is an admin's, the gate asks
//     GET /api/channels/{id} with the caller's credentials; 200 lets it
//     through, 404 / 403 (a channel of a library the viewer may not read,
//     or no such channel) is a 403. The verdict is cached per credential
//     and channel for streamAuthTTL, so segment fetches cost no API call;
//   - a video session's path (/stream/{session}/…) and direct play
//     (/stream/direct/{video}), also behind a node's /stream/via-home/, pass
//     without a cookie or bearer token when they carry the signed URL's ?sig=
//     (Epic 48 story 48.12, gap G2): an AirPlay or Cast receiver fetches the
//     URL itself and carries neither. The gate reads the token's claims (it
//     holds no key): the audience must be the path family's (streaming /
//     streaming-direct) and the subject the session or video in the path;
//     otherwise 403 (unreadable: 401). The streaming service still verifies it
//     in full (signature, audience, subject, expiry with its configured leeway:
//     an expired token is its 403, LibraryGuard), as for every session and
//     direct path.
//     A channel's path has no ?sig= and still needs a session; so does the
//     staging preview. No viewer is named for a signed request. A signed
//     answer allows any origin to read it (Access-Control-Allow-Origin: *):
//     a Cast receiver reads the HLS with fetch from its own page (story
//     48.33); the signed URL is the credential, never a cookie;
//   - strips Cookie and Authorization before proxying, so the session
//     never reaches the streaming service;
//   - names the signed-in person to the streaming service in
//     X-Maktaba-Viewer (the user_id /api/auth/me answered; one a client sent
//     is dropped), for the record of who watches a channel (Epic 44 story
//     44.4). The front door trusts it only on loopback: from this gate.
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
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/pagetheme"
)

// streamAuthTTL is how long a session the API accepted stays accepted.
const streamAuthTTL = 30 * time.Second

// streamAuthCacheMax bounds the accepted-session cache.
const streamAuthCacheMax = 1024

// viewerHeader names the signed-in person to the streaming service
// (streaming/internal/viewing.ViewerHeader).
const viewerHeader = "X-Maktaba-Viewer"

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

// channelPathRe finds the channel a stream path plays: /stream/channel/{id}/…,
// also /stream/via-home/channel/{id}/… on a node (allowedNodeStreamPath).
var channelPathRe = regexp.MustCompile(`^/stream/(?:via-home/)?channel/(` + uuidRe + `)/`)

// streamChannel is the channel id path plays, or "" for a video session.
func streamChannel(path string) string {
	if m := channelPathRe.FindStringSubmatch(path); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

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
	apiURL  string       // the API's origin (GET /api/channels/{id})
	client  *http.Client
	now     func() time.Time
	// allow is the path filter (allowedStreamPath; a full node adds its
	// /stream/via-home/ fallback, Epic 35)
	allow func(string) bool

	mu    sync.Mutex
	ok    map[string]time.Time // credential hash → accepted until
	admin map[string]bool      // credential hash → an admin's session (while in ok)
	user  map[string]string    // credential hash → the session's user id (while in ok)
	// chans is the channel verdicts: credential hash + channel id → verdict
	chans map[string]channelVerdict
}

// channelVerdict is what GET /api/channels/{id} said of a viewer and a channel.
type channelVerdict struct {
	allowed bool
	until   time.Time
}

// sessionInfo is what /api/auth/me said of a session.
type sessionInfo struct {
	admin bool
	user  string
}

func newStreamGate(next http.Handler, apiOrigin *url.URL) *streamGate {
	return &streamGate{
		next:    next,
		authURL: apiOrigin.String() + "/api/auth/me",
		apiURL:  apiOrigin.String(),
		client:  &http.Client{Timeout: 5 * time.Second},
		now:     time.Now,
		ok:      map[string]time.Time{},
		admin:   map[string]bool{},
		user:    map[string]string{},
		chans:   map[string]channelVerdict{},
		allow:   allowedStreamPath,
	}
}

func (g *streamGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		pagetheme.Error(w, r, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	staging := stagingPathRe.MatchString(r.URL.Path)
	allow := g.allow
	if allow == nil {
		allow = allowedStreamPath
	}
	if !staging && !allow(r.URL.Path) {
		pagetheme.NotFound(w, r)
		return
	}
	cookie, authz := r.Header.Get("Cookie"), r.Header.Get("Authorization")
	if cookie == "" && authz == "" {
		if sig := r.URL.Query().Get("sig"); sig != "" && !staging {
			g.serveSigned(w, r, sig)
			return
		}
		pagetheme.Error(w, r, "sign in to watch", http.StatusUnauthorized)
		return
	}
	key := credentialKey(cookie, authz)
	ok, who, err := g.session(r, key, cookie, authz)
	if err != nil {
		// the API did not answer the check (down, restarting): not the viewer's sign-in,
		// so not a 401 that sends them to sign in again; still nothing is streamed
		stderrProxyError(authCheckErrs, "stream gate: the sign-in check", r, err)
		pagetheme.Error(w, r, "the sign-in check is not answering; try again shortly", http.StatusBadGateway)
		return
	}
	if !ok {
		pagetheme.Error(w, r, "sign in to watch", http.StatusUnauthorized)
		return
	}
	if staging && !who.admin {
		pagetheme.Error(w, r, "the staging preview is for admins", http.StatusForbidden)
		return
	}
	if ch := streamChannel(r.URL.Path); ch != "" && !staging && !who.admin {
		answer, err := g.channel(r, key, ch, cookie, authz)
		switch {
		case err != nil:
			stderrProxyError(authCheckErrs, "stream gate: the channel check", r, err)
			pagetheme.Error(w, r, "the sign-in check is not answering; try again shortly", http.StatusBadGateway)
			return
		case answer == channelSignedOut:
			pagetheme.Error(w, r, "sign in to watch", http.StatusUnauthorized)
			return
		case answer == channelRefused:
			// the same answer for a channel of another library and for no such
			// channel: the gate does not tell which ids exist
			pagetheme.Error(w, r, "this channel is not in your libraries", http.StatusForbidden)
			return
		}
	}
	out := r.Clone(r.Context())
	out.Header.Del("Cookie")
	out.Header.Del("Authorization")
	out.Header.Del(viewerHeader)
	if who.user != "" {
		out.Header.Set(viewerHeader, who.user)
	}
	g.next.ServeHTTP(w, out)
}

// signedTokenMax bounds the ?sig= the gate reads (a minted token is ~700 bytes).
const signedTokenMax = 4096

// signedPathRe is a path a signed URL may open without a session: a video
// session's (sub = the session, aud streaming) or direct play (sub = the video,
// aud streaming-direct); on a node also behind /stream/via-home/. Channel and
// staging paths do not match: they need a session.
var signedPathRe = regexp.MustCompile(`^/stream/(?:via-home/)?(?:(direct)/)?(` + uuidRe + `)(?:/|$)`)

// Audiences of the API's signed-URL minter (api/internal/auth/signedurl).
const (
	audSession = "streaming"
	audDirect  = "streaming-direct"
)

// signedVerdict is the gate's reading of a ?sig= for a path.
type signedVerdict int

const (
	signedOK         signedVerdict = iota
	signedUnreadable               // not a token at all: 401
	signedRefused                  // a token, but not this path's: 403
)

// checkSigned reads the claims of a signed URL's token (no signature check: the
// gate has no key; the streaming service verifies it) and reports whether it is
// this path's: aud the path family's, sub the session or video in the path.
//
// Expiry is left to the streaming service, which checks it on every request
// with its own configurable leeway (MAKTABA_CLOCK_SKEW_LEEWAY_SEC; on a node's
// via-home path, the home's) and answers an expired token 403 (Story 23.2
// AC-3). A second, differently-tuned check here would only refuse tokens the
// streaming service still accepts.
func checkSigned(path, sig string) signedVerdict {
	m := signedPathRe.FindStringSubmatch(path)
	if m == nil {
		return signedRefused
	}
	wantAud, wantSub := audSession, m[2]
	if m[1] == "direct" {
		wantAud = audDirect
	}
	if len(sig) > signedTokenMax {
		return signedUnreadable
	}
	parts := strings.Split(sig, ".")
	if len(parts) != 3 {
		return signedUnreadable
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return signedUnreadable
	}
	var c struct {
		Aud string `json:"aud"`
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return signedUnreadable
	}
	if c.Aud != wantAud || !strings.EqualFold(c.Sub, wantSub) {
		return signedRefused
	}
	return signedOK
}

// serveSigned serves a request that carries a signed URL and no session: only a
// video session's or direct play's path, with a token for that path. The
// streaming service then verifies the token in full.
func (g *streamGate) serveSigned(w http.ResponseWriter, r *http.Request, sig string) {
	if !signedPathRe.MatchString(r.URL.Path) {
		// a channel's path: a signed URL does not open it
		pagetheme.Error(w, r, "sign in to watch", http.StatusUnauthorized)
		return
	}
	switch checkSigned(r.URL.Path, sig) {
	case signedUnreadable:
		pagetheme.Error(w, r, "sign in to watch", http.StatusUnauthorized)
		return
	case signedRefused:
		pagetheme.Error(w, r, "this link is not for this video", http.StatusForbidden)
		return
	}
	// A Cast receiver (Google's Default Media Receiver, story 48.33) is a web
	// page on the TV that reads the playlist and segments with fetch/XHR from
	// its own origin, so it needs CORS to read them. Any origin may: the
	// signed URL is the credential, no cookie is involved (a wildcard never
	// carries credentials), and the streaming service verifies the token.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	out := r.Clone(r.Context())
	out.Header.Del(viewerHeader)
	g.next.ServeHTTP(w, out)
}

// session reports whether the credentials are a signed-in session, and whose
// (GET /api/auth/me: 200, user_id, is_admin), with a short cache of the accepted
// ones. err: the API did not answer at all (no verdict on the session).
func (g *streamGate) session(r *http.Request, key, cookie, authz string) (bool, sessionInfo, error) {
	now := g.now()
	g.mu.Lock()
	until, hit := g.ok[key]
	who := sessionInfo{admin: g.admin[key], user: g.user[key]}
	g.mu.Unlock()
	if hit && now.Before(until) {
		return true, who, nil
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, g.authURL, nil)
	if err != nil {
		return false, sessionInfo{}, err
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
		return false, sessionInfo{}, err
	}
	var me struct {
		UserID  string `json:"user_id"`
		IsAdmin bool   `json:"is_admin"`
	}
	if resp.StatusCode == http.StatusOK {
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&me)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		g.mu.Lock()
		delete(g.ok, key)
		delete(g.admin, key)
		delete(g.user, key)
		g.forgetChannels(key)
		g.mu.Unlock()
		return false, sessionInfo{}, nil
	}
	who = sessionInfo{admin: me.IsAdmin, user: me.UserID}
	g.mu.Lock()
	if len(g.ok) >= streamAuthCacheMax {
		for k, t := range g.ok {
			if !now.Before(t) {
				delete(g.ok, k)
				delete(g.admin, k)
				delete(g.user, k)
			}
		}
		if len(g.ok) >= streamAuthCacheMax {
			g.ok = map[string]time.Time{}
			g.admin = map[string]bool{}
			g.user = map[string]string{}
			g.chans = map[string]channelVerdict{}
		}
	}
	if g.user == nil {
		g.user = map[string]string{}
	}
	g.ok[key] = now.Add(streamAuthTTL)
	g.admin[key] = me.IsAdmin
	g.user[key] = me.UserID
	g.mu.Unlock()
	return true, who, nil
}

// credentialKey is the cache key of a request's credentials (a hash, never
// the credentials themselves).
func credentialKey(cookie, authz string) string {
	sum := sha256.Sum256([]byte(cookie + "\x00" + authz))
	return hex.EncodeToString(sum[:])
}

// channelAnswer is the channel check's verdict.
type channelAnswer int

const (
	channelAllowed   channelAnswer = iota
	channelRefused                 // not the viewer's to watch (or no such channel)
	channelSignedOut               // the API no longer accepts the credentials
)

// channel asks the API whether the credentials may watch channel ch
// (GET /api/channels/{id}: the channels handler's canRead, the guide's rule),
// with the verdict cached per credential and channel for streamAuthTTL.
// err: the API did not answer with a verdict.
func (g *streamGate) channel(r *http.Request, key, ch, cookie, authz string) (channelAnswer, error) {
	ck := key + "\x00" + ch
	now := g.now()
	g.mu.Lock()
	v, hit := g.chans[ck]
	g.mu.Unlock()
	if hit && now.Before(v.until) {
		if v.allowed {
			return channelAllowed, nil
		}
		return channelRefused, nil
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, g.apiURL+"/api/channels/"+ch, nil)
	if err != nil {
		return channelRefused, err
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
		return channelRefused, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	var answer channelAnswer
	switch resp.StatusCode {
	case http.StatusOK:
		answer = channelAllowed
	case http.StatusNotFound, http.StatusForbidden:
		answer = channelRefused
	case http.StatusUnauthorized:
		// signed out since the session check: forget the session too
		g.mu.Lock()
		delete(g.ok, key)
		delete(g.admin, key)
		delete(g.user, key)
		g.forgetChannels(key)
		g.mu.Unlock()
		return channelSignedOut, nil
	default:
		// a 5xx (the API or its database in trouble) is no verdict: never
		// cached, and not a refusal the viewer can do nothing about
		return channelRefused, fmt.Errorf("GET /api/channels/{id}: %s", resp.Status)
	}
	g.mu.Lock()
	if g.chans == nil {
		g.chans = map[string]channelVerdict{}
	}
	if len(g.chans) >= streamAuthCacheMax {
		for k, cv := range g.chans {
			if !now.Before(cv.until) {
				delete(g.chans, k)
			}
		}
		if len(g.chans) >= streamAuthCacheMax {
			g.chans = map[string]channelVerdict{}
		}
	}
	g.chans[ck] = channelVerdict{allowed: answer == channelAllowed, until: now.Add(streamAuthTTL)}
	g.mu.Unlock()
	return answer, nil
}

// forgetChannels drops the channel verdicts of credential key (g.mu held).
func (g *streamGate) forgetChannels(key string) {
	prefix := key + "\x00"
	for k := range g.chans {
		if strings.HasPrefix(k, prefix) {
			delete(g.chans, k)
		}
	}
}

// Package liveguard is the streaming service's own check of live TV under parental
// controls (Epic 57, story 57.10). The API hides channels and programmes from a
// restricted account, but the stream is served here, so here is where it is refused:
//
//   - a channel's stream (/stream/channel/{id}/…) is served only to a named viewer the
//     API allows the channel's programme now, or to an address that may use the tuner;
//   - the tuner (the HDHomeRun routes) and the guide exports (/xmltv.xml, /lineup.m3u)
//     answer only the addresses that may use the tuner.
//
// Everything fails closed: no verdict, no stream.
package liveguard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/clientip"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/httpx"
)

// AudChannel is the audience of a signed channel URL (the API's
// signedurl.AudChannel): sub is the channel, usr the viewer.
const AudChannel = "streaming-channel"

// ViewerHeader names the signed-in person; believed only from this machine's web port
// gate, on loopback (streaming/internal/viewing.ViewerHeader).
const ViewerHeader = "X-Maktaba-Viewer"

// AdminTokenHeader is the web port gate's word that the session is the admin token's:
// an admin with no account, so no viewer to name. Believed only on loopback.
const AdminTokenHeader = "X-Maktaba-Admin-Token"

// adminTokenUsr is the usr of a channel URL signed for the admin token; it counts only
// with the signed is_admin claim.
const adminTokenUsr = "admin"

// Slate texts: what a player shows while the programme on air is above the limit.
const (
	SlateEN = "Not available right now. This channel comes back when the next programme starts."
	SlateAR = "غير متاح الآن. تعود هذه القناة عند بدء البرنامج التالي."
)

// Guard is the check. The zero value refuses everything it guards.
type Guard struct {
	// Keys verifies a signed channel URL.
	Keys   *auth.JWKSCache
	Leeway time.Duration
	// Verdicts is the API's answer for a viewer and a channel.
	Verdicts Verdicts
	// Tuner is the addresses that may use the tuner.
	Tuner *Tuner
	// Now and After are the clock (tests inject them).
	Now    func() time.Time
	After  func(time.Duration) <-chan time.Time
	Logger *slog.Logger

	cache cache
}

func (g *Guard) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func (g *Guard) after(d time.Duration) <-chan time.Time {
	if g.After != nil {
		return g.After(d)
	}
	return time.After(d)
}

// Wrap is mount with the guard in front of every route it registers.
func (g *Guard) Wrap(mount func(chi.Router)) func(chi.Router) {
	return func(r chi.Router) {
		r.Group(func(gr chi.Router) {
			gr.Use(g.Middleware)
			mount(gr)
		})
	}
}

// surface is what a path is to the guard.
type surface int

const (
	surfaceOpen    surface = iota // not live TV's to guard here
	surfaceChannel                // a channel's stream
	surfaceTuner                  // the tuner and the guide exports
)

// ChannelPrefix is the channel streams' path prefix.
const ChannelPrefix = "/stream/channel/"

// TunerPaths are the tuner's and the guide exports' paths; TunerPrefix is the tuner's
// stream (/auto/v{number}).
var TunerPaths = []string{"/discover.json", "/device.xml", "/lineup.json", "/lineup_status.json",
	"/lineup.post", "/xmltv.xml", "/lineup.m3u"}

// TunerPrefix is the tuner's stream.
const TunerPrefix = "/auto/"

func classify(path string) surface {
	if strings.HasPrefix(path, ChannelPrefix) {
		return surfaceChannel
	}
	if strings.HasPrefix(path, TunerPrefix) {
		return surfaceTuner
	}
	for _, p := range TunerPaths {
		if path == p {
			return surfaceTuner
		}
	}
	return surfaceOpen
}

// Middleware guards the live TV paths; every other path passes.
func (g *Guard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		s := classify(path)
		// the router may match on the escaped path: the stricter reading of the two
		// wins, and a guarded path written two ways is no path
		if raw := r.URL.RawPath; raw != "" && raw != path {
			if s != surfaceOpen || classify(raw) != surfaceOpen {
				notFound(w)
				return
			}
		}
		switch s {
		case surfaceChannel:
			g.channel(w, r, next, strings.TrimPrefix(path, ChannelPrefix))
		case surfaceTuner:
			if !g.Tuner.Allowed(r.Context(), clientip.PeerAddr(r.RemoteAddr)) {
				httpx.Write(w, http.StatusForbidden, "tuner-address-not-allowed", "Not allowed",
					"the tuner and the guide files answer this machine, the Plex server set up in Maktaba, and the addresses an admin listed")
				return
			}
			next.ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func notFound(w http.ResponseWriter) {
	httpx.Write(w, http.StatusNotFound, "not-found", "Not found", "")
}

// who is the caller of a channel's stream.
type who struct {
	viewer uuid.UUID
	// open: no viewer to ask about (the admin token, or a tuner address)
	open bool
	// sig is the signed URL's token when it came in the query (a playlist's own
	// addresses then carry it on)
	sig string
}

// channel serves rest of /stream/channel/{id}/… when the caller may watch it now.
func (g *Guard) channel(w http.ResponseWriter, r *http.Request, next http.Handler, rest string) {
	idStr, tail, _ := strings.Cut(rest, "/")
	id, err := uuid.Parse(idStr)
	if err != nil || len(idStr) != 36 {
		notFound(w)
		return
	}
	c, ok := g.identify(w, r, id)
	if !ok {
		return
	}
	var v Verdict
	if !c.open {
		if v, err = g.verdict(r.Context(), c.viewer, id); err != nil {
			g.unavailable(w, r, err)
			return
		}
		if !v.ChannelAllowed {
			notFound(w)
			return
		}
		if !v.Allowed {
			writeRestricted(w, v.Until)
			return
		}
	}
	switch {
	case !c.open && tail == "live.ts":
		g.serveWatched(w, r, next, c.viewer, id, v)
	case c.sig != "" && strings.HasSuffix(tail, ".m3u8") && r.Method == http.MethodGet:
		serveSignedPlaylist(w, r, next, c.sig)
	default:
		next.ServeHTTP(w, r)
	}
}

// identify names the caller, or answers the refusal and reports false.
func (g *Guard) identify(w http.ResponseWriter, r *http.Request, channel uuid.UUID) (who, bool) {
	tok, fromQuery := r.URL.Query().Get("sig"), true
	if tok == "" {
		if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
			tok, fromQuery = strings.TrimSpace(h[7:]), false
		}
	}
	if tok != "" {
		if g.Keys == nil {
			httpx.WriteSignedURLError(w, httpx.SignedURLBadSignature)
			return who{}, false
		}
		claims, err := auth.Verify(tok, g.Keys, auth.VerifyConfig{Now: g.now, Leeway: g.Leeway})
		switch {
		case errors.Is(err, auth.ErrExpired):
			httpx.WriteSignedURLError(w, httpx.SignedURLExpired)
			return who{}, false
		case err != nil:
			httpx.WriteSignedURLError(w, httpx.SignedURLBadSignature)
			return who{}, false
		case claims.Aud != AudChannel:
			httpx.WriteSignedURLError(w, httpx.SignedURLWrongAud)
			return who{}, false
		}
		if sub, err := uuid.Parse(claims.Sub); err != nil || sub != channel {
			httpx.WriteSignedURLError(w, httpx.SignedURLWrongSub)
			return who{}, false
		}
		c := who{}
		if fromQuery {
			c.sig = tok
		}
		if u, err := uuid.Parse(claims.Usr); err == nil && u != uuid.Nil {
			c.viewer = u
			return c, true
		}
		// no account in usr: only the admin token's URL, which the API signs with
		// is_admin; anything else names no one, and no one is not unrestricted
		if claims.Usr == adminTokenUsr && claims.IsAdmin {
			c.open = true
			return c, true
		}
		httpx.Write(w, http.StatusForbidden, "signed-url-no-viewer", "Signed URL invalid",
			"the signed URL names no viewer")
		return who{}, false
	}
	if clientip.PeerAddr(clientip.Peer(r)).IsLoopback() {
		// this machine's web port gate, which checked the session
		if h := r.Header.Get(ViewerHeader); h != "" {
			u, err := uuid.Parse(h)
			if err != nil || u == uuid.Nil {
				httpx.Write(w, http.StatusForbidden, "viewer-unknown", "Not allowed", "the viewer is not an account")
				return who{}, false
			}
			return who{viewer: u}, true
		}
		if r.Header.Get(AdminTokenHeader) != "" {
			return who{open: true}, true
		}
	}
	// no viewer named: a tuner (Plex, this machine's own tools), by its address
	if g.Tuner.Allowed(r.Context(), clientip.PeerAddr(r.RemoteAddr)) {
		return who{open: true}, true
	}
	httpx.WriteSignedURLError(w, httpx.SignedURLMissing)
	return who{}, false
}

// verdict is the API's answer, reused for a short while (cache).
func (g *Guard) verdict(ctx context.Context, viewer, channel uuid.UUID) (Verdict, error) {
	k := verdictKey{viewer, channel}
	now := g.now()
	if v, ok := g.cache.get(k, now); ok {
		return v, nil
	}
	if g.Verdicts == nil {
		return Verdict{}, ErrNoAPI
	}
	v, err := g.Verdicts.Verdict(ctx, viewer, channel)
	if err != nil {
		return Verdict{}, err
	}
	g.cache.put(k, v, now)
	return v, nil
}

// unavailable is the refusal when the verdict could not be had: nothing is streamed.
func (g *Guard) unavailable(w http.ResponseWriter, r *http.Request, err error) {
	if g.Logger != nil && r.Context().Err() == nil {
		g.Logger.Warn("tv: the parental verdict could not be had; the stream is refused",
			"err", err.Error(), "event", "tv_parental_verdict_failed")
	}
	w.Header().Set("Retry-After", "5")
	httpx.Write(w, http.StatusServiceUnavailable, "parental-verdict-unavailable", "Not available right now",
		"the parental check is not answering; nothing is streamed until it does")
}

// restricted is the 403 for a channel whose programme now is above the viewer's limit
// (the API's parentalh.ProgrammeRestricted, plus the slate's text).
type restricted struct {
	httpx.Problem
	Code    string            `json:"code"`
	Reason  string            `json:"reason"`
	RetryAt string            `json:"retry_at,omitempty"`
	Message map[string]string `json:"message"`
}

func writeRestricted(w http.ResponseWriter, until *time.Time) {
	body := restricted{
		Problem: httpx.Problem{Type: "https://maktaba.invalid/problems/restricted", Title: "Not available right now", Status: http.StatusForbidden,
			Detail: SlateEN},
		Code: "restricted", Reason: "programme",
		Message: map[string]string{"en": SlateEN, "ar": SlateAR},
	}
	if until != nil {
		body.RetryAt = until.UTC().Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(httpx.ProblemHeader, "restricted")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(body)
}

// watchFloor is the shortest wait between two checks of a running stream.
const watchFloor = time.Second

// serveWatched serves a continuous stream (live.ts) and cuts it when the viewer may no
// longer watch: checked again when the verdict runs out (its Until, the programme
// boundary, or VerdictTTL), and cut on a refusal or when no verdict can be had.
func (g *Guard) serveWatched(w http.ResponseWriter, r *http.Request, next http.Handler, viewer, channel uuid.UUID, v Verdict) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	cw := &cutWriter{ResponseWriter: w}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			wait := reuse(v, g.now())
			if wait < watchFloor {
				wait = watchFloor
			}
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-g.after(wait):
			}
			var err error
			v, err = g.verdict(ctx, viewer, channel)
			if err != nil || !v.ChannelAllowed || !v.Allowed {
				cw.cut.Store(true)
				cancel()
				return
			}
		}
	}()
	next.ServeHTTP(cw, r.WithContext(ctx))
	close(done)
	wg.Wait()
}

// cutWriter refuses writes once cut.
type cutWriter struct {
	http.ResponseWriter
	cut atomic.Bool
}

// errCut is a write to a stream the guard cut.
var errCut = errors.New("liveguard: the stream was stopped: the programme is above the viewer's limit")

func (c *cutWriter) Write(p []byte) (int, error) {
	if c.cut.Load() {
		return 0, errCut
	}
	return c.ResponseWriter.Write(p)
}

func (c *cutWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok && !c.cut.Load() {
		f.Flush()
	}
}

// Unwrap is for http.ResponseController.
func (c *cutWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

// playlistMax bounds a playlist held to add the signature.
const playlistMax = 4 << 20

// serveSignedPlaylist serves a playlist asked for with a signed URL, with the same
// signature on every address in it: a player given only the signed playlist address
// then fetches the variant playlists and segments with it.
func serveSignedPlaylist(w http.ResponseWriter, r *http.Request, next http.Handler, sig string) {
	buf := &bufferWriter{ResponseWriter: w, status: http.StatusOK}
	next.ServeHTTP(buf, r)
	if buf.passed {
		return
	}
	body := buf.body.Bytes()
	if buf.status == http.StatusOK {
		body = signPlaylist(body, sig)
	}
	w.Header().Del("Content-Length")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(buf.status)
	_, _ = w.Write(body)
}

// bufferWriter holds a response's body; one too large is passed through as it is
// (passed), unsigned: its addresses then fail without the signature, never open.
type bufferWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
	passed bool
}

func (b *bufferWriter) WriteHeader(status int) {
	if !b.passed {
		b.status = status
		return
	}
	b.ResponseWriter.WriteHeader(status)
}

func (b *bufferWriter) Write(p []byte) (int, error) {
	if b.passed {
		return b.ResponseWriter.Write(p)
	}
	if b.body.Len()+len(p) > playlistMax {
		b.passed = true
		b.ResponseWriter.WriteHeader(b.status)
		if _, err := b.ResponseWriter.Write(b.body.Bytes()); err != nil {
			return 0, err
		}
		return b.ResponseWriter.Write(p)
	}
	return b.body.Write(p)
}

var uriAttrRe = regexp.MustCompile(`URI="([^"]+)"`)

// signPlaylist adds sig to every address of an HLS playlist: each address line and each
// URI="…" attribute.
func signPlaylist(body []byte, sig string) []byte {
	lines := strings.Split(string(body), "\n")
	for i, line := range lines {
		t := strings.TrimRight(line, "\r")
		cr := line[len(t):]
		switch {
		case strings.TrimSpace(t) == "":
		case strings.HasPrefix(t, "#"):
			lines[i] = uriAttrRe.ReplaceAllStringFunc(t, func(m string) string {
				return `URI="` + withSig(m[len(`URI="`):len(m)-1], sig) + `"`
			}) + cr
		default:
			lines[i] = withSig(t, sig) + cr
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func withSig(uri, sig string) string {
	if strings.Contains(uri, "sig=") {
		return uri
	}
	sep := "?"
	if strings.Contains(uri, "?") {
		sep = "&"
	}
	return uri + sep + "sig=" + sig
}

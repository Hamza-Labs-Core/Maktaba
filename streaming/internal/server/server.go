// Package server wires the chi router for the Streaming service.
// All Story 8 routes are registered here; signed-URL middleware is
// applied per route family, and the public mux is split from the
// admin mux at the listener boundary (admin holds /healthz and
// /readyz, public holds bytes).
package server

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/clientip"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/capability"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/handlers"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/httpx"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/timelimit"
)

// Deps bundles the dependencies the server needs. Production fills
// these from main.go; tests inject fakes.
type Deps struct {
	Cfg         config.Config
	JWKS        *auth.JWKSCache
	Probe       *probe.Cache
	Profiles    *capability.Registry
	Sessions    session.Store
	Layout      *cache.Layout
	Files       handlers.FileOpener
	Transcripts handlers.TranscriptStreamer
	// Subtitles are the subtitles kept in the database for a read-only library (story
	// 59.03), served after the side-cars; nil: side-cars only.
	Subtitles    handlers.StoredSubtitles
	Chapters     handlers.ChapterReader
	StaticAssets handlers.StaticAssetResolver
	Now          func() time.Time

	// ExtraMounts are additional route registrars applied after the
	// built-in routes — used to mount the Epic 27 live-channel HLS
	// routes (Story 27.3) and the HDHomeRun protocol surface (Story
	// 27.5) without this package importing them. Each is called with the
	// public mux. Nil/empty is a no-op (the no-DB local path).
	ExtraMounts []func(chi.Router)

	// TrustedProxies are the reverse proxies in front of the home ([server]
	// trusted_proxies, Epic 59 story 59.08) whose X-Forwarded-For is believed, besides
	// loopback (the web port's proxy and gate) which always is. Empty: loopback only.
	TrustedProxies []netip.Prefix

	// TimeLimits are the time limits and stream leases the API pushed for restricted
	// accounts (Epic 57 story 57.14). Nil: a URL that needs a lease is refused.
	TimeLimits *timelimit.Registry
}

// New builds the chi router for the public byte-pumping mux.
func New(deps Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	// The client address (Epic 59 story 59.08): X-Forwarded-For (else X-Real-IP) is
	// believed only from loopback (the web port's proxy and stream gate) or a proxy in
	// [server] trusted_proxies, walked right to left past the trusted hops; from anyone
	// else (a TV on the LAN) the peer address stands. chi's RealIP believed it from every
	// peer. The connection's own address stays readable (clientip.Peer) for the checks on
	// this machine's own callers.
	r.Use(clientip.Middleware(deps.TrustedProxies))
	r.Use(middleware.Recoverer)

	leeway := deps.Cfg.LeewayDuration()
	verifier := &auth.Verifier{JWKS: deps.JWKS, Leeway: leeway, Now: deps.Now}

	// /healthz lives on both the public mux (so callers that only
	// know about :8081 still get a 200) and on the admin mux.
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// Direct play (Story 8.3)
	directHandler := &handlers.DirectHandler{
		Probe: deps.Probe, Profiles: deps.Profiles, Files: deps.Files,
		NowFn: deps.Now,
	}
	directLib := auth.LibraryGuard(libResolverForDirect(deps.Probe))
	timeGuard := timelimit.Guard(deps.TimeLimits)
	r.With(auth.SignedURL(verifier, auth.AudDirect, "video_id"), directLib, timeGuard).
		Get("/stream/direct/{video_id}", directHandler.ServeHTTP)
	r.With(auth.SignedURL(verifier, auth.AudDirect, "video_id"), directLib, timeGuard).
		Head("/stream/direct/{video_id}", directHandler.ServeHTTP)

	// Epic 52 (story 52.8): the audio around a suspect word, signed for the video by the
	// API for a home admin
	clipDir := ""
	if deps.Layout != nil {
		clipDir = filepath.Join(deps.Layout.Root, "clips")
	}
	clipHandler := &handlers.ClipHandler{
		Probe: deps.Probe, CacheDir: clipDir, FFmpeg: deps.Cfg.FFmpeg.Binary,
		CacheBytes: handlers.DefaultClipCacheBytes,
	}
	r.With(auth.SignedURL(verifier, auth.AudDirect, "video_id"), directLib, timeGuard).
		Get("/stream/clip/{video_id}", clipHandler.ServeHTTP)

	// HLS / DASH (Stories 8.5/8.6)
	manifestHandler := &handlers.ManifestHandler{
		Sessions: sessionStreamReaderAdapter{deps.Sessions},
		Layout:   deps.Layout,
		Now:      deps.Now,
		// a transcode's first playlist is a second or two away when the player asks
		FirstWrite: 15 * time.Second,
		// story 57.14: sessions of one video share one pace
		PaceVideo: func(ctx context.Context) (string, error) { return videoIDForSession(ctx, deps.Sessions) },
	}
	sessLib := auth.LibraryGuard(libResolverForSession(deps.Probe, deps.Sessions))
	r.Route("/stream/{session_id}", func(sub chi.Router) {
		sub.Use(auth.SignedURL(verifier, auth.AudSession, "session_id"))
		sub.Use(sessLib)
		sub.Use(timeGuard)
		sub.Get("/manifest.m3u8", manifestHandler.ServeMaster)
		sub.Get("/manifest.mpd", manifestHandler.ServeMaster)
		sub.Get("/{rendition}/index.m3u8", manifestHandler.ServeRenditionIndex)
		sub.Get("/{rendition}/{segment}", manifestHandler.ServeSegment)
		sub.Get("/{segment}", manifestHandler.ServeSegment) // DASH: init-*.m4s, chunk-*.m4s
		// Story 8.11 — subtitles. auto.vtt streams transcript_segments;
		// the sidecar path serves .srt/.vtt files sitting next to the
		// source media. Both hang off the same handler so the wiring is
		// shared; each route registers only when its dependency is set.
		subs := &handlers.SubtitleHandler{Transcripts: deps.Transcripts}
		if deps.Files != nil {
			subs.SidecarReader = func(ctx context.Context, lang string) ([]byte, error) {
				path, err := sessionVideoPath(ctx, deps.Sessions, deps.Probe)
				if err != nil {
					return nil, err
				}
				// story 59.03: the side-car beside the video first, then the database's copy
				var videoID string
				if deps.Subtitles != nil {
					videoID, _ = videoIDForSession(ctx, deps.Sessions)
				}
				return handlers.ReadSubtitle(ctx, path, videoID, lang, deps.Files, deps.Subtitles)
			}
			sub.Get("/subs/sidecar/{lang}.vtt", subs.ServeSidecar)
			// Story 48.4 — the sidecars as HLS subtitle renditions too, for
			// players that cannot side-load (AVPlayer, AirPlay, Cast)
			manifestHandler.Subtitles = func(ctx context.Context) (handlers.SubtitleSet, error) {
				row, err := sessionProbe(ctx, deps.Sessions, deps.Probe)
				if err != nil {
					return handlers.SubtitleSet{}, err
				}
				langs := handlers.ListSidecars(pathmap.Resolve(row.Path), os.ReadDir)
				if deps.Subtitles != nil {
					if id, err := videoIDForSession(ctx, deps.Sessions); err == nil && id != "" {
						if stored, err := deps.Subtitles.Langs(ctx, id); err == nil {
							langs = handlers.MergeLangs(langs, stored)
						}
					}
				}
				return handlers.SubtitleSet{DurationSec: row.DurationSec, Langs: langs}, nil
			}
			sub.Get("/subs-{lang}/index.m3u8", manifestHandler.ServeSubtitlePlaylist)
		}
		if deps.Transcripts != nil {
			sub.Get("/subs/auto.vtt", func(w http.ResponseWriter, req *http.Request) {
				videoID, _ := videoIDForSession(req.Context(), deps.Sessions)
				ctx := handlers.WithVideoID(req.Context(), videoID)
				subs.ServeAuto(w, req.WithContext(ctx))
			})
		}
		// Story 8.12 — chapters
		if deps.Chapters != nil {
			ch := &handlers.ChapterHandler{
				Reader: deps.Chapters,
				Resolve: func(ctx context.Context, _ string) (string, error) {
					return videoIDForSession(ctx, deps.Sessions)
				},
			}
			sub.Get("/chapters.json", ch.ServeJSON)
		}
	})

	// Static assets (Story 8.13)
	if deps.StaticAssets != nil {
		static := &handlers.StaticHandler{Resolver: deps.StaticAssets, Files: deps.Files}
		// posters/sprites/thumbs use streaming-static aud; sub = sha256(path).
		// We don't enforce sub == hash inside the handler — middleware does it.
		r.Route("/stream/posters", func(sub chi.Router) {
			sub.Use(auth.SignedURL(verifier, auth.AudStatic, "video_id"))
			sub.Get("/{video_id}", static.ServePoster)
		})
		r.Route("/stream/sprites", func(sub chi.Router) {
			sub.Use(auth.SignedURL(verifier, auth.AudStatic, "video_id"))
			sub.Get("/{video_id}", static.ServeSprite)
		})
		r.Route("/stream/thumbs", func(sub chi.Router) {
			sub.Use(auth.SignedURL(verifier, auth.AudStatic, "video_id"))
			sub.Get("/{video_id}/{name}", static.ServeThumb)
		})
	}

	// Epic 27 live-channel + HDHomeRun routes (Stories 27.3 / 27.5),
	// mounted via injected registrars so this package stays decoupled
	// from the channel/hdhr packages.
	for _, mount := range deps.ExtraMounts {
		if mount != nil {
			mount(r)
		}
	}

	// Fallback — anything else is a 404 problem+json.
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		httpx.Write(w, http.StatusNotFound, "route-not-found", "no such route", "")
	})

	return r
}

// sessionStreamReaderAdapter narrows session.Store down to the small
// surface ManifestHandler needs.
type sessionStreamReaderAdapter struct{ s session.Store }

func (a sessionStreamReaderAdapter) Get(ctx context.Context, id uuid.UUID) (*session.Row, bool, error) {
	return a.s.Get(ctx, id)
}
func (a sessionStreamReaderAdapter) Touch(ctx context.Context, id uuid.UUID, at time.Time) error {
	return a.s.Touch(ctx, id, at)
}

// libResolverForDirect reads the URL's video_id parameter, looks the
// probe row up, and returns its library_id.
func libResolverForDirect(p *probe.Cache) auth.LibraryResolverFunc {
	return func(ctx context.Context, r *http.Request, _ *auth.Claims) (uuid.UUID, error) {
		vid := chi.URLParam(r, "video_id")
		id, err := uuid.Parse(filepath.Base(vid)) // strip any extension
		if err != nil {
			return uuid.Nil, err
		}
		row, err := p.Lookup(ctx, id)
		if err != nil {
			return uuid.Nil, err
		}
		return row.LibraryID, nil
	}
}

// libResolverForSession looks up the session, then the video, then
// returns the video's library.
func libResolverForSession(p *probe.Cache, s session.Store) auth.LibraryResolverFunc {
	return func(ctx context.Context, r *http.Request, cl *auth.Claims) (uuid.UUID, error) {
		sid := chi.URLParam(r, "session_id")
		id, err := uuid.Parse(sid)
		if err != nil {
			return uuid.Nil, err
		}
		row, ok, err := s.Get(ctx, id)
		if err != nil || !ok {
			return uuid.Nil, errors.New("session not found")
		}
		if !sessionOwnedBy(row.UserID, cl) {
			// a URL signed for another user's session: the same 404 as an
			// unknown session, so the session's existence does not leak
			return uuid.Nil, errors.New("session not found")
		}
		probeRow, err := p.Lookup(ctx, row.VideoID)
		if err != nil {
			return uuid.Nil, err
		}
		return probeRow.LibraryID, nil
	}
}

// sessionOwnedBy reports whether the signed URL's user (usr) may use a
// session owned by owner. The API signs a session's manifest for the user
// who opened it, so the two match; a session stored without an owner, or a
// token without usr (older signers), keeps the signature + lib[] check.
func sessionOwnedBy(owner uuid.UUID, cl *auth.Claims) bool {
	if cl == nil || owner == uuid.Nil || cl.IsAdmin {
		return true
	}
	usr, err := cl.UserID()
	if err != nil {
		return cl.Usr == ""
	}
	return usr == owner
}

// videoIDForSession resolves the session id (stored in context by
// SignedURL) to its video id via the session store.
func videoIDForSession(ctx context.Context, s session.Store) (string, error) {
	sid := auth.SubjectFromContext(ctx)
	if sid == "" {
		return "", errors.New("no subject in context")
	}
	id, err := uuid.Parse(sid)
	if err != nil {
		return "", err
	}
	row, ok, err := s.Get(ctx, id)
	if err != nil || !ok {
		return "", errors.New("session not found")
	}
	return row.VideoID.String(), nil
}

// sessionVideoPath resolves the session (the SignedURL subject for
// session-scoped routes) to the source media path on disk, via the
// session store then the probe row. Used by the sidecar subtitle
// reader, which needs the directory the source file lives in.
func sessionVideoPath(ctx context.Context, s session.Store, p *probe.Cache) (string, error) {
	probeRow, err := sessionProbe(ctx, s, p)
	if err != nil {
		return "", err
	}
	return pathmap.Resolve(probeRow.Path), nil // story 31.3: canonical -> this machine
}

// sessionProbe is the probe row of the session's video (the SignedURL
// subject for session-scoped routes).
func sessionProbe(ctx context.Context, s session.Store, p *probe.Cache) (*probe.Row, error) {
	sid := auth.SubjectFromContext(ctx)
	id, err := uuid.Parse(sid)
	if err != nil {
		return nil, err
	}
	row, ok, err := s.Get(ctx, id)
	if err != nil || !ok {
		return nil, errors.New("session not found")
	}
	return p.Lookup(ctx, row.VideoID)
}

// EnsureCacheRoot creates the cache root if it doesn't exist. Called
// from main.go before serving.
func EnsureCacheRoot(root string) error {
	if root == "" {
		return nil
	}
	return os.MkdirAll(root, 0o755)
}

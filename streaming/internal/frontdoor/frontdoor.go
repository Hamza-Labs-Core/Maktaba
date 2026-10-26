// Package frontdoor is the home's front door for Maktaba TV (Epic 32.3, design §4):
// every channel request a client makes reaches the home, which resolves the channel's
// placement and serves it itself (the channel on the home: today's path) or forwards
// it to the node that encodes it (signed with the cluster stream key). HLS playlists
// are always proxied; segments are proxied or redirected (tv_cluster.hls_mode); the
// MPEG-TS of /auto/v{n} and live.ts goes through a splicing copier that keeps the
// client's connection across a failover (Epic 32.4).
package frontdoor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/placement"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/tvmetrics"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/viewing"
)

// Handler is the front door.
type Handler struct {
	Svc      *placement.Service
	Settings placement.SettingsSource
	// Self is the home's host id.
	Self string
	// Local serves a channel placed on the home (channel.HTTPHandler's routes).
	Local http.Handler
	// Engine streams a home channel's TS.
	Engine  *channel.Engine
	Keyring *cluster.Keyring
	Tracker *Tracker
	// Viewings, when set, records who watches (Epic 44 story 44.4): each channel
	// segment served moves its viewer's viewing on.
	Viewings *viewing.Tracker
	Client   *http.Client
	Logger   *slog.Logger
	// Metrics, when set, counts the bytes forwarded from nodes.
	Bytes func(n int64)
	// SpliceRetry is the pause between two attempts to reach a channel's source
	// (0: 500 ms).
	SpliceRetry time.Duration
	// Slots, when set, is this host's TV slots (Epic 33 story 33.1): the
	// channels placed here are served by the live slot (Engine and Local are
	// not used for them).
	Slots *Slots
	// CacheRoot is the slots' cache root (<root>/tv/<slot>/…): a crashed slot's
	// listed segments are still served from disk.
	CacheRoot string
	// PublicBase is the streaming origin the staging preview's M3U and guide
	// (streams, logos) point at ("": the request's).
	PublicBase string
	// PreviewLookup, when set, answers a change's state and release for the
	// preview (tests; nil reads tv_changes).
	PreviewLookup func(ctx context.Context, changeID string) (state, release string)

	clientOnce sync.Once
	stitch     stitchers
	preview    previewCache
}

// NewClient is the front door's HTTP client: keep-alives, no overall timeout (TS
// streams are endless), bounded dial and header waits.
func NewClient() *http.Client {
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DisableCompression:    true,
	}
	return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func (h *Handler) log() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

func (h *Handler) client() *http.Client {
	h.clientOnce.Do(func() {
		if h.Client == nil {
			h.Client = NewClient()
		}
	})
	return h.Client
}

func (h *Handler) settings(ctx context.Context) placement.Settings {
	if h.Settings == nil {
		return placement.DefaultSettings()
	}
	return h.Settings.Cluster(ctx)
}

// Mount wires the channel routes (in place of channel.HTTPHandler's).
func (h *Handler) Mount(r chi.Router) {
	r.Route("/stream/channel/{id}", func(sub chi.Router) {
		sub.Get("/index.m3u8", h.servePlaylist)
		sub.Get("/master.m3u8", h.servePlaylist)
		sub.Get("/manifest.m3u8", h.servePlaylist)
		sub.Get("/live.ts", h.serveLiveTS)
		sub.Get("/s/{slot}/{segment}", h.serveSlotSegment)
		sub.Get("/{segment}", h.serveSegment)
	})
}

// serveLocal hands the request to the home's own channel routes. The outer router's
// route context is dropped so the local router matches the full path again.
func (h *Handler) serveLocal(w http.ResponseWriter, r *http.Request) {
	h.Local.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, nil)))
}

func channelParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad channel id", http.StatusBadRequest)
		return uuid.Nil, false
	}
	return id, true
}

// writeResolveError maps a placement failure to today's answers (AC10).
func writeResolveError(w http.ResponseWriter, err error) {
	var nh placement.ErrNoHost
	if errors.As(err, &nh) {
		w.Header().Set("X-Maktaba-Reason", nh.Reason)
	}
	switch {
	case errors.Is(err, channel.ErrAtCapacity):
		w.Header().Set("Retry-After", "30")
		http.Error(w, "all channel encoders busy", http.StatusServiceUnavailable)
	case errors.Is(err, context.Canceled):
	default:
		w.Header().Set("Retry-After", "5")
		http.Error(w, "channel unavailable", http.StatusServiceUnavailable)
	}
}

func (h *Handler) servePlaylist(w http.ResponseWriter, r *http.Request) {
	id, ok := channelParam(w, r)
	if !ok {
		return
	}
	h.Tracker.TouchHLS(id, r)
	for attempt := 0; attempt < 2; attempt++ {
		p, err := h.Svc.Resolve(r.Context(), id)
		if err != nil {
			writeResolveError(w, err)
			return
		}
		if p.Host == h.Self {
			if h.Slots != nil {
				h.serveSlotPlaylist(w, r, id)
				return
			}
			h.serveLocal(w, r)
			return
		}
		resp, err := h.forward(r.Context(), p, r.URL.Path, nil)
		if err == nil {
			retry := resp.StatusCode == http.StatusConflict ||
				(resp.StatusCode >= 500 && resp.StatusCode != http.StatusServiceUnavailable)
			if !retry || attempt == 1 {
				copyResponse(w, resp, h.count("hls", p.Host))
				return
			}
			_ = resp.Body.Close()
		}
		h.Svc.Invalidate(id) // re-resolve once (AC6)
	}
	w.Header().Set("Retry-After", "2")
	http.Error(w, "channel unavailable", http.StatusBadGateway)
}

func (h *Handler) serveSegment(w http.ResponseWriter, r *http.Request) {
	id, ok := channelParam(w, r)
	if !ok {
		return
	}
	seg := chi.URLParam(r, "segment")
	if !strings.HasPrefix(seg, "seg-") || !strings.HasSuffix(seg, ".ts") || strings.ContainsAny(seg, `/\`) {
		if h.Slots != nil {
			h.proxyLiveSlot(w, r)
			return
		}
		h.serveLocal(w, r)
		return
	}
	h.Tracker.TouchHLS(id, r)
	sw := &segmentWriter{ResponseWriter: w}
	defer h.segmentServed(id, r, sw)
	w = sw
	// a segment costs no query while the placement is cached (AC7)
	p, ok := h.Svc.Cached(id)
	if !ok {
		var err error
		p, err = h.Svc.Resolve(r.Context(), id)
		if err != nil {
			writeResolveError(w, err)
			return
		}
	}
	if p.Host == h.Self {
		if h.Slots != nil {
			h.proxyLiveSlot(w, r) // a playlist from before the slots
			return
		}
		h.serveLocal(w, r)
		return
	}
	if h.settings(r.Context()).HLSMode == placement.HLSRedirect {
		host, _ := h.Svc.Hosts.Get(p.Host)
		if base := strings.TrimRight(host.BaseURL, "/"); base != "" {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, base+r.URL.Path+"?"+h.Keyring.SignedQuery(r.URL.Path, p.Gen), http.StatusTemporaryRedirect)
			return
		}
	}
	hdr := http.Header{}
	if rg := r.Header.Get("Range"); rg != "" {
		hdr.Set("Range", rg)
	}
	resp, err := h.forward(r.Context(), p, r.URL.Path, hdr)
	if err != nil {
		h.Svc.Invalidate(id)
		http.Error(w, "segment unavailable", http.StatusBadGateway)
		return
	}
	copyResponse(w, resp, h.count("hls", p.Host))
}

// forward sends a signed GET for path to the placement's host and accounts the
// result against the host (three failures in a row mark it down).
func (h *Handler) forward(ctx context.Context, p placement.Placement, path string, hdr http.Header) (*http.Response, error) {
	host, ok := h.Svc.Hosts.Get(p.Host)
	base := strings.TrimRight(host.BaseURL, "/")
	if !ok || base == "" {
		h.fail(p)
		tvmetrics.Errors.Inc(p.Host, kindOf(path))
		return nil, errors.New("frontdoor: host address unknown")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	h.Keyring.SignRequest(req, p.Gen)
	if !host.IsHome() {
		markLink(req, p.Host) // on the machine's cluster link when it has one (story 35.16)
	}
	resp, err := h.client().Do(req)
	if err != nil {
		if ctx.Err() == nil {
			h.fail(p)
			tvmetrics.Errors.Inc(p.Host, kindOf(path))
		}
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusConflict:
		h.Svc.Invalidate(p.Channel) // the node says it is not its channel (stale cache)
	case resp.StatusCode >= 500 && resp.StatusCode != http.StatusServiceUnavailable:
		h.fail(p)
		tvmetrics.Errors.Inc(p.Host, kindOf(path))
	default:
		h.Svc.Hosts.OK(p.Host)
	}
	return resp, nil
}

// segmentWriter keeps a segment answer's status (a viewing counts a served one). The
// body goes straight to the writer underneath: only the status is kept, and an answer
// that never set one is a 200 (net/http's).
type segmentWriter struct {
	http.ResponseWriter
	code int
}

func (w *segmentWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

// status is the answer's status (200 when none was set).
func (w *segmentWriter) status() int {
	if w.code == 0 {
		return http.StatusOK
	}
	return w.code
}

// Flush passes through (segments are copied with a flush after each write).
func (w *segmentWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap is the writer underneath (http.ResponseController).
func (w *segmentWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// segmentServed records a segment served (or redirected to its node) for the
// request's viewer; a failed one counts for no one.
func (h *Handler) segmentServed(ch uuid.UUID, r *http.Request, w *segmentWriter) {
	if h.Viewings == nil {
		return
	}
	if code := w.status(); code >= 200 && code < 300 || code == http.StatusTemporaryRedirect {
		h.Viewings.Segment(ch, viewing.FromRequest(r))
	}
}

// ChannelViewers is every channel's viewers now: its TS clients (tuners, live.ts)
// and its app viewings (HLS). The front door sees every viewer of the home's
// channels, wherever they encode, so it writes channel_runtime.viewer_count
// (the engines behind it leave the column alone: channel.PGRepo.KeepViewers).
func (h *Handler) ChannelViewers() map[uuid.UUID]int {
	out := h.Tracker.TSClients()
	for ch, n := range h.Viewings.AppViewers() {
		out[ch] += n
	}
	return out
}

func (h *Handler) fail(p placement.Placement) {
	h.Svc.Invalidate(p.Channel)
	h.Svc.Hosts.Fail(p.Host)
}

// count is the byte counter of a forwarded answer: the Bytes hook and
// maktaba_tv_frontdoor_bytes_total{kind,host} (story 32.11).
func (h *Handler) count(kind, host string) func(int64) {
	return func(n int64) {
		if h.Bytes != nil {
			h.Bytes(n)
		}
		tvmetrics.Bytes.Add(float64(n), kind, host)
	}
}

// kindOf is the error kind of a forwarded path: playlist, segment or ts.
func kindOf(path string) string {
	switch {
	case strings.HasSuffix(path, "/live.ts"):
		return "ts"
	case strings.HasSuffix(path, ".m3u8"):
		return "playlist"
	}
	return "segment"
}

// copyResponse passes the upstream answer through, flushing as it goes.
func copyResponse(w http.ResponseWriter, resp *http.Response, count func(int64)) {
	defer resp.Body.Close()
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges",
		"Cache-Control", "Access-Control-Allow-Origin", "Retry-After", "Last-Modified", "ETag"} {
		if v := resp.Header.Values(k); len(v) > 0 {
			w.Header()[k] = v
		}
	}
	w.WriteHeader(resp.StatusCode)
	n, _ := copyFlush(w, resp.Body)
	if count != nil {
		count(n)
	}
}

// copyFlush copies with a flush after every write (no buffering, R3.4).
func copyFlush(w io.Writer, r io.Reader) (int64, error) {
	fl, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	var total int64
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
			if fl != nil {
				fl.Flush()
			}
		}
		if err != nil {
			if err == io.EOF {
				return total, nil
			}
			return total, err
		}
	}
}

func (h *Handler) serveLiveTS(w http.ResponseWriter, r *http.Request) {
	id, ok := channelParam(w, r)
	if !ok {
		return
	}
	p, err := h.Svc.Resolve(r.Context(), id)
	if err != nil {
		writeResolveError(w, err)
		return
	}
	if p.Host == h.Self && h.Slots == nil {
		if _, err := h.Engine.Ensure(r.Context(), id); err != nil {
			if !errors.Is(err, channel.ErrRunningElsewhere) {
				h.serveLocal(w, r) // today's error answers
				return
			}
		}
	}
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if err := h.StreamChannelTS(r.Context(), id, w); err != nil && r.Context().Err() == nil {
		h.log().Warn("tv: live stream ended with an error", "channel_id", id.String(), "err", err.Error(),
			"event", "channel_ts_ended")
	}
}

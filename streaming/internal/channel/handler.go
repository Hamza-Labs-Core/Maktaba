package channel

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// HTTPHandler serves a channel's live output:
//
//	/stream/channel/{id}/index.m3u8     live HLS playlist (tunes lazily)
//	/stream/channel/{id}/master.m3u8    single-rendition master (alias: manifest.m3u8)
//	/stream/channel/{id}/seg-N.ts       HLS segments
//	/stream/channel/{id}/live.ts        continuous MPEG-TS (shared fan-out)
//
// Requesting a playlist starts the channel if it is cold and waits (up
// to the engine's ReadyTimeout) for its first segment, so no separate
// tune call is needed. Every fetch keeps the channel warm.
type HTTPHandler struct {
	Engine *Engine

	pdt pdtCache
}

// Mount wires the channel routes.
func (h *HTTPHandler) Mount(r chi.Router) {
	r.Route("/stream/channel/{id}", func(sub chi.Router) {
		sub.Get("/index.m3u8", h.servePlaylist)
		sub.Get("/master.m3u8", h.servePlaylist)
		sub.Get("/manifest.m3u8", h.servePlaylist)
		sub.Get("/live.ts", h.serveLiveTS)
		sub.Get("/{segment}", h.serveSegment)
	})
}

func (h *HTTPHandler) servePlaylist(w http.ResponseWriter, r *http.Request) {
	id, ok := channelParam(w, r)
	if !ok {
		return
	}
	p, err := h.Engine.EnsureReady(r.Context(), id)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	name := "index.m3u8"
	if !strings.HasSuffix(r.URL.Path, "/index.m3u8") {
		name = "master.m3u8"
	}
	h.Engine.Touch(id)
	if name == "index.m3u8" {
		// segments dated by their timestamps, the same in every slot (33.4)
		b, err := readShared(filepath.Join(p.Dir(), name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		b = h.pdt.rewritePDT(p.Dir(), b, time.Now())
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache, no-store")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = w.Write(b)
		return
	}
	serveShared(w, r, p.Dir(), name)
}

func (h *HTTPHandler) serveSegment(w http.ResponseWriter, r *http.Request) {
	id, ok := channelParam(w, r)
	if !ok {
		return
	}
	seg := chi.URLParam(r, "segment")
	if !safeComponent(seg) || !strings.HasSuffix(seg, ".ts") {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	h.Engine.Touch(id)
	serveShared(w, r, h.Engine.HLSDir(id), seg)
}

func (h *HTTPHandler) serveLiveTS(w http.ResponseWriter, r *http.Request) {
	id, ok := channelParam(w, r)
	if !ok {
		return
	}
	if _, err := h.Engine.Ensure(r.Context(), id); err != nil {
		writeEngineError(w, err)
		return
	}
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if err := h.Engine.StreamChannelTS(r.Context(), id, w); err != nil && r.Context().Err() == nil {
		slog.Warn("channel: live stream ended with an error", "channel_id", id.String(), "err", err.Error(),
			"event", "channel_ts_ended")
	}
}

func channelParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad channel id", http.StatusBadRequest)
		return uuid.Nil, false
	}
	return id, true
}

func writeEngineError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrChannelNotFound):
		http.Error(w, "no such channel", http.StatusNotFound)
	case errors.Is(err, ErrRunningElsewhere):
		// one encode per channel cluster-wide: another host runs it (the
		// home proxies or redirects there; a follow-up wires that)
		var re *RunningElsewhereError
		if errors.As(err, &re) && re.StreamBaseURL != "" {
			w.Header().Set("X-Maktaba-Channel-Host", re.StreamBaseURL)
		}
		http.Error(w, "channel is on air on another host", http.StatusConflict)
	case errors.Is(err, ErrAtCapacity):
		w.Header().Set("Retry-After", "30")
		http.Error(w, "all channel encoders busy", http.StatusServiceUnavailable)
	case errors.Is(err, context.DeadlineExceeded):
		w.Header().Set("Retry-After", "2")
		http.Error(w, "channel is starting", http.StatusServiceUnavailable)
	default:
		slog.Warn("channel: request failed", "err", err.Error(), "event", "channel_request_failed")
		http.Error(w, "channel unavailable", http.StatusServiceUnavailable)
	}
}

// serveShared serves base/name, opened so ffmpeg can still rotate it.
func serveShared(w http.ResponseWriter, r *http.Request, base, name string) {
	full := filepath.Join(base, name)
	if rel, err := filepath.Rel(base, full); err != nil || strings.HasPrefix(rel, "..") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if strings.HasSuffix(name, ".m3u8") {
		// Playlists are tiny and rewritten constantly: read fully, serve
		// from memory, never cache.
		b, err := readShared(full)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache, no-store")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = w.Write(b)
		return
	}
	f, err := openShared(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	http.ServeContent(w, r, name, time.Time{}, f)
}

// safeComponent rejects path components that could escape the HLS dir.
func safeComponent(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	return !strings.ContainsAny(s, "/\\") && !strings.Contains(s, "..")
}

var _ io.Writer = (*packetWriter)(nil)

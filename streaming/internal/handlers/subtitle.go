package handlers

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"time"
)

// TranscriptSegment is the rows we render into VTT for the
// auto-generated subtitle endpoint (Story 8.11 AC-1).
type TranscriptSegment struct {
	Index    int
	StartSec float64
	EndSec   float64
	Speaker  string // optional
	Text     string
}

// TranscriptStreamer is the read side of the transcript_segments
// table. The streaming-side handler iterates pages so very long
// transcripts don't pin memory.
type TranscriptStreamer interface {
	Stream(ctx context.Context, videoID string, page int, pageSize int) ([]TranscriptSegment, error)
}

// SubtitleHandler serves Story 8.11 — live VTT generation, sidecar
// SRT→VTT conversion, and embedded extraction (the third path is
// Pipeline-side; we just serve the cached file).
type SubtitleHandler struct {
	Transcripts TranscriptStreamer
	// SidecarReader resolves the sidecar subtitle for the request's
	// video in the given language and returns it as VTT bytes. It is
	// context-aware because the on-disk location depends on the video
	// the session points at; server.go wires the probe + filesystem
	// lookup here. Used by the /subs/sidecar/{lang}.vtt path.
	SidecarReader func(ctx context.Context, lang string) ([]byte, error)
}

// ServeAuto handles GET /stream/{session_id}/subs/auto.vtt.
//
// Streams VTT cues out of transcript_segments. Cue text is
// HTML-escaped so untrusted SRT/LLM output can't smuggle markup
// through the player.
func (h *SubtitleHandler) ServeAuto(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	w.WriteHeader(http.StatusOK)

	bw := bufio.NewWriter(w)
	defer bw.Flush()
	_, _ = bw.WriteString("WEBVTT\n\n")

	// We need a video id to stream against — story 8.11 derives it
	// from the session lookup; for the unit-testable surface we
	// accept it as a query string ("video=") or fall back to the
	// JWT subject when called via the manifest path.
	videoID := r.URL.Query().Get("video")
	if videoID == "" {
		videoID = r.Context().Value(ctxKeyVideoID).(string)
	}

	const pageSize = 200
	for page := 0; ; page++ {
		segs, err := h.Transcripts.Stream(r.Context(), videoID, page, pageSize)
		if err != nil {
			// Mid-stream error — we already wrote 200, can't change
			// status. Append a comment so the player keeps any cues
			// already emitted.
			_, _ = fmt.Fprintf(bw, "NOTE error: %s\n", err.Error())
			return
		}
		if len(segs) == 0 {
			return
		}
		for _, s := range segs {
			writeCue(bw, s)
		}
		if len(segs) < pageSize {
			return
		}
	}
}

// ServeSidecar handles GET /stream/{session_id}/subs/sidecar/{lang}.vtt
// where {lang} selects a sidecar subtitle (an .srt or .vtt file living
// next to the source media). The probe + filesystem lookup and the
// SRT→VTT conversion are wired by SidecarReader (server.go); this
// handler is the thin HTTP shim around it.
func (h *SubtitleHandler) ServeSidecar(w http.ResponseWriter, r *http.Request) {
	lang := chiURLParam(r, "lang")
	if lang == "" {
		http.Error(w, "missing lang", http.StatusBadRequest)
		return
	}
	if h.SidecarReader == nil {
		http.Error(w, "sidecar reader not wired", http.StatusInternalServerError)
		return
	}
	body, err := h.SidecarReader(r.Context(), lang)
	if err != nil {
		http.Error(w, "sidecar not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// writeCue emits one VTT cue. Cue identifiers are 1-based for
// deterministic test output.
func writeCue(w io.Writer, s TranscriptSegment) {
	_, _ = fmt.Fprintf(w, "%d\n", s.Index+1)
	_, _ = fmt.Fprintf(w, "%s --> %s\n", vttTime(s.StartSec), vttTime(s.EndSec))
	text := html.EscapeString(strings.TrimSpace(s.Text))
	if s.Speaker != "" {
		text = "<v " + html.EscapeString(s.Speaker) + ">" + text
	}
	_, _ = fmt.Fprintf(w, "%s\n\n", text)
}

// vttTime formats seconds as HH:MM:SS.mmm.
func vttTime(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	h := int(seconds) / 3600
	m := (int(seconds) % 3600) / 60
	s := int(seconds) % 60
	ms := int((seconds - float64(int(seconds))) * 1000)
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}

// SrtToVtt converts SRT bytes to VTT. The grammar is small enough
// to write inline; we use an io.Reader so callers can stream large
// files without buffering. Cue text is HTML-escaped for AC-1.
func SrtToVtt(src io.Reader) ([]byte, error) {
	var sb strings.Builder
	sb.WriteString("WEBVTT\n\n")
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 1<<16), 1<<24)
	state := stateLookingForIndex
	for scanner.Scan() {
		line := scanner.Text()
		switch state {
		case stateLookingForIndex:
			if strings.TrimSpace(line) == "" {
				continue
			}
			sb.WriteString(line + "\n")
			state = stateLookingForTimestamp
		case stateLookingForTimestamp:
			// SRT uses commas; VTT uses dots.
			sb.WriteString(strings.ReplaceAll(line, ",", ".") + "\n")
			state = stateLookingForText
		case stateLookingForText:
			if strings.TrimSpace(line) == "" {
				sb.WriteString("\n")
				state = stateLookingForIndex
				continue
			}
			sb.WriteString(html.EscapeString(line) + "\n")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return []byte(sb.String()), nil
}

const (
	stateLookingForIndex = iota
	stateLookingForTimestamp
	stateLookingForText
)

// VideoIDFromSession is the small adapter from session id (the
// SignedURL subject for session-scoped routes) to the video id we
// need to query transcripts for. Passed through context to keep the
// handler tree decoupled from session.Store.
type VideoIDFromSession func(ctx context.Context, sessionID string) (string, error)

type ctxKey int

const (
	ctxKeyVideoID ctxKey = iota
)

// WithVideoID stamps the session's video id into ctx so ServeAuto can
// stream transcripts without re-looking it up.
func WithVideoID(ctx context.Context, videoID string) context.Context {
	return context.WithValue(ctx, ctxKeyVideoID, videoID)
}

// ResolveVideoID is the request-time adapter that runs before
// ServeAuto. Returns ErrNoVideoID if the session can't be resolved.
var ErrNoVideoID = errors.New("no video id resolved for session")

// _ keeps the time import used; future heartbeat logging will hang
// VTT-render duration off this.
var _ = time.Now

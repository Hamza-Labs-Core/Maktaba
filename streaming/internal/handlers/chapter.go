package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/httpx"
)

// Chapter is the wire shape returned by chapters.json (Story 8.12 AC-1).
type Chapter struct {
	Seq      int     `json:"seq"`
	StartSec float64 `json:"start_sec"`
	EndSec   float64 `json:"end_sec"`
	Title    string  `json:"title"`
	Source   string  `json:"source"` // "embedded" | "manual" | "inferred"
}

// ChapterReader is the read side of the chapters table the handler
// queries. Production wires Postgres; tests use FakeChapterReader.
type ChapterReader interface {
	List(ctx context.Context, videoID string) ([]Chapter, error)
}

// ChapterHandler serves Story 8.12 — chapters.json plus
// helpers for the master playlist DATERANGE markers.
type ChapterHandler struct {
	Reader  ChapterReader
	Resolve VideoIDFromSession
}

// ServeJSON serves GET /stream/{session_id}/chapters.json.
func (h *ChapterHandler) ServeJSON(w http.ResponseWriter, r *http.Request) {
	sub := auth.SubjectFromContext(r.Context())
	videoID, err := h.Resolve(r.Context(), sub)
	if err != nil {
		httpx.Write(w, http.StatusNotFound, "video-not-found", "video not found", err.Error())
		return
	}
	chs, err := h.Reader.List(r.Context(), videoID)
	if err != nil {
		httpx.Write(w, http.StatusInternalServerError, "chapter-read-failed", "chapter read failed", err.Error())
		return
	}
	chs = MergeChapters(chs)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(chs)
}

// MergeChapters resolves the priority merge from AC-1: embedded >
// manual > inferred on overlapping ranges. Returns ranges sorted
// by start_sec.
func MergeChapters(in []Chapter) []Chapter {
	priority := map[string]int{"inferred": 0, "manual": 1, "embedded": 2}
	sort.Slice(in, func(i, j int) bool {
		if in[i].StartSec == in[j].StartSec {
			return priority[in[i].Source] > priority[in[j].Source]
		}
		return in[i].StartSec < in[j].StartSec
	})

	out := make([]Chapter, 0, len(in))
	for _, c := range in {
		if len(out) == 0 {
			out = append(out, c)
			continue
		}
		last := out[len(out)-1]
		if c.StartSec >= last.EndSec {
			out = append(out, c)
			continue
		}
		// overlap — keep the higher priority one
		if priority[c.Source] > priority[last.Source] {
			out[len(out)-1] = c
		}
	}
	for i := range out {
		out[i].Seq = i
	}
	return out
}

// DateRangeTagsForPlaylist emits #EXT-X-DATERANGE lines for HLS
// (Story 8.12 AC-2). startedAt anchors the math.
func DateRangeTagsForPlaylist(chs []Chapter, startedAt time.Time) []string {
	out := make([]string, 0, len(chs))
	for _, c := range chs {
		ts := startedAt.Add(time.Duration(c.StartSec * float64(time.Second))).UTC().Format(time.RFC3339)
		dur := c.EndSec - c.StartSec
		out = append(out,
			"#EXT-X-DATERANGE:CLASS=\"chapter\",ID=\""+itoa(c.Seq)+"\",START-DATE=\""+ts+"\",DURATION="+ftoa(dur)+",X-TITLE=\""+escapeAttr(c.Title)+"\"",
		)
	}
	return out
}

func itoa(n int) string { return formatInt(int64(n)) }
func ftoa(f float64) string {
	return strings.TrimRight(strings.TrimRight(formatFloat(f, 'f', 3), "0"), ".")
}

// formatInt and formatFloat avoid pulling strconv in the hot path —
// we already import the rest of stdlib.
func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := make([]byte, 0, 20)
	for n > 0 {
		buf = append(buf, byte('0'+n%10))
		n /= 10
	}
	if neg {
		buf = append(buf, '-')
	}
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

func formatFloat(f float64, fmt byte, prec int) string {
	// minimal; uses fmt-like format only for our small needs
	return formatFloatSimple(f, prec)
}

func formatFloatSimple(f float64, prec int) string {
	if f != f { // NaN
		return "0"
	}
	neg := f < 0
	if neg {
		f = -f
	}
	intPart := int64(f)
	frac := f - float64(intPart)
	mult := 1
	for i := 0; i < prec; i++ {
		mult *= 10
	}
	fracInt := int64(frac*float64(mult) + 0.5)
	out := formatInt(intPart) + "." + padLeft(formatInt(fracInt), prec, '0')
	if neg {
		out = "-" + out
	}
	return out
}

func padLeft(s string, n int, pad byte) string {
	if len(s) >= n {
		return s
	}
	buf := make([]byte, n-len(s))
	for i := range buf {
		buf[i] = pad
	}
	return string(buf) + s
}

func escapeAttr(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

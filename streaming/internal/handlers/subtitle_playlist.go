package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/httpx"
)

// A video session's subtitles as HLS renditions (Epic 48 story 48.4, gap G3).
//
// The sidecar route (/stream/{session}/subs/sidecar/{lang}.vtt) serves each
// subtitle as one WebVTT file, which hls.js and ExoPlayer side-load. AVPlayer
// cannot side-load, and neither can an AirPlay or Cast receiver: they show only
// what the master playlist lists. So the master also lists each subtitle as an
// EXT-X-MEDIA TYPE=SUBTITLES rendition whose media playlist
// (/stream/{session}/subs-{lang}/index.m3u8) has one segment: the sidecar
// route's file. Both paths sit inside the web port's allowed /stream/ paths
// (cmd/maktaba-server/stream_gate.go), and both are signed like the rest of the
// session (the playlists carry the session's ?sig= on each child URI).

// subtitleGroup is the GROUP-ID of the session's subtitle renditions.
const subtitleGroup = "subs"

// subtitleDirPrefix names a subtitle rendition's folder: subs-{lang}.
const subtitleDirPrefix = "subs-"

// subtitleLang is a sidecar language the renditions can carry: one path
// segment the stream gate allows after "subs-" (32 characters in all).
var subtitleLang = regexp.MustCompile(`^[A-Za-z0-9_-]{1,27}$`)

// bareSubtitleLang is a sidecar named by its language alone (en.srt beside the
// video): only a language tag's shape is taken, so notes.srt is not a subtitle.
var bareSubtitleLang = regexp.MustCompile(`^[A-Za-z]{2,3}([_-][A-Za-z0-9]{2,8})*$`)

// SubtitleSet is what a session's subtitle renditions are made of.
type SubtitleSet struct {
	// DurationSec is the video's length: the one segment's duration. With
	// none known (0), no rendition is listed.
	DurationSec float64
	// Langs are the sidecar languages, each one the sidecar route serves.
	Langs []string
}

// SubtitleLister finds the subtitles of the request's session (the signed
// URL's subject). server.go wires it to the session's video and its folder.
type SubtitleLister func(ctx context.Context) (SubtitleSet, error)

// ListSidecars lists the languages of the sidecar subtitles ReadSidecar
// serves for videoPath: movie.<lang>.vtt / .srt beside it, and <lang>.vtt /
// .srt named by a language tag alone. Sorted, each once; a language the
// renditions' paths cannot carry is left out. A folder that cannot be read
// has none.
func ListSidecars(videoPath string, readDir func(string) ([]fs.DirEntry, error)) []string {
	entries, err := readDir(filepath.Dir(videoPath))
	if err != nil {
		return nil
	}
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	var langs []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		if ext != ".vtt" && ext != ".srt" {
			continue
		}
		stem := strings.TrimSuffix(name, ext)
		var lang string
		if rest, ok := strings.CutPrefix(stem, base+"."); ok {
			lang = rest
		} else if bareSubtitleLang.MatchString(stem) {
			lang = stem
		}
		if subtitleLang.MatchString(lang) && !slices.Contains(langs, lang) {
			langs = append(langs, lang)
		}
	}
	slices.Sort(langs)
	return langs
}

// subtitleName is a rendition's NAME: the language's own name (English,
// العربية), the same in every app language; the code itself when it is no
// language tag.
func subtitleName(lang string) (name, tag string) {
	t, err := language.Parse(strings.ReplaceAll(lang, "_", "-"))
	if err != nil || t == language.Und {
		return lang, ""
	}
	if n := display.Self.Name(t); n != "" {
		return n, t.String()
	}
	return lang, t.String()
}

// WithSubtitleRenditions adds the session's subtitle renditions to an HLS
// master playlist: an EXT-X-MEDIA TYPE=SUBTITLES line per language (before the
// first variant, DEFAULT=NO, AUTOSELECT=YES so a viewer's system preference may
// pick one), and SUBTITLES="subs" on each EXT-X-STREAM-INF. With no languages,
// or a master without a variant, the master is returned unchanged.
func WithSubtitleRenditions(master []byte, langs []string) []byte {
	if len(langs) == 0 || !bytes.Contains(master, []byte("#EXT-X-STREAM-INF:")) {
		return master
	}
	nl := lineEnd(master, max(bytes.IndexByte(master, '\n'), 0))
	var media bytes.Buffer
	names := map[string]bool{}
	for _, lang := range langs {
		if !subtitleLang.MatchString(lang) {
			continue
		}
		name, tag := subtitleName(lang)
		if names[name] { // NAME is unique in its group
			name += " (" + lang + ")"
		}
		names[name] = true
		fmt.Fprintf(&media, `#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="%s",NAME="%s",`, subtitleGroup, strings.ReplaceAll(name, `"`, "'"))
		if tag != "" {
			fmt.Fprintf(&media, `LANGUAGE="%s",`, tag)
		}
		fmt.Fprintf(&media, `DEFAULT=NO,AUTOSELECT=YES,URI="%s%s/index.m3u8"%s`, subtitleDirPrefix, lang, nl)
	}
	if media.Len() == 0 {
		return master
	}
	lines := bytes.SplitAfter(master, []byte("\n"))
	out := make([]byte, 0, len(master)+media.Len()+64)
	added := false
	for _, line := range lines {
		body := bytes.TrimRight(line, "\r\n")
		if bytes.HasPrefix(body, []byte("#EXT-X-STREAM-INF:")) {
			if !added {
				out = append(out, media.Bytes()...)
				added = true
			}
			if !bytes.Contains(body, []byte("SUBTITLES=")) {
				out = append(out, body...)
				out = append(out, `,SUBTITLES="`+subtitleGroup+`"`...)
				out = append(out, line[len(body):]...)
				continue
			}
		}
		out = append(out, line...)
	}
	return out
}

// hlsTimestampBase is the MPEG-TS timestamp (90 kHz) of the video's time 0 in
// a session's HLS segments: ffmpeg's MPEG-TS muxer starts 1.4 s in, and a
// transcode opened at a position keeps the video's times (-output_ts_offset),
// so time t is always at 126000 + t×90000. TestHLSTimestampBase_RealFFmpeg
// measures it.
const hlsTimestampBase = 126000

// hlsTimestampQuery on the sidecar route asks for the WebVTT as an HLS
// segment: with X-TIMESTAMP-MAP, which ties its cue times to the segments'
// timestamps. Without it, a player takes cue time 0 for timestamp 0 and shows
// every cue 1.4 s early (AVPlayer and hls.js both, measured).
const hlsTimestampQuery = "hls"

// WithTimestampMap gives a WebVTT file the session's X-TIMESTAMP-MAP, right
// after its WEBVTT line, so a player places its cues on the session's
// timestamps (time t of the video at hlsTimestampBase + t\u00D790000). A file that
// is no WebVTT is returned as it is.
//
// A map the file already has is rebased, not kept: its MPEGTS was some other
// packaging's timestamps (MPEGTS:900000 would put the cues 8.6 s late here),
// so it becomes hlsTimestampBase. Its LOCAL is kept: it says which cue time is
// the video's start (a file whose cues count from 01:00:00.000 says
// LOCAL:01:00:00.000), so with the map rebased, cue time c plays at video time
// c \u2212 LOCAL. A LOCAL that is no WebVTT timestamp counts as 00:00:00.000.
//
// Lines may end in CR, LF or CRLF (WebVTT allows all three): the header is
// read with any of them, the map line takes the WEBVTT line's ending, and the
// rest of the file is left as it is.
func WithTimestampMap(vtt []byte) []byte {
	body := bytes.TrimPrefix(vtt, []byte("\uFEFF"))
	if !bytes.HasPrefix(body, []byte("WEBVTT")) {
		return vtt
	}
	first, nl, rest := vttLine(body)
	if len(nl) == 0 {
		nl = []byte("\n")
	}
	local := "00:00:00.000"
	var header []byte // the header's other lines, each with its own ending
	for len(rest) > 0 {
		line, end, after := vttLine(rest)
		if len(line) == 0 { // the blank line that ends the header
			break
		}
		if v, ok := bytes.CutPrefix(line, []byte("X-TIMESTAMP-MAP=")); ok {
			if l := mapLocal(string(v)); l != "" {
				local = l
			}
		} else {
			header = append(append(header, line...), end...)
		}
		rest = after
	}
	out := make([]byte, 0, len(body)+64)
	out = append(out, first...)
	out = fmt.Appendf(out, "%sX-TIMESTAMP-MAP=MPEGTS:%d,LOCAL:%s%s", nl, hlsTimestampBase, local, nl)
	out = append(out, header...)
	return append(out, rest...)
}

// vttLine splits off data's first line: the line, its ending (CRLF, CR, LF,
// or none at the end of data) and what follows.
func vttLine(data []byte) (line, end, rest []byte) {
	i := bytes.IndexAny(data, "\r\n")
	if i < 0 {
		return data, nil, nil
	}
	n := 1
	if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
		n = 2
	}
	return data[:i], data[i : i+n], data[i+n:]
}

// vttTimestamp is a WebVTT timestamp: (hh:)mm:ss.ttt, the hours two digits or
// more.
var vttTimestamp = regexp.MustCompile(`^(?:(\d{2,}):)?([0-5]\d):([0-5]\d)\.(\d{3})$`)

// mapLocal is the LOCAL of an X-TIMESTAMP-MAP value (MPEGTS:\u2026 and LOCAL:\u2026 in
// either order) as hh:mm:ss.ttt; "" when it has none that is a WebVTT
// timestamp.
func mapLocal(v string) string {
	for _, part := range strings.Split(v, ",") {
		l, ok := strings.CutPrefix(strings.TrimSpace(part), "LOCAL:")
		if !ok {
			continue
		}
		m := vttTimestamp.FindStringSubmatch(l)
		if m == nil {
			return ""
		}
		h := m[1]
		if h == "" {
			h = "00"
		}
		return h + ":" + m[2] + ":" + m[3] + "." + m[4]
	}
	return ""
}

// SubtitlePlaylist is a subtitle rendition's media playlist: one WebVTT
// segment, the whole video long, at the sidecar route asked for as an HLS
// segment (relative to the playlist's folder, so a front door mounting
// /stream/ elsewhere keeps it).
func SubtitlePlaylist(lang string, durationSec float64) []byte {
	target := max(int(math.Ceil(durationSec)), 1)
	return fmt.Appendf(nil, "#EXTM3U\n"+
		"#EXT-X-VERSION:3\n"+
		"#EXT-X-TARGETDURATION:%d\n"+
		"#EXT-X-MEDIA-SEQUENCE:0\n"+
		"#EXT-X-PLAYLIST-TYPE:VOD\n"+
		"#EXTINF:%.3f,\n"+
		"../subs/sidecar/%s.vtt?%s=1\n"+
		"#EXT-X-ENDLIST\n", target, durationSec, lang, hlsTimestampQuery)
}

// subtitles is the request's session's subtitle set when renditions can be
// made of it (a lister wired, the video's length known, a sidecar there).
func (h *ManifestHandler) subtitles(r *http.Request) (SubtitleSet, bool) {
	if h.Subtitles == nil {
		return SubtitleSet{}, false
	}
	set, err := h.Subtitles(r.Context())
	if err != nil || set.DurationSec <= 0 || len(set.Langs) == 0 {
		return SubtitleSet{}, false
	}
	return set, true
}

// ServeSubtitlePlaylist handles GET /stream/{session_id}/subs-{lang}/index.m3u8:
// a subtitle rendition's one-segment playlist, signed with the request's sig.
// A language the session has no sidecar in is a 404.
func (h *ManifestHandler) ServeSubtitlePlaylist(w http.ResponseWriter, r *http.Request) {
	lang := chiURLParam(r, "lang")
	set, ok := h.subtitles(r)
	if !subtitleLang.MatchString(lang) || !ok || !slices.Contains(set.Langs, lang) {
		httpx.Write(w, http.StatusNotFound, "subtitle-not-found", "no subtitle in this language", "")
		return
	}
	data := SubtitlePlaylist(lang, set.DurationSec)
	if sig := r.URL.Query().Get("sig"); sig != "" {
		data = SignPlaylist(data, sig)
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

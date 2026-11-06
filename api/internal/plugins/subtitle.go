package plugins

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// A cue of a subtitle a plugin wrote.
type cue struct {
	start, end int64 // ms
	text       string
}

var (
	vttTime = regexp.MustCompile(`^(?:(\d{1,3}):)?(\d{2}):(\d{2})[.,](\d{3})\s+-->\s+(?:(\d{1,3}):)?(\d{2}):(\d{2})[.,](\d{3})`)
	tagRe   = regexp.MustCompile(`<[^>]*>`)
)

func ms(h, m, s, f string) int64 {
	n := func(x string) int64 {
		v, _ := strconv.ParseInt(x, 10, 64)
		return v
	}
	return ((n(h)*60+n(m))*60+n(s))*1000 + n(f)
}

// Subtitle parses WebVTT or SRT text and writes it again as WebVTT (54.04 §4, README §9
// T3): only cue times and plain text survive. Tags (HTML, voice, styling), notes, styles,
// regions and anything that is not a cue are dropped, so no bytes a plugin chose reach a
// file beyond its words.
func Subtitle(text string) (string, error) {
	if !utf8.ValidString(text) {
		return "", errors.New("subtitles are UTF-8")
	}
	text = strings.ReplaceAll(strings.TrimPrefix(text, "\ufeff"), "\r\n", "\n")
	var cues []cue
	blocks := strings.Split(text, "\n\n")
	for _, b := range blocks {
		lines := strings.Split(strings.Trim(b, "\n"), "\n")
		for i, l := range lines {
			m := vttTime.FindStringSubmatch(strings.TrimSpace(l))
			if m == nil {
				continue
			}
			start, end := ms(m[1], m[2], m[3], m[4]), ms(m[5], m[6], m[7], m[8])
			if end <= start {
				break
			}
			var words []string
			for _, t := range lines[i+1:] {
				t = strings.TrimSpace(tagRe.ReplaceAllString(t, ""))
				t = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&", "&nbsp;", " ").Replace(t)
				t = strings.ReplaceAll(strings.ReplaceAll(t, "-->", "->"), "<", "\u2039")
				if t != "" {
					words = append(words, t)
				}
			}
			if len(words) > 0 {
				cues = append(cues, cue{start, end, strings.Join(words, "\n")})
			}
			break
		}
	}
	if len(cues) == 0 {
		return "", errors.New("no cues")
	}
	var out strings.Builder
	out.WriteString("WEBVTT\n")
	for _, c := range cues {
		fmt.Fprintf(&out, "\n%s --> %s\n%s\n", vttStamp(c.start), vttStamp(c.end), c.text)
	}
	return out.String(), nil
}

func vttStamp(ms int64) string {
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

package channel

// Paging a long strip line (overflow "cycle", the default): a line wider
// than the strip is split at word boundaries into pages that take turns in
// place, each for a few seconds, with a small «١/٢» beside it, so the whole
// title is shown over the cycle and nothing is cut. A quoted verse keeps its
// brackets on every page («﴿…» on the first, «…﴾» on the last, each page
// closed and reopened), and its reference («البقرة ١٨٣») follows on the last
// page or a page of its own. The pages are switched by the same sendcmd
// timeline as the clock, in the block's own time, so they change in step
// with the picture.

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// emOf is the approximate advance of a rune in ems (no shaping metrics
// here: a per-class estimate; widthMargin keeps a safety margin).
func emOf(r rune) float64 {
	switch {
	case r == ' ':
		return 0.28
	case r >= 0x064B && r <= 0x065F, r == 0x0670, r == 0x200F, r == 0x200E:
		return 0 // harakat and marks take no width
	case unicode.Is(unicode.Arabic, r):
		return 0.50
	case r >= '0' && r <= '9':
		return 0.56
	case unicode.IsUpper(r):
		return 0.68
	case unicode.IsLetter(r):
		return 0.52
	case unicode.IsPunct(r), unicode.IsSymbol(r):
		return 0.36
	}
	return 0.55
}

// widthMargin: a page is filled to this share of the line's width.
const widthMargin = 0.9

// textWidth estimates the width of s in pixels at a font size.
func textWidth(s string, size int) float64 {
	var em float64
	for _, r := range s {
		em += emOf(r)
	}
	return em * float64(size)
}

var bracketPairs = map[string]string{"﴿": "﴾", "{": "}", "(": ")", "«": "»", "[": "]", "“": "”"}
var closerOf = func() map[string]string {
	m := map[string]string{}
	for o, c := range bracketPairs {
		m[c] = o
	}
	return m
}()

// bracketState is the stack of brackets open at the end of s, given those
// open before it. A straight quote (") toggles.
func bracketState(open []string, s string) []string {
	st := append([]string(nil), open...)
	for _, r := range s {
		ch := string(r)
		switch {
		case ch == `"`:
			if n := len(st); n > 0 && st[n-1] == `"` {
				st = st[:n-1]
			} else {
				st = append(st, `"`)
			}
		case bracketPairs[ch] != "":
			st = append(st, ch)
		case closerOf[ch] != "":
			if n := len(st); n > 0 && st[n-1] == closerOf[ch] {
				st = st[:n-1]
			}
		}
	}
	return st
}

func closers(st []string) string {
	var b strings.Builder
	for i := len(st) - 1; i >= 0; i-- {
		if st[i] == `"` {
			b.WriteString(`"`)
		} else {
			b.WriteString(bracketPairs[st[i]])
		}
	}
	return b.String()
}

// PageText splits s into pages that fit width px at a font size, at word
// boundaries (a word wider than a page is never split: it is a page of its
// own). Brackets open at a page's end are closed there and reopened on the
// next page, so a quoted verse keeps its brackets on every page.
func PageText(s string, width, size int) []string {
	pages := pageGreedy(s, float64(width)*widthMargin, size)
	if len(pages) < 2 {
		return pages
	}
	// balance: the narrowest width that still needs no more pages, so the
	// last page is not a lone word
	lo, hi := float64(width)*widthMargin/float64(len(pages)), float64(width)*widthMargin
	for i := 0; i < 12; i++ {
		mid := (lo + hi) / 2
		if len(pageGreedy(s, mid, size)) <= len(pages) {
			hi = mid
		} else {
			lo = mid
		}
	}
	return pageGreedy(s, hi, size)
}

// pageGreedy fills pages up to limit px, word by word.
func pageGreedy(s string, limit float64, size int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	render := func(open []string, ws []string) (string, []string) {
		body := strings.Join(open, "") + strings.Join(ws, " ")
		end := bracketState(nil, body)
		return body + closers(end), end
	}
	if txt, _ := render(nil, words); textWidth(txt, size) <= limit {
		return []string{txt}
	}
	var pages []string
	var open []string
	var cur []string
	for _, w := range words {
		if len(cur) == 0 {
			cur = []string{w}
			continue
		}
		cand, _ := render(open, append(append([]string(nil), cur...), w))
		if textWidth(cand, size) <= limit {
			cur = append(cur, w)
			continue
		}
		txt, end := render(open, cur)
		pages = append(pages, txt)
		open, cur = end, []string{w}
	}
	if len(cur) > 0 {
		txt, _ := render(open, cur)
		pages = append(pages, txt)
	}
	return pages
}

// withRef adds a verse reference after the pages: on the last page when it
// fits, else as a page of its own.
func withRef(pages []string, ref string, width, size int) []string {
	if ref == "" || len(pages) == 0 {
		return pages
	}
	last := pages[len(pages)-1] + " · " + ref
	if textWidth(last, size) <= float64(width)*widthMargin {
		return append(pages[:len(pages)-1:len(pages)-1], last)
	}
	return append(pages, ref)
}

// pageLabel is «١/٢» (Arabic-Indic digits) or "1/2".
func pageLabel(i, n int, arabicDigits bool) string {
	s := strconv.Itoa(i) + "/" + strconv.Itoa(n)
	if arabicDigits {
		s = toArabicDigits(s)
	}
	return s
}

// pageSeconds is how long a page shows: the base for a short page, up to
// twice it for a full one.
func pageSeconds(page string, base float64, width, size int) float64 {
	full := float64(width) * widthMargin
	if full <= 0 {
		return base
	}
	f := 0.75 + 0.75*textWidth(page, size)/full
	return base * math.Max(1, math.Min(2, f))
}

// StripCmd is one timed text change: from At (seconds into the block) the
// drawtext instance Target shows Text.
type StripCmd struct {
	At     float64
	Target string
	Text   string
}

// PageSchedule cycles pages through a block of dur seconds from its start:
// page k shows for its pageSeconds, then the next, round and round; the
// indicator target (if any) shows «k/n» with it.
func PageSchedule(target, indicator string, pages []string, base, dur float64, width, size int, arabicDigits bool) []StripCmd {
	if len(pages) < 2 || dur <= 0 {
		return nil
	}
	var out []StripCmd
	t := 0.0
	const maxCmds = 40000
	for k := 0; t < dur && len(out) < maxCmds; k = (k + 1) % len(pages) {
		out = append(out, StripCmd{At: t, Target: target, Text: pages[k]})
		if indicator != "" {
			out = append(out, StripCmd{At: t, Target: indicator, Text: pageLabel(k+1, len(pages), arabicDigits)})
		}
		t += pageSeconds(pages[k], base, width, size)
	}
	return out
}

// clockCmds turns the clock's steps into commands for its two drawtexts.
func clockCmds(steps []ClockStep) []StripCmd {
	out := make([]StripCmd, 0, 2*len(steps))
	for _, s := range steps {
		out = append(out, StripCmd{At: s.At, Target: "drawtext@clkl", Text: s.Label},
			StripCmd{At: s.At, Target: "drawtext@clkt", Text: s.Time})
	}
	return out
}

// CmdScript is the sendcmd file for all the strip's timed changes: one
// line per instant, every change at that instant on it.
func CmdScript(cmds []StripCmd) string {
	sorted := append([]StripCmd(nil), cmds...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At < sorted[j].At })
	var b strings.Builder
	for i := 0; i < len(sorted); {
		at := strconv.FormatFloat(sorted[i].At, 'f', 3, 64)
		var parts []string
		j := i
		for ; j < len(sorted) && strconv.FormatFloat(sorted[j].At, 'f', 3, 64) == at; j++ {
			parts = append(parts, fmt.Sprintf("%s reinit 'text=%s'", sorted[j].Target, escapeReinit(sorted[j].Text)))
		}
		b.WriteString(at + " " + strings.Join(parts, ", ") + ";\n")
		i = j
	}
	return b.String()
}

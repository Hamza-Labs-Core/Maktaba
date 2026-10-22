package search

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// highlightSnippet wraps every occurrence of the query's terms in s with
// “<mark>...</mark>“ and trims to about maxLen runes around the first match,
// with an ellipsis on each cut side. Matching is case-insensitive.
//
// A query of several words marks each word (the full-text search matches the
// words, not the phrase); a one-letter word of a longer query is not marked on
// its own (an Arabic "و" would mark every other word).
//
// Everything works on runes, folded one rune to one rune (unicode.ToLower), so
// the folded text has exactly the original's runes: a match found in the folded
// text wraps exactly the original-case runes, whatever the fold does to the byte
// length (Turkish İ, uppercase ẞ), and the window never cuts a multi-byte rune
// (Arabic; Story 5.4 "Arabic grapheme-aware" highlighting).
func highlightSnippet(s, q string, maxLen int) string {
	if s == "" || strings.TrimSpace(q) == "" {
		return s
	}
	runes := []rune(s)
	folded := foldRunes(runes)
	terms := highlightTerms(q)

	// The matches, leftmost and then longest first, never overlapping.
	type span struct{ start, end int }
	var spans []span
	for i := 0; i < len(folded); {
		best := 0
		for _, t := range terms {
			if len(t) > best && hasPrefixRunes(folded[i:], t) {
				best = len(t)
			}
		}
		if best == 0 {
			i++
			continue
		}
		spans = append(spans, span{i, i + best})
		i += best
	}
	if len(spans) == 0 {
		if len(runes) > maxLen {
			return string(runes[:maxLen]) + "…"
		}
		return s
	}

	first := spans[0]
	start := first.start - 60
	if start < 0 {
		start = 0
	}
	end := first.end + maxLen - (first.start - start)
	if end > len(runes) {
		end = len(runes)
	}

	var out strings.Builder
	if start > 0 {
		out.WriteString("…")
	}
	at := start
	for _, sp := range spans {
		if sp.start < start {
			continue
		}
		if sp.start >= end {
			break
		}
		e := sp.end
		if e > end {
			e = end
		}
		out.WriteString(string(runes[at:sp.start]))
		out.WriteString("<mark>")
		out.WriteString(string(runes[sp.start:e]))
		out.WriteString("</mark>")
		at = e
	}
	out.WriteString(string(runes[at:end]))
	if end < len(runes) {
		out.WriteString("…")
	}
	return out.String()
}

// highlightTerms are the folded words of q to mark.
func highlightTerms(q string) [][]rune {
	words := strings.Fields(q)
	out := make([][]rune, 0, len(words))
	seen := map[string]bool{}
	for _, w := range words {
		if len(words) > 1 && utf8.RuneCountInString(w) < 2 {
			continue
		}
		f := foldRunes([]rune(w))
		if k := string(f); !seen[k] {
			seen[k] = true
			out = append(out, f)
		}
	}
	return out
}

func foldRunes(rs []rune) []rune {
	out := make([]rune, len(rs))
	for i, r := range rs {
		out[i] = unicode.ToLower(r)
	}
	return out
}

func hasPrefixRunes(s, prefix []rune) bool {
	if len(prefix) == 0 || len(prefix) > len(s) {
		return false
	}
	for i, r := range prefix {
		if s[i] != r {
			return false
		}
	}
	return true
}

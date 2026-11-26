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
// Folded matches are translated back to original rune boundaries before slicing.
func highlightSnippet(s, q string, maxLen int) string {
	return highlightSnippetWith(s, q, nil, maxLen)
}

// highlightSnippetWith is highlightSnippet that also marks words, the line's own words
// the query matched another way (story 49.7: a spelling variant, another form of the
// word), as they are written in s. A word's leading and trailing punctuation is not
// part of it.
func highlightSnippetWith(s, q string, words []string, maxLen int) string {
	if s == "" || strings.TrimSpace(q) == "" {
		return s
	}
	runes := []rune(s)
	folded, offsets := foldHighlightRunes(runes)
	terms := highlightTerms(q)
	for _, w := range words {
		w = strings.TrimFunc(w, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
		if w == "" {
			continue
		}
		term, _ := foldHighlightRunes([]rune(w))
		if len(term) >= 2 {
			terms = append(terms, term)
		}
	}

	// The matches, leftmost and then longest first, never overlapping.
	type span struct{ start, end int }
	var spans []span
	for i := 0; i < len(folded); {
		if offsets[i] < 0 {
			i++
			continue
		}
		best := 0
		for _, t := range terms {
			if len(t) > best && hasPrefixRunes(folded[i:], t) && offsets[i+len(t)] >= 0 {
				best = len(t)
			}
		}
		if best == 0 {
			i++
			continue
		}
		spans = append(spans, span{offsets[i], offsets[i+best]})
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
		f, _ := foldHighlightRunes([]rune(w))
		if k := string(f); !seen[k] {
			seen[k] = true
			out = append(out, f)
		}
	}
	return out
}

// offsets maps folded rune boundaries to original rune boundaries. A boundary
// inside a replacement such as æ → ae cannot start or end a match.
func foldHighlightRunes(rs []rune) (folded []rune, offsets []int) {
	folded = make([]rune, 0, len(rs))
	offsets = make([]int, 0, len(rs)+1)
	for start := 0; start < len(rs); {
		end := start + 1
		offsets = append(offsets, start)
		r := rs[start]
		if unicode.Is(unicode.Latin, r) {
			for end < len(rs) && unicode.Is(unicode.Mn, rs[end]) {
				end++
			}
			if r >= utf8.RuneSelf || end > start+1 {
				replacement := []rune(foldAccents(string(rs[start:end])))
				folded = append(folded, replacement...)
				for range replacement[1:] {
					offsets = append(offsets, -1)
				}
				start = end
				continue
			}
		}
		folded = append(folded, unicode.ToLower(r))
		start = end
	}
	return folded, append(offsets, len(rs))
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

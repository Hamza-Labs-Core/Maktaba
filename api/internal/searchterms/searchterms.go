// Package searchterms is keyword search's terms dictionary in Go (Epic 49 story 49.8): a
// word's key, as search_terms holds it (slot 0235), and the words of a text.
//
// A word's key is the word folded as maktaba_normalize() folds it (textnorm), in lower case,
// with Latin accents removed. Its shown form is the word as written, harakat and tatweel
// removed, in lower case. The pipeline's search/terms.py, which fills the dictionary, is the
// same: both read shared/testdata/searchterms.json in a test.
//
// A word is a run of letters and numbers (unicode.IsLetter or unicode.IsNumber; Python's
// str.isalnum) of MinLen to MaxLen runes, not digits only.
package searchterms

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/textnorm"
)

const (
	// MinLen is the shortest word the dictionary holds, in runes.
	MinLen = 2
	// MaxLen is the longest.
	MaxLen = 40
)

// marks are what a shown form drops: the harakat, the superscript alef and tatweel.
func mark(r rune) bool { return (r >= 0x064B && r <= 0x0652) || r == 0x0670 || r == 0x0640 }

func stripAccents(s string) string {
	d := norm.NFD.String(s)
	var b strings.Builder
	b.Grow(len(d))
	for _, r := range d {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	return norm.NFC.String(b.String())
}

// Key is the word's key: folded, lower case, without Latin accents.
func Key(word string) string {
	return stripAccents(strings.ToLower(textnorm.Normalize(norm.NFC.String(word))))
}

func digits(w string) bool {
	for _, r := range w {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// Words are the words of s as written (harakat and tatweel removed), in order.
func Words(s string) []string {
	s = strings.Map(func(r rune) rune {
		if mark(r) {
			return -1
		}
		return r
	}, norm.NFC.String(s))
	var out []string
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		w := s[start:end]
		start = -1
		if n := utf8.RuneCountInString(w); n >= MinLen && n <= MaxLen && !digits(w) {
			out = append(out, w)
		}
	}
	for i, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		flush(i)
	}
	flush(len(s))
	return out
}

// DocTerms is each key of the texts' words with its most written shown form (ties: the
// form seen first).
func DocTerms(texts ...string) map[string]string {
	type tally struct {
		n     map[string]int
		order []string
	}
	seen := map[string]*tally{}
	var keys []string
	for _, t := range texts {
		for _, w := range Words(t) {
			k := Key(w)
			if n := utf8.RuneCountInString(k); n < MinLen || n > MaxLen {
				continue
			}
			c := seen[k]
			if c == nil {
				c = &tally{n: map[string]int{}}
				seen[k] = c
				keys = append(keys, k)
			}
			lw := strings.ToLower(w)
			if c.n[lw] == 0 {
				c.order = append(c.order, lw)
			}
			c.n[lw]++
		}
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		c := seen[k]
		best := c.order[0]
		for _, w := range c.order[1:] {
			if c.n[w] > c.n[best] {
				best = w
			}
		}
		out[k] = best
	}
	return out
}

// SpeakerKeys are each named speaker (one a line) by each word start of the name: (key,
// name) pairs, sorted, as search_speakers holds them (the pipeline's speaker_keys), so
// الشيخ أحمد is found by الشي and by أحم.
func SpeakerKeys(names string) [][2]string {
	seen := map[[2]string]bool{}
	var out [][2]string
	for _, line := range strings.Split(names, "\n") {
		parts := strings.Fields(line)
		name := strings.Join(parts, " ")
		for i := range parts {
			k := Key(strings.Join(parts[i:], " "))
			if k == "" {
				continue
			}
			if utf8.RuneCountInString(k) > 200 {
				k = string([]rune(k)[:200])
			}
			if p := [2]string{k, name}; !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

// QueryKeys are the keys of the query's words, in order, once each.
func QueryKeys(q string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range Words(q) {
		k := Key(w)
		if n := utf8.RuneCountInString(k); n >= MinLen && n <= MaxLen && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// Arabic is whether the key holds a letter outside ASCII (did-you-mean compares a key with
// keys of the same kind: search_terms.trgm spells Arabic letters in ASCII).
func Arabic(key string) bool {
	for _, r := range key {
		if r >= utf8.RuneSelf {
			return true
		}
	}
	return false
}

// Similarity is pg_trgm's similarity of two keys (the share of their trigrams in common,
// each word padded with two spaces before and one after), on runes: SQLite has no pg_trgm,
// and Postgres's in the C locale counts no Arabic letter.
func Similarity(a, b string) float64 {
	ta, tb := trigrams(a), trigrams(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	common := 0
	for t := range ta {
		if tb[t] {
			common++
		}
	}
	return float64(common) / float64(len(ta)+len(tb)-common)
}

func trigrams(s string) map[string]bool {
	r := []rune("  " + s + " ")
	out := map[string]bool{}
	for i := 0; i+3 <= len(r); i++ {
		out[string(r[i:i+3])] = true
	}
	return out
}

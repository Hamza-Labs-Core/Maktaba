package libraries

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// sameName is a typed confirmation of a library's name, read as a person means it: the
// same letters whatever their Unicode spelling (an Arabic name typed on another
// keyboard), spaces at the ends or between words, or the invisible direction marks a
// right-to-left name picks up when copied. Letter case still counts, and a different
// name is still refused.
func sameName(typed, name string) bool {
	return nameKey(typed) == nameKey(name)
}

func nameKey(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\u200e', '\u200f', '\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
			'\u2066', '\u2067', '\u2068', '\u2069', '\ufeff':
			return -1
		}
		return r
	}, norm.NFC.String(s))
	return strings.Join(strings.Fields(s), " ")
}

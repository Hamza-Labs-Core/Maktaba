package relay

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"
)

// maxParamValue bounds the value of a secret query parameter the scrubber
// removes (a Plex token is about 20 characters).
const maxParamValue = 512

// Scrubber removes a secret from a streamed answer: every
// "<param>=<value>" (as in Plex's thumb and stream URLs) and every literal
// or URL-encoded copy of the secret itself. It holds back the tail of what
// it was fed so a match split across two chunks is still found.
type Scrubber struct {
	re *regexp.Regexp
	// needles: one of them (the parameter's name lower-cased, or a form of
	// the secret) is in every match; a buffer with none skips the regexp
	lower   []byte
	needles [][]byte
	hold    int
	carry   []byte
	// Hits counts what was removed.
	Hits int
}

// NewScrubber scrubs param (case-insensitive) and secret ("" for none).
func NewScrubber(param, secret string) *Scrubber {
	parts := []string{}
	if param != "" {
		// an optional separator, the name, "=" or its escape, the value
		parts = append(parts, `[?&;]?(?i:`+regexp.QuoteMeta(param)+`)(?:=|%3[dD])[^&"'\s<>\\]{0,512}`)
	}
	hold := 1 + len(param) + 3 + maxParamValue
	var lower []byte
	var needles [][]byte
	if param != "" {
		lower = []byte(strings.ToLower(param))
	}
	// a secret too short to be one would match ordinary text; the
	// parameter rule still removes it from URLs
	if len(secret) >= 8 {
		seen := map[string]bool{}
		for _, v := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret)} {
			if v != "" && !seen[v] {
				seen[v] = true
				parts = append(parts, regexp.QuoteMeta(v))
				needles = append(needles, []byte(v))
			}
		}
		if n := 3 * len(secret); n > hold {
			hold = n
		}
	}
	s := &Scrubber{hold: hold + 8, lower: lower, needles: needles}
	if len(parts) > 0 {
		s.re = regexp.MustCompile(strings.Join(parts, "|"))
	}
	return s
}

func (s *Scrubber) mayMatch(b []byte) bool {
	if s.re == nil {
		return false
	}
	for _, n := range s.needles {
		if bytes.Contains(b, n) {
			return true
		}
	}
	return s.lower != nil && bytes.Contains(bytes.ToLower(b), s.lower)
}

func (s *Scrubber) replace(b []byte) []byte {
	if !s.mayMatch(b) {
		return b
	}
	return s.re.ReplaceAllFunc(b, func(m []byte) []byte {
		s.Hits++
		if len(m) > 0 && m[0] == '?' {
			return []byte{'?'}
		}
		return nil
	})
}

// Feed takes the next chunk and returns what can be passed on now.
func (s *Scrubber) Feed(p []byte) []byte {
	buf := append(s.carry, p...)
	if len(buf) <= s.hold {
		s.carry = buf
		return nil
	}
	cut := len(buf) - s.hold
	if s.mayMatch(buf) {
		for _, loc := range s.re.FindAllIndex(buf, -1) {
			if loc[0] < cut && loc[1] > cut {
				cut = loc[0]
				break
			}
		}
	}
	out := s.replace(append([]byte(nil), buf[:cut]...))
	s.carry = append([]byte(nil), buf[cut:]...)
	return out
}

// Flush returns the rest at the end of the answer.
func (s *Scrubber) Flush() []byte {
	out := s.replace(s.carry)
	s.carry = nil
	return out
}

// ScrubString removes the secret from one string (a header, an error).
func ScrubString(param, secret, v string) string {
	sc := NewScrubber(param, secret)
	return string(append(sc.Feed([]byte(v)), sc.Flush()...))
}

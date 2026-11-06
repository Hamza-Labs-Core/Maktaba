package log

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// redactRulesJSON is shared/redact/rules.json (Epic 53 story 53.01), the one
// redaction list every Maktaba logger loads. tools/log-schema/sync.py writes this
// copy; the repo job fails when it differs from the original.
//
//go:embed redact_rules.json
var redactRulesJSON []byte

// RedactRules is the parsed shared list.
type RedactRules struct {
	KeyMask          string      `json:"key_mask"`
	ValueMask        string      `json:"value_mask"`
	Keys             []string    `json:"keys"`
	KeySuffixes      []string    `json:"key_suffixes"`
	KeyPartsIfString []string    `json:"key_parts_string_only"`
	Values           []valueRule `json:"values"`
	Cards            struct {
		Pattern     string `json:"pattern"`
		FirstDigits string `json:"first_digits"`
		MinDigits   int    `json:"min_digits"`
		MaxDigits   int    `json:"max_digits"`
		Replace     string `json:"replace"`
	} `json:"cards"`
	IPv4 struct {
		Pattern        string   `json:"pattern"`
		KeepFirstOctet []string `json:"keep_first_octet"`
	} `json:"ipv4"`
	IPv6 struct {
		Pattern string `json:"pattern"`
	} `json:"ipv6"`

	values []compiledRule
	cards  *regexp.Regexp
	ipv4   *regexp.Regexp
	ipv6   *regexp.Regexp
}

type valueRule struct {
	Name       string   `json:"name"`
	Pattern    string   `json:"pattern"`
	IgnoreCase bool     `json:"ignore_case"`
	Replace    string   `json:"replace"`
	Needs      []string `json:"needs"`
}

type compiledRule struct {
	re         *regexp.Regexp
	replace    string
	needs      []string
	ignoreCase bool
}

// applies reports whether text (and its lower-cased form) holds one of the
// rule's needs: the cheap check that keeps most values off the regexes.
func (c compiledRule) applies(s, low string) bool {
	if len(c.needs) == 0 {
		return true
	}
	for _, n := range c.needs {
		if c.ignoreCase {
			if strings.Contains(low, n) {
				return true
			}
		} else if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// sharedRules is the embedded list, parsed once.
var sharedRules = mustRules(redactRulesJSON)

// SharedRedactRules returns the embedded shared list (the API's ingest of client
// lines scrubs with the same one).
func SharedRedactRules() *RedactRules { return sharedRules }

func mustRules(raw []byte) *RedactRules {
	r, err := ParseRedactRules(raw)
	if err != nil {
		panic("shared/redact/rules.json: " + err.Error())
	}
	return r
}

// ParseRedactRules parses and compiles a rules file.
func ParseRedactRules(raw []byte) (*RedactRules, error) {
	var r RedactRules
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	for _, v := range r.Values {
		p := v.Pattern
		if v.IgnoreCase {
			p = "(?i)" + p
		}
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, err
		}
		needs := v.Needs
		if v.IgnoreCase {
			needs = make([]string, len(v.Needs))
			for i, n := range v.Needs {
				needs[i] = strings.ToLower(n)
			}
		}
		r.values = append(r.values, compiledRule{re: re, replace: v.Replace, needs: needs, ignoreCase: v.IgnoreCase})
	}
	var err error
	if r.cards, err = regexp.Compile(r.Cards.Pattern); err != nil {
		return nil, err
	}
	if r.ipv4, err = regexp.Compile(r.IPv4.Pattern); err != nil {
		return nil, err
	}
	if r.ipv6, err = regexp.Compile(r.IPv6.Pattern); err != nil {
		return nil, err
	}
	for i := range r.Keys {
		r.Keys[i] = strings.ToLower(r.Keys[i])
	}
	return &r, nil
}

// KeySensitive reports whether a field named key is masked whole (KeyMask).
// isString says whether its value is a string: the "parts" rule (a key that
// merely contains "token") masks strings only, never a count like max_tokens.
func (r *RedactRules) KeySensitive(key string, isString bool) bool {
	low := strings.ToLower(key)
	for _, k := range r.Keys {
		if low == k {
			return true
		}
	}
	for _, s := range r.KeySuffixes {
		if strings.HasSuffix(low, s) {
			return true
		}
	}
	if isString {
		for _, p := range r.KeyPartsIfString {
			if strings.Contains(low, p) {
				return true
			}
		}
	}
	return false
}

// Scrub applies the value rules to text: JWTs, bearer tokens, URL passwords,
// credentials in query strings, emails, home directories, card numbers and the
// host part of IP addresses.
func (r *RedactRules) Scrub(s string) string {
	if s == "" {
		return s
	}
	out := s
	low := strings.ToLower(s)
	for _, v := range r.values {
		if v.applies(out, low) {
			if n := v.re.ReplaceAllString(out, v.replace); n != out {
				out = n
				low = strings.ToLower(out)
			}
		}
	}
	digits, dots, colons := 0, 0, 0
	for i := 0; i < len(out); i++ {
		switch c := out[i]; {
		case c >= '0' && c <= '9':
			digits++
		case c == '.':
			dots++
		case c == ':':
			colons++
		}
	}
	if digits >= r.Cards.MinDigits {
		out = replaceChecked(out, r.cards, r.card)
	}
	if dots >= 3 && digits >= 4 {
		out = replaceChecked(out, r.ipv4, r.v4)
	}
	if colons >= 3 {
		out = replaceChecked(out, r.ipv6, r.v6)
	}
	return out
}

// replaceChecked replaces each match of re by fn(match, before, after); fn
// returns the match itself to keep it.
func replaceChecked(s string, re *regexp.Regexp, fn func(m string, before, after byte) string) string {
	locs := re.FindAllStringIndex(s, -1)
	if locs == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for _, l := range locs {
		var before, after byte
		if l[0] > 0 {
			before = s[l[0]-1]
		}
		if l[1] < len(s) {
			after = s[l[1]]
		}
		b.WriteString(s[last:l[0]])
		b.WriteString(fn(s[l[0]:l[1]], before, after))
		last = l[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func (r *RedactRules) card(m string, before, after byte) string {
	if isDigit(before) || isDigit(after) {
		return m
	}
	digits := make([]byte, 0, len(m))
	for i := 0; i < len(m); i++ {
		if isDigit(m[i]) {
			digits = append(digits, m[i])
		}
	}
	if len(digits) < r.Cards.MinDigits || len(digits) > r.Cards.MaxDigits ||
		!strings.ContainsRune(r.Cards.FirstDigits, rune(digits[0])) || !luhn(digits) {
		return m
	}
	return r.Cards.Replace
}

func luhn(d []byte) bool {
	sum := 0
	double := false
	for i := len(d) - 1; i >= 0; i-- {
		n := int(d[i] - '0')
		if double {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		double = !double
	}
	return sum%10 == 0
}

func (r *RedactRules) v4(m string, before, after byte) string {
	// part of a longer number (a version like 1.2.3.4.5 is cut from its start only)
	if isDigit(before) || before == '.' || isDigit(after) {
		return m
	}
	parts := strings.Split(m, ".")
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n > 255 {
			return m
		}
	}
	for _, k := range r.IPv4.KeepFirstOctet {
		if parts[0] == k {
			return m
		}
	}
	return parts[0] + "." + parts[1] + "." + parts[2] + ".0"
}

func (r *RedactRules) v6(m string, before, after byte) string {
	if isHex(before) || before == ':' || isHex(after) || after == ':' {
		return m
	}
	groups, ok := ipv6Groups(m)
	if !ok {
		return m
	}
	loop := true
	for i, g := range groups {
		n, _ := strconv.ParseUint(g, 16, 16)
		if (i < 7 && n != 0) || (i == 7 && n != 1) {
			loop = false
		}
	}
	if loop {
		return m
	}
	return groups[0] + ":" + groups[1] + ":" + groups[2] + "::"
}

// ipv6Groups expands an address to its eight groups; ok is false when m does not
// read as one.
func ipv6Groups(m string) ([]string, bool) {
	if strings.Contains(m, ":::") || strings.Count(m, "::") > 1 {
		return nil, false
	}
	var groups []string
	if i := strings.Index(m, "::"); i >= 0 {
		var left, right []string
		if l := m[:i]; l != "" {
			left = strings.Split(l, ":")
		}
		if rt := m[i+2:]; rt != "" {
			right = strings.Split(rt, ":")
		}
		if len(left)+len(right) > 7 {
			return nil, false
		}
		groups = append(groups, left...)
		for k := 0; k < 8-len(left)-len(right); k++ {
			groups = append(groups, "0")
		}
		groups = append(groups, right...)
	} else {
		groups = strings.Split(m, ":")
		if len(groups) != 8 {
			return nil, false
		}
	}
	for _, g := range groups {
		if g == "" {
			return nil, false
		}
	}
	return groups, true
}

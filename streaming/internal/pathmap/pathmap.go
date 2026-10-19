// Package pathmap rewrites library paths stored in the database (e.g.
// "Y:/Shows/a.mp4", written by the machine that scanned the library)
// into paths this streaming host can open. The mapping comes from
// MAKTABA_STREAM_PATH_MAP as "from=>to;from2=>to2"; the first (longest)
// matching prefix wins. Matching is case-insensitive and treats "\" and
// "/" as the same separator, so "Y:\Shows" matches "y:/shows/a.mp4".
package pathmap

import (
	"os"
	"sort"
	"strings"
)

// EnvVar is the environment variable the default map is read from.
const EnvVar = "MAKTABA_STREAM_PATH_MAP"

// Rule is one from=>to prefix rewrite.
type Rule struct {
	From string
	To   string
}

// Map is an ordered set of prefix rewrites (longest From first).
type Map struct {
	rules []Rule
}

// Parse builds a Map from "from=>to;from2=>to2". Blank or malformed
// entries (no "=>", empty from) are ignored.
func Parse(spec string) Map {
	var rules []Rule
	for _, part := range strings.Split(spec, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		from, to, ok := strings.Cut(part, "=>")
		if !ok {
			continue
		}
		from = strings.TrimSpace(from)
		to = strings.TrimSpace(to)
		if from == "" {
			continue
		}
		rules = append(rules, Rule{From: from, To: to})
	}
	sort.SliceStable(rules, func(i, j int) bool { return len(rules[i].From) > len(rules[j].From) })
	return Map{rules: rules}
}

// FromEnv parses MAKTABA_STREAM_PATH_MAP.
func FromEnv() Map { return Parse(os.Getenv(EnvVar)) }

// Rules returns the parsed rules (longest prefix first).
func (m Map) Rules() []Rule { return append([]Rule(nil), m.rules...) }

// Apply rewrites p with the first matching rule; unmatched paths are
// returned unchanged. URLs (scheme://) are never rewritten.
func (m Map) Apply(p string) string {
	if p == "" || strings.Contains(p, "://") {
		return p
	}
	norm := normalise(p)
	for _, r := range m.rules {
		from := normalise(r.From)
		if !strings.HasPrefix(strings.ToLower(norm), strings.ToLower(from)) {
			continue
		}
		rest := norm[len(from):]
		// Only match on a path boundary ("Y:/Show" must not match
		// "Y:/Shows/..."), unless the rule itself ends with a separator.
		if rest != "" && !strings.HasSuffix(from, "/") && !strings.HasPrefix(rest, "/") {
			continue
		}
		to := strings.TrimRight(normalise(r.To), "/")
		if rest != "" && !strings.HasPrefix(rest, "/") {
			rest = "/" + rest
		}
		return to + rest
	}
	return p
}

func normalise(p string) string { return strings.ReplaceAll(p, `\`, "/") }

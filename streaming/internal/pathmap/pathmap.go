// Package pathmap rewrites library paths stored in the database (e.g.
// "Y:/Shows/a.mp4", written by the machine that scanned the library)
// into paths this streaming host can open. The mapping comes from
// MAKTABA_STREAM_PATH_MAP as "from=>to;from2=>to2"; the first (longest)
// matching prefix wins. Matching is case-insensitive and treats "\" and
// "/" as the same separator, so "Y:\Shows" matches "y:/shows/a.mp4".
package pathmap

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
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

// FromJSON builds a Map from a JSON object {db_prefix: local_prefix}, the form the
// cluster keeps a node's path map in (cluster_nodes.path_map, Epic 31). Empty or null
// is the empty map.
func FromJSON(raw []byte) (Map, error) {
	var m map[string]string
	if len(raw) == 0 || string(raw) == "null" {
		return Map{}, nil
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return Map{}, err
	}
	var rules []Rule
	for from, to := range m {
		if from = strings.TrimSpace(from); from != "" {
			rules = append(rules, Rule{From: from, To: strings.TrimSpace(to)})
		}
	}
	sort.SliceStable(rules, func(i, j int) bool {
		if len(rules[i].From) != len(rules[j].From) {
			return len(rules[i].From) > len(rules[j].From)
		}
		return rules[i].From < rules[j].From
	})
	return Map{rules: rules}, nil
}

// Empty reports whether the map has no rules.
func (m Map) Empty() bool { return len(m.rules) == 0 }

// FromEnv parses MAKTABA_STREAM_PATH_MAP.
func FromEnv() Map { return Parse(os.Getenv(EnvVar)) }

// Rules returns the parsed rules (longest prefix first).
func (m Map) Rules() []Rule { return append([]Rule(nil), m.rules...) }

// Apply rewrites p with the first matching rule; unmatched paths are
// returned unchanged. URLs (scheme://) are never rewritten, except the
// canonical media paths of story 31.3: "smb://nas/share/..." and
// "nfs://..." go through the rules and then the process default (the
// machine's share map, see SetDefault), "file:///abs" is the path itself.
func (m Map) Apply(p string) string {
	if p == "" {
		return p
	}
	if strings.Contains(p, "://") {
		if !IsCanonical(p) {
			return p
		}
		if local, ok := fileURL(p); ok {
			return local
		}
		if out, ok := m.apply(p); ok {
			return out
		}
		if d := global.Load(); d != nil {
			if out, ok := d.apply(p); ok {
				return out
			}
		}
		return p
	}
	if out, ok := m.apply(p); ok {
		return out
	}
	return p
}

func (m Map) apply(p string) (string, bool) {
	norm := normalise(p)
	for _, r := range m.rules {
		from := normalise(r.From)
		if strings.HasSuffix(from, "/") && strings.EqualFold(norm+"/", from) {
			// a share root stored without its slash (a library root)
			if driveRoot(r.To) || strings.TrimRight(normalise(r.To), "/") == "" {
				return normalise(r.To), true
			}
			return strings.TrimRight(normalise(r.To), "/"), true
		}
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
		if to == "" && rest == "" {
			return normalise(r.To), true
		}
		if rest == "" && driveRoot(r.To) {
			return normalise(r.To), true
		}
		return to + rest, true
	}
	return p, false
}

func normalise(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// --- story 31.3: canonical share paths --------------------------------

var global atomic.Pointer[Map]

// SetDefault installs the machine's share map (canonical prefix -> local
// mount), consulted by every Map for a canonical path its own rules do not
// cover; main refreshes it from the home's storage report.
func SetDefault(m Map) { global.Store(&m) }

// Default is the installed share map (empty before SetDefault).
func Default() Map {
	if d := global.Load(); d != nil {
		return *d
	}
	return Map{}
}

// Resolve maps a stored media path to this machine's path with the
// default map (canonical paths; legacy paths are returned unchanged).
func Resolve(p string) string { return Default().Apply(p) }

var canonRE = regexp.MustCompile(`(?i)^(smb|nfs)://[^/]+/[^/]+|^(?i)file:///`)

// IsCanonical reports a story 31.3 canonical path (smb://, nfs://, file:///).
func IsCanonical(p string) bool { return canonRE.MatchString(p) }

var driveRE = regexp.MustCompile(`^[A-Za-z]:[/\\]?$`)

func driveRoot(p string) bool { return driveRE.MatchString(p) }

// fileURL: "file:///C:/x" -> "C:/x", "file:///Users/x" -> "/Users/x".
func fileURL(p string) (string, bool) {
	if len(p) < 8 || !strings.EqualFold(p[:8], "file:///") {
		return "", false
	}
	rest := p[8:]
	if len(rest) >= 2 && rest[1] == ':' {
		return rest, true
	}
	return "/" + rest, true
}

// FromEntries builds a Map from {from: to} (the JSON form the pipeline
// reports: instance_identity.storage.path_map, cluster_nodes.path_map).
func FromEntries(entries map[string]string) Map {
	var b strings.Builder
	for from, to := range entries {
		if strings.ContainsAny(from, ";") || strings.Contains(from, "=>") || strings.ContainsAny(to, ";") {
			continue
		}
		b.WriteString(from + "=>" + to + ";")
	}
	return Parse(b.String())
}

// Merge returns m's rules plus other's rules whose From m does not have
// (case-insensitive): m wins on an equal prefix.
func (m Map) Merge(other Map) Map {
	have := map[string]bool{}
	rules := append([]Rule(nil), m.rules...)
	for _, r := range m.rules {
		have[strings.ToLower(normalise(r.From))] = true
	}
	for _, r := range other.rules {
		if !have[strings.ToLower(normalise(r.From))] {
			rules = append(rules, r)
		}
	}
	sort.SliceStable(rules, func(i, j int) bool { return len(rules[i].From) > len(rules[j].From) })
	return Map{rules: rules}
}

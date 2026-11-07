package log

import (
	"sort"
	"strconv"
	"strings"
)

// Export is the export-only pass (shared/redact/rules.json "export_only", story 53.16):
// what leaves the home for Maktaba, on top of the rules every line already had at home.
//
//   - a field named in remove_keys (title, file_name, …) is removed;
//   - a path under one of the home's library roots becomes library_mask ("<library>/…");
//   - an address is removed whole (ip_mask), where at home only its host part is;
//   - each person's user_h becomes user-1, user-2, … in the order met (one Export per
//     report, so the numbers say nothing across reports);
//   - the names it is given (the sender's, a library's people) are removed from every text;
//   - and every text goes through Scrub again.
//
// An Export is used by one goroutine.
type Export struct {
	rules  *RedactRules
	roots  []string
	names  []string
	remove map[string]bool
	users  map[string]string
}

// NameMask replaces a person's name in a text that leaves the home.
const NameMask = "[name]"

// NewExport is the pass for one report: the home's library roots and the names to remove.
func (r *RedactRules) NewExport(libraryRoots, names []string) *Export {
	e := &Export{rules: r, remove: map[string]bool{}, users: map[string]string{}}
	for _, k := range r.ExportOnly.RemoveKeys {
		e.remove[strings.ToLower(k)] = true
	}
	for _, root := range libraryRoots {
		root = strings.TrimRight(strings.TrimSpace(root), `/\`)
		if len(root) > 1 {
			e.roots = append(e.roots, root)
		}
	}
	// the longest first: a root inside another is replaced as itself
	sort.Slice(e.roots, func(i, j int) bool { return len(e.roots[i]) > len(e.roots[j]) })
	for _, n := range names {
		// a one- or two-letter name would cut words apart
		if n = strings.TrimSpace(n); len([]rune(n)) >= 3 {
			e.names = append(e.names, n)
		}
	}
	sort.Slice(e.names, func(i, j int) bool { return len(e.names[i]) > len(e.names[j]) })
	return e
}

// NewExport is the pass with the shared rules.
func NewExport(libraryRoots, names []string) *Export {
	return sharedRules.NewExport(libraryRoots, names)
}

// User is the per-report stand-in for a user hash ("" stays "").
func (e *Export) User(userH string) string {
	if userH == "" {
		return ""
	}
	if u, ok := e.users[userH]; ok {
		return u
	}
	u := "user-" + strconv.Itoa(len(e.users)+1)
	e.users[userH] = u
	return u
}

// Value is v (decoded JSON: maps, lists, strings, numbers) with the pass applied, in place
// where it can be.
func (e *Export) Value(v any) any {
	switch t := v.(type) {
	case string:
		return e.String(t)
	case map[string]any:
		for k, x := range t {
			low := strings.ToLower(k)
			if e.remove[low] {
				delete(t, k)
				continue
			}
			s, isStr := x.(string)
			switch {
			case isStr && (low == "user_h" || strings.HasSuffix(low, "_user_h")):
				t[k] = e.User(s)
			case e.rules.KeySensitive(k, isStr):
				t[k] = e.rules.KeyMask
			default:
				t[k] = e.Value(x)
			}
		}
		return t
	case []any:
		for i := range t {
			t[i] = e.Value(t[i])
		}
		return t
	}
	return v
}

// String is a text with the pass applied.
func (e *Export) String(s string) string {
	if s == "" {
		return s
	}
	out := e.rules.Scrub(s)
	for _, root := range e.roots {
		out = e.maskPaths(out, root)
	}
	for _, n := range e.names {
		out = replaceFold(out, n, NameMask)
	}
	mask := e.rules.ExportOnly.IPMask
	if mask == "" {
		mask = e.rules.ValueMask
	}
	out = replaceChecked(out, e.rules.ipv4, func(m string, before, after byte) string {
		if e.rules.isAddress4(m, before, after) {
			return mask
		}
		return m
	})
	out = replaceChecked(out, e.rules.ipv6, func(m string, before, after byte) string {
		if e.rules.isAddress6(m, before, after) {
			return mask
		}
		return m
	})
	return out
}

// maskPaths replaces each path under root (the root, a separator and what follows, spaces
// included, up to a comma, a semicolon, a quote or the line's end: a file name often holds
// spaces, and masking a few words too many is the safe side) with the library mask.
func (e *Export) maskPaths(s, root string) string {
	mask := e.rules.ExportOnly.LibraryMask
	var b strings.Builder
	for {
		i := strings.Index(s, root)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := i + len(root)
		if end < len(s) && s[end] != '/' && s[end] != '\\' {
			// another name that starts like the root (/media/videos2)
			b.WriteString(s[:end])
			s = s[end:]
			continue
		}
		for end < len(s) && !strings.ContainsRune("\n\r\t\",;'`<>|", rune(s[end])) {
			end++
		}
		b.WriteString(s[:i])
		b.WriteString(mask)
		s = s[end:]
	}
}

// isAddress4 is a match of the IPv4 pattern that is an address (not part of a longer number
// or version, and not one the rules keep, such as loopback).
func (r *RedactRules) isAddress4(m string, before, after byte) bool {
	if isDigit(before) || before == '.' || isDigit(after) {
		return false
	}
	parts := strings.Split(m, ".")
	for _, p := range parts {
		if n, err := strconv.Atoi(p); err != nil || n > 255 {
			return false
		}
	}
	for _, k := range r.IPv4.KeepFirstOctet {
		if parts[0] == k {
			return false
		}
	}
	return true
}

// isAddress6 is a match of the IPv6 pattern that is an address other than loopback.
func (r *RedactRules) isAddress6(m string, before, after byte) bool {
	if isHex(before) || before == ':' || isHex(after) || after == ':' {
		return false
	}
	groups, ok := ipv6Groups(m)
	if !ok {
		return false
	}
	for i, g := range groups {
		n, _ := strconv.ParseUint(g, 16, 16)
		if (i < 7 && n != 0) || (i == 7 && n != 1) {
			return true
		}
	}
	return false
}

// replaceFold replaces every occurrence of old in s, ignoring case, with repl.
func replaceFold(s, old, repl string) string {
	low, lold := strings.ToLower(s), strings.ToLower(old)
	if len(low) != len(s) || !strings.Contains(low, lold) {
		if len(low) != len(s) {
			// a case change that changes byte lengths: only the exact spelling
			return strings.ReplaceAll(s, old, repl)
		}
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(low, lold)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString(repl)
		s, low = s[i+len(old):], low[i+len(old):]
	}
}

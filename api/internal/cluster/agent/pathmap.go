package agent

import (
	"encoding/json"
	"sort"
	"strings"
)

// PathMap translates database paths (the scanner's form, e.g.
// "Y:/Channel/a.mp4") to this host's paths ("/Volumes/Y/Channel/a.mp4").
// It is the Go twin of the pipeline's engine/paths.py PathMap: the same
// text format (`Y:/=/Volumes/Y;O:/=/Volumes/O` or a JSON object), the same
// normalisation (forward slashes, a trailing "/", UNC `\\nas\share` →
// `//nas/share/`), case-insensitive matching, longest database prefix wins.
type PathMap struct {
	entries [][2]string // (db prefix, local prefix), normalised, sorted
}

// ParsePathMap parses MAKTABA_PATH_MAP. Empty text is an empty map.
func ParsePathMap(text string) (PathMap, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return PathMap{}, nil
	}
	var pairs [][2]string
	if strings.HasPrefix(text, "{") {
		var m map[string]string
		if err := json.Unmarshal([]byte(text), &m); err != nil {
			return PathMap{}, err
		}
		for k, v := range m {
			pairs = append(pairs, [2]string{k, v})
		}
	} else {
		for _, part := range strings.Split(text, ";") {
			a, b, ok := strings.Cut(part, "=")
			if !ok {
				continue
			}
			pairs = append(pairs, [2]string{strings.TrimSpace(a), strings.TrimSpace(b)})
		}
	}
	var out PathMap
	for _, p := range pairs {
		if p[0] == "" || p[1] == "" {
			continue
		}
		out.entries = append(out.entries, [2]string{normPrefix(p[0]), normPrefix(p[1])})
	}
	sort.Slice(out.entries, func(i, j int) bool {
		if out.entries[i][0] != out.entries[j][0] {
			return out.entries[i][0] < out.entries[j][0]
		}
		return out.entries[i][1] < out.entries[j][1]
	})
	return out, nil
}

func normPrefix(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

// Len is the number of entries.
func (m PathMap) Len() int { return len(m.entries) }

// ToLocal maps a database path to this host's path; unmapped paths are
// returned unchanged.
func (m PathMap) ToLocal(dbPath string) string {
	p := strings.ReplaceAll(dbPath, `\`, "/")
	lp := strings.ToLower(p)
	best := -1
	for i, e := range m.entries {
		if strings.HasPrefix(lp, strings.ToLower(e[0])) && (best < 0 || len(e[0]) > len(m.entries[best][0])) {
			best = i
		}
	}
	if best < 0 {
		return dbPath
	}
	return m.entries[best][1] + p[len(m.entries[best][0]):]
}

// ToDB maps a local path back to the database's form; unmapped paths come
// back with forward slashes.
func (m PathMap) ToDB(localPath string) string {
	p := strings.ReplaceAll(localPath, `\`, "/")
	lp := strings.ToLower(p)
	best := -1
	for i, e := range m.entries {
		if strings.HasPrefix(lp, strings.ToLower(e[1])) && (best < 0 || len(e[1]) > len(m.entries[best][1])) {
			best = i
		}
	}
	if best < 0 {
		return p
	}
	return m.entries[best][0] + p[len(m.entries[best][1]):]
}

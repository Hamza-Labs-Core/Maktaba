// Package sharepath resolves the canonical media paths of story 31.3
// (smb://<server>/<share>/<path>, nfs://..., file:///...) on the home.
//
// The pipeline on the home works out which share each of its mounts is
// and reports the result as instance_identity.storage.path_map
// ({"smb://nas/Originals/": "/Volumes/Originals/"}); the API installs that
// map (Start) and uses it wherever it touches a stored media path: the
// playable check, a library root an admin enters (stored canonical when it
// is on a share, checked through its local mount).
package sharepath

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Entry is one canonical prefix and this machine's mount of it.
type Entry struct {
	Canonical string // "smb://nas/Originals/"
	Local     string // "/Volumes/Originals/"
}

// Map is a set of entries, longest canonical prefix first.
type Map struct{ entries []Entry }

var canonRE = regexp.MustCompile(`(?i)^(?:(?:smb|nfs)://[^/]+/[^/]+|file:///)`)

// IsCanonical reports a canonical path.
func IsCanonical(p string) bool { return canonRE.MatchString(p) }

func slash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

func dir(p string) string {
	p = slash(p)
	if strings.HasSuffix(p, "/") {
		return p
	}
	return p + "/"
}

// FromEntries builds a Map from {canonical prefix: local prefix}.
func FromEntries(m map[string]string) Map {
	out := Map{}
	for c, l := range m {
		if !IsCanonical(strings.TrimRight(c, "/")) || l == "" {
			continue
		}
		out.entries = append(out.entries, Entry{Canonical: dir(c), Local: dir(l)})
	}
	sort.Slice(out.entries, func(i, j int) bool {
		return len(out.entries[i].Canonical) > len(out.entries[j].Canonical)
	})
	return out
}

// Len is the number of entries.
func (m Map) Len() int { return len(m.entries) }

var driveRE = regexp.MustCompile(`^[A-Za-z]:/$`)

func root(local string) string {
	if local == "/" || driveRE.MatchString(local) {
		return local
	}
	return strings.TrimRight(local, "/")
}

// ToLocal maps a canonical path to this machine's path; anything else
// (a legacy local path, an unmounted share) is returned unchanged.
func (m Map) ToLocal(p string) string {
	if len(p) >= 8 && strings.EqualFold(p[:8], "file:///") {
		rest := p[8:]
		if len(rest) >= 2 && rest[1] == ':' {
			return rest
		}
		return "/" + rest
	}
	if !IsCanonical(p) {
		return p
	}
	low := strings.ToLower(slash(p))
	for _, e := range m.entries {
		c := strings.ToLower(e.Canonical)
		if low+"/" == c {
			return root(e.Local)
		}
		if strings.HasPrefix(low, c) {
			return e.Local + slash(p)[len(e.Canonical):]
		}
	}
	return p
}

// ToCanonical maps a local path under one of the mounts to its canonical
// path (ok=false when it is on none).
func (m Map) ToCanonical(local string) (string, bool) {
	p := slash(local)
	low := strings.ToLower(p)
	var best *Entry
	for i := range m.entries {
		e := &m.entries[i]
		l := strings.ToLower(e.Local)
		if (strings.HasPrefix(low, l) || low+"/" == l) && (best == nil || len(e.Local) > len(best.Local)) {
			best = e
		}
	}
	if best == nil {
		return "", false
	}
	rest := ""
	if len(p) > len(best.Local) {
		rest = strings.Trim(p[len(best.Local):], "/")
	}
	base := strings.TrimRight(best.Canonical, "/")
	if rest == "" {
		return base, true
	}
	return base + "/" + rest, true
}

var global atomic.Pointer[Map]

// Default is the installed map (empty before Start / Set).
func Default() Map {
	if m := global.Load(); m != nil {
		return *m
	}
	return Map{}
}

// Set installs m as the default map.
func Set(m Map) { global.Store(&m) }

// ToLocal maps p with the default map.
func ToLocal(p string) string { return Default().ToLocal(p) }

// ToCanonical maps a local path with the default map.
func ToCanonical(local string) (string, bool) { return Default().ToCanonical(local) }

const homeMapSQL = `SELECT COALESCE(storage->'path_map', '{}'::jsonb)::text
	FROM instance_identity WHERE id = 1`

// Load reads the home's reported map.
func Load(ctx context.Context, db *sql.DB) (Map, error) {
	var raw string
	if err := db.QueryRowContext(ctx, homeMapSQL).Scan(&raw); err != nil {
		return Map{}, err
	}
	entries := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return Map{}, err
	}
	return FromEntries(entries), nil
}

// Start loads the map now and every 5 minutes until ctx ends.
func Start(ctx context.Context, db *sql.DB, logger *slog.Logger) {
	if db == nil {
		return
	}
	load := func() {
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		m, err := Load(c, db)
		if err != nil {
			if logger != nil {
				logger.Debug("share map not read", "err", err)
			}
			return
		}
		Set(m)
	}
	load()
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				load()
			}
		}
	}()
}

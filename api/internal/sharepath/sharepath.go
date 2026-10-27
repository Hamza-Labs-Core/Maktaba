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

// Map is a set of entries, longest canonical prefix first; a full node's also
// rewrites the prefixes the database stores (FromNodeEntries).
type Map struct {
	entries []Entry
	// Canonical is a prefix as the database stores it ("/Volumes/Originals/", the
	// home's own mount), longest first
	stored []Entry
}

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
	longestFirst(out.entries)
	return out
}

// FromNodeEntries builds a full node's Map from cluster_nodes.path_map, which its
// streaming role reads as {db_prefix: local_prefix}: a canonical prefix as in
// FromEntries, and any other the prefix of the paths the database stores
// ({"/Volumes/Originals/": "O:/"}, inferred from the home's mounts and this
// machine's), so this API opens a file where its streaming role does.
func FromNodeEntries(m map[string]string) Map {
	out := FromEntries(m)
	// in the streaming role's order (the longer key first): of two keys for one prefix
	// ("/Volumes/Originals" and "/Volumes/Originals/") the first wins, every time
	froms := make([]string, 0, len(m))
	for from := range m {
		froms = append(froms, from)
	}
	sort.Slice(froms, func(i, j int) bool {
		a, b := strings.TrimSpace(froms[i]), strings.TrimSpace(froms[j])
		if len(a) != len(b) {
			return len(a) > len(b)
		}
		return a < b
	})
	seen := map[string]bool{}
	for _, raw := range froms {
		from, l := strings.TrimSpace(raw), strings.TrimSpace(m[raw])
		if from == "" || IsCanonical(strings.TrimRight(from, "/")) || l == "" || seen[strings.ToLower(dir(from))] {
			continue
		}
		seen[strings.ToLower(dir(from))] = true
		out.stored = append(out.stored, Entry{Canonical: dir(from), Local: dir(l)})
	}
	longestFirst(out.stored)
	return out
}

func longestFirst(es []Entry) {
	sort.Slice(es, func(i, j int) bool {
		if len(es[i].Canonical) != len(es[j].Canonical) {
			return len(es[i].Canonical) > len(es[j].Canonical)
		}
		return es[i].Canonical < es[j].Canonical
	})
}

// Len is the number of entries.
func (m Map) Len() int { return len(m.entries) + len(m.stored) }

var driveRE = regexp.MustCompile(`^[A-Za-z]:/$`)

func root(local string) string {
	if local == "/" || driveRE.MatchString(local) {
		return local
	}
	return strings.TrimRight(local, "/")
}

// ToLocal maps a canonical path to this machine's path, and on a full node a
// stored path under one of its stored prefixes; anything else (a legacy local
// path, an unmounted share) is returned unchanged.
func (m Map) ToLocal(p string) string {
	if len(p) >= 8 && strings.EqualFold(p[:8], "file:///") {
		rest := p[8:]
		if len(rest) >= 2 && rest[1] == ':' {
			return rest
		}
		return "/" + rest
	}
	entries := m.entries
	if !IsCanonical(p) {
		if strings.Contains(p, "://") {
			return p
		}
		entries = m.stored
	}
	low := strings.ToLower(slash(p))
	for _, e := range entries {
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

// nodeMapSQL is a full node's own map (Epic 35 story 35.8): the node's database role
// has no access to cluster_nodes, but cluster_node_beat answers its own row, path map
// included (slot 0103), as the node's pipeline reported or the admin set it.
const nodeMapSQL = `SELECT COALESCE(path_map, '{}'::jsonb)::text FROM cluster_node_beat($1)`

// LoadNode reads a node's map.
func LoadNode(ctx context.Context, db *sql.DB, nodeID string) (Map, error) {
	var raw string
	if err := db.QueryRowContext(ctx, nodeMapSQL, nodeID).Scan(&raw); err != nil {
		return Map{}, err
	}
	entries := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return Map{}, err
	}
	return FromNodeEntries(entries), nil
}

// StartNode is Start for a full node: media is read through this machine's own mounts
// (design §11.5), never the home's.
func StartNode(ctx context.Context, db *sql.DB, nodeID string, logger *slog.Logger) {
	if db == nil || nodeID == "" {
		return
	}
	load := func() {
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		m, err := LoadNode(c, db, nodeID)
		if err != nil {
			if logger != nil {
				logger.Debug("node share map not read", "err", err)
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

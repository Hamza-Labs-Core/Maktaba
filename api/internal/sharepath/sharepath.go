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
func FromEntries(m map[string]string) Map { return build(inOrder(m), m, false) }

// FromNodeEntries builds a full node's Map from cluster_nodes.path_map, which its
// streaming role reads as {db_prefix: local_prefix}: a canonical prefix as in
// FromEntries, and any other the prefix of the paths the database stores
// ({"/Volumes/Originals/": "O:/"}, inferred from the home's mounts and this
// machine's), so this API opens a file where its streaming role does.
func FromNodeEntries(m map[string]string) Map { return build(inOrder(m), m, true) }

// ParseStreamMap reads MAKTABA_STREAM_PATH_MAP ("from=>to;from2=>to2") as a node's
// streaming role does when an operator sets it (pathmap.Parse, the only map it then
// reads): the longer prefix first, and of two rules for one prefix the first given.
func ParseStreamMap(spec string) Map {
	m := map[string]string{}
	var keys []string
	for _, part := range strings.Split(spec, ";") {
		from, to, ok := strings.Cut(strings.TrimSpace(part), "=>")
		if from = strings.TrimSpace(from); !ok || from == "" {
			continue
		}
		if _, dup := m[from]; !dup {
			m[from] = strings.TrimSpace(to)
			keys = append(keys, from)
		}
	}
	sort.SliceStable(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	return build(keys, m, true)
}

// inOrder is m's keys in the streaming role's order (pathmap.FromJSON: the longer
// first, then by text).
func inOrder(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := strings.TrimSpace(keys[i]), strings.TrimSpace(keys[j])
		if len(a) != len(b) {
			return len(a) > len(b)
		}
		return a < b
	})
	return keys
}

// build makes a Map of m's keys taken in order: of two keys for one prefix
// ("/Volumes/Originals" and "/Volumes/Originals/") the first wins, every time, as in
// the streaming role. With stored (a node's map), a key that is not canonical
// rewrites the paths under it, and an empty target strips the prefix ("/mnt/nas=>");
// without (the home's), both are dropped.
func build(keys []string, m map[string]string, stored bool) Map {
	out := Map{}
	seen := map[string]bool{}
	for _, raw := range keys {
		from, l := strings.TrimSpace(raw), strings.TrimSpace(m[raw])
		canon := IsCanonical(strings.TrimRight(from, "/"))
		if from == "" || (!stored && (l == "" || !canon)) || seen[strings.ToLower(dir(from))] {
			continue
		}
		seen[strings.ToLower(dir(from))] = true
		e := Entry{Canonical: dir(from)}
		if l != "" {
			e.Local = dir(l) // "": the prefix is stripped
		}
		if canon {
			out.entries = append(out.entries, e)
		} else {
			out.stored = append(out.stored, e)
		}
	}
	longestFirst(out.entries)
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
	if !IsCanonical(p) {
		if strings.Contains(p, "://") {
			return p
		}
		if out, _, ok := match(m.stored, p); ok {
			return out
		}
		return p
	}
	// the shares' entries and a node's other URL keys ("smb://nas", every share of a
	// server): the longest prefix that matches, as in the streaming role
	best, bestLen := p, -1
	for _, es := range [][]Entry{m.entries, m.stored} {
		if out, n, ok := match(es, p); ok && n > bestLen {
			best, bestLen = out, n
		}
	}
	return best
}

// match maps p with the first of es (longest first) whose prefix it is under, and
// says how long that prefix is.
func match(es []Entry, p string) (string, int, bool) {
	low := strings.ToLower(slash(p))
	for _, e := range es {
		c := strings.ToLower(e.Canonical)
		local := e.Local
		if local == "" {
			local = "/"
		}
		if low+"/" == c {
			return root(local), len(c), true
		}
		if strings.HasPrefix(low, c) {
			return local + slash(p)[len(e.Canonical):], len(c), true
		}
	}
	return "", 0, false
}

// ToCanonical maps a local path under one of the mounts to its canonical
// path (ok=false when it is on none).
func (m Map) ToCanonical(local string) (string, bool) {
	p := slash(local)
	low := strings.ToLower(p)
	var best *Entry
	for i := range m.entries {
		e := &m.entries[i]
		if e.Local == "" {
			continue // a stripped prefix is no mount
		}
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

// Package dsn classifies a database connection string by scheme and
// maps it onto the database/sql driver + goose dialect the rest of the
// stack expects.
//
// Maktaba ships two storage backends:
//
//   - Postgres (driver "postgres" via lib/pq) — the production default,
//     recommended for libraries over ~5 TB where concurrent writers and
//     a real query planner matter.
//   - SQLite (driver "sqlite" via modernc.org/sqlite, a pure-Go,
//     CGO-free implementation) — the zero-dependency single-binary
//     default for small/home installs.
//
// The migration tree already carries `.sqlite.sql` parity siblings for
// every Postgres migration (see api/migrate.go), so the only thing the
// runtime has to get right is: given a DSN, which driver name do I hand
// to sql.Open and which dialect do I hand to goose.
package dsn

import (
	"fmt"
	"strings"
)

// Backend enumerates the storage engines maktaba-server understands.
type Backend int

const (
	// Unknown is returned for DSNs whose scheme we don't recognise.
	Unknown Backend = iota
	// Postgres covers postgres:// and postgresql:// DSNs.
	Postgres
	// SQLite covers sqlite://, sqlite3:// and file: DSNs.
	SQLite
)

// Info is the resolved view of a DSN: the backend, the database/sql
// driver name to pass to sql.Open, the goose dialect string, and — for
// SQLite — the filesystem path the database lives at (useful for the
// setup wizard so it can pre-create the parent directory).
type Info struct {
	Backend    Backend
	Driver     string // database/sql driver name
	Dialect    string // goose dialect
	SQLitePath string // populated only for SQLite
	DSN        string // the (possibly rewritten) DSN to pass to sql.Open
}

// Classify inspects the scheme of a DSN and returns its resolved Info.
//
// SQLite DSNs are accepted in the forms the spec documents
// (`sqlite:///abs/path/maktaba.db`) plus the shorter `sqlite:` and
// `file:` variants. For modernc.org/sqlite the driver wants a bare
// filesystem path (optionally with `?_pragma=...` query params), so we
// strip the scheme and the leading authority slash here.
func Classify(raw string) (Info, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Info{}, fmt.Errorf("empty DSN")
	}

	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "postgres://"), strings.HasPrefix(lower, "postgresql://"):
		return Info{
			Backend: Postgres,
			Driver:  "postgres",
			Dialect: "postgres",
			DSN:     trimmed,
		}, nil

	case strings.HasPrefix(lower, "sqlite://"),
		strings.HasPrefix(lower, "sqlite3://"),
		strings.HasPrefix(lower, "sqlite:"),
		strings.HasPrefix(lower, "file:"):
		path := sqlitePath(trimmed)
		return Info{
			Backend:    SQLite,
			Driver:     "sqlite",
			Dialect:    "sqlite3",
			SQLitePath: stripQuery(path),
			DSN:        path,
		}, nil

	default:
		return Info{Backend: Unknown}, fmt.Errorf("unrecognised DSN scheme in %q (want postgres:// or sqlite://)", redact(trimmed))
	}
}

// sqlitePath turns a sqlite DSN into the bare path modernc.org/sqlite
// expects. It tolerates every documented spelling:
//
//	sqlite:///var/lib/maktaba/maktaba.db -> /var/lib/maktaba/maktaba.db
//	sqlite://./maktaba.db                -> ./maktaba.db
//	sqlite:maktaba.db                    -> maktaba.db
//	file:maktaba.db?cache=shared         -> maktaba.db?cache=shared (passed through)
func sqlitePath(raw string) string {
	for _, prefix := range []string{"sqlite3://", "sqlite://", "sqlite3:", "sqlite:"} {
		if rest, ok := cutPrefixFold(raw, prefix); ok {
			// `sqlite:///abs` leaves a leading slash after the
			// authority-empty `//` is stripped — that IS the absolute
			// path, so keep it. `sqlite://rel` (no third slash) yields a
			// relative path with no leading slash, also correct.
			return rest
		}
	}
	// file: form is handed to modernc.org/sqlite verbatim — it speaks
	// the SQLite URI dialect natively.
	return raw
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return "", false
}

func stripQuery(path string) string {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		return path[:i]
	}
	return path
}

// redact masks anything that looks like userinfo credentials before a
// DSN ends up in an error string. Cheap belt-and-braces so a typo'd
// postgres DSN never logs its password.
func redact(raw string) string {
	at := strings.IndexByte(raw, '@')
	slashSlash := strings.Index(raw, "//")
	if at < 0 || slashSlash < 0 || at < slashSlash {
		return raw
	}
	return raw[:slashSlash+2] + "***@" + raw[at+1:]
}

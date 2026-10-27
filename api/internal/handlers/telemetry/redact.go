package telemetry

import (
	"context"
	"database/sql"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// LibraryPlaceholder replaces a library root in a free-text field; the folders
// and the file name after it go too (Scrub), only the file's extension stays.
const LibraryPlaceholder = "<library>"

// HomePlaceholder replaces a home folder (/Users/<name>, /home/<name>,
// C:\Users\<name>) the client did not strip already.
const HomePlaceholder = "<home>"

// homeDirs are the home folders of macOS, Linux and Windows; the user name in
// them is the part that names someone.
var homeDirs = regexp.MustCompile(`(?i)(/Users/[^/\s]+|/home/[^/\s]+|[A-Z]:\\Users\\[^\\\s]+)`)

// AddressPlaceholder replaces what names a place on a network: a URL's origin,
// an IP address, a host name, an email address.
const AddressPlaceholder = "<address>"

var (
	// an origin (http://192.168.1.20:8088, wss://home.example): the home's address
	origins = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]{1,15}://[^/\s"')]*`)
	// a query string or fragment after a path (a search lives in one)
	queries = regexp.MustCompile(`[?#][^\s"')]*`)
	emails  = regexp.MustCompile(`(?i)\b[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}\b`)
	ipv4    = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?\b`)
	// eight groups, or fewer around "::" (a bare "::" stays: it is a Go scope, not an address)
	ipv6 = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{1,4}:){7}[0-9a-f]{1,4}\b|\b(?:[0-9a-f]{1,4}:){1,6}:(?:[0-9a-f]{1,4}(?::[0-9a-f]{1,4})*)?\b`)
	// a host name under a top-level domain a home is likely under: home.local,
	// nas.example.com:8088 (a file name is gone by then: paths are scrubbed first)
	hosts = regexp.MustCompile(`(?i)\b(?:[a-z0-9-]+\.)+(?:local|lan|home|internal|arpa|com|net|org|io|dev|app|cloud|me|co|uk|de|fr|sa|eg|ae)(?::\d{1,5})?\b`)
	// a path, from its root (a library's, a home folder, a drive, "/") to where
	// an error message goes on (": ", a quote, the line's end): spaces in folder
	// and file names are part of it
	paths = regexp.MustCompile(`(^|[\s"'(=\[])((?:<library>|<home>|[A-Za-z]:\\|/)[^"'\n]*?)(:\s|["'\n]|$)`)
	// a file name's extension worth keeping (which kind of file failed)
	extension = regexp.MustCompile(`\.[A-Za-z0-9]{1,5}$`)
)

// Redactor replaces the configured library roots in free text. Every root is
// quoted (regexp.QuoteMeta), so a root with regex metacharacters ("/data[archive]")
// matches itself only.
type Redactor struct {
	roots []*regexp.Regexp
}

// NewRedactor builds a Redactor for roots; the longest root is tried first, so a
// root inside another is replaced by the longer one's match.
func NewRedactor(roots []string) *Redactor {
	seen := map[string]struct{}{}
	var list []string
	for _, r := range roots {
		r = strings.TrimRight(strings.TrimSpace(r), `/\`)
		if r == "" {
			continue
		}
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool { return len(list[i]) > len(list[j]) })
	out := &Redactor{roots: make([]*regexp.Regexp, 0, len(list))}
	for _, r := range list {
		out.roots = append(out.roots, regexp.MustCompile(regexp.QuoteMeta(r)))
	}
	return out
}

// Apply replaces every library root in s, then every home folder left, then
// every address (Scrub).
func (r *Redactor) Apply(s string) string {
	if r != nil {
		for _, re := range r.roots {
			s = re.ReplaceAllLiteralString(s, LibraryPlaceholder)
		}
	}
	return Scrub(homeDirs.ReplaceAllLiteralString(s, HomePlaceholder))
}

// Scrub removes from free text what could name a person, a place or a file:
// origins, query strings, paths (a file's name too: only its extension stays),
// email and IP addresses and host names. An error keeps its shape:
// "open <library>/<file>.mp4: permission denied".
func Scrub(s string) string {
	s = origins.ReplaceAllLiteralString(s, AddressPlaceholder)
	s = queries.ReplaceAllLiteralString(s, "")
	s = homeDirs.ReplaceAllLiteralString(s, HomePlaceholder)
	s = scrubPaths(s)
	s = emails.ReplaceAllLiteralString(s, AddressPlaceholder)
	s = ipv4.ReplaceAllLiteralString(s, AddressPlaceholder)
	s = ipv6.ReplaceAllLiteralString(s, AddressPlaceholder)
	return hosts.ReplaceAllLiteralString(s, AddressPlaceholder)
}

// scrubPaths replaces every path with where it is (<library>, <home>, <path>)
// and, when its file has one, the extension: "<library>/<file>.mp4".
func scrubPaths(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range paths.FindAllStringSubmatchIndex(s, -1) {
		start, end := m[4], m[5]
		path := s[start:end]
		b.WriteString(s[last:start])
		switch {
		case strings.HasPrefix(path, LibraryPlaceholder):
			b.WriteString(LibraryPlaceholder)
		case strings.HasPrefix(path, HomePlaceholder):
			b.WriteString(HomePlaceholder)
		default:
			b.WriteString(PathPlaceholder)
		}
		if ext := extension.FindString(path); ext != "" {
			b.WriteString("/" + FilePlaceholder + ext)
		}
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

// FilePlaceholder replaces a file's name; its extension stays.
const FilePlaceholder = "<file>"

const PathPlaceholder = "<path>"

// rootsTTL is how long the library roots the sink read stay in use: a library
// added since is redacted within a minute (the client strips paths too).
const rootsTTL = time.Minute

// rootCache is the Redactor for the libraries' roots, read again from
// library_roots (slot 0043, the canonical store) every rootsTTL.
type rootCache struct {
	mu     sync.Mutex
	at     time.Time
	red    *Redactor
	loader func(context.Context) ([]string, error)
}

func (c *rootCache) get(ctx context.Context, now time.Time) (*Redactor, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.red != nil && now.Sub(c.at) < rootsTTL {
		return c.red, nil
	}
	roots, err := c.loader(ctx)
	if err != nil {
		return nil, err
	}
	c.red, c.at = NewRedactor(roots), now
	return c.red, nil
}

// loadRoots reads every library root, as entered and canonical.
func loadRoots(db *sql.DB) func(context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		rows, err := db.QueryContext(ctx, `SELECT path, path_canonical FROM library_roots`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var p, c string
			if err := rows.Scan(&p, &c); err != nil {
				return nil, err
			}
			out = append(out, p, c)
		}
		return out, rows.Err()
	}
}

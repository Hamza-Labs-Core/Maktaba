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

// LibraryPlaceholder replaces a library root in a free-text field: the file name
// after it stays (it says which kind of file failed, not whose), the folders that
// lead to it go.
const LibraryPlaceholder = "<library>"

// HomePlaceholder replaces a home folder (/Users/<name>, /home/<name>,
// C:\Users\<name>) the client did not strip already.
const HomePlaceholder = "<home>"

// homeDirs are the home folders of macOS, Linux and Windows; the user name in
// them is the part that names someone.
var homeDirs = regexp.MustCompile(`(?i)(/Users/[^/\s]+|/home/[^/\s]+|[A-Z]:\\Users\\[^\\\s]+)`)

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

// Apply replaces every library root in s, then every home folder left.
func (r *Redactor) Apply(s string) string {
	if r != nil {
		for _, re := range r.roots {
			s = re.ReplaceAllLiteralString(s, LibraryPlaceholder)
		}
	}
	return homeDirs.ReplaceAllLiteralString(s, HomePlaceholder)
}

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

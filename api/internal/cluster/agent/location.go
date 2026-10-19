package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Location is where the cluster files live (design-agent.md §4) as the
// processes and browsers see it: `files: {dir, on_share, reason}`.
type Location struct {
	Dir     string  `json:"dir"`
	OnShare bool    `json:"on_share"`
	Reason  *string `json:"reason"`
	// ShareRoot is the share root in database form ("" when unknown).
	ShareRoot string `json:"share_root,omitempty"`
}

func (l Location) equal(o Location) bool {
	return l.Dir == o.Dir && l.OnShare == o.OnShare && l.ShareRoot == o.ShareRoot &&
		((l.Reason == nil && o.Reason == nil) || (l.Reason != nil && o.Reason != nil && *l.Reason == *o.Reason))
}

// LibraryRoot is one library root as stored, with its library's video
// count (the tie-breaker when the roots share nothing but the filesystem
// root).
type LibraryRoot struct {
	LibraryID string
	Path      string
	Videos    int64
}

// Locator resolves the cluster directory: MAKTABA_CLUSTER_DIR when set;
// else <share root>/maktaba/cluster through the path map when the share is
// reachable; else the local fallback (~/.maktaba/cluster).
type Locator struct {
	// Override is MAKTABA_CLUSTER_DIR (a local path).
	Override string
	// ShareRoot is MAKTABA_SHARE_ROOT (database form); empty: derived from
	// the library roots.
	ShareRoot string
	PathMap   PathMap
	// Roots reads the library roots (nil: none known).
	Roots func(ctx context.Context) ([]LibraryRoot, error)
	// Fallback is the local directory used when the share is not
	// reachable (default ~/.maktaba/cluster).
	Fallback string
}

// FallbackDir is ~/.maktaba/cluster (the working directory's
// .maktaba/cluster when there is no home directory).
func FallbackDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".maktaba", "cluster")
	}
	return filepath.Join(home, ".maktaba", "cluster")
}

// Resolve works out the location now. It creates the cluster directory
// (never the share root itself: a share that is not mounted must not be
// shadowed by a local directory of the same name).
func (l *Locator) Resolve(ctx context.Context) Location {
	if l.Override != "" {
		dir := filepath.Clean(l.Override)
		if err := ensureWritable(dir); err != nil {
			return l.fallback("", fmt.Sprintf("MAKTABA_CLUSTER_DIR %s is not writable: %v", dir, err))
		}
		return Location{Dir: dir, Reason: strPtr("set by MAKTABA_CLUSTER_DIR")}
	}
	root := strings.TrimSpace(l.ShareRoot)
	if root == "" {
		if l.Roots == nil {
			return l.fallback("", "no library roots to find the share")
		}
		roots, err := l.Roots(ctx)
		if err != nil {
			return l.fallback("", "reading the library roots failed: "+err.Error())
		}
		root = ShareRootOf(roots)
		if root == "" {
			return l.fallback("", "no library roots to find the share")
		}
	}
	localRoot := l.PathMap.ToLocal(root)
	if !filepath.IsAbs(filepath.FromSlash(localRoot)) {
		return l.fallback(root, fmt.Sprintf("the share root %s has no local path here (set MAKTABA_PATH_MAP)", root))
	}
	localRoot = filepath.Clean(filepath.FromSlash(localRoot))
	if err := shareReachable(localRoot); err != nil {
		return l.fallback(root, fmt.Sprintf("the share %s (%s) is not reachable: %v", root, localRoot, err))
	}
	dir := filepath.Join(localRoot, "maktaba", "cluster")
	// maktaba/cluster must be real directories on the share: a link
	// planted by any machine must not move the hub's files elsewhere
	if err := shareDirs(localRoot); err != nil {
		return l.fallback(root, fmt.Sprintf("cannot use %s on the share: %v", dir, err))
	}
	if err := ensureWritable(dir); err != nil {
		return l.fallback(root, fmt.Sprintf("cannot write %s on the share: %v", dir, err))
	}
	return Location{Dir: dir, OnShare: true, ShareRoot: root}
}

func (l *Locator) fallback(root, reason string) Location {
	dir := l.Fallback
	if dir == "" {
		dir = FallbackDir()
	}
	if err := ensureWritable(dir); err != nil {
		reason += fmt.Sprintf("; the local fallback %s is not writable either: %v", dir, err)
	}
	return Location{Dir: dir, Reason: &reason, ShareRoot: root}
}

// shareReachable: the root must be an existing, non-empty directory (an
// unmounted mount point is usually an empty directory).
func shareReachable(root string) error {
	st, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return errors.New("not a directory")
	}
	f, err := os.Open(root) //nolint:gosec // the configured share root
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if names, err := f.Readdirnames(1); err != nil || len(names) == 0 {
		return errors.New("empty directory (not mounted?)")
	}
	return nil
}

// shareDirs creates maktaba/cluster under the share root through an
// os.Root and checks neither is a link.
func shareDirs(localRoot string) error {
	root, err := os.OpenRoot(localRoot)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return mkdirShared(root, filepath.Join("maktaba", "cluster"))
}

func ensureWritable(dir string) error {
	if err := mkdirSharedAbs(dir); err != nil {
		return err
	}
	probe, err := os.CreateTemp(dir, ".maktaba-hub-probe-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	_ = probe.Close()
	return os.Remove(name)
}

// splitDBPath splits a database path into its root (kept whole) and its
// components: "/a/b" → ("/", [a b]); "Y:/a" → ("Y:/", [a]);
// "//nas/share/a" → ("//nas/share/", [a]).
func splitDBPath(p string) (root string, parts []string) {
	p = strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	var rest string
	switch {
	case strings.HasPrefix(p, "//"):
		segs := strings.SplitN(strings.TrimPrefix(p, "//"), "/", 3)
		switch len(segs) {
		case 1:
			root, rest = "//"+segs[0]+"/", ""
		case 2:
			root, rest = "//"+segs[0]+"/"+segs[1]+"/", ""
		default:
			root, rest = "//"+segs[0]+"/"+segs[1]+"/", segs[2]
		}
	case len(p) >= 2 && p[1] == ':' && isLetter(p[0]):
		root, rest = p[:2]+"/", strings.TrimPrefix(p[2:], "/")
	case strings.HasPrefix(p, "/"):
		root, rest = "/", strings.TrimPrefix(p, "/")
	default:
		root, rest = "", p
	}
	for _, s := range strings.Split(rest, "/") {
		if s != "" && s != "." {
			parts = append(parts, s)
		}
	}
	return root, parts
}

func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func joinDBPath(root string, parts []string) string {
	return root + strings.Join(parts, "/")
}

// commonDir is the deepest directory shared by every path (database form,
// compared case-insensitively) and whether it is more than a filesystem
// root ("/", "Y:/", "//nas/share/").
func commonDir(paths []string) (string, bool) {
	if len(paths) == 0 {
		return "", false
	}
	root, common := splitDBPath(paths[0])
	for _, p := range paths[1:] {
		r, parts := splitDBPath(p)
		if !strings.EqualFold(r, root) {
			return "", false
		}
		n := 0
		for n < len(common) && n < len(parts) && strings.EqualFold(common[n], parts[n]) {
			n++
		}
		common = common[:n]
	}
	if root == "" && len(common) == 0 {
		return "", false
	}
	return joinDBPath(root, common), len(common) > 0
}

// ShareRootOf is the share root of design-agent.md §4: the deepest
// directory common to every library root; when they share nothing but the
// filesystem root, the root of the library with the most videos.
func ShareRootOf(roots []LibraryRoot) string {
	var paths []string
	for _, r := range roots {
		if strings.TrimSpace(r.Path) != "" {
			paths = append(paths, r.Path)
		}
	}
	if len(paths) == 0 {
		return ""
	}
	if dir, ok := commonDir(paths); ok {
		return dir
	}
	// the library with the most videos (ties: the smallest id), then its
	// roots' common directory or, failing that, its first root
	videos := map[string]int64{}
	byLib := map[string][]string{}
	for _, r := range roots {
		if strings.TrimSpace(r.Path) == "" {
			continue
		}
		videos[r.LibraryID] = r.Videos
		byLib[r.LibraryID] = append(byLib[r.LibraryID], r.Path)
	}
	ids := make([]string, 0, len(byLib))
	for id := range byLib {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if videos[ids[i]] != videos[ids[j]] {
			return videos[ids[i]] > videos[ids[j]]
		}
		return ids[i] < ids[j]
	})
	lib := byLib[ids[0]]
	if dir, ok := commonDir(lib); ok {
		return dir
	}
	sort.Strings(lib)
	r, parts := splitDBPath(lib[0])
	return joinDBPath(r, parts)
}

func strPtr(s string) *string { return &s }

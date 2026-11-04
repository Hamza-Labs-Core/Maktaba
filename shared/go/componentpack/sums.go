// Package componentpack is the pack format of Epic 51 (story 51.15): a .tar.zst of the
// files one asset component needs on one platform, built by the release from the commit
// being released.
//
// Inside, in this order:
//
//	pack.json      {"schema":1,"id":…,"version":…,"platform":…,"files_sha256":…,"unpacked_bytes":…}
//	files.sha256   one line per entry, tab-separated, sorted by path (the Linux payload's
//	               format, tools/release/linux-app/payload_tool.py):
//	                 f <mode, 4 octal digits> <size> <sha256> <path>
//	                 l <target> <path>
//	the files
//
// Build writes one byte for byte the same from the same tree and time. Unpack is the only
// way a pack is opened: it refuses absolute paths, "..", links that leave the folder,
// devices, hard links and anything files.sha256 does not list, and checks every file's
// size and sha256 while it writes it. Nothing in a pack is run (README §6.4).
//
// The pack file itself is checked against the signed index (size, then sha256) by
// releaseverify before it is unpacked.
package componentpack

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Names inside a pack.
const (
	ManifestName = "pack.json"
	SumsName     = "files.sha256"
	// Schema is the pack.json schema this package writes and reads.
	Schema = 1
)

// ErrInvalid is every refusal of a pack's contents.
var ErrInvalid = errors.New("pack invalid")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

var hexRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Entry is one line of files.sha256: a file (Kind 'f') or a symlink ('l').
type Entry struct {
	Kind   byte
	Mode   fs.FileMode // permission bits only
	Size   int64
	SHA256 string
	Target string // a link's target, as stored (relative)
	Path   string // relative, "/"-separated, clean
}

// Line is the entry as files.sha256 writes it (no newline).
func (e Entry) Line() string {
	if e.Kind == 'l' {
		return "l\t" + e.Target + "\t" + e.Path
	}
	return fmt.Sprintf("f\t%04o\t%d\t%s\t%s", uint32(e.Mode.Perm()), e.Size, e.SHA256, e.Path)
}

// FormatSums is files.sha256 for entries, sorted by path (bytes).
func FormatSums(entries []Entry) []byte {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	var b bytes.Buffer
	for _, e := range sorted {
		b.WriteString(e.Line())
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// ParseSums reads files.sha256 and checks every path and link target: a path is clean,
// relative and inside the folder, a link's target stays inside, no path is listed twice,
// and the lines are sorted.
func ParseSums(b []byte) ([]Entry, error) {
	var out []Entry
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	prev := ""
	for n := 1; sc.Scan(); n++ {
		e, err := parseEntry(sc.Text())
		if err != nil {
			return nil, invalid("%s line %d: %v", SumsName, n, err)
		}
		if seen[e.Path] {
			return nil, invalid("%s line %d: %s listed twice", SumsName, n, e.Path)
		}
		if n > 1 && e.Path < prev {
			return nil, invalid("%s line %d: not sorted by path", SumsName, n)
		}
		seen[e.Path], prev = true, e.Path
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, invalid("%s: %v", SumsName, err)
	}
	// a path under another entry: a file or link cannot also be a folder
	for _, e := range out {
		for d := path.Dir(e.Path); d != "."; d = path.Dir(d) {
			if seen[d] {
				return nil, invalid("%s: %s is under the entry %s", SumsName, e.Path, d)
			}
		}
	}
	// two paths (or folders on the way) that differ only by case: one file on the Mac's
	// and Windows' disks
	folded := map[string]string{}
	for _, e := range out {
		for p := e.Path; p != "."; p = path.Dir(p) {
			k := strings.ToLower(p)
			if was, ok := folded[k]; ok && was != p {
				return nil, invalid("%s: %s and %s differ only by case", SumsName, was, p)
			}
			folded[k] = p
		}
	}
	return out, nil
}

func parseEntry(line string) (Entry, error) {
	parts := strings.Split(line, "\t")
	var e Entry
	switch {
	case len(parts) == 5 && parts[0] == "f":
		mode, err := strconv.ParseUint(parts[1], 8, 32)
		if err != nil || len(parts[1]) != 4 || mode&^0o777 != 0 {
			return e, fmt.Errorf("bad mode %q (permission bits only)", parts[1])
		}
		size, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil || size < 0 {
			return e, fmt.Errorf("bad size %q", parts[2])
		}
		if !hexRe.MatchString(parts[3]) {
			return e, fmt.Errorf("bad sha256 %q", parts[3])
		}
		e = Entry{Kind: 'f', Mode: fs.FileMode(mode), Size: size, SHA256: parts[3], Path: parts[4]}
	case len(parts) == 3 && parts[0] == "l":
		e = Entry{Kind: 'l', Target: parts[1], Path: parts[2]}
		if !safeTarget(e.Path, e.Target) {
			return e, fmt.Errorf("link %s -> %q leaves the folder", e.Path, e.Target)
		}
	default:
		return e, fmt.Errorf("not an entry: %q", line)
	}
	if !SafePath(e.Path) {
		return e, fmt.Errorf("bad path %q", e.Path)
	}
	return e, nil
}

// SafePath reports a path a pack may hold: relative, "/"-separated, clean, with no ".."
// element, no backslash, colon or control character, and not one of the pack's own two
// files.
func SafePath(p string) bool {
	if p == "" || p != path.Clean(p) || path.IsAbs(p) || p == "." || p == ManifestName || p == SumsName {
		return false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f || r == '\\' || r == ':' {
			return false
		}
	}
	for _, el := range strings.Split(p, "/") {
		if el == ".." || el == "." || el == "" {
			return false
		}
	}
	return true
}

// safeTarget: a link's target is relative and, from the link's own folder, ends inside
// the pack's folder (not at the folder itself).
func safeTarget(link, target string) bool {
	if target == "" || path.IsAbs(target) || strings.ContainsAny(target, "\\:\x00\t\n\r") {
		return false
	}
	end := path.Join(path.Dir(link), target)
	return end != "." && end != ".." && !strings.HasPrefix(end, "../") && !path.IsAbs(end)
}

// Scan lists a folder's files and links as files.sha256 entries, and the files' total
// size. It refuses anything else (a device, a socket, a pipe), a link that leaves the
// folder, a path the format cannot hold, and setuid, setgid or sticky bits.
func Scan(dir string) ([]Entry, int64, error) {
	var out []Entry
	var total int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		mode := info.Mode()
		if !mode.IsDir() && !SafePath(rel) {
			return invalid("%q: a pack cannot hold this path", rel)
		}
		switch {
		case mode.IsDir():
			return nil
		case mode&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			target = filepath.ToSlash(target)
			if !safeTarget(rel, target) {
				return invalid("%s -> %s leaves the folder", rel, target)
			}
			out = append(out, Entry{Kind: 'l', Target: target, Path: rel})
		case mode.IsRegular():
			if mode&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
				return invalid("%s: setuid, setgid or sticky", rel)
			}
			sum, n, err := hashFile(p)
			if err != nil {
				return err
			}
			out = append(out, Entry{Kind: 'f', Mode: mode.Perm(), Size: n, SHA256: sum, Path: rel})
			total += n
		default:
			return invalid("%s: not a file, link or folder", rel)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, total, nil
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

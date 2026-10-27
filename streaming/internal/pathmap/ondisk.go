package pathmap

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// OnDisk is p as this machine's disk spells it. A name has two Unicode spellings when it
// holds a letter with a mark: macOS hands a scanner the decomposed one (أ as ا + ◌ٔ, é
// as e + ◌́) on a share it mounts, while an NFS mount or a Linux or Windows disk keeps
// the bytes the file was named with, usually composed. Opened byte for byte the other
// spelling is "no such file", so a path stored from one mount would never play from the
// other. p itself when it is there (or cannot be found either way); else the same path
// composed or decomposed; else each missing name looked up in its folder by its
// composed form (a folder named one way, a file the other).
func OnDisk(p string) string {
	if p == "" || strings.Contains(p, "://") || exists(p) {
		return p
	}
	for _, f := range []norm.Form{norm.NFC, norm.NFD} {
		if q := f.String(p); q != p && exists(q) {
			return q
		}
	}
	if q, ok := walk(p); ok {
		return q
	}
	return p
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// walk finds p's names one folder at a time, each by its composed form.
func walk(p string) (string, bool) {
	vol := filepath.VolumeName(p)
	rest := p[len(vol):]
	sep := string(filepath.Separator)
	cur := vol
	if strings.HasPrefix(rest, sep) || strings.HasPrefix(rest, "/") {
		cur += sep
	}
	for _, name := range strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == filepath.Separator }) {
		next := filepath.Join(cur, name)
		if !exists(next) {
			found, ok := sameName(cur, name)
			if !ok {
				return "", false
			}
			next = filepath.Join(cur, found)
		}
		cur = next
	}
	return cur, true
}

// sameName is the entry of dir whose composed form is name's.
func sameName(dir, name string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	want := norm.NFC.String(name)
	for _, e := range entries {
		if norm.NFC.String(e.Name()) == want {
			return e.Name(), true
		}
	}
	return "", false
}

package mediaaccess

import (
	"os"
	"path/filepath"
	"strings"
)

// mountReadOnly reports whether the mount holding root is read-only, from a mountinfo file
// (proc(5)): the mount whose mount point is the longest prefix of root, read-only when its
// own options or its superblock's say "ro". A missing or unreadable file (not Linux) is
// false: unknown.
func mountReadOnly(root, mountinfo string) bool {
	raw, err := os.ReadFile(mountinfo)
	if err != nil {
		return false
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	root = filepath.Clean(root)
	best, ro := -1, false
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 10 {
			continue
		}
		point := unescapeMount(f[4])
		if !within(root, point) || len(point) <= best {
			continue
		}
		super := ""
		for i := 6; i < len(f)-2; i++ {
			if f[i] == "-" {
				super = f[i+3]
				break
			}
		}
		best = len(point)
		ro = hasOpt(f[5], "ro") || hasOpt(super, "ro")
	}
	return ro
}

func within(path, point string) bool {
	if point == "/" {
		return strings.HasPrefix(path, "/")
	}
	return path == point || strings.HasPrefix(path, point+"/")
}

func hasOpt(opts, want string) bool {
	for _, o := range strings.Split(opts, ",") {
		if o == want {
			return true
		}
	}
	return false
}

// unescapeMount undoes mountinfo's octal escapes (\040 is a space).
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			n := 0
			ok := true
			for _, c := range s[i+1 : i+4] {
				if c < '0' || c > '7' {
					ok = false
					break
				}
				n = n*8 + int(c-'0')
			}
			if ok {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

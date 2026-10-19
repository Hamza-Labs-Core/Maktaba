//go:build unix

package releaseverify

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// trustedDir resolves dir and checks that nobody but this process's user (or
// root) can change what it holds: dir and every ancestor must be owned by the
// effective uid or by root, and not writable by group or others unless sticky
// (/tmp). A cache that fails this is not a trust anchor: `sudo maktaba-server
// update` with the invoking user's $HOME would otherwise let that user plant a
// trusted_root.json naming their own CA and have root install what they sign.
func trustedDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	euid := os.Geteuid()
	for p := resolved; ; p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if err != nil {
			return "", err
		}
		if err := safeOwner(p, fi, euid); err != nil {
			return "", err
		}
		if fi.Mode().Perm()&0o022 != 0 && fi.Mode()&os.ModeSticky == 0 {
			return "", fmt.Errorf("%s is writable by group or others", p)
		}
		if filepath.Dir(p) == p {
			return resolved, nil
		}
	}
}

// trustedFile checks a cache file inside a trustedDir: a regular file (not a
// symlink) owned by this user or root and not writable by group or others.
func trustedFile(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if err := safeOwner(path, fi, os.Geteuid()); err != nil {
		return err
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is writable by group or others", path)
	}
	return nil
}

func safeOwner(path string, fi os.FileInfo, euid int) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s: no owner information", path)
	}
	if uid := int(st.Uid); uid != euid && uid != 0 {
		return fmt.Errorf("%s is owned by uid %d, not by this user (uid %d) or root", path, uid, euid)
	}
	return nil
}

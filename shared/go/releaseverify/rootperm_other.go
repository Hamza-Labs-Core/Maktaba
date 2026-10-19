//go:build !unix

package releaseverify

import (
	"fmt"
	"os"
	"path/filepath"
)

// trustedDir resolves dir. Windows has no uid/mode bits to check here (the
// data directory's ACLs are the installer's job); see rootperm_unix.go.
func trustedDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// trustedFile requires a regular file (not a symlink or reparse point).
func trustedFile(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}

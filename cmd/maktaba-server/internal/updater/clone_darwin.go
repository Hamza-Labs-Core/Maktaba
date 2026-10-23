package updater

import (
	"io/fs"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// cloneTree clones a directory tree on APFS (clonefile(2)): instant, and it takes no
// room until either copy changes. The clone carries the app's quarantine attribute;
// it is taken off the copy, which runs outside the app Gatekeeper approved.
func cloneTree(src, dst string) error {
	if err := unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW); err != nil {
		return err
	}
	_ = filepath.WalkDir(dst, func(p string, d fs.DirEntry, err error) error {
		if err == nil {
			_ = unix.Lremovexattr(p, "com.apple.quarantine")
		}
		return nil
	})
	return nil
}

package devmigrate

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// cloneOrCopy clones from to to in one call (APFS copies a whole folder
// copy-on-write: no space until either side changes), else copies it.
func cloneOrCopy(from, to string) error {
	err := unix.Clonefile(from, to, unix.CLONE_NOFOLLOW)
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.EXDEV) || errors.Is(err, unix.ENOTSUP) {
		_ = os.RemoveAll(to)
		return copyTree(from, to)
	}
	return err
}

func sameVolume(from, to string) bool {
	var a, b syscall.Stat_t
	if syscall.Stat(from, &a) != nil || syscall.Stat(existingParent(to), &b) != nil {
		return false
	}
	return a.Dev == b.Dev
}

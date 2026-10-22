//go:build unix

package cache

import "golang.org/x/sys/unix"

// DiskSpace is the space free to this process on path's file system, and the
// file system's size.
func DiskSpace(path string) (free, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), uint64(st.Blocks) * uint64(st.Bsize), nil
}

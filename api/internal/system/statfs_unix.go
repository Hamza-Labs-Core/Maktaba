//go:build unix

package system

import "syscall"

// diskFreeBytes returns the bytes available to an unprivileged process
// on the filesystem holding path. Bavail (not Bfree) is the right field
// — it excludes the root-reserved blocks the operator can't actually
// use. Bsize is the fundamental block size; both casts to uint64 are
// safe across the darwin (uint32 Bsize) and linux (int64 Bsize)
// Statfs_t shapes.
func diskFreeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
